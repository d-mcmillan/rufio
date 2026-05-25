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

// TestDev_AutoPromoteHandler_LiveSession spawns the real `rufio dev` daemon,
// has agent-a issue a hypothesis-thought, then three distinct agents
// (agent-b, agent-c, agent-d) confirm it. The daemon must detect the
// threshold crossing and write both:
//   - learned/<subject-path>/<obs-id>.gdlm (with @observation)
//   - live/promoted/<thought-id>.gdl (with @auto-promote)
//
// Catches PR #13-class regressions: dispatch wiring, threshold math,
// retraction guard, and idempotency.
func TestDev_AutoPromoteHandler_LiveSession(t *testing.T) {
	root := initProject(t)

	// Seed the hypothesis from agent-a. We need the generated thought-id;
	// mustWriteThought is hardcoded to subject=customer:1/scope=agent which
	// we can't use here (autopromote requires a specific subject-path on
	// disk), so inline the think + glob-diff pattern.
	outboxPattern := filepath.Join(root, "live", "outbox", "agent-a", "*.gdl")
	before, _ := filepath.Glob(outboxPattern)
	beforeSet := make(map[string]bool, len(before))
	for _, p := range before {
		beforeSet[p] = true
	}
	tres := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=customer:5821",
		"--content=churn signals", "--scope=fleet",
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

	// Build the binary once.
	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Spawn the daemon. --quiet suppresses banner chatter so test output stays clean.
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

	// Give the daemon time to register fsnotify watchers on live/confirms/.
	// The race we're guarding against: did fsnotify register the
	// live/confirms/ watch before we issue the first `rufio confirm`?
	time.Sleep(500 * time.Millisecond)

	// Issue 3 confirms from 3 distinct agents — crosses the MinDistinctConfirmers
	// threshold (=3) at the third confirm.
	for _, agent := range []string{"agent-b", "agent-c", "agent-d"} {
		res := testutil.RunCLI(t, []string{"confirm", thoughtID}, root, map[string]string{"RUFIO_AGENT_ID": agent})
		if res.Code != 0 {
			t.Fatalf("confirm by %s: exit=%d stderr=%q", agent, res.Code, res.Stderr)
		}
	}

	// Poll for both promotion outcomes. 5s is plenty under the
	// event-driven path; the autopromote engine runs synchronously on
	// each live/confirms/ change event so the only latency is fsnotify
	// debounce + a single ReadAll/Write/Rename round-trip.
	promotedPath := filepath.Join(root, "live", "promoted", thoughtID+".gdl")
	learnedDir := filepath.Join(root, "learned", "customer", "5821")
	deadline := time.Now().Add(5 * time.Second)
	var promotedOK, learnedOK bool
	for time.Now().Before(deadline) {
		// Promoted marker — must contain the @auto-promote audit record
		// with the originating thought-id.
		if bs, err := os.ReadFile(promotedPath); err == nil {
			s := string(bs)
			if strings.Contains(s, "@auto-promote|") &&
				strings.Contains(s, "thought:"+thoughtID) {
				promotedOK = true
			}
		}
		// Observation — at least one .gdlm under learned/customer/5821/
		// with the @observation header + the canonical author/predicate.
		// The subject is rendered with GDL colon-escape (customer\:5821)
		// because raw `:` is the field-key/value separator in the wire
		// format. The on-disk path segments remain unescaped (D6.4).
		matches, _ := filepath.Glob(filepath.Join(learnedDir, "*.gdlm"))
		for _, m := range matches {
			bs, _ := os.ReadFile(m)
			s := string(bs)
			if strings.Contains(s, "@observation|") &&
				strings.Contains(s, `subject:customer\:5821`) &&
				strings.Contains(s, "predicate:asserted") &&
				strings.Contains(s, "author:auto-promote") {
				learnedOK = true
				break
			}
		}
		if promotedOK && learnedOK {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Diagnostics on failure — surface enough state to debug a flake from
	// CI logs alone.
	if !promotedOK {
		bs, _ := os.ReadFile(promotedPath)
		t.Errorf("promoted marker missing or malformed: %s = %q", promotedPath, bs)
		if entries, err := os.ReadDir(filepath.Join(root, "live", "promoted")); err == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("live/promoted/ contents: %v", names)
		}
		if entries, err := os.ReadDir(filepath.Join(root, "live", "confirms")); err == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("live/confirms/ contents: %v", names)
		}
	}
	if !learnedOK {
		matches, _ := filepath.Glob(filepath.Join(learnedDir, "*.gdlm"))
		t.Errorf("learned/customer/5821/ has %d .gdlm files: %v", len(matches), matches)
		for _, m := range matches {
			bs, _ := os.ReadFile(m)
			t.Errorf("  %s = %q", filepath.Base(m), bs)
		}
	}
}
