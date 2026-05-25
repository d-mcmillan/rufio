package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	"github.com/d-mcmillan/rufio/internal/lib/devhealth"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// writeAttention is a test helper that writes a minimal attention file
// for agent under root. Mirrors attention.Write but skips the lock-
// acquisition path so the test focuses on the read-side.
func writeAttention(t *testing.T, root, agent, intent string) {
	t.Helper()
	rec := attention.BuildRecord(agent, intent, "fleet", []string{"app:test"}, nil, "2026-05-14T00:00:00Z")
	dir := filepath.Join(root, "live", "attention")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := gdl.RenderLine(rec) + "\n"
	if err := os.WriteFile(filepath.Join(dir, agent+".gdl"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeThought is a test helper that writes a minimal @thought file at
// live/outbox/<agent>/<id>.gdl with the given unix-millis prefix.
func writeThought(t *testing.T, root, agent, prefix string, subject string) string {
	t.Helper()
	id := prefix + "-aaaaaa"
	rec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID:      id,
		Author:  agent,
		Type:    "observation",
		Subject: subject,
		Content: "test content",
		Scope:   "agent",
		TS:      "2026-05-14T00:00:00Z",
		TTL:     0,
	})
	dir := filepath.Join(root, "live", "outbox", agent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := gdl.RenderLine(rec) + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".gdl"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestInitialWalkEmpty verifies the walk returns an empty slice when
// no agents have attended and the outbox is empty.
func TestInitialWalkEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live", "attention"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "live", "outbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	msgs := InitialWalk(root)
	if len(msgs) != 0 {
		t.Fatalf("expected empty walk, got %d msgs", len(msgs))
	}
}

// TestInitialWalkMissingDirs verifies the walk tolerates a project
// where live/attention/ and live/outbox/ don't yet exist. Returns
// empty slice; never errors.
func TestInitialWalkMissingDirs(t *testing.T) {
	root := t.TempDir()
	msgs := InitialWalk(root)
	if len(msgs) != 0 {
		t.Fatalf("expected empty walk on missing dirs, got %d msgs", len(msgs))
	}
}

// TestInitialWalkTwoAgents verifies the walk projects one AttentionMsg
// per agent + one ThoughtMsg per recent thought.
func TestInitialWalkTwoAgents(t *testing.T) {
	root := t.TempDir()
	writeAttention(t, root, "alice", "investigating prod outage")
	writeAttention(t, root, "bob", "reviewing pr feedback")
	writeThought(t, root, "alice", "1700000000000", "deploy:prod")
	writeThought(t, root, "alice", "1700000001000", "deploy:prod")
	writeThought(t, root, "bob", "1700000002000", "pr:1234")

	msgs := InitialWalk(root)

	var attentions, thoughts int
	for _, m := range msgs {
		switch m.(type) {
		case AttentionMsg:
			attentions++
		case ThoughtMsg:
			thoughts++
		}
	}
	if attentions != 2 {
		t.Fatalf("expected 2 AttentionMsg, got %d", attentions)
	}
	if thoughts != 3 {
		t.Fatalf("expected 3 ThoughtMsg, got %d", thoughts)
	}
}

// TestInitialWalkCapsAtMax verifies the per-agent cap drops oldest
// thoughts beyond MaxRecentThoughtsPerAgent (sorted by id prefix
// descending — newest first).
func TestInitialWalkCapsAtMax(t *testing.T) {
	root := t.TempDir()
	writeAttention(t, root, "alice", "intent")
	// Write MaxRecentThoughtsPerAgent + 5 thoughts with monotonically
	// increasing unix-millis prefixes.
	total := MaxRecentThoughtsPerAgent + 5
	for i := 0; i < total; i++ {
		prefix := strconv.FormatInt(int64(1700000000000+i*1000), 10)
		writeThought(t, root, "alice", prefix, "deploy:prod")
	}

	msgs := InitialWalk(root)

	var thoughts int
	var firstThoughtID string
	for _, m := range msgs {
		if tm, ok := m.(ThoughtMsg); ok {
			thoughts++
			if firstThoughtID == "" {
				firstThoughtID = tm.Summary.ID
			}
		}
	}
	if thoughts != MaxRecentThoughtsPerAgent {
		t.Fatalf("expected %d thoughts after cap, got %d", MaxRecentThoughtsPerAgent, thoughts)
	}
	// First thought (sorted ts-descending) should be the highest prefix.
	wantPrefix := strconv.FormatInt(int64(1700000000000+(total-1)*1000), 10)
	if !strings.HasPrefix(firstThoughtID, wantPrefix) {
		t.Fatalf("expected first thought id to begin with %s, got %s", wantPrefix, firstThoughtID)
	}
}

// TestDaemonOnlineDetectsFreshHeartbeat verifies the v1.0.6.3 fix to
// DaemonOnline: it now reads .rufio/dev.heartbeat (the #154 daemon-
// supervision heartbeat) and treats the daemon as online iff last_tick
// is within devhealth.StaleThreshold. The pre-v1.0.6.3 implementation
// only stat'd .rufio/locks/dev.pid — which left the PID file behind
// when the daemon died ungracefully (SIGKILL, crash, OOM, pkill), so
// the TUI rendered `live`/`syncing` indicators against a dead daemon
// forever.
//
// Three branches the new implementation must satisfy:
//
//  1. No heartbeat file        → DaemonOnline=false (cold-start case)
//  2. Fresh heartbeat (now)    → DaemonOnline=true  (daemon ticking)
//  3. Stale heartbeat (>30s)   → DaemonOnline=false (dead-or-paused case)
func TestDaemonOnlineDetectsFreshHeartbeat(t *testing.T) {
	root := t.TempDir()

	// (1) No heartbeat at cold start ⇒ offline.
	if DaemonOnline(root) {
		t.Fatalf("expected DaemonOnline=false with no heartbeat file")
	}

	// (2) Fresh heartbeat ⇒ online.
	now := time.Now()
	if err := devhealth.WriteHeartbeat(root, 12345, now.Add(-1*time.Second), now); err != nil {
		t.Fatal(err)
	}
	if !DaemonOnline(root) {
		t.Fatalf("expected DaemonOnline=true with fresh heartbeat")
	}

	// (3) Stale heartbeat (last_tick older than StaleThreshold) ⇒
	// offline — the v1.0.6.3 fix's central case.
	stale := now.Add(-(devhealth.StaleThreshold + 5*time.Second))
	if err := devhealth.WriteHeartbeat(root, 12345, now.Add(-2*time.Minute), stale); err != nil {
		t.Fatal(err)
	}
	if DaemonOnline(root) {
		t.Fatalf("expected DaemonOnline=false with stale heartbeat (>StaleThreshold old) — this is the dead-daemon case")
	}
}

// TestNewWatcherDoesNotCrashOnMissingDirs verifies the watcher
// constructor creates the target directories if absent. Defence in
// depth — the TUI might be invoked before `rufio init` finishes.
func TestNewWatcherDoesNotCrashOnMissingDirs(t *testing.T) {
	root := t.TempDir()
	// Project root has only rufio.gdl marker (we don't actually need it
	// for the watcher — the watcher only uses live/*).
	w, _, stop, err := NewWatcher(root)
	if err != nil {
		t.Fatalf("NewWatcher returned err: %v", err)
	}
	defer stop()
	if w == nil {
		t.Fatalf("expected non-nil watcher")
	}
	// Directories should now exist.
	for _, d := range []string{
		filepath.Join(root, "live", "attention"),
		filepath.Join(root, "live", "outbox"),
	} {
		if _, err := os.Stat(d); err != nil {
			t.Fatalf("expected %s to exist after NewWatcher, got err %v", d, err)
		}
	}
}

// awaitWatcherMsg deterministically guards the fsnotify watcher by
// looping on BOTH sides of the pipe — the writer side AND the consumer
// side — until a message of the EXPECTED type arrives or a bounded
// deadline elapses.
//
// Two independent races motivate the two-sided loop (tasks #100 / #106
// / followup V8G3-M1):
//
//  1. Registration race (writer side): the old "sleep 50ms once, write
//     once, wait 2s" construct assumed fsnotify backend registration
//     always finishes inside the guessed 50ms window. Under heavy
//     parallel `-race` load (CI runs `go test -race ./...` with
//     packages concurrent) it may not, the single write is missed
//     forever, and the test times out. Fixed by re-issuing the
//     (idempotent) write on an interval: a later write is guaranteed
//     caught once registration completes, so it no longer depends on a
//     guessed window. (Same re-write-until-observed philosophy used by
//     test/integration/listen_test.go — the in-repo precedent.)
//
//  2. Truncate race (consumer side): the test write helpers use
//     non-atomic os.WriteFile (truncate-to-0 then write — deliberate
//     per writeAttention's own comment), and confirm.Append is an
//     O_APPEND write. fsnotify fires a Write on the truncate; the
//     watcher's consumeEvent synchronously re-reads the file
//     (loadAttention → attention.LoadOne → os.ReadFile). Under
//     concurrent contention that read can interleave between the
//     truncate and the content-write, LoadOne sees an empty/partial
//     file, and consumeEvent emits a transient WatcherErrMsg
//     (watch.go:289, emit=true) — a real product behaviour, NOT a bug
//     (the watcher must surface read errors; the next fs event self-
//     heals it). A consumer that drains exactly ONE message would
//     wrongly assert that transient error. So this helper keeps
//     re-draining the watcher cmd, DISCARDING transient WatcherErrMsg
//     (and any other not-yet-expected msg) as "not yet", until the
//     expected-type message arrives. This still genuinely guards the
//     watcher: a real regression (no event ever delivered, or the
//     wrong typed message) still fails at the bounded deadline with the
//     last transient error reported.
//
// Drain discipline: a single drainer goroutine invokes cmd()
// sequentially — one outstanding call at a time. cmd() is a closure
// over the watcher's single buffered `out` channel (watch.go
// watcherCmd); each invocation is exactly ONE receive, which mirrors
// production's watcherRearmWith "consume one ⇒ re-arm exactly once,
// never dropped, never duplicated" contract (app.go:978). We never run
// two cmd() calls concurrently (that would double-arm / race the
// channel). On the test's deferred stop(), runWatcher closes `out`,
// cmd() returns WatcherClosedMsg, and the drainer exits — no leak.
//
// `write` must be idempotent (re-writing the same content / re-
// appending the same record yields the same projected message). The
// returned message is the first one satisfying isExpected; the caller
// type-asserts it.
func awaitWatcherMsg(t *testing.T, cmd tea.Cmd, write func(), isExpected func(tea.Msg) bool) tea.Msg {
	t.Helper()

	// Single sequential drainer: forwards every watcher message to msgs.
	// quit stops it once the helper is done so it never blocks forever on
	// a cmd() that will only unblock at stop() (drained then via the
	// WatcherClosedMsg path). Buffered so the drainer can stay one step
	// ahead without blocking between forward and the next cmd().
	msgs := make(chan tea.Msg, 8)
	quit := make(chan struct{})
	go func() {
		for {
			m := cmd() // exactly one receive — the re-arm-once contract.
			select {
			case msgs <- m:
			case <-quit:
				return
			}
			if _, closed := m.(WatcherClosedMsg); closed {
				return // out closed (stop() called) — nothing more will come.
			}
		}
	}()
	defer close(quit)

	// Writer side: re-issue the write on a calm cadence. 100ms (not the
	// old 50ms) deliberately REDUCES truncate-window exposure while still
	// removing the registration race — the consumer-side discard loop is
	// what makes any residual transient error tolerable.
	const (
		retryInterval = 100 * time.Millisecond
		deadline      = 8 * time.Second
	)
	timeout := time.After(deadline)
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	var lastTransient tea.Msg
	write() // first attempt promptly — no fixed pre-sleep relied upon.
	for {
		select {
		case msg := <-msgs:
			if isExpected(msg) {
				return msg
			}
			// Not yet: a transient WatcherErrMsg from the truncate race,
			// or some other non-target msg. Record + keep draining.
			lastTransient = msg
		case <-timeout:
			t.Fatalf("timed out after %s waiting for the expected watcher "+
				"message (writer re-issued every %s; consumer discarded "+
				"non-target msgs); last non-target msg seen: %T %v",
				deadline, retryInterval, lastTransient, lastTransient)
			return nil
		case <-ticker.C:
			write() // a later write is guaranteed caught once fsnotify registers.
		}
	}
}

// TestNewWatcherEmitsAttentionMsg verifies that writing an attention
// file after the watcher is up surfaces an AttentionMsg via the
// returned tea.Cmd.
func TestNewWatcherEmitsAttentionMsg(t *testing.T) {
	root := t.TempDir()
	_, cmd, stop, err := NewWatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	msg := awaitWatcherMsg(t, cmd, func() {
		writeAttention(t, root, "alice", "watching")
	}, func(m tea.Msg) bool {
		_, ok := m.(AttentionMsg)
		return ok
	})

	att, ok := msg.(AttentionMsg)
	if !ok {
		t.Fatalf("expected AttentionMsg, got %T: %v", msg, msg)
	}
	if att.Agent != "alice" {
		t.Fatalf("expected agent=alice, got %q", att.Agent)
	}
}

// TestPollDaemonOnlineCmdNotNil is a smoke check — the cmd should be
// non-nil and produce a DaemonOnlineMsg when invoked.
func TestPollDaemonOnlineCmdNotNil(t *testing.T) {
	root := t.TempDir()
	cmd := PollDaemonOnline(root)
	if cmd == nil {
		t.Fatal("PollDaemonOnline returned nil cmd")
	}
	// tea.Tick blocks for the interval; we don't actually invoke it
	// here (would block 2s). Just verify the shape — the integration
	// path is covered by TestDaemonOnlineDetectsFreshHeartbeat + the
	// Model tests.
}

// TestWatchPathsIncludesConfirms is the PR-G1 retained-reader extension
// guard: live/confirms/ must be in the watched set so the v8 substrate
// chat's decision-row quorum updates live (the rest of WatchPaths is
// unchanged — attention + outbox stay).
func TestWatchPathsIncludesConfirms(t *testing.T) {
	root := "/some/root"
	got := WatchPaths(root)
	want := map[string]bool{
		filepath.Join(root, "live", "attention"): false,
		filepath.Join(root, "live", "outbox"):    false,
		filepath.Join(root, "live", "confirms"):  false,
	}
	for _, p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("unexpected watch path %q", p)
			continue
		}
		want[p] = true
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("WatchPaths missing %q", p)
		}
	}
}

