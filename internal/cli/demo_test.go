package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

// seedProject creates a minimal Rufio project at t.TempDir():
// rufio.gdl + the live/ subdirs the inbox-check needs to traverse.
// Mirrors init.go's layout for the parts checkOrResetLiveState reads.
func seedProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("evalsymlinks: %v", err)
	}
	root = real
	if err := os.WriteFile(filepath.Join(root, "rufio.gdl"), []byte("@config|name:test|version:1\n"), 0o644); err != nil {
		t.Fatalf("write rufio.gdl: %v", err)
	}
	for _, sub := range demoSubdirsToReset {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".rufio", "locks"), 0o755); err != nil {
		t.Fatalf("mkdir .rufio/locks: %v", err)
	}
	return root
}

// TestCheckOrResetLiveState_EmptyInbox_OK verifies the pre-flight
// happy path: an empty inbox returns nil without --reset.
func TestCheckOrResetLiveState_EmptyInbox_OK(t *testing.T) {
	root := seedProject(t)
	if err := checkOrResetLiveState(root, false); err != nil {
		t.Fatalf("expected nil; got %v", err)
	}
}

// TestCheckOrResetLiveState_MissingInbox_OK verifies that a fresh
// project that never had a live/inbox directory is also fine.
func TestCheckOrResetLiveState_MissingInbox_OK(t *testing.T) {
	root := seedProject(t)
	if err := os.RemoveAll(filepath.Join(root, "live", "inbox")); err != nil {
		t.Fatalf("rm inbox: %v", err)
	}
	if err := checkOrResetLiveState(root, false); err != nil {
		t.Fatalf("expected nil; got %v", err)
	}
}

