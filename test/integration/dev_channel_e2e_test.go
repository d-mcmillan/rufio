package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestDev_ChannelSayRoute_LiveSession spawns the real `rufio dev` daemon,
// opens a channel between agent-a and agent-b (summon → daemon routes →
// agent-b accepts), then has agent-a issue a real `rufio say` and polls
// for the daemon to route the @say record to agent-b's inbox. Catches
// regressions in the RoutingHandler @say dispatch wiring (PR #16 T7b),
// channels.LoadMeta member resolution, and the no-echo-to-sender contract
// (D16-routing).
func TestDev_ChannelSayRoute_LiveSession(t *testing.T) {
	root := initProject(t)

	// Build the binary once.
	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Spawn the daemon. --quiet suppresses banner chatter so test output stays clean.
	cmd := exec.Command(binPath, "dev", "--quiet")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "RUFIO_AGENT_ID=agent-a")
	// os.NewFile(0, os.DevNull) wraps fd 0 (stdin); closes leak into the
	// parent test-process fd table → spurious EBADF on subsequent
	// TempDir RemoveAll cleanups. Use os.Open(os.DevNull) (PR #14 fix).
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

	// Give the daemon time to register fsnotify watchers on the canonical
	// subdir set (which now includes live/channels/active/). 500ms is the
	// canonical budget — guards against the race where the first summon /
	// say lands before fsnotify is armed.
	time.Sleep(500 * time.Millisecond)

	// --- Open the channel via the real CLI surface. ---
	//
	// agent-a issues a real summon, daemon routes it, agent-b accepts.
	// We poll for the routed summon in agent-b's inbox (5s deadline) so
	// the test fails fast on a regression in the @summon dispatch wiring
	// rather than spuriously on accept (which is unrelated). This mirrors
	// TestDev_SummonRoute_LiveSession.
	pendingPattern := filepath.Join(root, "live", "summons", "pending", "*.gdl")
	before, _ := filepath.Glob(pendingPattern)
	beforeSet := make(map[string]bool, len(before))
	for _, p := range before {
		beforeSet[p] = true
	}
	res := testutil.RunCLI(t, []string{
		"summon", "agent-b",
		"--topic=customer:5821",
		"--intent=test",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("summon: exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	after, _ := filepath.Glob(pendingPattern)
	var fresh []string
	for _, p := range after {
		if !beforeSet[p] {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) != 1 {
		t.Fatalf("summon did not produce exactly one new pending file: got %d (%v)", len(fresh), fresh)
	}
	summonID := strings.TrimSuffix(filepath.Base(fresh[0]), ".gdl")

	// Poll agent-b's inbox for the routed summon.
	summonInbox := filepath.Join(root, "live", "inbox", "agent-b", summonID+".gdl")
	{
		deadline := time.Now().Add(5 * time.Second)
		var routedOK bool
		for time.Now().Before(deadline) {
			if bs, err := os.ReadFile(summonInbox); err == nil {
				s := string(bs)
				if strings.Contains(s, "@summon|") &&
					strings.Contains(s, "@route|to:agent-b|from:agent-a") {
					routedOK = true
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !routedOK {
			t.Fatalf("daemon did not route summon to agent-b within deadline: %s", summonInbox)
		}
	}

	// agent-b accepts via the real CLI. Accept is synchronous — the
	// pending file moves, the channel meta is written, and the channel id
	// is on stdout (we use --json for clean parsing).
	ares := testutil.RunCLI(t, []string{"accept", summonID, "--json"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if ares.Code != 0 {
		t.Fatalf("accept: exit=%d stderr=%q stdout=%q", ares.Code, ares.Stderr, ares.Stdout)
	}
	// Parse channel id from JSON output rather than the human stdout — the
	// JSON shape is locked under TestAccept_JSONOutput, so this is more
	// robust to future presentational tweaks.
	chID := mustParseAcceptChannel(t, ares.Stdout)

	// --- The actual subject under test: say → daemon routes → other inbox. ---
	pattern := filepath.Join(root, "live", "channels", "active", chID, "messages", "*.gdl")
	msgsBefore, _ := filepath.Glob(pattern)
	msgsBeforeSet := make(map[string]bool, len(msgsBefore))
	for _, p := range msgsBefore {
		msgsBeforeSet[p] = true
	}
	sayRes := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID, "--content=hello from a",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if sayRes.Code != 0 {
		t.Fatalf("say: exit=%d stderr=%q stdout=%q", sayRes.Code, sayRes.Stderr, sayRes.Stdout)
	}
	// Identify the freshly written message file and derive the msg id from
	// its basename. Parallel to the pending glob-diff above — avoids
	// coupling to the say command's stdout shape.
	msgsAfter, _ := filepath.Glob(pattern)
	var freshMsgs []string
	for _, p := range msgsAfter {
		if !msgsBeforeSet[p] {
			freshMsgs = append(freshMsgs, p)
		}
	}
	if len(freshMsgs) != 1 {
		t.Fatalf("say did not produce exactly one new message file: got %d (%v)", len(freshMsgs), freshMsgs)
	}
	msgID := strings.TrimSuffix(filepath.Base(freshMsgs[0]), ".gdl")

	// Poll agent-b's inbox for the routed @say message. 5s is the
	// canonical e2e budget — same as the summon-route test above. The
	// latency budget covers fsnotify event delivery + one
	// RouteChannelMessage Render/Write round-trip.
	msgInbox := filepath.Join(root, "live", "inbox", "agent-b", msgID+".gdl")
	{
		deadline := time.Now().Add(5 * time.Second)
		var routedOK bool
		for time.Now().Before(deadline) {
			if bs, err := os.ReadFile(msgInbox); err == nil {
				s := string(bs)
				// Issue #107: on-disk Type is "channel-message" (CLI verb still `say`).
				if strings.Contains(s, "@channel-message|") &&
					strings.Contains(s, "channel:"+chID) &&
					strings.Contains(s, "by:agent-a") &&
					strings.Contains(s, "content:hello from a") &&
					strings.Contains(s, "@route|to:agent-b|from:agent-a") {
					routedOK = true
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !routedOK {
			// Surface enough state to debug a flake from CI logs alone.
			if bs, err := os.ReadFile(msgInbox); err == nil {
				t.Errorf("inbox file present but malformed: %s = %q", msgInbox, bs)
			} else {
				t.Errorf("inbox file missing at %s: %v", msgInbox, err)
			}
			if entries, derr := os.ReadDir(filepath.Join(root, "live", "inbox", "agent-b")); derr == nil {
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("live/inbox/agent-b/ contents: %v", names)
			} else {
				t.Errorf("live/inbox/agent-b/ unreadable: %v", derr)
			}
			if bs, derr := os.ReadFile(freshMsgs[0]); derr == nil {
				t.Errorf("source message file (%s):\n%s", freshMsgs[0], bs)
			}
			t.FailNow()
		}
	}

	// Negative assertion: the sender (agent-a) must NOT receive an echo
	// in their own inbox. RouteChannelMessage skips the author per the
	// no-self-echo contract — this guards against a regression that
	// would deliver to every current member indiscriminately.
	senderInbox := filepath.Join(root, "live", "inbox", "agent-a", msgID+".gdl")
	if _, err := os.Stat(senderInbox); !os.IsNotExist(err) {
		bs, _ := os.ReadFile(senderInbox)
		t.Errorf("sender agent-a got an echo of their own say (must not happen): %s\n%s", senderInbox, bs)
		if entries, derr := os.ReadDir(filepath.Join(root, "live", "inbox", "agent-a")); derr == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("live/inbox/agent-a/ contents: %v", names)
		}
	}
}

// TestDev_ChannelClose_LiveSession spawns the real `rufio dev` daemon,
// opens a channel, then closes it via the real CLI. The close is
// synchronous (active/ → closed/ rename happens in the close command,
// not the daemon) so no polling is required for the close itself. The
// daemon is present to verify it tolerates the rename event (a Remove
// followed by a Create on the closed/ subtree) without crashing.
// Subsequent say attempts on the closed channel must surface as
// NoSuchChannel per D16.6.
func TestDev_ChannelClose_LiveSession(t *testing.T) {
	root := initProject(t)

	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

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

	// Open a channel. We don't need to verify the summon route for this
	// test (Test 1 covers that), so we use the synchronous-call shortcut:
	// summon + accept return success even if the daemon hasn't routed
	// yet, since accept reads from live/summons/pending/ directly.
	summonID := mustSeedSummon(t, root, "agent-a", "agent-b", "customer:5821", "test")
	ares := testutil.RunCLI(t, []string{"accept", summonID, "--json"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if ares.Code != 0 {
		t.Fatalf("accept: exit=%d stderr=%q stdout=%q", ares.Code, ares.Stderr, ares.Stdout)
	}
	chID := mustParseAcceptChannel(t, ares.Stdout)

	// --- Close the channel as the opener (agent-a). ---
	cres := testutil.RunCLI(t, []string{"close", chID}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if cres.Code != 0 {
		t.Fatalf("close: exit=%d stderr=%q stdout=%q", cres.Code, cres.Stderr, cres.Stdout)
	}

	// active/<chID>/ must be gone (rename, not copy).
	activeDir := filepath.Join(root, "live", "channels", "active", chID)
	if _, err := os.Stat(activeDir); !os.IsNotExist(err) {
		t.Errorf("active/%s/ still exists after close (err=%v)", chID, err)
		if entries, derr := os.ReadDir(filepath.Join(root, "live", "channels", "active")); derr == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("live/channels/active/ contents: %v", names)
		}
	}

	// closed/<chID>/meta.gdl must exist with both @channel header and
	// @channel-close record naming the closer.
	closedMeta := filepath.Join(root, "live", "channels", "closed", chID, "meta.gdl")
	bs, err := os.ReadFile(closedMeta)
	if err != nil {
		t.Fatalf("read closed meta %s: %v", closedMeta, err)
	}
	contents := string(bs)
	for _, want := range []string{
		"@channel|",
		"@channel-close|by:agent-a",
	} {
		if !strings.Contains(contents, want) {
			t.Errorf("closed meta missing %q.\n%s", want, contents)
		}
	}

	// Further writes must be refused. D16.6: a closed channel is
	// indistinguishable from a never-existing one at the say/leave
	// surfaces — both surface as NoSuchChannel (exit 1).
	sayRes := testutil.RunCLI(t, []string{
		"say", "--channel=" + chID, "--content=x",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if sayRes.Code != 1 {
		t.Errorf("say after close: exit=%d stderr=%q (want exit 1)", sayRes.Code, sayRes.Stderr)
	}
	if !strings.Contains(sayRes.Stderr, "no such channel") {
		t.Errorf("say after close stderr missing 'no such channel': %q", sayRes.Stderr)
	}
}

// mustParseAcceptChannel extracts the `channel` field from a `rufio
// accept --json` payload. Fails the test on parse error or shape
// mismatch. Local to this file — accept_test.go does the same via an
// inline json.Unmarshal, but we'd rather keep the daemon e2e tests
// readable than chase a tiny refactor.
func mustParseAcceptChannel(t *testing.T, stdout string) string {
	t.Helper()
	// The accept payload is a single JSONL line: `{"_type":"accept",..."channel":"ch-..."}`.
	// We re-use the existing chIDPattern (defined in accept_test.go, same
	// package) for shape validation. Reusing the canonical regex avoids
	// drift if the channel-id scheme changes.
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		t.Fatalf("accept stdout empty")
	}
	// Naive extraction: find `"channel":"...",` substring. Robust enough
	// for the locked JSON shape and avoids pulling encoding/json into
	// this file's import block twice.
	const key = `"channel":"`
	i := strings.Index(trimmed, key)
	if i < 0 {
		t.Fatalf("accept stdout missing channel key: %q", trimmed)
	}
	rest := trimmed[i+len(key):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		t.Fatalf("accept stdout malformed channel value: %q", trimmed)
	}
	chID := rest[:j]
	if !chIDPattern.MatchString(chID) {
		t.Fatalf("accept channel %q does not match canonical shape", chID)
	}
	return chID
}
