package serve

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// startListenServer brings up a real httptest server fronted by the
// /listen handler (auth-wrapped). Returns the URL + alice's token + the
// substrate root so tests can seed events and tear down cleanly.
func startListenServer(t *testing.T) (string, string, string) {
	t.Helper()
	root := initProject(t)
	for _, sub := range []string{"live/outbox", "live/inbox", "live/attention"} {
		mustMkdir(t, root, sub)
	}
	tokAlice := mintTestToken(t, root, "alice")
	h, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts.URL, tokAlice, root
}

func seedTopic(t *testing.T, root, author, content string) {
	t.Helper()
	id, err := thought.GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	rec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID:      id,
		Author:  author,
		Type:    "hypothesis",
		Subject: "test:1",
		Content: content,
		Scope:   "fleet",
		TS:      versioning.NowISO(),
	})
	if err := thought.Write(root, author, id, []gdl.Record{rec}); err != nil {
		t.Fatalf("thought.Write: %v", err)
	}
}

// connectListen opens an SSE GET on /listen with the supplied token.
// Returns the response so the caller can scan event lines and Cancel
// the context to tear it down.
func connectListen(t *testing.T, baseURL, token string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/listen", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("connect /listen: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("/listen returned %d", resp.StatusCode)
	}
	return resp, cancel
}

// connectListenWithQuery is like connectListen but lets the caller pass
// a raw query string (without the leading `?`).
func connectListenWithQuery(t *testing.T, baseURL, query, token string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	full := baseURL + "/listen"
	if query != "" {
		full = full + "?" + query
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("connect /listen: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("/listen returned %d", resp.StatusCode)
	}
	return resp, cancel
}

func TestListenEndpoint_RequiresAuth(t *testing.T) {
	url, _, _ := startListenServer(t)
	resp, err := http.Get(url + "/listen")
	if err != nil {
		t.Fatalf("GET /listen: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", resp.StatusCode)
	}
}

func TestListenEndpoint_StreamsEvents(t *testing.T) {
	url, token, root := startListenServer(t)

	resp, cancel := connectListen(t, url, token)
	defer cancel()
	defer resp.Body.Close()

	// Seed a thought after the connection is open so it shows up via
	// the poll loop.
	go func() {
		time.Sleep(50 * time.Millisecond)
		seedTopic(t, root, "alice", "fresh hypothesis incoming")
	}()

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(3 * time.Second)
	got := false
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "fresh hypothesis incoming") {
			got = true
			break
		}
	}
	if !got {
		t.Fatal("did not receive the seeded event within 3s")
	}
}

func TestListenEndpoint_RespectsPrivacy(t *testing.T) {
	url, _, root := startListenServer(t)
	// Mint bob's token so we can connect AS bob and confirm fleet
	// events are visible, while alice's private agent-scoped record
	// is NOT visible to bob.
	tokBob := mintTestToken(t, root, "bob")

	resp, cancel := connectListen(t, url, tokBob)
	defer cancel()
	defer resp.Body.Close()

	// Seed alice's scope=agent record + bob's own scope=agent + a
	// fleet record. Bob should see his own and the fleet, but not
	// alice's agent-scoped record.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = writeRecord(root, "alice", "agent", "alice's private secret")
		_ = writeRecord(root, "bob", "agent", "bob's own private")
		_ = writeRecord(root, "alice", "fleet", "alice fleet broadcast")
	}()

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(3 * time.Second)
	var captured strings.Builder
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		captured.WriteString(line)
		if strings.Contains(captured.String(), "alice fleet broadcast") &&
			strings.Contains(captured.String(), "bob's own private") {
			break
		}
	}
	body := captured.String()
	if strings.Contains(body, "alice's private secret") {
		t.Errorf("bob's stream leaked alice's scope=agent record:\n%s", body)
	}
}

