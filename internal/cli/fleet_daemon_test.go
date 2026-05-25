// Package cli — tests for the `rufio fleet` daemon-health line (#154).
//
// `rufio fleet` emits a single advisory line on STDERR surfacing daemon
// liveness, so an attentive operator sees it next to the rows but
// stdout-line-counting integration tests / pipes / wc -l remain
// unaffected. The line never fails the command; it is purely advisory.
//
//	daemon: ok (heartbeat 4s ago)
//	daemon: STALE - last heartbeat 47s ago; routing may be delayed
//	daemon: not running (no heartbeat)
//
// runFleet itself runs the on-disk scan which we don't need here — the
// renderer-level helper renderDaemonHealthHeader does the printing, and
// is the smallest unit we can lock the contract against.
package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/devhealth"
	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// captureStderr is defined in goal_hierarchy_test.go — reuse that
// existing helper rather than duplicate the os.Stderr pipe dance.

// TestFleet_DaemonOk_PrintsOkHeader asserts the header line says
// "daemon: ok" with a heartbeat age when the heartbeat is fresh.
func TestFleet_DaemonOk_PrintsOkHeader(t *testing.T) {
	root := initSupervisionProject(t)
	started := time.Unix(1700000000, 0)
	tick := time.Unix(1700000100, 0)
	if err := devhealth.WriteHeartbeat(root, 4242, started, tick); err != nil {
		t.Fatalf("seed: %v", err)
	}
	now := func() time.Time { return tick.Add(4 * time.Second) }
	out := captureStderr(t, func() {
		renderDaemonHealthHeader(root, output.RenderOpts{}, now)
	})
	if !strings.HasPrefix(out, "daemon: ok") {
		t.Errorf("expected 'daemon: ok ...' as the leading line, got:\n%q", out)
	}
}

// TestFleet_DaemonStale_WarnsAtTop asserts a stale heartbeat surfaces a
// STALE warning at the top of fleet output, with the last-tick age.
func TestFleet_DaemonStale_WarnsAtTop(t *testing.T) {
	root := initSupervisionProject(t)
	started := time.Unix(1700000000, 0)
	tick := time.Unix(1700000100, 0)
	if err := devhealth.WriteHeartbeat(root, 4242, started, tick); err != nil {
		t.Fatalf("seed: %v", err)
	}
	now := func() time.Time { return tick.Add(47 * time.Second) }
	out := captureStderr(t, func() {
		renderDaemonHealthHeader(root, output.RenderOpts{}, now)
	})
	if !strings.Contains(strings.ToLower(out), "stale") {
		t.Errorf("expected 'STALE' (case-insensitive) in header, got:\n%q", out)
	}
	if !strings.Contains(out, "47s") {
		t.Errorf("expected '47s' last-tick age in header, got:\n%q", out)
	}
}

// TestFleet_DaemonNotRunning_WarnsAtTop asserts a missing heartbeat
// surfaces a "not running" header.
func TestFleet_DaemonNotRunning_WarnsAtTop(t *testing.T) {
	root := initSupervisionProject(t)
	// No heartbeat seeded.
	out := captureStderr(t, func() {
		renderDaemonHealthHeader(root, output.RenderOpts{}, time.Now)
	})
	if !strings.Contains(strings.ToLower(out), "not running") {
		t.Errorf("expected 'not running' in header, got:\n%q", out)
	}
}

// TestFleet_DaemonHeader_StdoutStaysPure asserts the health header
// lands on stderr (not stdout). Stdout-purity matters because callers
// parse `rufio fleet` line-by-line — the existing fleet integration
// tests count exact line numbers.
func TestFleet_DaemonHeader_StdoutStaysPure(t *testing.T) {
	root := initSupervisionProject(t)
	stdout := captureStdout(t, func() {
		renderDaemonHealthHeader(root, output.RenderOpts{}, time.Now)
	})
	if stdout != "" {
		t.Errorf("expected daemon-health header on STDERR only; got on stdout:\n%q", stdout)
	}
}

// TestFleet_DaemonHeader_QuietSuppresses asserts --quiet suppresses the
// health header (it's chatter, not data — agents who want JSON or raw
// rows shouldn't get a free header line in their parsing path).
func TestFleet_DaemonHeader_QuietSuppresses(t *testing.T) {
	root := initSupervisionProject(t)
	out := captureStderr(t, func() {
		renderDaemonHealthHeader(root, output.RenderOpts{Quiet: true}, time.Now)
	})
	if out != "" {
		t.Errorf("expected --quiet to suppress header, got:\n%q", out)
	}
}

// TestFleet_DaemonHeader_JSONSuppresses asserts --json suppresses the
// header so the output remains parseable JSONL.
func TestFleet_DaemonHeader_JSONSuppresses(t *testing.T) {
	root := initSupervisionProject(t)
	out := captureStderr(t, func() {
		renderDaemonHealthHeader(root, output.RenderOpts{JSON: true}, time.Now)
	})
	if out != "" {
		t.Errorf("expected --json to suppress header (keeps JSONL pure), got:\n%q", out)
	}
}
