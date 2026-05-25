// Package cli — tests for the daemon supervision surface (#154).
//
// The daemon dies silently under stress (R14 vet). These tests lock the
// recovery + visibility surface that surrounds the existing runDev loop:
//
//   - `rufio dev --status` reports daemon liveness from .rufio/dev.heartbeat.
//   - `rufio dev` writes a heartbeat record on a configurable interval.
//   - `rufio dev`'s top-level panic-recover persists a crash record to
//     .rufio/dev.crash.log (so a redirect-lost stderr is recoverable).
//
// Unit-tested in isolation (no blocking watch loop) — same convention as
// dev_lock_test.go / dev_quiet_test.go.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/devhealth"
	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// TestNewDevCmd_StatusFlag asserts --status is wired on `rufio dev`,
// defaults false, no shorthand. Convention mirrors --force.
func TestNewDevCmd_StatusFlag(t *testing.T) {
	cmd := NewDevCmd("test")
	flag := cmd.Flags().Lookup("status")
	if flag == nil {
		t.Fatal("expected new --status flag (#154 daemon supervision)")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected --status default false, got %q", flag.DefValue)
	}
	if flag.Shorthand != "" {
		t.Errorf("--status should have no shorthand, got -%s", flag.Shorthand)
	}
}

// TestDev_Status_NoHeartbeat_ReportsNotRunning asserts the --status
// renderer prints "daemon: not running" (no heartbeat present) and
// exits 0. The output is a single human-readable line on stdout.
func TestDev_Status_NoHeartbeat_ReportsNotRunning(t *testing.T) {
	root := initSupervisionProject(t)
	out := captureStdout(t, func() {
		if err := runDevStatus(root, output.RenderOpts{}, time.Now); err != nil {
			t.Fatalf("runDevStatus: %v", err)
		}
	})
	if !strings.Contains(out, "daemon: not running") {
		t.Errorf("expected 'daemon: not running' in --status output, got:\n%s", out)
	}
}

// TestDev_Status_WithHeartbeat_ReportsRunningPidUptime asserts --status
// surfaces pid + uptime + last-tick age when a fresh heartbeat is on
// disk.
func TestDev_Status_WithHeartbeat_ReportsRunningPidUptime(t *testing.T) {
	root := initSupervisionProject(t)
	started := time.Unix(1700000000, 0)
	tick := time.Unix(1700000100, 0)
	if err := devhealth.WriteHeartbeat(root, 7777, started, tick); err != nil {
		t.Fatalf("seed heartbeat: %v", err)
	}
	now := func() time.Time { return tick.Add(3 * time.Second) }
	out := captureStdout(t, func() {
		if err := runDevStatus(root, output.RenderOpts{}, now); err != nil {
			t.Fatalf("runDevStatus: %v", err)
		}
	})
	if !strings.Contains(out, "daemon: ok") {
		t.Errorf("expected 'daemon: ok' in --status output, got:\n%s", out)
	}
	if !strings.Contains(out, "pid 7777") {
		t.Errorf("expected 'pid 7777' in --status output, got:\n%s", out)
	}
	if !strings.Contains(out, "uptime") {
		t.Errorf("expected 'uptime' field in --status output, got:\n%s", out)
	}
}

// TestDev_Status_StaleHeartbeat_ReportsStale asserts --status surfaces a
// STALE warning when the heartbeat is older than the threshold — this
// is the operator's "daemon died" signal.
func TestDev_Status_StaleHeartbeat_ReportsStale(t *testing.T) {
	root := initSupervisionProject(t)
	started := time.Unix(1700000000, 0)
	tick := time.Unix(1700000100, 0)
	if err := devhealth.WriteHeartbeat(root, 7777, started, tick); err != nil {
		t.Fatalf("seed heartbeat: %v", err)
	}
	now := func() time.Time { return tick.Add(60 * time.Second) }
	out := captureStdout(t, func() {
		if err := runDevStatus(root, output.RenderOpts{}, now); err != nil {
			t.Fatalf("runDevStatus: %v", err)
		}
	})
	if !strings.ContainsAny(out, "Ss") || !strings.Contains(strings.ToLower(out), "stale") {
		t.Errorf("expected 'stale' (case-insensitive) in --status output, got:\n%s", out)
	}
}

