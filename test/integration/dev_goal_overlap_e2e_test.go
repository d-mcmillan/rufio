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

// TestDev_GoalOverlapHandler_LiveSession spawns the real `rufio dev` daemon,
// has two agents (agent-a, agent-b) write active goals that share an
// entity-id (customer:5821), and polls for the daemon to deliver
// @goal-overlap notifications to BOTH agents' inboxes. Catches regressions
// in the GoalOverlapHandler dispatch wiring (PR #18), the
// routing.RouteGoalOverlap entity-intersection scan + double-delivery
// (D18.5), the (source, target) inbox filename shape (D18.6), and the
// no-self-pair self-suppression (D18.2).
//
// Sequencing note: agent-a writes FIRST. When the daemon scans, there is
// no peer to overlap with — no inbox files appear. Then agent-b writes,
// which collides on customer:5821. The daemon delivers ONE inbox file
// per recipient: agent-a (the peer) and agent-b (the new-goal author).
// Both files are named `<b-goal-id>-overlap-<a-goal-id>.gdl` — the
// source is always the newly-written goal.
func TestDev_GoalOverlapHandler_LiveSession(t *testing.T) {
	root := initProject(t)

	// Build the binary once.
	binPath, err := testutil.BuildBinary()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Spawn the daemon. --quiet suppresses banner chatter so test output
	// stays clean.
	cmd := exec.Command(binPath, "dev", "--quiet")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "RUFIO_AGENT_ID=agent-a")
	// os.NewFile(0, os.DevNull) wraps fd 0 (stdin); closes leak into the
	// parent test-process fd table → spurious EBADF on subsequent
	// TempDir RemoveAll cleanups. Use os.Open(os.DevNull) (PR #14 fix).
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

	// Give the daemon time to register fsnotify watchers on the canonical
	// subdir set (which includes live/goals/active/). 500ms is the
	// canonical budget — guards against the race where the first `goal`
	// command lands before fsnotify is armed.
	time.Sleep(500 * time.Millisecond)

	// --- Round 1: agent-a writes first. No peer exists yet, so the
	// daemon's scan finds no overlaps. We capture agent-a's goal id via
	// glob-diff against live/goals/active/ (parallel to the
	// summon-route test's pending glob-diff pattern).
	activePattern := filepath.Join(root, "live", "goals", "active", "*.gdl")
	beforeA, _ := filepath.Glob(activePattern)
	beforeASet := make(map[string]bool, len(beforeA))
	for _, p := range beforeA {
		beforeASet[p] = true
	}
	resA := testutil.RunCLI(t, []string{
		"goal", "--statement=reduce customer:5821 churn", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if resA.Code != 0 {
		t.Fatalf("goal A: exit=%d stderr=%q stdout=%q", resA.Code, resA.Stderr, resA.Stdout)
	}
	afterA, _ := filepath.Glob(activePattern)
	var freshA []string
	for _, p := range afterA {
		if !beforeASet[p] {
			freshA = append(freshA, p)
		}
	}
	if len(freshA) != 1 {
		t.Fatalf("goal A did not produce exactly one new active file: got %d (%v)", len(freshA), freshA)
	}
	aGoalID := strings.TrimSuffix(filepath.Base(freshA[0]), ".gdl")

	// Wait briefly for the daemon to process agent-a's goal. There is no
	// peer to overlap with, so this scan produces no inbox files — but we
	// need the engine to complete its no-op pass before agent-b lands so
	// the second scan reliably sees agent-a's goal on disk.
	time.Sleep(200 * time.Millisecond)

	// --- Round 2: agent-b writes a goal on the SAME entity. This is the
	// write the daemon must detect as overlapping.
	beforeB, _ := filepath.Glob(activePattern)
	beforeBSet := make(map[string]bool, len(beforeB))
	for _, p := range beforeB {
		beforeBSet[p] = true
	}
	resB := testutil.RunCLI(t, []string{
		"goal", "--statement=onboarding flow for customer:5821 reactivation", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if resB.Code != 0 {
		t.Fatalf("goal B: exit=%d stderr=%q stdout=%q", resB.Code, resB.Stderr, resB.Stdout)
	}
	afterB, _ := filepath.Glob(activePattern)
	var freshB []string
	for _, p := range afterB {
		if !beforeBSet[p] {
			freshB = append(freshB, p)
		}
	}
	if len(freshB) != 1 {
		t.Fatalf("goal B did not produce exactly one new active file: got %d (%v)", len(freshB), freshB)
	}
	bGoalID := strings.TrimSuffix(filepath.Base(freshB[0]), ".gdl")

	// Poll both inboxes for the routed @goal-overlap notification. 5s is
	// the canonical e2e budget — the latency budget covers fsnotify event
	// delivery + one RouteGoalOverlap Render/Write round-trip.
	//
	// File shape per D18.6: <source-goal-id>-overlap-<target-goal-id>.gdl
	// where the SOURCE is always the newly-written goal (here agent-b's),
	// and the TARGET is the peer's pre-existing active goal (here
	// agent-a's). Same filename in both inboxes because the (source,
	// target) pair is identical from both recipients' point of view.
	overlapName := bGoalID + "-overlap-" + aGoalID + ".gdl"
	peerInbox := filepath.Join(root, "live", "inbox", "agent-a", overlapName)
	selfInbox := filepath.Join(root, "live", "inbox", "agent-b", overlapName)
	deadline := time.Now().Add(5 * time.Second)
	var peerOK, selfOK bool
	for time.Now().Before(deadline) {
		if bs, err := os.ReadFile(peerInbox); err == nil {
			s := string(bs)
			// Note: GDL render escapes colons in values, so the on-disk
			// form is `entity:customer\:5821`, not `entity:customer:5821`.
			if strings.Contains(s, "@goal-overlap|") &&
				strings.Contains(s, "to:agent-a") &&
				strings.Contains(s, "from:agent-b") &&
				strings.Contains(s, `entity:customer\:5821`) &&
				strings.Contains(s, "source-goal:"+bGoalID) &&
				strings.Contains(s, "target-goal:"+aGoalID) {
				peerOK = true
			}
		}
		if bs, err := os.ReadFile(selfInbox); err == nil {
			s := string(bs)
			if strings.Contains(s, "@goal-overlap|") &&
				strings.Contains(s, "to:agent-b") &&
				strings.Contains(s, "from:agent-b") &&
				strings.Contains(s, `entity:customer\:5821`) &&
				strings.Contains(s, "source-goal:"+bGoalID) &&
				strings.Contains(s, "target-goal:"+aGoalID) {
				selfOK = true
			}
		}
		if peerOK && selfOK {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !peerOK || !selfOK {
		// Surface enough state to debug a flake from CI logs alone.
		if bs, err := os.ReadFile(peerInbox); err == nil {
			t.Errorf("peer inbox file present but malformed: %s = %q", peerInbox, bs)
		} else {
			t.Errorf("peer inbox file (peerOK=%v) at %s: %v", peerOK, peerInbox, err)
		}
		if bs, err := os.ReadFile(selfInbox); err == nil {
			t.Errorf("self inbox file present but malformed: %s = %q", selfInbox, bs)
		} else {
			t.Errorf("self inbox file (selfOK=%v) at %s: %v", selfOK, selfInbox, err)
		}
		for _, recipient := range []string{"agent-a", "agent-b"} {
			dir := filepath.Join(root, "live", "inbox", recipient)
			if entries, err := os.ReadDir(dir); err == nil {
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("live/inbox/%s/ contents: %v", recipient, names)
			} else {
				t.Errorf("live/inbox/%s/ unreadable: %v", recipient, err)
			}
		}
		// Surface the source goal files so we can rule out malformed
		// statements masking the overlap.
		for _, p := range []string{freshA[0], freshB[0]} {
			if bs, err := os.ReadFile(p); err == nil {
				t.Errorf("source goal file %s:\n%s", p, bs)
			}
		}
		t.FailNow()
	}

	// Negative assertion: agent-a's earlier goal (when it was the FIRST
	// and had no peer to overlap with) did NOT generate any inbox file
	// where agent-a's goal is the source. Specifically — no file matching
	// `<a-goal-id>-overlap-*.gdl` in agent-a's inbox. The only legitimate
	// overlap file is the b→a one named `<b>-overlap-<a>.gdl`, which has
	// b as the source.
	noSelfSource, err := filepath.Glob(filepath.Join(root, "live", "inbox", "agent-a", aGoalID+"-overlap-*.gdl"))
	if err != nil {
		t.Fatalf("glob noSelfSource: %v", err)
	}
	if len(noSelfSource) != 0 {
		t.Errorf("agent-a's earlier no-peer goal should not have produced any inbox file with a as source; got %v", noSelfSource)
	}
}
