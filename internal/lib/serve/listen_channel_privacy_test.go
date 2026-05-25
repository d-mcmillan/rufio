package serve

import (
	"bufio"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
)

// TestSSEListen_FiltersChannelMessagesByMembership — third-pass
// audit (post-v1.0.5 channel-privacy regression). The /listen SSE
// endpoint shares the stream package's filter pipeline with the CLI
// listen and the MCP stdio listen tool. A non-member identity
// connecting to /listen with a bearer token MUST NOT receive
// channel-message events from a channel they're not part of.
//
// Pre-fix: any authenticated identity received every channel-message
// the substrate held. Post-fix: only opener + target receive them;
// carol (or any other non-member) sees an empty stream.
func TestSSEListen_FiltersChannelMessagesByMembership(t *testing.T) {
	root := initProject(t)
	for _, sub := range []string{"live/outbox", "live/inbox", "live/attention"} {
		mustMkdir(t, root, sub)
	}

	// Seed a channel alice⇄bob with two messages BEFORE connecting,
	// so the SSE initial-drain (catch-up) is the surface we exercise.
	chID := "ch-1779000000000-sse001"
	chanDir := filepath.Join(root, "live", "channels", "active", chID)
	msgDir := filepath.Join(chanDir, "messages")
	if err := os.MkdirAll(msgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := "@channel|id:" + chID + "|opener:alice|target:bob|topic:lunch|intent:planning|ts:2026-05-22T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(chanDir, "meta.gdl"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	msg1 := "@channel-message|id:1779000000001-sse-a|channel:" + chID + "|by:alice|content:confidential-alice-sse|ts:2026-05-22T00:00:01Z\n"
	if err := os.WriteFile(filepath.Join(msgDir, "1779000000001-sse-a.gdl"), []byte(msg1), 0o644); err != nil {
		t.Fatalf("write msg1: %v", err)
	}
	msg2 := "@channel-message|id:1779000000002-sse-b|channel:" + chID + "|by:bob|content:confidential-bob-sse|ts:2026-05-22T00:00:02Z\n"
	if err := os.WriteFile(filepath.Join(msgDir, "1779000000002-sse-b.gdl"), []byte(msg2), 0o644); err != nil {
		t.Fatalf("write msg2: %v", err)
	}

	// Mint a token for carol — NOT a member of the channel.
	carolToken, _, err := admin.MintToken(root, "carol")
	if err != nil {
		t.Fatalf("MintToken carol: %v", err)
	}

	// Stand up the server.
	h, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	// Connect carol with ?types=channel-message — the worst-case
	// query a hostile non-member would issue to scrape channel data.
	resp, cancel := connectListenWithQuery(t, ts.URL, "types=channel-message", carolToken)
	defer cancel()
	defer resp.Body.Close()

	// Run the read in a goroutine with a deadline-driven cancel, so
	// we don't block the test for 30s on a stream with zero events
	// (the SSE protocol holds the connection open with periodic
	// heartbeats). 1 second is plenty for any leaked event to land.
	type result struct{ body string }
	done := make(chan result, 1)
	go func() {
		reader := bufio.NewReader(resp.Body)
		var captured strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- result{captured.String()}
				return
			}
			captured.WriteString(line)
			if strings.Contains(captured.String(), "confidential-alice-sse") ||
				strings.Contains(captured.String(), "confidential-bob-sse") {
				done <- result{captured.String()}
				return
			}
		}
	}()

	var body string
	select {
	case r := <-done:
		body = r.body
	case <-time.After(1500 * time.Millisecond):
		cancel() // drops the body-read goroutine off the conn
		// best-effort: drain what landed before we gave up
		select {
		case r := <-done:
			body = r.body
		case <-time.After(500 * time.Millisecond):
			body = ""
		}
	}
	if strings.Contains(body, "confidential-alice-sse") {
		t.Errorf("carol's /listen stream leaked alice's channel message:\n%s", body)
	}
	if strings.Contains(body, "confidential-bob-sse") {
		t.Errorf("carol's /listen stream leaked bob's channel message:\n%s", body)
	}
}

// TestSSEListen_MemberSeesOwnChannelMessages — symmetric positive
// guard. Alice IS a member; her /listen must see both her own and
// bob's messages on the channel.
func TestSSEListen_MemberSeesOwnChannelMessages(t *testing.T) {
	root := initProject(t)
	for _, sub := range []string{"live/outbox", "live/inbox", "live/attention"} {
		mustMkdir(t, root, sub)
	}

	chID := "ch-1779000000000-sse002"
	chanDir := filepath.Join(root, "live", "channels", "active", chID)
	msgDir := filepath.Join(chanDir, "messages")
	if err := os.MkdirAll(msgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := "@channel|id:" + chID + "|opener:alice|target:bob|topic:lunch|intent:planning|ts:2026-05-22T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(chanDir, "meta.gdl"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	msg1 := "@channel-message|id:1779000000001-mem-a|channel:" + chID + "|by:alice|content:alice-says-hi|ts:2026-05-22T00:00:01Z\n"
	if err := os.WriteFile(filepath.Join(msgDir, "1779000000001-mem-a.gdl"), []byte(msg1), 0o644); err != nil {
		t.Fatalf("write msg1: %v", err)
	}
	msg2 := "@channel-message|id:1779000000002-mem-b|channel:" + chID + "|by:bob|content:bob-replies-yo|ts:2026-05-22T00:00:02Z\n"
	if err := os.WriteFile(filepath.Join(msgDir, "1779000000002-mem-b.gdl"), []byte(msg2), 0o644); err != nil {
		t.Fatalf("write msg2: %v", err)
	}

	aliceToken, _, err := admin.MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken alice: %v", err)
	}

	h, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp, cancel := connectListenWithQuery(t, ts.URL, "types=channel-message", aliceToken)
	defer cancel()
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	var captured strings.Builder
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		captured.WriteString(line)
		if strings.Contains(captured.String(), "alice-says-hi") &&
			strings.Contains(captured.String(), "bob-replies-yo") {
			break
		}
	}
	body := captured.String()
	if !strings.Contains(body, "alice-says-hi") {
		t.Errorf("alice (member) didn't see her own channel message:\n%s", body)
	}
	if !strings.Contains(body, "bob-replies-yo") {
		t.Errorf("alice (member) didn't see bob's channel message:\n%s", body)
	}
}
