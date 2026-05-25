package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// Integration tests for `rufio goal complete <goal-id> --outcome=<text>`.
// Exit-code semantics (D17.13 + dispatcher contract):
//   - 0 success
//   - 1 runtime/auth errors (NoIdentity, NoSuchGoal, GoalAuthError)
//   - 2 validation errors (InvalidContentError on --outcome)
//
// Single-prefix invariant: all error envelopes carry `rufio goal:` —
// the subcommand routes through the parent's name to keep the user-facing
// prefix predictable.
//
// mustSeedGoal is the shared seeder defined in goals_test.go.

func TestGoalComplete_HappyPath_MovesActiveToCompleted(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res := testutil.RunCLI(t, []string{
		"goal", "complete", id, "--outcome=shipped v1",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}

	completedPath := filepath.Join(root, "live", "goals", "completed", id+".gdl")
	bs, err := os.ReadFile(completedPath)
	if err != nil {
		t.Fatalf("completed file missing: %v", err)
	}
	for _, want := range []string{"@goal|", "@goal-complete|", "by:agent-a", "outcome:shipped v1"} {
		if !strings.Contains(string(bs), want) {
			t.Errorf("completed file missing %q.\n%s", want, bs)
		}
	}

	activePath := filepath.Join(root, "live", "goals", "active", id+".gdl")
	if _, err := os.Stat(activePath); !os.IsNotExist(err) {
		t.Errorf("active file still exists after complete: err=%v", err)
	}
}

func TestGoalComplete_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res := testutil.RunCLI(t, []string{
		"goal", "complete", id, "--outcome=done", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, res.Stdout)
	}
	checks := map[string]interface{}{
		"_type":    "goal-complete",
		"_version": "1",
		"id":       id,
		"by":       "agent-a",
		"outcome":  "done",
	}
	for k, want := range checks {
		if got[k] != want {
			t.Errorf("%s=%v want %v", k, got[k], want)
		}
	}
	if ts, ok := got["ts"].(string); !ok || ts == "" {
		t.Errorf("ts missing or empty: %v", got["ts"])
	}
}

func TestGoalComplete_ConfirmationLine_HasCanonicalPrefix(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res := testutil.RunCLI(t, []string{
		"goal", "complete", id, "--outcome=shipped",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	out := strings.TrimSpace(res.Stdout)
	if !strings.Contains(out, "completed: id="+id+" outcome=shipped") {
		t.Errorf("missing canonical confirmation: %q", out)
	}
	// Single-prefix invariant: success output must NOT carry the
	// "rufio goal:" error prefix.
	if strings.HasPrefix(out, "rufio goal:") {
		t.Errorf("success stdout carries error prefix: %q", out)
	}
}

func TestGoalComplete_MissingOutcome_Exit2(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res := testutil.RunCLI(t, []string{
		"goal", "complete", id,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--outcome must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio goal:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
}

func TestGoalComplete_EmptyOutcome_Exit2(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res := testutil.RunCLI(t, []string{
		"goal", "complete", id, "--outcome=   ",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--outcome must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestGoalComplete_NoSuchGoal_Exit1(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "complete", "1727000000-fake12", "--outcome=x",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such goal") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio goal:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
}

// TestGoalComplete_AlreadyCompleted_Exit1 covers D17.13: state transitions
// are one-way. Once a goal is in completed/, a second `complete` call
// surfaces as NoSuchGoalError — the caller can't distinguish "never
// existed" from "already handled" by exit code or message.
func TestGoalComplete_AlreadyCompleted_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res1 := testutil.RunCLI(t, []string{
		"goal", "complete", id, "--outcome=first",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res1.Code != 0 {
		t.Fatalf("first complete failed: exit=%d stderr=%q", res1.Code, res1.Stderr)
	}

	res2 := testutil.RunCLI(t, []string{
		"goal", "complete", id, "--outcome=second",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res2.Code != 1 {
		t.Fatalf("second complete exit=%d stderr=%q (want 1 NoSuchGoal per D17.13)", res2.Code, res2.Stderr)
	}
	if !strings.Contains(res2.Stderr, "no such goal") {
		t.Errorf("stderr=%q (expected NoSuchGoalError per D17.13)", res2.Stderr)
	}
}

func TestGoalComplete_WrongAuthor_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	// agent-b (not the author) tries to complete.
	res := testutil.RunCLI(t, []string{
		"goal", "complete", id, "--outcome=stolen",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "only author agent-a may") {
		t.Errorf("stderr=%q (expected mention of agent-a as the only valid author)", res.Stderr)
	}
	// Active file must still exist; no side effect on unauthorised attempt.
	activePath := filepath.Join(root, "live", "goals", "active", id+".gdl")
	if _, err := os.Stat(activePath); err != nil {
		t.Errorf("active file removed after unauthorised complete: %v", err)
	}
}

func TestGoalComplete_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	// Seed under agent-a so we have a real goal-id; the complete-side
	// identity lookup fails before we touch the goal record either way,
	// but a real id keeps the test honest if the order ever changes.
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res := testutil.RunCLI(t, []string{
		"goal", "complete", id, "--outcome=x",
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio goal:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
}
