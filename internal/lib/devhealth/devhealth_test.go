// Package devhealth — unit tests for the daemon-supervision health surface
// (issue #154). The daemon writes a single heartbeat record at
// .rufio/dev.heartbeat every N seconds; readers (rufio dev --status,
// rufio fleet) parse it to render daemon liveness.
//
// These tests lock the contract BEFORE the implementation lands (RED
// first per the task brief). Format is a single gdl-like line:
//
//	@dev-heartbeat|pid:<pid>|started_at:<unix>|last_tick:<unix>|version:1
//
// Atomic write (tmp + rename) so a partially-written file never confuses
// a reader.
package devhealth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHeartbeatFile_PathUnderRufioDir asserts the canonical on-disk
// location relative to a project root. Locked so readers (fleet,
// --status) can hard-code the path.
func TestHeartbeatFile_PathUnderRufioDir(t *testing.T) {
	root := t.TempDir()
	got := HeartbeatPath(root)
	want := filepath.Join(root, ".rufio", "dev.heartbeat")
	if got != want {
		t.Errorf("HeartbeatPath = %q, want %q", got, want)
	}
}

// TestWriteHeartbeat_CreatesFileWithExpectedShape asserts an initial
// write produces the canonical single-line record with all four fields
// in the locked order.
func TestWriteHeartbeat_CreatesFileWithExpectedShape(t *testing.T) {
	root := t.TempDir()
	started := time.Unix(1700000000, 0)
	tick := time.Unix(1700000045, 0)
	if err := WriteHeartbeat(root, 4242, started, tick); err != nil {
		t.Fatalf("WriteHeartbeat: %v", err)
	}
	bs, err := os.ReadFile(HeartbeatPath(root))
	if err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}
	line := strings.TrimSpace(string(bs))
	// Strict shape: exact field order, exact prefix, exact separator.
	want := "@dev-heartbeat|pid:4242|started_at:1700000000|last_tick:1700000045|version:1"
	if line != want {
		t.Errorf("heartbeat line\n got: %q\nwant: %q", line, want)
	}
}

