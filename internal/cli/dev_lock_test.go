// Package cli — tests for the daemon singleton lock guard (dev.go
// devLockConflict + the runDev pre-check + --force escape hatch).
//
// Defect being guarded (#133, surfaced LIVE in a multi-agent demo):
// `rufio dev` writes .rufio/locks/dev.pid but never CHECKS it, so a
// second `rufio dev` (here: an agent spawning its own daemon on top of
// the running one) could start, duplicating/corrupting event
// processing. The fix: before writeDevPid clobbers the pidfile, inspect
// it and refuse to start if a live same-host daemon owns it.
//
// Contract these tests lock — devLockConflict(pidFile) is a pure,
// blocking-loop-free decision function (same unit-not-running-daemon
// convention the sibling dev_*_test.go files use):
//
//	(a) missing / unreadable / empty pidfile → NO conflict (first run,
//	    or scaffolded-but-no-daemon).
//	(b) pidfile naming a LIVE same-host pid → conflict (the running
//	    daemon). Proven with os.Getpid() of the test process — a
//	    guaranteed-live pid — and the real os.Hostname().
//	(c) pidfile naming a STALE/dead same-host pid → NO conflict (the
//	    previous daemon died; legitimate restart path — writeDevPid then
//	    overwrites it exactly as today).
//	(d) pidfile from a DIFFERENT hostname (even a live-looking pid) → NO
//	    conflict (a remote pid on a shared/networked FS is meaningless
//	    locally).
//	(e) malformed / legacy / partial pidfile content → NO conflict and
//	    NO panic (defensive: never crash the daemon on a bad lock file).
//	(f) the --force flag exists on NewDevCmd, defaults false, no
//	    shorthand, and is the documented bypass.
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writePid writes raw bytes to a fresh dev.pid under a temp .rufio/locks
// dir and returns the path. Mirrors the real on-disk location/layout so
// the guard exercises the same filepath the daemon uses.
func writePid(t *testing.T, contents string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".rufio", "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir locks: %v", err)
	}
	pidFile := filepath.Join(dir, "dev.pid")
	if err := os.WriteFile(pidFile, []byte(contents), 0o644); err != nil {
		t.Fatalf("write pidfile: %v", err)
	}
	return pidFile
}

// thisHost is the real local hostname, matching what writeDevPid records
// and what the guard compares against. Falls back to "unknown" exactly
// like writeDevPid so the test stays faithful even on hosts where
// os.Hostname() errors.
func thisHost(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

// deadPid returns a pid that is guaranteed NOT to be a running process
// on this host: it starts a trivial child, waits for it to exit, then
// returns its (now-reaped, dead) pid.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		// Fallback: a very high pid that is overwhelmingly unlikely to
		// be live. Still a valid "stale" case for the guard.
		return 0x7fffff00
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait() // reap — pid is now dead
	return pid
}

// TestDevLockConflict_MissingPidfile asserts (a): a pidfile that does
// not exist → no conflict (first run on a fresh project).
func TestDevLockConflict_MissingPidfile(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), ".rufio", "locks", "dev.pid")
	if _, conflict := devLockConflict(pidFile); conflict {
		t.Errorf("missing pidfile must NOT be a conflict (first run); got conflict")
	}
}

// TestDevLockConflict_EmptyPidfile asserts (a): an empty pidfile (e.g.
// a half-written/truncated file) → no conflict, no panic.
func TestDevLockConflict_EmptyPidfile(t *testing.T) {
	pidFile := writePid(t, "")
	if _, conflict := devLockConflict(pidFile); conflict {
		t.Errorf("empty pidfile must NOT be a conflict (defensive); got conflict")
	}
}

// TestDevLockConflict_LiveSameHost asserts (b): a pidfile naming a live
// pid on THIS host → conflict. os.Getpid() (the running test process)
// is a guaranteed-live pid and os.Hostname() is the real local host, so
// the pidfile describes a genuinely-running same-host daemon. The
// guard's "names our own pid" defensive branch must not mask a real
// conflict, so we drive it via devLockConflictForPID with a DIFFERENT
// ownPID — from the function's perspective the test process is "another
// live daemon" (precisely the #133 scenario: a second `rufio dev`
// process inspecting the first's pidfile).
func TestDevLockConflict_LiveSameHost(t *testing.T) {
	live := os.Getpid()
	pidFile := writePid(t, fmt.Sprintf("%s:%d:%d\n", thisHost(t), live, time.Now().Unix()))

	got, conflict := devLockConflictForPID(pidFile, live+1) // pretend we're a different process
	if !conflict {
		t.Fatalf("a live same-host pid (%d) must be a conflict; got no conflict", live)
	}
	if got != live {
		t.Errorf("conflict pid = %d, want %d (the live owner)", got, live)
	}
}

