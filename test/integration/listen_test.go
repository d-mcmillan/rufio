package integration_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// pollSubprocessStdout reads stdout from a long-running subprocess in a
// goroutine and times out via select. The test goroutine NEVER blocks on
// pipe IO. SIGTERMs the subprocess as soon as match succeeds or the
// deadline elapses.
//
// Returns true if matched within deadline; false on timeout.
func pollSubprocessStdout(t *testing.T, cmd *exec.Cmd, stdout io.Reader, deadline time.Duration, match func(line string) bool) bool {
	t.Helper()

	lineCh := make(chan string, 32)
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case lineCh <- scanner.Text():
			case <-doneCh:
				return
			}
		}
	}()

	timeout := time.After(deadline)
	matched := false
loop:
	for {
		select {
		case <-timeout:
			break loop
		case line, ok := <-lineCh:
			if !ok {
				break loop // pipe closed (daemon exited)
			}
			if match(line) {
				matched = true
				break loop
			}
		}
	}

	// Tear down: SIGTERM the daemon and SIGKILL after 2s if it doesn't
	// exit. Then Wait() to reap the child. This NEVER blocks the test
	// longer than 2s.
	_ = cmd.Process.Signal(syscall.SIGTERM)
	killTimer := time.AfterFunc(2*time.Second, func() {
		_ = cmd.Process.Signal(syscall.SIGKILL)
	})
	_, _ = cmd.Process.Wait()
	killTimer.Stop()
	return matched
}

// TestListen_StreamsNewInboxFiles spawns `rufio listen` as agent-b,
// then writes a synthetic inbox file from the test goroutine, and
// expects the daemon to emit the corresponding JSONL line on stdout.
func TestListen_StreamsNewInboxFiles(t *testing.T) {
	root := initProject(t)

	// Pre-create the inbox dir so the watcher catches the file event.
	inboxDir := filepath.Join(root, "live", "inbox", "agent-b")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatal(err)
	}

	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	cmd := exec.Command(binPath, "listen")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "RUFIO_AGENT_ID=agent-b")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &lineCapture{w: &stderrBuf}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Signal(syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
		}
		if stderrBuf.Len() > 0 {
			t.Logf("daemon stderr:\n%s", stderrBuf.String())
		}
	}()

	// Deterministic race-avoidance: a bounded create-a-new-file-until-
	// observed loop is the SOLE synchronization mechanism. The
	// subprocess's fsnotify watcher (internal/lib/stream WatchAndEmit)
	// may not have completed backend registration when this goroutine
	// starts, so we do NOT rely on any fixed pre-sleep being "long
	// enough" (the old `time.Sleep(1s)` was a guessed window that, under
	// full-suite parallel `-race` load, could elapse before registration
	// finished — leaving the single post-sleep write missed and the test
	// hanging to its deadline; deflake task #106 / followup V8G3-M1).
	//
	// CRITICAL CONTRACT: stream.WatchAndEmit acts ONLY on
	// `fsnotify.Create` events — it deliberately ignores `Write` events
	// (stream.go:262; the documented WK2-STREAM-2 write-once-per-file
	// design). So the OLD loop that re-wrote the SAME path was useless
	// for retrying: only its first os.WriteFile was a Create; every
	// subsequent same-path write was a Write the daemon drops — there
	// was effectively NO retry, just the original single-Create race.
	// The deterministic fix is to emit a DISTINCT new file each
	// iteration: every new path is a fresh Create event, so a later
	// iteration is guaranteed to be caught once registration completes,
	// regardless of how long that takes (the product code is unchanged
	// and correct — this is a test-only synchronization fix). The
	// content is identical each time, so the matched event is identical;
	// the daemon emits one JSONL line per file but the test only needs
	// the first match. The deadline below is then only a bounded
	// backstop, not the synchronization. (Same re-write-until-observed
	// philosophy as internal/tui/watch_test.go awaitWatcherMsg, adapted
	// to the daemon's Create-only contract — tasks #100/#106.)
	contents := "@thought|id:1-test|author:agent-a|type:hypothesis|subject:customer:5821|content:hello|scope:fleet|ts:2026-05-12T12:00:00Z|ttl:0\n"
	stopWrite := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		seq := 0
		writeFresh := func() {
			seq++
			// Distinct filename each iteration ⇒ a genuine Create event
			// (the only op WatchAndEmit honours). All carry the same
			// subject:customer:5821 record, so any one satisfies the match.
			p := filepath.Join(inboxDir, fmt.Sprintf("%d-test.gdl", seq))
			_ = os.WriteFile(p, []byte(contents), 0o644)
		}
		writeFresh() // first attempt promptly — no fixed pre-sleep relied upon.
		for {
			select {
			case <-stopWrite:
				return
			case <-ticker.C:
				writeFresh()
			}
		}
	}()

	// Generous-but-bounded backstop. The re-write loop above removes the
	// race; this deadline only guards against a genuine watcher
	// regression. 20s is ample even under full-suite parallel load
	// (the binary build is already done before this point) without
	// being absurd.
	matched := pollSubprocessStdout(t, cmd, stdout, 20*time.Second, func(line string) bool {
		var got map[string]interface{}
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			return false
		}
		return got["_type"] == "thought" && got["subject"] == "customer:5821"
	})
	close(stopWrite)
	if !matched {
		t.Errorf("listen did not emit expected JSON line within deadline")
	}
}