// TestCheckOrResetLiveState_NonEmptyInboxNoReset_Errors confirms the
// guard fires when inbox contains a routed thought and --reset is off.
// The error must be *DemoStateError so HandleError emits exit 2.
func TestCheckOrResetLiveState_NonEmptyInboxNoReset_Errors(t *testing.T) {
	root := seedProject(t)
	inboxAgentDir := filepath.Join(root, "live", "inbox", "cursor")
	if err := os.MkdirAll(inboxAgentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxAgentDir, "x.gdl"), []byte("@thought|x:1\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := checkOrResetLiveState(root, false)
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	var demoErr *rufioerr.DemoStateError
	if !errors.As(err, &demoErr) {
		t.Fatalf("error type = %T, want *DemoStateError", err)
	}
	if demoErr.ExitCode() != 2 {
		t.Errorf("ExitCode = %d, want 2", demoErr.ExitCode())
	}
	if !strings.Contains(demoErr.Error(), "live/inbox is non-empty") {
		t.Errorf("error message = %q, want contains 'live/inbox is non-empty'", demoErr.Error())
	}
}

// TestCheckOrResetLiveState_StrayFileInInbox_Errors confirms that a
// file directly at live/inbox/ (not under an agent subdir) also trips
// the guard. The error sticks to the existing wording — the operator
// pivots to --reset either way.
func TestCheckOrResetLiveState_StrayFileInInbox_Errors(t *testing.T) {
	root := seedProject(t)
	if err := os.WriteFile(filepath.Join(root, "live", "inbox", "stray.gdl"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := checkOrResetLiveState(root, false)
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	var demoErr *rufioerr.DemoStateError
	if !errors.As(err, &demoErr) {
		t.Fatalf("error type = %T, want *DemoStateError", err)
	}
}

// TestCheckOrResetLiveState_NonEmptyInboxReset_Nukes confirms that
// with --reset the demo subdirs are wiped clean (and re-created).
func TestCheckOrResetLiveState_NonEmptyInboxReset_Nukes(t *testing.T) {
	root := seedProject(t)
	// Seed enough stuff to be sure the wipe is real.
	if err := os.MkdirAll(filepath.Join(root, "live", "inbox", "cursor"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "live", "inbox", "cursor", "x.gdl"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "live", "promoted", "y.gdl"), []byte("y"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := checkOrResetLiveState(root, true); err != nil {
		t.Fatalf("expected nil; got %v", err)
	}
	// Each subdir must still exist (re-created), and be empty.
	for _, sub := range demoSubdirsToReset {
		abs := filepath.Join(root, sub)
		info, err := os.Stat(abs)
		if err != nil {
			t.Errorf("subdir %s missing after reset: %v", sub, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a dir after reset", sub)
			continue
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			t.Errorf("readdir %s: %v", sub, err)
			continue
		}
		if len(entries) != 0 {
			t.Errorf("%s not empty after reset: %d entries", sub, len(entries))
		}
	}
}

// TestSeedDemoIdentities_WritesSwarmRecord asserts that the two
// scripted demo agents land in .rufio/swarm.local.gdl as @spawned
// records. Both lines must be present; the exact ordering is
// guaranteed by swarm.Append's preserved input order.
func TestSeedDemoIdentities_WritesSwarmRecord(t *testing.T) {
	root := seedProject(t)
	if err := seedDemoIdentities(root); err != nil {
		t.Fatalf("seedDemoIdentities: %v", err)
	}
	bs, err := os.ReadFile(filepath.Join(root, ".rufio", "swarm.local.gdl"))
	if err != nil {
		t.Fatalf("read swarm.local.gdl: %v", err)
	}
	got := string(bs)
	for _, agent := range []string{demoAgentClaude, demoAgentCursor} {
		if !strings.Contains(got, "agent:"+agent) {
			t.Errorf("missing agent:%s in:\n%s", agent, got)
		}
	}
	if !strings.Contains(got, "persona:"+demoPersonaTag) {
		t.Errorf("missing persona:%s in:\n%s", demoPersonaTag, got)
	}
}

// TestSeedDemoIdentities_Idempotent asserts a second seed call does
// not error (swarm.Append returns the duplicates in `skipped`) and
// the file still has exactly two records.
func TestSeedDemoIdentities_Idempotent(t *testing.T) {
	root := seedProject(t)
	if err := seedDemoIdentities(root); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if err := seedDemoIdentities(root); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	bs, err := os.ReadFile(filepath.Join(root, ".rufio", "swarm.local.gdl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(bs)
	// Each agent line should appear exactly once even though we
	// invoked seedDemoIdentities twice.
	for _, agent := range []string{demoAgentClaude, demoAgentCursor} {
		count := strings.Count(got, "agent:"+agent)
		if count != 1 {
			t.Errorf("agent:%s appears %d times, want 1:\n%s", agent, count, got)
		}
	}
}

// TestWaitForDaemonPid_Appears_OK confirms that when the pid file
// shows up before the deadline, waitForDaemonPid returns nil. We
// drop the file in via a goroutine after a short delay.
func TestWaitForDaemonPid_Appears_OK(t *testing.T) {
	root := seedProject(t)
	pidDir := filepath.Join(root, ".rufio", "locks")
	pidFile := filepath.Join(pidDir, "dev.pid")
	go func() {
		time.Sleep(80 * time.Millisecond)
		_ = os.WriteFile(pidFile, []byte("host:1234:0\n"), 0o644)
	}()
	if err := waitForDaemonPid(root, 2*time.Second); err != nil {
		t.Fatalf("expected nil; got %v", err)
	}
}

// TestWaitForDaemonPid_Timeout_Errors confirms the timeout path
// surfaces *DemoStateError with the right reason.
func TestWaitForDaemonPid_Timeout_Errors(t *testing.T) {
	root := seedProject(t)
	// Never write the pid file.
	err := waitForDaemonPid(root, 150*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error; got nil")
	}
	var demoErr *rufioerr.DemoStateError
	if !errors.As(err, &demoErr) {
		t.Fatalf("error type = %T, want *DemoStateError", err)
	}
	if !strings.Contains(demoErr.Error(), "dev.pid") {
		t.Errorf("error %q does not mention dev.pid", demoErr.Error())
	}
}

// TestCleanupChildren_SIGTERMTerminatesShortSubprocess spawns a
// short-lived `sleep` and asserts cleanup tears it down before the
// grace period runs out. We use a sleep that's much longer than the
// grace so the assertion is meaningful (the process is alive at
// SIGTERM-time).
func TestCleanupChildren_SIGTERMTerminatesShortSubprocess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = newProcessGroupAttr()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	start := time.Now()
	cleanupChildren([]*exec.Cmd{cmd}, 3*time.Second)
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Errorf("cleanupChildren took %s, want <2s (SIGTERM honoured)", elapsed)
	}
	// At this point the process must have exited. cmd.ProcessState is
	// populated by cmd.Wait() inside cleanupChildren.
	if cmd.ProcessState == nil {
		t.Fatal("cmd.ProcessState is nil after cleanup; process not reaped")
	}
	if cmd.ProcessState.Exited() && cmd.ProcessState.ExitCode() == 0 {
		t.Errorf("expected non-zero exit (signalled), got 0")
	}
}

// TestCleanupChildren_SIGKILLOnHoldout spawns a process that traps
// SIGTERM and ignores it. cleanupChildren must escalate to SIGKILL
// after the grace window. We use a short grace (300ms) to keep the
// test snappy.
func TestCleanupChildren_SIGKILLOnHoldout(t *testing.T) {
	// `sh -c 'trap "" TERM; sleep 30'` ignores SIGTERM.
	cmd := exec.Command("sh", "-c", `trap "" TERM; sleep 30`)
	cmd.SysProcAttr = newProcessGroupAttr()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	start := time.Now()
	cleanupChildren([]*exec.Cmd{cmd}, 300*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Errorf("cleanupChildren took %s, want <2s (SIGKILL escalation)", elapsed)
	}
	// Give the goroutine a moment to reap. cmd.Wait may not have
	// observed the exit yet because the deadline branch returns
	// without draining further done sends. We poll for the kill.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		// signal 0 returns nil if the process exists.
		if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
			return // process gone — SIGKILL did its job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("process still alive 2s after cleanup; SIGKILL didn't fire")
	_ = cmd.Process.Kill()
}

// TestCleanupChildren_NilSliceIsNoop is the defensive case: a nil
// or empty slice must not panic.
func TestCleanupChildren_NilSliceIsNoop(t *testing.T) {
	cleanupChildren(nil, 1*time.Second)
	cleanupChildren([]*exec.Cmd{}, 1*time.Second)
	cleanupChildren([]*exec.Cmd{nil}, 1*time.Second)
}

// TestWriteDevPid_CreatesFile asserts the dev daemon's pid-file
// helper produces a parseable record. We don't test the whole dev
// command (covered by integration tests) — just the file shape so
// the design-line-228 contract is documented in code.
func TestWriteDevPid_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, ".rufio", "locks", "dev.pid")
	if err := writeDevPid(pidFile); err != nil {
		t.Fatalf("writeDevPid: %v", err)
	}
	bs, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pidfile: %v", err)
	}
	got := strings.TrimSpace(string(bs))
	// Expect `<host>:<pid>:<ts>` shape — three colon-separated
	// segments. We don't pin the exact values (host varies by box).
	parts := strings.Split(got, ":")
	if len(parts) != 3 {
		t.Errorf("pidfile body = %q, want three colon-separated segments", got)
	}
}
