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

// TestDev_TTLSweeper_CatchUp_MovesExpired spawns the real `rufio dev` daemon
// after seeding an already-expired @thought (ttl=1, then sleep 1.5s) in
// live/outbox/agent-a/. The daemon's startup catch-up sweep must atomically
// move the expired file to live/expired/agent-a/<id>.gdl while preserving
// the original record bytes (the sweep moves; it does not rewrite).
//
// We exercise the catch-up path rather than waiting for the 10s ticker
// because (a) it is the same engine call — ttlsweep.Sweep(root, time.Now,
// os.Stderr) — so a regression in the engine surfaces here, and (b) a
// tick-based test would add 10+ seconds of unavoidable latency to CI for
// no extra coverage. The ticker wiring itself is exercised by the unit
// test in internal/cli/dev_ttl_test.go.
func TestDev_TTLSweeper_CatchUp_MovesExpired(t *testing.T) {
	root := initProject(t)

	// Seed a hypothesis-thought from agent-a with TTL=1s. We need the
	// generated thought-id to assert against the moved file, so use the
	// same outbox glob-diff pattern as dev_autopromote_e2e_test.go
	// (mustWriteThought hardcodes customer:1/scope=agent and does not
	// thread a ttl, so we issue the CLI directly here).
	outboxPattern := filepath.Join(root, "live", "outbox", "agent-a", "*.gdl")
	before, _ := filepath.Glob(outboxPattern)
	beforeSet := make(map[string]bool, len(before))
	for _, p := range before {
		beforeSet[p] = true
	}
	tres := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=customer:5821",
		"--content=ephemeral", "--scope=fleet", "--ttl=1",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if tres.Code != 0 {
		t.Fatalf("think: exit=%d stderr=%q", tres.Code, tres.Stderr)
	}
	after, _ := filepath.Glob(outboxPattern)
	var fresh []string
	for _, p := range after {
		if !beforeSet[p] {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) != 1 {
		t.Fatalf("seed think did not produce exactly one new outbox file: got %d", len(fresh))
	}
	thoughtID := strings.TrimSuffix(filepath.Base(fresh[0]), ".gdl")

	// Cross the TTL boundary BEFORE starting the daemon. At ttl=1 and
	// 1500ms sleep, now > ts + ttl by a comfortable margin (500ms) — the
	// sweep's IsExpired check is now.After(ts.Add(ttl)) with second-level
	// ttl precision, so anything past the next whole second is safe.
	time.Sleep(1500 * time.Millisecond)

	// Build the binary once.
	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Spawn the daemon. --quiet suppresses banner chatter so test output
	// stays clean. The daemon's startup runs the catch-up TTL sweep
	// synchronously before the watch loop registers (see dev.go), so the
	// expired file must be moved within startup time + one MkdirAll +
	// link/unlink round-trip.
	cmd := exec.Command(binPath, "dev", "--quiet")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "RUFIO_AGENT_ID=agent-a")
	// os.NewFile(0, os.DevNull) wraps fd 0 (stdin) — wrong; closes leak into
	// the parent test-process fd table. Use os.Open(os.DevNull) for a real
	// /dev/null fd.
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

	// Poll for the move. 5s deadline matches the autopromote e2e budget —
	// the work here is strictly less (no fsnotify wait, just process
	// startup + one Move call), so this is generous on purpose.
	outboxPath := filepath.Join(root, "live", "outbox", "agent-a", thoughtID+".gdl")
	expiredPath := filepath.Join(root, "live", "expired", "agent-a", thoughtID+".gdl")
	deadline := time.Now().Add(5 * time.Second)
	var movedOK bool
	for time.Now().Before(deadline) {
		_, outboxErr := os.Stat(outboxPath)
		_, expiredErr := os.Stat(expiredPath)
		if os.IsNotExist(outboxErr) && expiredErr == nil {
			movedOK = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !movedOK {
		outboxFiles, _ := filepath.Glob(filepath.Join(root, "live", "outbox", "agent-a", "*.gdl"))
		expiredFiles, _ := filepath.Glob(filepath.Join(root, "live", "expired", "agent-a", "*.gdl"))
		t.Errorf("expected outbox empty + expired populated. outbox=%v expired=%v", outboxFiles, expiredFiles)
		return
	}

	// Content-preservation: the sweep MOVES; it must not rewrite. Read
	// the expired file and verify the original header, content, and ttl
	// survive intact. We don't pin the exact ts because we don't know
	// the think command's clock to the nanosecond — but we know the
	// thought-id is in the filename and the fields below are stable.
	bs, err := os.ReadFile(expiredPath)
	if err != nil {
		t.Fatalf("read expired: %v", err)
	}
	s := string(bs)
	for _, want := range []string{"@thought|", "content:ephemeral", "ttl:1"} {
		if !strings.Contains(s, want) {
			t.Errorf("expired file missing %q in:\n%s", want, s)
		}
	}
}
