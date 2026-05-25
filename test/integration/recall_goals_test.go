package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// Integration tests for F2 (v1.0.6.2): `rufio recall --types=goal` must
// actually surface goals.
//
// The cold-reader pass on v1.0.6.1 found that AllTypes advertised
// "goal" as a valid --types value, ValidateTypes accepted it, but the
// recall scanner had no walker for live/goals/{active,completed,
// abandoned}/. An agent wrote a goal, tried to recall it, saw nothing,
// and concluded the write had failed.
//
// These four tests are the regression guard:
//   - --types=goal surfaces goals (text + JSON)
//   - positional query matches the @goal statement
//   - default recall (no filter) includes goals alongside thoughts
//   - bob does NOT see alice's scope:agent goal (#147 privacy floor)

// TestRecall_TypesGoal_SurfacesGoals — write a goal via the CLI, then
// `recall --types=goal` and `recall --types=goal --json` must both
// surface it.
func TestRecall_TypesGoal_SurfacesGoals(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=ship v1.0.7", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "alice"})
	if res.Code != 0 {
		t.Fatalf("seed goal: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	// Plain text.
	out := testutil.RunCLI(t, []string{"recall", "--types=goal"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("recall --types=goal exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "ship v1.0.7") {
		t.Errorf("recall --types=goal did not surface the goal statement:\n%s", out.Stdout)
	}
	if !strings.Contains(out.Stdout, "alice") {
		t.Errorf("recall --types=goal did not surface the goal author:\n%s", out.Stdout)
	}

	// JSON: an object whose _type is "goal".
	outJSON := testutil.RunCLI(t, []string{"recall", "--types=goal", "--json"}, root, nil)
	if outJSON.Code != 0 {
		t.Fatalf("recall --types=goal --json exit=%d stderr=%q", outJSON.Code, outJSON.Stderr)
	}
	foundGoal := false
	for _, line := range strings.Split(strings.TrimSpace(outJSON.Stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]interface{}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("decode JSON line %q: %v", line, err)
		}
		if row["_type"] == "goal" {
			foundGoal = true
			if row["author"] != "alice" {
				t.Errorf("JSON goal row: author=%v want alice", row["author"])
			}
			if row["subject"] != "ship v1.0.7" {
				t.Errorf("JSON goal row: subject=%v want statement %q", row["subject"], "ship v1.0.7")
			}
		}
	}
	if !foundGoal {
		t.Errorf("recall --types=goal --json had no _type=goal row:\n%s", outJSON.Stdout)
	}
}

// TestRecall_PositionalQueryMatchesGoalStatement — `recall "ship v1.0.7"`
// (positional query, no --types filter) must match a goal whose
// statement contains that phrase. Verifies the goal scanner mirrors
// statement→Content (or Subject) so substring search hits.
func TestRecall_PositionalQueryMatchesGoalStatement(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=ship v1.0.7", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "alice"})
	if res.Code != 0 {
		t.Fatalf("seed goal: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	out := testutil.RunCLI(t, []string{"recall", "ship v1.0.7"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("recall positional exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "ship v1.0.7") {
		t.Errorf("positional recall did not match goal statement:\n%s", out.Stdout)
	}
}

// TestRecall_AllTypes_IncludesGoals — `recall` with no type filter
// (default = AllTypes) must include goals alongside thoughts and
// observations. Confirms the goal walker is wired into Scan, not
// gated behind an explicit --types flag.
func TestRecall_AllTypes_IncludesGoals(t *testing.T) {
	root := initProject(t)

	// One thought + one goal — recall (no filter) must surface both.
	if r := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=svc:auth",
		"--content=thoughtmarker phrase", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "alice"}); r.Code != 0 {
		t.Fatalf("seed think: exit=%d stderr=%q", r.Code, r.Stderr)
	}
	if r := testutil.RunCLI(t, []string{
		"goal", "--statement=goalmarker phrase", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "alice"}); r.Code != 0 {
		t.Fatalf("seed goal: exit=%d stderr=%q", r.Code, r.Stderr)
	}

	out := testutil.RunCLI(t, []string{"recall"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("recall (no filter) exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "thoughtmarker") {
		t.Errorf("default recall dropped the thought:\n%s", out.Stdout)
	}
	if !strings.Contains(out.Stdout, "goalmarker") {
		t.Errorf("default recall dropped the goal — F2 regression:\n%s", out.Stdout)
	}
}

// TestRecall_Goals_PrivacyFloor — alice writes a scope:agent goal.
// bob's `recall` (and `recall --types=goal`) must NOT see it. This is
// the #147 privacy floor — same gate `goals list` enforces. F2 brief:
// the new walker must populate Scope+Author so the existing Filter
// privacy gate fires.
func TestRecall_Goals_PrivacyFloor(t *testing.T) {
	root := initProject(t)

	// alice writes a PRIVATE goal.
	if r := testutil.RunCLI(t, []string{
		"goal", "--statement=privategoalmarker alice's private goal",
		"--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "alice"}); r.Code != 0 {
		t.Fatalf("seed private goal: exit=%d stderr=%q", r.Code, r.Stderr)
	}

	// bob queries — must see nothing.
	out := testutil.RunCLI(t, []string{"recall", "--types=goal"}, root,
		map[string]string{"RUFIO_AGENT_ID": "bob"})
	if out.Code != 0 {
		t.Fatalf("recall --types=goal exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if strings.Contains(out.Stdout, "privategoalmarker") {
		t.Errorf("scope:agent goal LEAKED to non-author bob (privacy floor breached):\n%s", out.Stdout)
	}

	// Same with a positional query — must also be hidden.
	out = testutil.RunCLI(t, []string{"recall", "privategoalmarker"}, root,
		map[string]string{"RUFIO_AGENT_ID": "bob"})
	if out.Code != 0 {
		t.Fatalf("recall positional exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if strings.Contains(out.Stdout, "privategoalmarker") {
		t.Errorf("scope:agent goal LEAKED via positional recall (privacy floor breached):\n%s", out.Stdout)
	}

	// alice CAN see her own goal.
	outAlice := testutil.RunCLI(t, []string{"recall", "--types=goal"}, root,
		map[string]string{"RUFIO_AGENT_ID": "alice"})
	if outAlice.Code != 0 {
		t.Fatalf("alice recall --types=goal exit=%d stderr=%q", outAlice.Code, outAlice.Stderr)
	}
	if !strings.Contains(outAlice.Stdout, "privategoalmarker") {
		t.Errorf("alice should see her own scope:agent goal:\n%s", outAlice.Stdout)
	}
}