// TestDev_HeartbeatTickerWritesEvery5s asserts the periodic heartbeat
// writer fires on the configured interval and updates last_tick. The
// real interval (5s) is locked in devhealth.TickInterval but the runtime
// helper accepts a shorter interval for tests so this exercise is fast.
func TestDev_HeartbeatTickerWritesEvery5s(t *testing.T) {
	root := initSupervisionProject(t)
	stop := make(chan struct{})
	done := make(chan struct{})
	// started is deliberately ~3 seconds in the past so the second-
	// precision heartbeat persistence (Unix() drops nanos) can still
	// produce a LastTick STRICTLY after started even when the ticker
	// fires within the same wall-clock second as test launch.
	started := time.Now().Add(-3 * time.Second)
	go func() {
		defer close(done)
		runHeartbeatTicker(root, os.Getpid(), started, 30*time.Millisecond, stop)
	}()
	// Wait long enough for multiple ticks.
	time.Sleep(200 * time.Millisecond)
	close(stop)
	<-done

	hb, ok, err := devhealth.ReadHeartbeat(root)
	if err != nil {
		t.Fatalf("read heartbeat: %v", err)
	}
	if !ok {
		t.Fatal("expected heartbeat file after ticker ran")
	}
	if hb.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", hb.PID, os.Getpid())
	}
	// last_tick must be later than started_at (the ticker advanced it).
	// Compare at Unix-second precision because heartbeat persistence
	// drops sub-second precision (line format stores Unix seconds).
	if hb.LastTick.Unix() <= started.Unix() {
		t.Errorf("LastTick (%v, unix=%d) must be > start (%v, unix=%d); ticker did not update",
			hb.LastTick, hb.LastTick.Unix(), started, started.Unix())
	}
}

// TestDev_PanicRecover_WritesCrashLog asserts the panic-recover wrapper
// persists a crash record to .rufio/dev.crash.log and re-emits to
// stderr — the brief's "stderr was redirected somewhere lost" path.
func TestDev_PanicRecover_WritesCrashLog(t *testing.T) {
	root := initSupervisionProject(t)
	// recoverDevPanic is the exported (lowercase-package) helper called
	// via `defer recoverDevPanic(root)` at the top of runDev. We invoke
	// it directly from a goroutine that panics, mirroring the production
	// shape.
	caught := make(chan struct{})
	go func() {
		defer close(caught)
		defer recoverDevPanic(root)
		panic("synthetic test panic")
	}()
	<-caught

	bs, err := os.ReadFile(filepath.Join(root, ".rufio", "dev.crash.log"))
	if err != nil {
		t.Fatalf("expected crash log at .rufio/dev.crash.log: %v", err)
	}
	s := string(bs)
	if !strings.Contains(s, "synthetic test panic") {
		t.Errorf("crash log missing panic message:\n%s", s)
	}
	// A stack trace is the load-bearing diagnostic — grep for a frame
	// containing this test function's name.
	if !strings.Contains(s, "TestDev_PanicRecover_WritesCrashLog") {
		t.Errorf("crash log missing stack trace (no test-fn frame):\n%s", s)
	}
}

// initSupervisionProject creates a tempdir with rufio.gdl and .rufio/
// scaffolded — enough state for paths.FindProjectRoot to anchor on. We
// don't shell out to `rufio init` because that pulls in identity
// resolution we don't need here.
func initSupervisionProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(real, "rufio.gdl"), []byte("@config|version:1\n"), 0o644); err != nil {
		t.Fatalf("seed rufio.gdl: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(real, ".rufio"), 0o755); err != nil {
		t.Fatalf("mkdir .rufio: %v", err)
	}
	return real
}

// _silenceUnused keeps the import alive when this test file has only
// a subset of the functions referenced.
var _ = fmt.Sprintf