// TestDevLockConflict_StaleSameHost asserts (c): a pidfile naming a dead
// pid on THIS host → no conflict (previous daemon died; legitimate
// restart — writeDevPid overwrites it as today). This is the regression
// guard for the "daemon crashed, restart it" path.
func TestDevLockConflict_StaleSameHost(t *testing.T) {
	dead := deadPid(t)
	pidFile := writePid(t, fmt.Sprintf("%s:%d:%d\n", thisHost(t), dead, time.Now().Unix()))

	if _, conflict := devLockConflict(pidFile); conflict {
		t.Errorf("a stale/dead same-host pid (%d) must NOT be a conflict (restart path); got conflict", dead)
	}
}

// TestDevLockConflict_DifferentHost asserts (d): a pidfile from another
// machine — even with a live-LOOKING pid (we use this process's own pid
// so it IS live, but under a foreign hostname) — is NOT a conflict. A
// remote pid on a shared/networked FS is meaningless locally.
func TestDevLockConflict_DifferentHost(t *testing.T) {
	live := os.Getpid()
	pidFile := writePid(t, fmt.Sprintf("some-other-host-xyz:%d:%d\n", live, time.Now().Unix()))

	if _, conflict := devLockConflict(pidFile); conflict {
		t.Errorf("a pid from a DIFFERENT host must NOT be a conflict (remote/networked FS); got conflict")
	}
}

// TestDevLockConflict_Malformed asserts (e): assorted malformed / legacy
// / partial pidfile contents → no conflict and NO panic. Each case must
// fail safe (proceed) rather than crash the daemon.
func TestDevLockConflict_Malformed(t *testing.T) {
	cases := []struct {
		name     string
		contents string
	}{
		{"no-colons-garbage", "this is not a pidfile\n"},
		{"only-hostname", thisHost(t) + "\n"},
		{"hostname-and-empty-pid", thisHost(t) + "::\n"},
		{"non-numeric-pid", thisHost(t) + ":notanumber:123\n"},
		{"negative-pid", thisHost(t) + ":-5:123\n"},
		{"zero-pid", thisHost(t) + ":0:123\n"},
		{"missing-ts-field", thisHost(t) + ":12345\n"},
		{"just-colons", ":::\n"},
		{"whitespace-only", "   \n"},
		{"trailing-newline-only", "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pidFile := writePid(t, tc.contents)
			// Must not panic; must report no conflict.
			if _, conflict := devLockConflict(pidFile); conflict {
				t.Errorf("malformed pidfile %q must NOT be a conflict (defensive); got conflict", tc.contents)
			}
		})
	}
}

// TestDevLockConflict_SelfPid asserts the defensive guard: a pidfile
// naming THIS very process is not treated as a conflict (a daemon must
// never refuse to start because the lock names itself).
func TestDevLockConflict_SelfPid(t *testing.T) {
	pidFile := writePid(t, fmt.Sprintf("%s:%d:%d\n", thisHost(t), os.Getpid(), time.Now().Unix()))
	// NOTE: os.Getpid() here is the test binary, which is genuinely
	// live; the "same as our pid" defensive branch must win over the
	// liveness check so the function never self-conflicts.
	if _, conflict := devLockConflictForPID(pidFile, os.Getpid()); conflict {
		t.Errorf("a pidfile naming our own pid must NOT be a conflict (defensive); got conflict")
	}
}

// TestDevLockConflict_NoTrailingNewline asserts the parser tolerates a
// pidfile written WITHOUT the trailing newline (defensive: don't depend
// on the exact byte layout of the writer).
func TestDevLockConflict_NoTrailingNewline(t *testing.T) {
	live := os.Getpid()
	pidFile := writePid(t, fmt.Sprintf("%s:%d:%d", thisHost(t), live, time.Now().Unix())) // no \n
	got, conflict := devLockConflictForPID(pidFile, live+1)                               // see TestDevLockConflict_LiveSameHost
	if !conflict {
		t.Fatalf("live same-host pid without trailing newline must still be a conflict; got none")
	}
	if got != live {
		t.Errorf("conflict pid = %d, want %d", got, live)
	}
}

// TestNewDevCmd_ForceFlag asserts (f): the --force escape hatch exists
// on `rufio dev`, defaults false, and has no shorthand (consistent with
// --log/--no-color which also avoid colliding with global shorthands).
func TestNewDevCmd_ForceFlag(t *testing.T) {
	cmd := NewDevCmd("test")

	force := cmd.Flags().Lookup("force")
	if force == nil {
		t.Fatal("expected new --force flag (documented singleton-guard bypass)")
	}
	if force.DefValue != "false" {
		t.Errorf("expected --force default false, got %q", force.DefValue)
	}
	if force.Shorthand != "" {
		t.Errorf("--force should have no shorthand, got -%s", force.Shorthand)
	}
	if !strings.Contains(force.Usage, "guard") && !strings.Contains(force.Usage, "singleton") {
		t.Errorf("--force help should describe bypassing the singleton guard; got %q", force.Usage)
	}
}