// writeRecord seeds one @thought record bypassing the validate path
// (tests need to write scope=agent records under arbitrary authors).
func writeRecord(root, author, scope, content string) error {
	id, err := thought.GenerateID()
	if err != nil {
		return err
	}
	rec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID: id, Author: author, Type: "hypothesis", Subject: "test:1",
		Content: content, Scope: scope, TS: versioning.NowISO(),
	})
	return thought.Write(root, author, id, []gdl.Record{rec})
}

func TestListenEndpoint_CursorResume(t *testing.T) {
	url, token, root := startListenServer(t)
	// Seed two events with distinguishable content BEFORE connecting.
	seedTopic(t, root, "alice", "event A")
	time.Sleep(10 * time.Millisecond)
	seedTopic(t, root, "alice", "event B")

	// Connect WITHOUT cursor; capture both initial events in a
	// time-bounded read loop.
	resp, cancel := connectListen(t, url, token)
	gotA, gotB := readWithBudget(t, resp, 2*time.Second, "event A", "event B")
	cancel()
	_ = resp.Body.Close()
	if !gotA["event A"] || !gotA["event B"] {
		t.Fatalf("initial connection did not see both events (A=%v B=%v)", gotA["event A"], gotA["event B"])
	}
	_ = gotB

	// Reconnect with a future cursor — no events should be re-emitted.
	resp2, cancel2 := connectListenWithQuery(t, url, "cursor=9999-99-99T99:99:99.999999999Z%2Fzzzzzzzzzzz", token)
	seen, _ := readWithBudget(t, resp2, 500*time.Millisecond, "event A", "event B")
	cancel2()
	_ = resp2.Body.Close()
	if seen["event A"] || seen["event B"] {
		t.Errorf("future cursor should not re-emit past events, got A=%v B=%v", seen["event A"], seen["event B"])
	}
}

// readWithBudget drains the response body for up to `budget`, returning
// which of the supplied needles were observed. Cancels the read promptly
// at budget expiry (no per-read block past the budget).
func readWithBudget(t *testing.T, resp *http.Response, budget time.Duration, needles ...string) (map[string]bool, map[string]bool) {
	t.Helper()
	found := map[string]bool{}
	allFound := map[string]bool{}
	for _, n := range needles {
		found[n] = false
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			for _, n := range needles {
				if strings.Contains(line, n) {
					found[n] = true
					allFound[n] = true
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(budget):
	}
	return found, allFound
}

func TestListenEndpoint_Heartbeat(t *testing.T) {
	// The production heartbeat tick is 30s — too slow to wait for in a
	// unit test without a seam. Instead, we assert that the /listen
	// handler stays connected through several poll cycles and tears
	// down cleanly when the client cancels.
	url, token, _ := startListenServer(t)

	resp, cancel := connectListen(t, url, token)
	// Read in a goroutine so the main goroutine can cancel cleanly.
	done := make(chan struct{})
	go func() {
		_, _ = bufio.NewReader(resp.Body).ReadString('\n')
		close(done)
	}()
	// Wait for poll ticks to fire a few times.
	time.Sleep(600 * time.Millisecond)
	cancel()
	_ = resp.Body.Close()
	select {
	case <-done:
		// reader returned (probably with EOF from cancel)
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not return after cancel — handler likely hung")
	}
}

// TestListen_ClosesOnTokenRevocation (security audit F3). Pre-fix, the
// /listen handler resolved the bearer token ONCE at connect time and
// then streamed indefinitely. If admin revoked the token mid-stream
// (the "compromised collaborator — revoke now" scenario), the
// existing connection kept feeding events to the attacker — new
// /mcp calls would 401 but the SSE stream survived until the TCP
// connection dropped. That is exactly the wrong behaviour.
//
// Fix: the handler re-verifies the token on each poll tick (500ms).
// When ResolveToken returns ErrTokenInvalid, the stream closes
// cleanly with a final SSE comment and the handler returns.
//
// This test connects, revokes the token, and asserts the connection
// closes within one poll tick (allow up to 3s for scheduling jitter).
func TestListen_ClosesOnTokenRevocation(t *testing.T) {
	root := initProject(t)
	for _, sub := range []string{"live/outbox", "live/inbox", "live/attention"} {
		mustMkdir(t, root, sub)
	}
	plaintext, tok, err := admin.MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	h, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp, cancel := connectListen(t, ts.URL, plaintext)
	defer cancel()
	defer resp.Body.Close()

	// Drain in a goroutine. When the server closes the stream the
	// Read returns (EOF or error), and the channel closes.
	closed := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := resp.Body.Read(buf)
			if err != nil {
				closed <- err
				return
			}
		}
	}()

	// Give the server one poll tick to fully establish the stream
	// state machine, then revoke the token. The next poll tick (≤500ms
	// later) MUST re-verify and close.
	time.Sleep(600 * time.Millisecond)
	if err := admin.RevokeToken(root, tok.ID); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	select {
	case err := <-closed:
		// Any error counts as a close — EOF on clean shutdown,
		// io.ErrUnexpectedEOF on hijack-style close, or a wrapped
		// net.OpError on TCP teardown. What matters is that the
		// server stopped streaming.
		if err == nil {
			t.Errorf("Read returned nil error — should have closed; got %v", err)
		}
		// io.EOF (or any err) within 3s of revoke = pass.
		_ = err
	case <-time.After(3 * time.Second):
		t.Fatal("server did not close /listen within 3s of token revocation — F3 floor breached")
	}
}

