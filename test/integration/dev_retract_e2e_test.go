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

// TestDev_RetractPropagatesDuringLiveSession is the regression test for
// WK2-RETRACT-1 (cold-start daemon missing retracts). Spawns the real
// `rufio dev` daemon, issues a retract via the CLI, then polls for the
// daemon to propagate @retract to the inbox file. Must succeed within
// the timeout — proving the fsnotify watch on live/retracted/ is active
// even on a freshly-init'd project.
func TestDev_RetractPropagatesDuringLiveSession(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "to retract live")

	// Seed an inbox copy synthetically (RoutingHandler is PR #11; we
	// inline what it will do).
	inboxDir := filepath.Join(root, "live", "inbox", "agent-b")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "live", "outbox", "agent-a", id+".gdl")
	bs, _ := os.ReadFile(src)
	inboxFile := filepath.Join(inboxDir, id+".gdl")
	if err := os.WriteFile(inboxFile, bs, 0o644); err != nil {
		t.Fatal(err)
	}

	// Build the binary once via the existing testutil mechanism.
	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Spawn the daemon. Redirect to /dev/null; we don't need to read
	// stdout — we poll the FS for the propagation effect.
	cmd := exec.Command(binPath, "dev", "--quiet")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "RUFIO_AGENT_ID=agent-a")
	// os.NewFile(0, os.DevNull) wraps fd 0 (stdin); closes leak into the
	// parent test-process fd table → spurious EBADF on subsequent
	// TempDir RemoveAll cleanups. Use os.Open(os.DevNull).
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

	// Wait briefly for the daemon to register fsnotify watchers.
	time.Sleep(500 * time.Millisecond)

	// Issue the retract via the CLI.
	res := testutil.RunCLI(t, []string{
		"retract", id, "--reason=outdated",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("retract: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	// Poll the inbox file for the @retract line. Daemon should propagate
	// within a few hundred ms.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		bs, _ := os.ReadFile(inboxFile)
		if strings.Contains(string(bs), "@retract|target:"+id) {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	bs, _ = os.ReadFile(inboxFile)
	t.Errorf("daemon did not propagate @retract within 3s. inbox content:\n%s", bs)
}