// TestInitialWalkEmitsConfirmMsgs is the PR-G1 cold-start guard: a
// confirms file already on disk surfaces as a ConfirmMsg in the initial
// walk (so quorum renders immediately, daemon offline, before any live
// stream). Sorted, one per id.
func TestInitialWalkEmitsConfirmMsgs(t *testing.T) {
	root := t.TempDir()
	if err := confirm.Append(root, "1747-d29",
		confirm.BuildConfirm("1747-d29", "cursor", "", "2026-05-15T14:02:14Z")); err != nil {
		t.Fatal(err)
	}
	if err := confirm.Append(root, "1700-a01",
		confirm.BuildConfirm("1700-a01", "data-analyst", "", "2026-05-15T14:00:00Z")); err != nil {
		t.Fatal(err)
	}

	var ids []string
	for _, m := range InitialWalk(root) {
		if cm, ok := m.(ConfirmMsg); ok {
			ids = append(ids, cm.ThoughtID)
		}
	}
	want := []string{"1700-a01", "1747-d29"} // sorted
	if len(ids) != len(want) {
		t.Fatalf("got %d ConfirmMsg ids %v, want %v", len(ids), ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ConfirmMsg ids = %v, want %v (sorted)", ids, want)
		}
	}
}

// TestNewWatcherEmitsConfirmMsg verifies a @confirm appended after the
// watcher is up surfaces a ConfirmMsg (live quorum-dot update path).
func TestNewWatcherEmitsConfirmMsg(t *testing.T) {
	root := t.TempDir()
	_, cmd, stop, err := NewWatcher(root)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	// confirm.Append is append-only; re-appending the same record grows
	// the file with duplicate @confirm lines, but the watcher derives
	// ConfirmMsg.ThoughtID from the FILENAME only (consumeEvent in
	// watch.go does not parse confirms-file contents), so each append is
	// an idempotent retrigger that yields an identical ConfirmMsg —
	// safe for the re-write-until-observed loop. The consumer-side
	// discard loop in awaitWatcherMsg tolerates any transient
	// WatcherErrMsg here symmetrically with the AttentionMsg test.
	msg := awaitWatcherMsg(t, cmd, func() {
		if err := confirm.Append(root, "1747-d29",
			confirm.BuildConfirm("1747-d29", "cursor", "", "2026-05-15T14:02:14Z")); err != nil {
			t.Fatal(err)
		}
	}, func(m tea.Msg) bool {
		_, ok := m.(ConfirmMsg)
		return ok
	})

	cm, ok := msg.(ConfirmMsg)
	if !ok {
		t.Fatalf("expected ConfirmMsg, got %T: %v", msg, msg)
	}
	if cm.ThoughtID != "1747-d29" {
		t.Fatalf("ConfirmMsg.ThoughtID = %q, want 1747-d29", cm.ThoughtID)
	}
}

// TestStampFromID confirms the parser extracts the unix-millis prefix
// from a canonical thought id and falls back to 0 on malformed input.
func TestStampFromID(t *testing.T) {
	if got := stampFromID("1700000000000-abc123"); got != 1700000000000 {
		t.Fatalf("expected 1700000000000, got %d", got)
	}
	if got := stampFromID("malformed"); got != 0 {
		t.Fatalf("expected 0 for malformed id, got %d", got)
	}
	if got := stampFromID(""); got != 0 {
		t.Fatalf("expected 0 for empty id, got %d", got)
	}
}