// lineCapture is a thread-safe io.Writer that buffers daemon stderr for
// diagnostic logging in test teardown.
type lineCapture struct {
	mu sync.Mutex
	w  *strings.Builder
}

func (c *lineCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.w.Write(p)
}

// TestListen_CatchUpEmitsExisting verifies --catch-up replays
// pre-existing inbox contents before entering the watch loop.
func TestListen_CatchUpEmitsExisting(t *testing.T) {
	root := initProject(t)
	inboxDir := filepath.Join(root, "live", "inbox", "agent-b")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "@thought|id:1-pre|author:agent-a|type:hypothesis|subject:order:1|content:pre|scope:fleet|ts:2026-05-12T12:00:00Z|ttl:0\n"
	if err := os.WriteFile(filepath.Join(inboxDir, "1-pre.gdl"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cmd := exec.Command(binPath, "listen", "--catch-up")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "RUFIO_AGENT_ID=agent-b")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	// os.NewFile(0, os.DevNull) wraps fd 0 (stdin); closes leak into the
	// parent test-process fd table → spurious EBADF on subsequent
	// TempDir RemoveAll cleanups. Use os.Open(os.DevNull).
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open /dev/null: %v", err)
	}
	defer devnull.Close()
	cmd.Stderr = devnull
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Signal(syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
		}
	}()

	matched := pollSubprocessStdout(t, cmd, stdout, 5*time.Second, func(line string) bool {
		return strings.Contains(line, `"subject":"order:1"`)
	})
	if !matched {
		t.Errorf("--catch-up did not emit pre-existing record within 5s")
	}
}

