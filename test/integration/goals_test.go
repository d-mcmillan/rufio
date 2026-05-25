package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/goal"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
	"github.com/d-mcmillan/rufio/internal/testutil"
)

// Integration tests for `rufio goals list`. Exit-code semantics:
//   - 0 success (including empty result)
//   - 1 runtime/project errors (NotInProject)
//   - 2 validation errors (InvalidScope, invalid --state)
//
// `goals list` is read-only and project-wide: there is no identity
// resolution, so there is no NoIdentityError path to test.

// mustSeedGoal shells out to `rufio goal --statement=...` as `agent` to
// drop an active goal, returning the freshly generated goal-id parsed
// from the canonical confirmation line `goal: id=<id> scope=<scope>`.
func mustSeedGoal(t *testing.T, root, agent, statement, scope string) string {
	t.Helper()
	res := testutil.RunCLI(t, []string{
		"goal", "--statement=" + statement, "--scope=" + scope,
	}, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("seed goal failed: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var id string
	for _, line := range strings.Split(res.Stdout, "\n") {
		if !strings.HasPrefix(line, "goal: id=") {
			continue
		}
		for _, p := range strings.Fields(line) {
			if strings.HasPrefix(p, "id=") {
				id = strings.TrimPrefix(p, "id=")
			}
		}
	}
	if id == "" {
		t.Fatalf("no id in goal output: %q", res.Stdout)
	}
	return id
}

// TestGoalsList_HappyPath_ListsActiveGoals seeds 2 goals; default list
// (no filters) returns both. Both are active by construction.
func TestGoalsList_HappyPath_ListsActiveGoals(t *testing.T) {
	root := initProject(t)
	id1 := mustSeedGoal(t, root, "agent-a", "first goal", "agent")
	id2 := mustSeedGoal(t, root, "agent-a", "second goal", "agent")

	res := testutil.RunCLI(t, []string{"goals", "list"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, id1) {
		t.Errorf("stdout missing id1=%s\n%s", id1, res.Stdout)
	}
	if !strings.Contains(res.Stdout, id2) {
		t.Errorf("stdout missing id2=%s\n%s", id2, res.Stdout)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("want 2 lines, got %d:\n%s", len(lines), res.Stdout)
	}
	if !strings.Contains(res.Stdout, "active") {
		t.Errorf("stdout missing state column 'active'\n%s", res.Stdout)
	}
}

// TestGoalsList_ScopeFilter seeds one agent-scope and one fleet-scope
// goal; `--scope=agent` returns only the agent-scoped one.
func TestGoalsList_ScopeFilter(t *testing.T) {
	root := initProject(t)
	agentID := mustSeedGoal(t, root, "agent-a", "for an agent", "agent")
	fleetID := mustSeedGoal(t, root, "agent-a", "for the fleet", "fleet")

	res := testutil.RunCLI(t, []string{"goals", "list", "--scope=agent"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, agentID) {
		t.Errorf("stdout missing agent-scope id=%s\n%s", agentID, res.Stdout)
	}
	if strings.Contains(res.Stdout, fleetID) {
		t.Errorf("stdout includes fleet-scope id=%s (should be filtered)\n%s", fleetID, res.Stdout)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("want 1 line, got %d:\n%s", len(lines), res.Stdout)
	}
}

// TestGoalsList_StateFilter_Active seeds 1 active goal; `--state=active`
// returns it.
func TestGoalsList_StateFilter_Active(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "still active", "agent")

	res := testutil.RunCLI(t, []string{"goals", "list", "--state=active"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, id) {
		t.Errorf("stdout missing active id=%s\n%s", id, res.Stdout)
	}
}

// TestGoalsList_StateFilter_Completed seeds a goal then calls
// goal.MoveToCompleted directly from the test (T5 owns the complete
// command). `--state=completed` returns 1; `--state=active` returns 0.
func TestGoalsList_StateFilter_Completed(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "to complete", "agent")
	if err := goal.MoveToCompleted(root, id, "agent-a", "shipped", versioning.NowISO()); err != nil {
		t.Fatalf("MoveToCompleted: %v", err)
	}

	resCompleted := testutil.RunCLI(t, []string{"goals", "list", "--state=completed"}, root, nil)
	if resCompleted.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", resCompleted.Code, resCompleted.Stderr)
	}
	if !strings.Contains(resCompleted.Stdout, id) {
		t.Errorf("stdout missing completed id=%s\n%s", id, resCompleted.Stdout)
	}
	if !strings.Contains(resCompleted.Stdout, "completed") {
		t.Errorf("stdout missing 'completed' state column\n%s", resCompleted.Stdout)
	}

	resActive := testutil.RunCLI(t, []string{"goals", "list", "--state=active"}, root, nil)
	if resActive.Code != 0 {
		t.Fatalf("active list exit=%d stderr=%q", resActive.Code, resActive.Stderr)
	}
	if strings.TrimSpace(resActive.Stdout) != "" {
		t.Errorf("active stdout should be empty after move:\n%q", resActive.Stdout)
	}
}

// TestGoalsList_BothFilters seeds varied goals and applies both
// --scope and --state simultaneously.
func TestGoalsList_BothFilters(t *testing.T) {
	root := initProject(t)
	// Active agent-scope (wanted match).
	wanted := mustSeedGoal(t, root, "agent-a", "agent + active", "agent")
	// Active fleet-scope (wrong scope).
	otherScope := mustSeedGoal(t, root, "agent-a", "fleet + active", "fleet")
	// Completed agent-scope (wrong state).
	toComplete := mustSeedGoal(t, root, "agent-a", "agent + completed", "agent")
	if err := goal.MoveToCompleted(root, toComplete, "agent-a", "done", versioning.NowISO()); err != nil {
		t.Fatalf("MoveToCompleted: %v", err)
	}

	res := testutil.RunCLI(t, []string{
		"goals", "list", "--scope=agent", "--state=active",
	}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, wanted) {
		t.Errorf("stdout missing wanted id=%s\n%s", wanted, res.Stdout)
	}
	if strings.Contains(res.Stdout, otherScope) {
		t.Errorf("stdout includes wrong-scope id=%s\n%s", otherScope, res.Stdout)
	}
	if strings.Contains(res.Stdout, toComplete) {
		t.Errorf("stdout includes wrong-state id=%s\n%s", toComplete, res.Stdout)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("want 1 line, got %d:\n%s", len(lines), res.Stdout)
	}
}

// TestGoalsList_InvalidScope_Exit2: `--scope=banana` → exit 2 with the
// InvalidScopeError envelope.
func TestGoalsList_InvalidScope_Exit2(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{"goals", "list", "--scope=banana"}, root, nil)
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --scope") {
		t.Errorf("stderr missing 'invalid --scope': %q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio goals:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
}

// TestGoalsList_InvalidState_Exit2: `--state=banana` → exit 2 with the
// UsageError envelope.
func TestGoalsList_InvalidState_Exit2(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{"goals", "list", "--state=banana"}, root, nil)
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --state") {
		t.Errorf("stderr missing 'invalid --state': %q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio goals:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
}

// TestGoalsList_EmptyResult_StdoutEmpty: a fresh project with no goals
// produces empty stdout and exit 0 (no header).
func TestGoalsList_EmptyResult_StdoutEmpty(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{"goals", "list"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "" {
		t.Errorf("stdout should be empty for no-goals case:\n%q", res.Stdout)
	}
}

// TestGoalsList_JSONOutput_HasExpectedShape: --json emits JSONL with
// the locked fields and active-state metadata.
func TestGoalsList_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	id := mustSeedGoal(t, root, "agent-a", "ship goals list", "fleet")

	res := testutil.RunCLI(t, []string{"goals", "list", "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 JSONL line, got %d:\n%s", len(lines), res.Stdout)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, lines[0])
	}
	checks := map[string]interface{}{
		"_type":     "goal",
		"_version":  "1",
		"id":        id,
		"author":    "agent-a",
		"statement": "ship goals list",
		"scope":     "fleet",
		"state":     "active",
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

// TestGoalsList_NotInProject_Exit1: bare t.TempDir() (no init) → exit 1
// with the NotInProjectError envelope.
func TestGoalsList_NotInProject_Exit1(t *testing.T) {
	root := mkProject(t)

	res := testutil.RunCLI(t, []string{"goals", "list"}, root, nil)
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio goals:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
}
