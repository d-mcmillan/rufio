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

// TestDev_RoutingHandler_LiveSession spawns the real `rufio dev` daemon,
// seeds agent-b's attention to match a subject, then issues a real
// `rufio think` from agent-a, and polls for the daemon to route the
// thought to agent-b's inbox. Catches PR #11-class bugs going forward.
func TestDev_RoutingHandler_LiveSession(t *testing.T) {
	root := initProject(t)

	// Seed agent-b's attention.
	res := testutil.RunCLI(t, []string{
		"attend", "--intent=watching", "--entities=customer:5821",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("attend: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	// Build the binary.
	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Spawn the daemon. Quiet flag suppresses chatter so test output stays clean.
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

	// Give the daemon time to register fsnotify watchers.
	time.Sleep(500 * time.Millisecond)

	// Issue the thought via the CLI (agent-a writes about customer:5821).
	tres := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=customer:5821",
		"--content=churn signals", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if tres.Code != 0 {
		t.Fatalf("think: exit=%d stderr=%q", tres.Code, tres.Stderr)
	}

	// Poll agent-b's inbox for the routed thought.
	inboxDir := filepath.Join(root, "live", "inbox", "agent-b")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(inboxDir, "*.gdl"))
		if len(matches) >= 1 {
			bs, _ := os.ReadFile(matches[0])
			content := string(bs)
			if strings.Contains(content, "@thought|") && strings.Contains(content, "@route|to:agent-b") {
				return // success
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	matches, _ := filepath.Glob(filepath.Join(inboxDir, "*.gdl"))
	if len(matches) == 0 {
		t.Errorf("daemon did not route thought to agent-b's inbox within 5s. inbox dir has %d files", len(matches))
	} else {
		bs, _ := os.ReadFile(matches[0])
		t.Errorf("inbox file present but malformed:\n%s", bs)
	}
}
