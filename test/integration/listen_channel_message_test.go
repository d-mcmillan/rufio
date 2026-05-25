package integration_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestDev_ChannelMessage_EmittedViaListenCatchUp is the locked
// regression for issue #107: channel messages must be visible to the
// recipient via `rufio listen --catch-up`. Before the fix, the on-disk
// record had Type="say" which is NOT in recall.AllTypes (the canonical
// taxonomy uses "channel-message"), so stream.Match silently dropped
// every channel-message event when `listen` was invoked without a
// `--types` flag (the implicit AllTypes filter).
//
// Flow:
//
//  1. Spawn the real `rufio dev` daemon (the live router).
//  2. agent-a summons agent-b; agent-b accepts → channel exists.
//  3. agent-a `say`s a message into the channel.
//  4. Poll agent-b's inbox until the @channel-message inbox file lands
//     (5s, matches the canonical e2e routing budget — see
//     TestDev_ChannelSayRoute_LiveSession).
//  5. Run `rufio listen --catch-up` as agent-b (one-shot, no watcher).
//  6. Parse the JSONL stdout. Assert at least one event has
//     `_type == "channel-message"` AND matches the content we sent.
//
// This test would FAIL before the fix because step 6 finds zero
// channel-message events on stdout — `listen --catch-up` walks the
// inbox and applies `stream.Match` with `Types=AllTypes` (the default).
// The on-disk record's Type is "say" which is NOT in AllTypes, so the
// event is silently dropped and the agent sees no transcript.
func TestDev_ChannelMessage_EmittedViaListenCatchUp(t *testing.T) {
	root := initProject(t)

	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Spawn the daemon. --quiet suppresses banner chatter so test output
	// stays clean.
	cmd := exec.Command(binPath, "dev", "--quiet")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "RUFIO_AGENT_ID=agent-a")
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open /dev/null: %v", err)
	}
	defer devnull.Close()
	cmd.Stdout = devnull
	cmd.Stderr = devnull
	if err := cmd.Start(); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_, _ = cmd.Process.Wait()
	}()
	time.Sleep(500 * time.Millisecond)

	// Open the channel: summon → accept. Uses the same shortcut as
	// TestDev_ChannelClose_LiveSession — accept is synchronous so we
	// don't need to wait for summon routing to inspect agent-b's inbox
	// before accept; accept reads pending/ directly.
	summonID := mustSeedSummon(t, root, "agent-a", "agent-b", "customer:5821", "channel-message regression")
	ares := testutil.RunCLI(t, []string{"accept", summonID, "--json"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if ares.Code != 0 {
		t.Fatalf("accept: exit=%d stderr=%q stdout=%q", ares.Code, ares.Stderr, ares.Stdout)
	}
	chID := mustParseAcceptChannel(t, ares.Stdout)

	// agent-a says three messages so we can prove ALL of them are
	// emitted (not just one — the bug filters every one, so any
	// emission would prove the fix; but the multi-message form makes
	// the demo flow's signal louder if a future regression filters by
	// some other axis).
	contents := []string{"hello bob", "second message", "third and final"}
	for _, msg := range contents {
		sayRes := testutil.RunCLI(t, []string{
			"say", "--channel=" + chID, "--content=" + msg,
		}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
		if sayRes.Code != 0 {
			t.Fatalf("say %q: exit=%d stderr=%q stdout=%q", msg, sayRes.Code, sayRes.Stderr, sayRes.Stdout)
		}
	}

	// Wait for the daemon to route ALL three messages to agent-b's
	// inbox. 5s is the canonical e2e budget (see
	// TestDev_ChannelSayRoute_LiveSession).
	inboxDir := filepath.Join(root, "live", "inbox", "agent-b")
	{
		deadline := time.Now().Add(5 * time.Second)
		var routedOK bool
		for time.Now().Before(deadline) {
			entries, err := os.ReadDir(inboxDir)
			if err == nil {
				// Count .gdl files that contain @channel-message (post-fix)
				// OR @say (pre-fix) lines. We accept either token here so
				// the inbox-poll passes both before AND after the writer
				// fix; the real assertion is on listen's JSONL output.
				n := 0
				for _, e := range entries {
					if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
						continue
					}
					bs, rerr := os.ReadFile(filepath.Join(inboxDir, e.Name()))
					if rerr != nil {
						continue
					}
					s := string(bs)
					if strings.Contains(s, "@channel-message|") || strings.Contains(s, "@say|") {
						n++
					}
				}
				if n >= len(contents) {
					routedOK = true
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !routedOK {
			t.Fatalf("daemon did not route all %d channel messages to agent-b within deadline (%s)", len(contents), inboxDir)
		}
	}

	// Now the real assertion: run `rufio listen --catch-up` as agent-b
	// and prove the channel-message events are emitted on stdout.
	// listen.go's runListen registers SIGINT/SIGTERM and then enters
	// WatchAndEmit which blocks forever. We need just the catch-up
	// portion — so we spawn listen as a child and SIGTERM it after a
	// short settle delay, capturing what it produced in catch-up.
	listenCmd := exec.Command(binPath, "listen", "--as=agent-b", "--catch-up")
	listenCmd.Dir = root
	listenCmd.Env = append(os.Environ(), "NO_COLOR=1")
	stdoutBuf, err := listenCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("listen stdout pipe: %v", err)
	}
	listenCmd.Stderr = devnull
	if err := listenCmd.Start(); err != nil {
		t.Fatalf("listen start: %v", err)
	}
	// Read until we've collected the catch-up output (the watcher loop
	// blocks for new events but emits nothing yet). 1s settle is
	// generous for catch-up of three small files in a fresh project.
	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 1024)
		for {
			n, rerr := stdoutBuf.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if rerr != nil {
				done <- buf
				return
			}
		}
	}()
	time.Sleep(1 * time.Second)
	_ = listenCmd.Process.Signal(syscall.SIGTERM)
	_, _ = listenCmd.Process.Wait()
	var out []byte
	select {
	case out = <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("listen stdout reader timed out")
	}

	// Parse JSONL: collect _type for every line, then assert
	// channel-message appears at least len(contents) times AND every
	// say's content is represented.
	type event struct {
		Type    string `json:"_type"`
		Content string `json:"content"`
		Raw     string `json:"raw"`
	}
	var events []event
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Errorf("listen jsonl parse %q: %v", line, err)
			continue
		}
		events = append(events, ev)
	}

	channelMessages := 0
	for _, ev := range events {
		if ev.Type == "channel-message" {
			channelMessages++
		}
	}
	if channelMessages < len(contents) {
		t.Fatalf("listen emitted %d channel-message events, want >= %d; full event list: %+v\n--- raw stdout ---\n%s",
			channelMessages, len(contents), events, string(out))
	}

	// Every content we said must appear somewhere in the channel-message
	// events. We match on the `content` field (Event.Content from
	// stream.go is r.Get("content")) — confirms the data round-trips.
	for _, want := range contents {
		found := false
		for _, ev := range events {
			if ev.Type != "channel-message" {
				continue
			}
			if ev.Content == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("listen did not emit a channel-message event with content=%q; got events: %+v", want, events)
		}
	}
}