// TestListen_ClosesOnTokenRevocation_HTTPS (security audit F3 follow-up)
// reproduces the WAN-style scenario Damon's cross-machine gate caught:
// the unit-test's HTTP/1.1 plain-text server closes within 1s on
// revoke, but the real droplet's HTTPS path kept streaming for many
// minutes. The fix uses a TLS test server (which negotiates HTTP/1.1
// because the serve config explicitly disables HTTP/2 on /listen —
// SSE is HTTP/1.1-native; HTTP/2 streaming bufferers Go's runtime
// uses can hold END_STREAM frames in the writer queue indefinitely
// when no further bytes are written by the handler after the close
// message).
//
// The crucial detail: this test deliberately seeds NO events on the
// substrate. The handler's writes-to-w don't happen until the 30s
// heartbeat OR a poll-time write — neither of which produces enough
// bytes to flush HTTP/2's frame buffer without an explicit hijack-
// and-close.
//
// What this test asserts: connect over HTTPS, revoke the token, and
// read from resp.Body. The read MUST return within 3s.
func TestListen_ClosesOnTokenRevocation_HTTPS(t *testing.T) {
	root := initProject(t)
	for _, sub := range []string{"live/outbox", "live/inbox", "live/attention"} {
		mustMkdir(t, root, sub)
	}
	plaintext, tok, err := admin.MintToken(root, "alice")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	h, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	// httptest.NewTLSServer brings up a real TLS listener; Go's net/http
	// will negotiate HTTP/2 with a compatible client.
	ts := httptest.NewTLSServer(h)
	defer ts.Close()

	// Use httptest.Server.Client() — it's pre-configured to trust the
	// server's self-signed cert.
	client := ts.Client()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/listen", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("connect /listen over HTTPS: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/listen HTTPS returned %d", resp.StatusCode)
	}

	closed := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := resp.Body.Read(buf)
			if err != nil {
				closed <- err
				return
			}
		}
	}()

	// One poll-tick warmup, then revoke.
	time.Sleep(600 * time.Millisecond)
	if err := admin.RevokeToken(root, tok.ID); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	select {
	case err := <-closed:
		if err == nil {
			t.Errorf("Read returned nil error — should have closed; got %v", err)
		}
		_ = err
	case <-time.After(3 * time.Second):
		t.Fatal("server did not close HTTPS /listen within 3s of token revocation — WAN gate breach reproduces locally")
	}
}
