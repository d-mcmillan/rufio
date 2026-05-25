package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// Integration tests for `rufio goal abandon <goal-id> --reason=<text>`.
// Exit-code semantics (D17.13 + dispatcher contract):
//   - 0 success
//   - 1 runtime/auth errors (NoIdentity, NoSuchGoal, GoalAuthError)
//   - 2 validation errors (InvalidContentError on --reason)
//
// Single-prefix invariant: all error envelopes carry `rufio goal:` —
// the subcommand routes through the parent's name to keep the user-facing
// prefix predictable.
//
// mustSeedGoal is the shared seeder defined in goals_test.go.

func TestGoalAbandon_HappyPath_MovesActiveToAbandoned(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res := testutil.RunCLI(t, []string{
		"goal", "abandon", id, "--reason=scope creep",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}

	abandonedPath := filepath.Join(root, "live", "goals", "abandoned", id+".gdl")
	bs, err := os.ReadFile(abandonedPath)
	if err != nil {
		t.Fatalf("abandoned file missing: %v", err)
	}
	for _, want := range []string{"@goal|", "@goal-abandon|", "by:agent-a", "reason:scope creep"} {
		if !strings.Contains(string(bs), want) {
			t.Errorf("abandoned file missing %q.\n%s", want, bs)
		}
	}

	activePath := filepath.Join(root, "live", "goals", "active", id+".gdl")
	if _, err := os.Stat(activePath); !os.IsNotExist(err) {
		t.Errorf("active file still exists after abandon: err=%v", err)
	}
}

func TestGoalAbandon_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res := testutil.RunCLI(t, []string{
		"goal", "abandon", id, "--reason=blocked", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, res.Stdout)
	}
	checks := map[string]interface{}{
		"_type":    "goal-abandon",
		"_version": "1",
		"id":       id,
		"by":       "agent-a",
		"reason":   "blocked",
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

func TestGoalAbandon_ConfirmationLine_HasCanonicalPrefix(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res := testutil.RunCLI(t, []string{
		"goal", "abandon", id, "--reason=descoped",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	out := strings.TrimSpace(res.Stdout)
	if !strings.Contains(out, "abandoned: id="+id+" reason=descoped") {
		t.Errorf("missing canonical confirmation: %q", out)
	}
	// Single-prefix invariant: success output must NOT carry the
	// "rufio goal:" error prefix.
	if strings.HasPrefix(out, "rufio goal:") {
		t.Errorf("success stdout carries error prefix: %q", out)
	}
}

func TestGoalAbandon_MissingReason_Exit2(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res := testutil.RunCLI(t, []string{
		"goal", "abandon", id,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--reason must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio goal:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
}

func TestGoalAbandon_EmptyReason_Exit2(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res := testutil.RunCLI(t, []string{
		"goal", "abandon", id, "--reason=   ",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--reason must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestGoalAbandon_NoSuchGoal_Exit1(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "abandon", "1727000000-fake12", "--reason=x",
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

// TestGoalAbandon_AlreadyAbandoned_Exit1 covers D17.13: state transitions
// are one-way. Once a goal is in abandoned/, a second `abandon` call
// surfaces as NoSuchGoalError — the caller can't distinguish "never
// existed" from "already handled" by exit code or message.
func TestGoalAbandon_AlreadyAbandoned_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res1 := testutil.RunCLI(t, []string{
		"goal", "abandon", id, "--reason=first",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res1.Code != 0 {
		t.Fatalf("first abandon failed: exit=%d stderr=%q", res1.Code, res1.Stderr)
	}

	res2 := testutil.RunCLI(t, []string{
		"goal", "abandon", id, "--reason=second",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res2.Code != 1 {
		t.Fatalf("second abandon exit=%d stderr=%q (want 1 NoSuchGoal per D17.13)", res2.Code, res2.Stderr)
	}
	if !strings.Contains(res2.Stderr, "no such goal") {
		t.Errorf("stderr=%q (expected NoSuchGoalError per D17.13)", res2.Stderr)
	}
}

// TestGoalAbandon_AlreadyCompleted_Exit1: terminal states are one-way
// (D17.13). A goal that has been completed cannot be abandoned — the
// active file is gone, so the lookup surfaces NoSuchGoalError.
func TestGoalAbandon_AlreadyCompleted_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res1 := testutil.RunCLI(t, []string{
		"goal", "complete", id, "--outcome=shipped",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res1.Code != 0 {
		t.Fatalf("complete failed: exit=%d stderr=%q", res1.Code, res1.Stderr)
	}

	res2 := testutil.RunCLI(t, []string{
		"goal", "abandon", id, "--reason=too late",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res2.Code != 1 {
		t.Fatalf("abandon after complete exit=%d stderr=%q (want 1 NoSuchGoal per D17.13)", res2.Code, res2.Stderr)
	}
	if !strings.Contains(res2.Stderr, "no such goal") {
		t.Errorf("stderr=%q (expected NoSuchGoalError per D17.13)", res2.Stderr)
	}
}

func TestGoalAbandon_WrongAuthor_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	// agent-b (not the author) tries to abandon.
	res := testutil.RunCLI(t, []string{
		"goal", "abandon", id, "--reason=stolen",
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
		t.Errorf("active file removed after unauthorised abandon: %v", err)
	}
}

func TestGoalAbandon_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	// Seed under agent-a so we have a real goal-id; the abandon-side
	// identity lookup fails before we touch the goal record either way,
	// but a real id keeps the test honest if the order ever changes.
	id := mustSeedGoal(t, root, "agent-a", "ship v1", "agent")

	res := testutil.RunCLI(t, []string{
		"goal", "abandon", id, "--reason=x",
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