// TestWriteHeartbeat_CreatesRufioDirIfMissing asserts the writer
// scaffolds .rufio/ defensively — the daemon may be run before the dir
// exists in some edge paths.
func TestWriteHeartbeat_CreatesRufioDirIfMissing(t *testing.T) {
	root := t.TempDir()
	// No .rufio/ exists yet.
	if err := WriteHeartbeat(root, 1, time.Unix(1, 0), time.Unix(2, 0)); err != nil {
		t.Fatalf("WriteHeartbeat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".rufio")); err != nil {
		t.Errorf("expected .rufio/ created defensively, got %v", err)
	}
}

// TestWriteHeartbeat_AtomicTempThenRename asserts the writer NEVER
// leaves a partial heartbeat file readable to consumers — partial writes
// must be invisible. We assert this by checking that after every write
// there is at most one heartbeat file (no leftover .tmp sibling) AND the
// file is a complete record.
func TestWriteHeartbeat_AtomicTempThenRename(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		if err := WriteHeartbeat(root, 1, time.Unix(1, 0), time.Unix(int64(2+i), 0)); err != nil {
			t.Fatalf("WriteHeartbeat iter %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, ".rufio"))
	if err != nil {
		t.Fatalf("read .rufio: %v", err)
	}
	var hb, tmp int
	for _, e := range entries {
		switch {
		case e.Name() == "dev.heartbeat":
			hb++
		case strings.HasPrefix(e.Name(), "dev.heartbeat") && strings.Contains(e.Name(), "tmp"):
			tmp++
		}
	}
	if hb != 1 {
		t.Errorf("want exactly 1 dev.heartbeat, got %d", hb)
	}
	if tmp != 0 {
		t.Errorf("want 0 leftover tmp siblings, got %d (atomic-rename leak)", tmp)
	}
}

// TestReadHeartbeat_Missing returns ok=false. Locked — readers depend on
// "not running" being signalled by Read.
func TestReadHeartbeat_Missing(t *testing.T) {
	root := t.TempDir()
	hb, ok, err := ReadHeartbeat(root)
	if err != nil {
		t.Fatalf("ReadHeartbeat: %v", err)
	}
	if ok {
		t.Errorf("missing heartbeat must return ok=false; got hb=%+v", hb)
	}
}

// TestReadHeartbeat_Roundtrip asserts that what WriteHeartbeat wrote is
// what ReadHeartbeat parses. PID, started_at, last_tick all preserved.
func TestReadHeartbeat_Roundtrip(t *testing.T) {
	root := t.TempDir()
	started := time.Unix(1700000000, 0)
	tick := time.Unix(1700000045, 0)
	if err := WriteHeartbeat(root, 4242, started, tick); err != nil {
		t.Fatalf("write: %v", err)
	}
	hb, ok, err := ReadHeartbeat(root)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true after write")
	}
	if hb.PID != 4242 {
		t.Errorf("PID = %d, want 4242", hb.PID)
	}
	if !hb.StartedAt.Equal(started) {
		t.Errorf("StartedAt = %v, want %v", hb.StartedAt, started)
	}
	if !hb.LastTick.Equal(tick) {
		t.Errorf("LastTick = %v, want %v", hb.LastTick, tick)
	}
}

// TestReadHeartbeat_MalformedReturnsOkFalse asserts assorted malformed
// inputs do NOT panic and do NOT fake an "ok" record. They surface as
// ok=false (treated by readers as "not running / unreadable") so a
// corrupted heartbeat fails closed, not open.
func TestReadHeartbeat_MalformedReturnsOkFalse(t *testing.T) {
	cases := []struct {
		name     string
		contents string
	}{
		{"empty", ""},
		{"whitespace-only", "   \n"},
		{"wrong-record-type", "@something-else|pid:1|started_at:1|last_tick:2|version:1\n"},
		{"missing-pid", "@dev-heartbeat|started_at:1|last_tick:2|version:1\n"},
		{"non-numeric-pid", "@dev-heartbeat|pid:abc|started_at:1|last_tick:2|version:1\n"},
		{"missing-last-tick", "@dev-heartbeat|pid:1|started_at:1|version:1\n"},
		{"garbage", "this is not a heartbeat at all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".rufio"), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, ".rufio", "dev.heartbeat"), []byte(tc.contents), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			_, ok, err := ReadHeartbeat(root)
			if err != nil {
				t.Fatalf("ReadHeartbeat panicked/errored on malformed input: %v", err)
			}
			if ok {
				t.Errorf("malformed heartbeat %q must return ok=false; got ok=true", tc.contents)
			}
		})
	}
}

// TestStatus_NotRunning_WhenNoHeartbeat asserts the high-level Status
// helper reports State=StateNotRunning when the file is missing. This is
// what `rufio dev --status` calls into.
func TestStatus_NotRunning_WhenNoHeartbeat(t *testing.T) {
	root := t.TempDir()
	now := time.Unix(1700000999, 0)
	st := Status(root, now)
	if st.State != StateNotRunning {
		t.Errorf("State = %v, want %v (no heartbeat)", st.State, StateNotRunning)
	}
}

// TestStatus_Fresh_WhenTickWithinThreshold asserts a tick newer than
// StaleThreshold reports StateRunning with computed uptime + last-tick
// age.
func TestStatus_Fresh_WhenTickWithinThreshold(t *testing.T) {
	root := t.TempDir()
	started := time.Unix(1700000000, 0)
	tick := time.Unix(1700000100, 0)
	now := tick.Add(4 * time.Second) // 4s old — well within fresh window
	if err := WriteHeartbeat(root, 7777, started, tick); err != nil {
		t.Fatalf("write: %v", err)
	}
	st := Status(root, now)
	if st.State != StateRunning {
		t.Errorf("State = %v, want %v", st.State, StateRunning)
	}
	if st.PID != 7777 {
		t.Errorf("PID = %d, want 7777", st.PID)
	}
	if st.Uptime != now.Sub(started) {
		t.Errorf("Uptime = %v, want %v", st.Uptime, now.Sub(started))
	}
	if st.LastTickAge != now.Sub(tick) {
		t.Errorf("LastTickAge = %v, want %v", st.LastTickAge, now.Sub(tick))
	}
}

// TestStatus_Stale_WhenTickBeyondThreshold asserts a tick older than
// StaleThreshold reports StateStale (daemon not writing heartbeats →
// almost certainly dead / hung).
func TestStatus_Stale_WhenTickBeyondThreshold(t *testing.T) {
	root := t.TempDir()
	started := time.Unix(1700000000, 0)
	tick := time.Unix(1700000100, 0)
	// 47s ago — beyond the 30s StaleThreshold the brief specifies.
	now := tick.Add(47 * time.Second)
	if err := WriteHeartbeat(root, 7777, started, tick); err != nil {
		t.Fatalf("write: %v", err)
	}
	st := Status(root, now)
	if st.State != StateStale {
		t.Errorf("State = %v, want %v (47s > StaleThreshold)", st.State, StateStale)
	}
	if st.LastTickAge != 47*time.Second {
		t.Errorf("LastTickAge = %v, want 47s", st.LastTickAge)
	}
}

// TestStaleThreshold_Is30Seconds locks the threshold per the brief:
// the brief specifies "30s" — codify it as the contract so a future
// drive-by change is caught.
func TestStaleThreshold_Is30Seconds(t *testing.T) {
	if StaleThreshold != 30*time.Second {
		t.Errorf("StaleThreshold = %v, want 30s (locked by brief)", StaleThreshold)
	}
}

// TestTickInterval_Is5Seconds locks the brief's "every 5 seconds" tick
// cadence. The runtime uses TickInterval; tests inject a shorter
// override via the helper in the dev tests.
func TestTickInterval_Is5Seconds(t *testing.T) {
	if TickInterval != 5*time.Second {
		t.Errorf("TickInterval = %v, want 5s (locked by brief)", TickInterval)
	}
}

// TestWriteCrashLog_AppendsToRufioDir asserts WriteCrashLog appends (not
// truncates) — multiple crashes must accumulate so an operator can see
// history. File lives at .rufio/dev.crash.log per the brief.
func TestWriteCrashLog_AppendsToRufioDir(t *testing.T) {
	root := t.TempDir()
	if err := WriteCrashLog(root, "first crash", []byte("stack-1")); err != nil {
		t.Fatalf("crash 1: %v", err)
	}
	if err := WriteCrashLog(root, "second crash", []byte("stack-2")); err != nil {
		t.Fatalf("crash 2: %v", err)
	}
	bs, err := os.ReadFile(filepath.Join(root, ".rufio", "dev.crash.log"))
	if err != nil {
		t.Fatalf("read crash log: %v", err)
	}
	s := string(bs)
	if !strings.Contains(s, "first crash") {
		t.Errorf("missing first crash record in log:\n%s", s)
	}
	if !strings.Contains(s, "second crash") {
		t.Errorf("missing second crash record in log (should append, not truncate):\n%s", s)
	}
	if !strings.Contains(s, "stack-1") || !strings.Contains(s, "stack-2") {
		t.Errorf("crash log should include both stack traces:\n%s", s)
	}
}
