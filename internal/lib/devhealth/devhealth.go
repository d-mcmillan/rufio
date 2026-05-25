// Package devhealth is the daemon-supervision health surface (issue
// #154). The daemon (`rufio dev`) writes a single-line heartbeat record
// at .rufio/dev.heartbeat on a fixed cadence. Readers — `rufio dev
// --status` and `rufio fleet` — parse that file to render liveness.
//
// Heartbeat record format (one line, no trailing data):
//
//	@dev-heartbeat|pid:<pid>|started_at:<unix>|last_tick:<unix>|version:1
//
// This is the same gdl-like single-record shape used elsewhere in
// .rufio/ (cf. .rufio/locks/dev.pid). Atomic writes (tmp + rename) keep
// concurrent readers from observing a half-written file.
//
// The brief locks two constants:
//
//   - TickInterval = 5s    (how often the daemon updates last_tick)
//   - StaleThreshold = 30s (older than this ⇒ the daemon is presumed
//     dead and readers warn)
//
// Failure modes ALL fail closed — a missing, unreadable, or malformed
// heartbeat surfaces as `ok=false`, never an "ok" record. The cost of
// a false negative (warn unnecessarily) is far smaller than a false
// positive (say "daemon ok" when routing is silently stalled).
package devhealth

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// TickInterval is the cadence at which the daemon refreshes the
// heartbeat's last_tick field. Locked at 5s by the brief.
const TickInterval = 5 * time.Second

// StaleThreshold is the maximum age of last_tick before readers treat
// the daemon as stopped. Locked at 30s by the brief (6× TickInterval —
// tolerates a few missed ticks before warning).
const StaleThreshold = 30 * time.Second

// recordTypePrefix is the canonical leading token of a heartbeat line.
// Validating against it (rather than just splitting on `|`) prevents an
// arbitrary single-line file from being mis-parsed as a heartbeat.
const recordTypePrefix = "@dev-heartbeat"

// Heartbeat is the parsed in-memory shape of .rufio/dev.heartbeat.
type Heartbeat struct {
	PID       int
	StartedAt time.Time
	LastTick  time.Time
}

// HeartbeatPath returns the canonical heartbeat-file path under root.
// Exported so test fixtures and external readers can locate the file
// without re-deriving the layout.
func HeartbeatPath(root string) string {
	return filepath.Join(root, ".rufio", "dev.heartbeat")
}

// crashLogPath is the on-disk location for panic records. Persisted to
// .rufio/dev.crash.log (alongside dev.heartbeat) so the trace survives
// even when stderr was redirected somewhere lost (R14: the failing
// daemon was launched as `rufio dev > /tmp/r14-dev.log 2>&1 &`, whose
// stderr capture was inconclusive).
func crashLogPath(root string) string {
	return filepath.Join(root, ".rufio", "dev.crash.log")
}

// WriteHeartbeat persists the daemon's liveness record at
// HeartbeatPath(root). Writes are atomic — content lands in a sibling
// .tmp file then is renamed into place — so concurrent readers never
// see a partial record.
func WriteHeartbeat(root string, pid int, startedAt, lastTick time.Time) error {
	if err := os.MkdirAll(filepath.Join(root, ".rufio"), 0o755); err != nil {
		return fmt.Errorf("devhealth: mkdir .rufio: %w", err)
	}
	line := fmt.Sprintf(
		"%s|pid:%d|started_at:%d|last_tick:%d|version:1\n",
		recordTypePrefix, pid, startedAt.Unix(), lastTick.Unix(),
	)
	final := HeartbeatPath(root)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, []byte(line), 0o644); err != nil {
		return fmt.Errorf("devhealth: write tmp heartbeat: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		// Best-effort cleanup of the orphaned tmp so the directory
		// doesn't accumulate stale .tmp siblings on rename failure.
		_ = os.Remove(tmp)
		return fmt.Errorf("devhealth: rename heartbeat: %w", err)
	}
	return nil
}