// TestListen_CatchUp_EmitsBroaderSubstrate is the issue-#139 regression
// test. The vet's repro was: A summoned B, B accepted, both exchanged
// channel messages, fleet thoughts existed — yet `rufio listen --as=B
// --catch-up` emitted ZERO bytes. Root cause: catch-up walked ONLY
// live/inbox/<agent>/, which is empty unless the dev daemon has been
// running to route copies; meanwhile the substrate state (channel
// messages, outbox broadcasts, pending summons) lived elsewhere and was
// never replayed.
//
// This test deliberately does NOT start `rufio dev` — it seeds the
// substrate dirs DIRECTLY in the on-disk shape the writers produce
// (live/channels/active/<ch>/messages/<id>.gdl, live/outbox/<agent>/
// <id>.gdl, live/summons/pending/<id>.gdl) and asserts that catch-up
// surfaces each of them. Before the fix the assertion fails with zero
// matching lines; after the fix all three types are emitted in one pass.
func TestListen_CatchUp_EmitsBroaderSubstrate(t *testing.T) {
	root := initProject(t)

	// NOTE: no live/inbox/agent-a created — repro of the "fresh substrate,
	// no dev daemon ever ran" path the vet hit.

	// Seed a pending summon (agent-b → agent-a).
	summonsDir := filepath.Join(root, "live", "summons", "pending")
	if err := os.MkdirAll(summonsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	summonBody := "@summon|id:s-139|from:agent-b|to:agent-a|topic:test:c|intent:hi|ts:2026-05-12T00:00:01Z|ttl:86400\n"
	if err := os.WriteFile(filepath.Join(summonsDir, "s-139.gdl"), []byte(summonBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// Seed an active channel with two messages (one from each agent).
	chanDir := filepath.Join(root, "live", "channels", "active", "ch-139", "messages")
	if err := os.MkdirAll(chanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaBody := "@channel|id:ch-139|opener:agent-b|target:agent-a|topic:test:c|intent:hi|created-at:2026-05-12T00:00:02Z\n"
	if err := os.WriteFile(filepath.Join(root, "live", "channels", "active", "ch-139", "meta.gdl"), []byte(metaBody), 0o644); err != nil {
		t.Fatal(err)
	}
	msgABody := "@channel-message|id:m-139-a|channel:ch-139|by:agent-a|content:hello-from-a|ts:2026-05-12T00:00:03Z\n"
	if err := os.WriteFile(filepath.Join(chanDir, "m-139-a.gdl"), []byte(msgABody), 0o644); err != nil {
		t.Fatal(err)
	}
	msgBBody := "@channel-message|id:m-139-b|channel:ch-139|by:agent-b|content:hello-from-b|ts:2026-05-12T00:00:04Z\n"
	if err := os.WriteFile(filepath.Join(chanDir, "m-139-b.gdl"), []byte(msgBBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// Seed a fleet-scoped thought in agent-b's outbox (broader than agent
	// scope → visible to anyone).
	outboxB := filepath.Join(root, "live", "outbox", "agent-b")
	if err := os.MkdirAll(outboxB, 0o755); err != nil {
		t.Fatal(err)
	}
	thoughtBody := "@thought|id:t-139|author:agent-b|type:hypothesis|subject:order:1|content:fleet-broadcast|scope:fleet|ts:2026-05-12T00:00:05Z|ttl:0\n"
	if err := os.WriteFile(filepath.Join(outboxB, "t-139.gdl"), []byte(thoughtBody), 0o644); err != nil {
		t.Fatal(err)
	}

	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cmd := exec.Command(binPath, "listen", "--catch-up")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "RUFIO_AGENT_ID=agent-a")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open /dev/null: %v", err)
	}
	defer devnull.Close()
	cmd.Stderr = devnull
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Signal(syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
		}
	}()

	// Drain stdout until we've seen all 4 expected records (1 summon + 2
	// channel-messages + 1 thought), or hit deadline. Catch-up is bounded
	// so the daemon proceeds to WatchAndEmit after — we then SIGTERM via
	// pollSubprocessStdout.
	seen := map[string]bool{}
	matched := pollSubprocessStdout(t, cmd, stdout, 10*time.Second, func(line string) bool {
		switch {
		case strings.Contains(line, `"_type":"summon"`) && strings.Contains(line, `"raw":"@summon|id:s-139`):
			seen["summon"] = true
		case strings.Contains(line, `"_type":"channel-message"`) && strings.Contains(line, "m-139-a"):
			seen["msg-a"] = true
		case strings.Contains(line, `"_type":"channel-message"`) && strings.Contains(line, "m-139-b"):
			seen["msg-b"] = true
		case strings.Contains(line, `"_type":"thought"`) && strings.Contains(line, "t-139"):
			seen["thought"] = true
		}
		return seen["summon"] && seen["msg-a"] && seen["msg-b"] && seen["thought"]
	})
	if !matched {
		t.Errorf("catch-up missing one or more substrate records — seen=%v (expected summon, msg-a, msg-b, thought)", seen)
	}
}

// --- Error-path tests (synchronous, no subprocess needed) ---

func TestListen_InvalidType_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"listen", "--types=bogus"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --types") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestListen_InvalidScope_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"listen", "--scope=global"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --scope") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestListen_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"listen"}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio listen:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}