// ReadHeartbeat reads and parses the heartbeat at root. Failure modes
// all return ok=false so readers can treat a missing/garbage file the
// same as "not running":
//
//   - file missing                  → ok=false, err=nil
//   - file unreadable               → ok=false, err=<read error>
//   - empty / wrong record type     → ok=false, err=nil
//   - any required field missing or non-numeric → ok=false, err=nil
//
// The only errors that propagate are filesystem-level (permission,
// I/O); parse failures are silent so a partially-corrupted heartbeat
// never crashes a reader.
func ReadHeartbeat(root string) (Heartbeat, bool, error) {
	path := HeartbeatPath(root)
	bs, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Heartbeat{}, false, nil
		}
		return Heartbeat{}, false, fmt.Errorf("devhealth: read heartbeat: %w", err)
	}
	line := strings.TrimSpace(string(bs))
	if line == "" {
		return Heartbeat{}, false, nil
	}
	parts := strings.Split(line, "|")
	if len(parts) < 2 || parts[0] != recordTypePrefix {
		return Heartbeat{}, false, nil
	}
	fields := map[string]string{}
	for _, p := range parts[1:] {
		kv := strings.SplitN(p, ":", 2)
		if len(kv) != 2 {
			continue
		}
		fields[kv[0]] = kv[1]
	}
	pidStr, ok1 := fields["pid"]
	startStr, ok2 := fields["started_at"]
	tickStr, ok3 := fields["last_tick"]
	if !ok1 || !ok2 || !ok3 {
		return Heartbeat{}, false, nil
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return Heartbeat{}, false, nil
	}
	startUnix, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil {
		return Heartbeat{}, false, nil
	}
	tickUnix, err := strconv.ParseInt(tickStr, 10, 64)
	if err != nil {
		return Heartbeat{}, false, nil
	}
	return Heartbeat{
		PID:       pid,
		StartedAt: time.Unix(startUnix, 0),
		LastTick:  time.Unix(tickUnix, 0),
	}, true, nil
}

// State is the high-level daemon-liveness conclusion.
type State int

const (
	// StateNotRunning — no heartbeat on disk (file missing) or the file
	// is unparseable. Treat as "the daemon is not running".
	StateNotRunning State = iota
	// StateRunning — heartbeat present and last_tick within StaleThreshold.
	StateRunning
	// StateStale — heartbeat present but last_tick older than
	// StaleThreshold. Daemon almost certainly dead/hung; routing stalled.
	StateStale
)

// String renders the state in a human-readable form. Used by the
// fleet/status renderers to keep the output text in one place.
func (s State) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StateStale:
		return "stale"
	default:
		return "not running"
	}
}

// StatusReport is the renderer-facing projection. PID/StartedAt/Uptime/
// LastTickAge are zero when State == StateNotRunning.
type StatusReport struct {
	State       State
	PID         int
	StartedAt   time.Time
	Uptime      time.Duration
	LastTick    time.Time
	LastTickAge time.Duration
}

// Status reads the heartbeat and computes a StatusReport relative to
// now. The now argument is injected (rather than calling time.Now
// directly) so tests can drive deterministic clock values — the
// "tick age" semantic depends on a stable now-vs-last_tick delta.
func Status(root string, now time.Time) StatusReport {
	hb, ok, _ := ReadHeartbeat(root)
	if !ok {
		return StatusReport{State: StateNotRunning}
	}
	age := now.Sub(hb.LastTick)
	state := StateRunning
	if age > StaleThreshold {
		state = StateStale
	}
	return StatusReport{
		State:       state,
		PID:         hb.PID,
		StartedAt:   hb.StartedAt,
		Uptime:      now.Sub(hb.StartedAt),
		LastTick:    hb.LastTick,
		LastTickAge: age,
	}
}

// WriteCrashLog appends a single crash entry to .rufio/dev.crash.log.
// Each entry is prefixed with a timestamp + the panic message, then the
// stack trace, then a separator line. Append-mode so a daemon that
// crashes repeatedly during a session leaves a history rather than only
// the last crash.
//
// stack is typically debug.Stack() captured inside the recover. When
// nil/empty the helper substitutes a no-trace placeholder so the entry
// still makes it to disk (better than silently dropping).
func WriteCrashLog(root string, message string, stack []byte) error {
	if err := os.MkdirAll(filepath.Join(root, ".rufio"), 0o755); err != nil {
		return fmt.Errorf("devhealth: mkdir .rufio: %w", err)
	}
	if len(stack) == 0 {
		stack = []byte("(no stack trace captured)\n")
	}
	entry := fmt.Sprintf(
		"---\nts:%s\nmsg:%s\nstack:\n%s\n",
		time.Now().UTC().Format(time.RFC3339Nano),
		message,
		string(stack),
	)
	f, err := os.OpenFile(crashLogPath(root), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("devhealth: open crash log: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(entry); err != nil {
		return fmt.Errorf("devhealth: write crash log: %w", err)
	}
	return nil
}

// CaptureStack returns the current goroutine's stack — exposed as a
// helper so callers don't need to import runtime/debug just to call
// debug.Stack() at panic-recovery time.
func CaptureStack() []byte {
	return debug.Stack()
}
