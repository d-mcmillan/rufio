package integration_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func TestMCP_Goal_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	r := testutil.RunCLI(t, []string{
		"goal", "--statement", "ship the MCP adapter",
		"--by", "EOW", "--scope", "fleet", "--json",
	}, cliRoot, envA())
	if r.Code != 0 {
		t.Fatalf("CLI goal exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "goal", map[string]any{
		"statement": "ship the MCP adapter",
		"by":        "EOW",
		"scope":     "fleet",
	})
	assertStructuredFidelity(t, mcpStructured, r.Stdout)

	cliFile := globOne(t, cliRoot, "live/goals/active/*.gdl")
	mcpFile := globOne(t, mcpRoot, "live/goals/active/*.gdl")
	if normaliseAll(readSingleFromAbs(t, cliFile)) != normaliseAll(readSingleFromAbs(t, mcpFile)) {
		t.Fatalf("goal record not byte-identical:\n cli=%q\n mcp=%q",
			readSingleFromAbs(t, cliFile), readSingleFromAbs(t, mcpFile))
	}
}

// TestMCP_Goal_DefaultScope: --scope defaults to "fleet" when omitted
// (H3a #125 — unified write-verb default); the MCP tool must default
// identically (the Out scope must be "fleet").
func TestMCP_Goal_DefaultScope(t *testing.T) {
	cliRoot := initProject(t)
	r := testutil.RunCLI(t, []string{
		"goal", "--statement", "minimal goal", "--json",
	}, cliRoot, envA())
	if r.Code != 0 {
		t.Fatalf("CLI goal exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "goal", map[string]any{
		"statement": "minimal goal",
	})
	if mcpStructured["scope"] != "fleet" {
		t.Fatalf("default scope must be fleet (H3a), got %v", mcpStructured["scope"])
	}
	assertStructuredFidelity(t, mcpStructured, r.Stdout)
}

func TestMCP_GoalsList_FidelityVsCLI(t *testing.T) {
	// Seed a heterogeneous set: an active goal and a completed goal (so the
	// audit-derived conditional keys are exercised).
	seed := func(t *testing.T, root string) {
		t.Helper()
		if rr := testutil.RunCLI(t, []string{
			"goal", "--statement", "active one", "--scope", "fleet",
		}, root, envA()); rr.Code != 0 {
			t.Fatalf("seed active goal exit=%d stderr=%q", rr.Code, rr.Stderr)
		}
		if rr := testutil.RunCLI(t, []string{
			"goal", "--statement", "to be completed", "--scope", "agent",
		}, root, envA()); rr.Code != 0 {
			t.Fatalf("seed goal exit=%d stderr=%q", rr.Code, rr.Stderr)
		}
		gid := idFromGlobPath(secondGoalFile(t, root, "to be completed"))
		if rr := testutil.RunCLI(t, []string{
			"goal", "complete", gid, "--outcome", "done and dusted",
		}, root, envA()); rr.Code != 0 {
			t.Fatalf("complete goal exit=%d stderr=%q", rr.Code, rr.Stderr)
		}
	}

	cliRoot := initProject(t)
	seed(t, cliRoot)
	r := testutil.RunCLI(t, []string{"goals", "list", "--json"}, cliRoot, envA())
	if r.Code != 0 {
		t.Fatalf("CLI goals list exit=%d stderr=%q", r.Code, r.Stderr)
	}
	var cliGoals []map[string]any
	for _, line := range strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("CLI goals list --json line not parseable: %v\n%q", err, line)
		}
		cliGoals = append(cliGoals, dropGoalVolatile(m))
	}

	mcpRoot := initProject(t)
	seed(t, mcpRoot)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "goals_list", map[string]any{})
	raw, ok := mcpStructured["goals"].([]any)
	if !ok {
		t.Fatalf("goals_list structuredContent.goals is not an array: %#v", mcpStructured)
	}
	var mcpGoals []map[string]any
	for _, g := range raw {
		m, ok := g.(map[string]any)
		if !ok {
			t.Fatalf("goal entry not an object: %#v", g)
		}
		mcpGoals = append(mcpGoals, dropGoalVolatile(m))
	}

	if !reflect.DeepEqual(cliGoals, mcpGoals) {
		t.Fatalf("MCP goals_list != CLI --json (volatile dropped):\n cli=%#v\n mcp=%#v", cliGoals, mcpGoals)
	}
	// Confirm the conditional audit keys actually appeared (otherwise the
	// equality above would be vacuously true for two empty shapes).
	foundCompleted := false
	for _, m := range mcpGoals {
		if m["state"] == "completed" {
			foundCompleted = true
			if _, ok := m["outcome"]; !ok {
				t.Fatalf("completed goal missing conditional `outcome` key: %#v", m)
			}
		}
	}
	if !foundCompleted {
		t.Fatalf("expected a completed goal in goals_list output: %#v", mcpGoals)
	}
}

func TestMCP_GoalComplete_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	cliGID := seedGoal(t, cliRoot)
	r := testutil.RunCLI(t, []string{
		"goal", "complete", cliGID, "--outcome", "shipped v1.1", "--json",
	}, cliRoot, envA())
	if r.Code != 0 {
		t.Fatalf("CLI goal complete exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	mcpGID := seedGoal(t, mcpRoot)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "goal_complete", map[string]any{
		"goal_id": mcpGID,
		"outcome": "shipped v1.1",
	})
	assertStructuredFidelityIgnoring(t, mcpStructured, r.Stdout, "id")

	norm := func(s string) string { return normaliseAll(s) }
	cliRecs := readAllLinesAbs(t, globOne(t, cliRoot, "live/goals/completed/*.gdl"))
	mcpRecs := readAllLinesAbs(t, globOne(t, mcpRoot, "live/goals/completed/*.gdl"))
	if len(cliRecs) != len(mcpRecs) {
		t.Fatalf("completed goal line count differs: cli=%d mcp=%d", len(cliRecs), len(mcpRecs))
	}
	for i := range cliRecs {
		if norm(cliRecs[i]) != norm(mcpRecs[i]) {
			t.Fatalf("completed goal record[%d] not byte-identical:\n cli=%q\n mcp=%q",
				i, cliRecs[i], mcpRecs[i])
		}
	}
}

func TestMCP_GoalAbandon_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	cliGID := seedGoal(t, cliRoot)
	r := testutil.RunCLI(t, []string{
		"goal", "abandon", cliGID, "--reason", "deprioritised", "--json",
	}, cliRoot, envA())
	if r.Code != 0 {
		t.Fatalf("CLI goal abandon exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	mcpGID := seedGoal(t, mcpRoot)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "goal_abandon", map[string]any{
		"goal_id": mcpGID,
		"reason":  "deprioritised",
	})
	assertStructuredFidelityIgnoring(t, mcpStructured, r.Stdout, "id")

	cliRecs := readAllLinesAbs(t, globOne(t, cliRoot, "live/goals/abandoned/*.gdl"))
	mcpRecs := readAllLinesAbs(t, globOne(t, mcpRoot, "live/goals/abandoned/*.gdl"))
	if len(cliRecs) != len(mcpRecs) {
		t.Fatalf("abandoned goal line count differs: cli=%d mcp=%d", len(cliRecs), len(mcpRecs))
	}
	for i := range cliRecs {
		if normaliseAll(cliRecs[i]) != normaliseAll(mcpRecs[i]) {
			t.Fatalf("abandoned goal record[%d] not byte-identical:\n cli=%q\n mcp=%q",
				i, cliRecs[i], mcpRecs[i])
		}
	}
}

// seedGoal writes one active goal via the CLI and returns its id.
func seedGoal(t *testing.T, root string) string {
	t.Helper()
	if rr := testutil.RunCLI(t, []string{
		"goal", "--statement", "seed goal", "--scope", "agent",
	}, root, envA()); rr.Code != 0 {
		t.Fatalf("seed goal exit=%d stderr=%q", rr.Code, rr.Stderr)
	}
	return idFromGlobPath(globOne(t, root, "live/goals/active/*.gdl"))
}

// secondGoalFile returns the active-goal file whose @goal statement
// contains want (used to disambiguate when two active goals exist).
func secondGoalFile(t *testing.T, root, want string) string {
	t.Helper()
	matches := globAll(t, root, "live/goals/active/*.gdl")
	for _, m := range matches {
		if strings.Contains(readSingleFromAbs(t, m), want) {
			return m
		}
	}
	t.Fatalf("no active goal file containing %q under %s", want, root)
	return ""
}

func dropGoalVolatile(g map[string]any) map[string]any {
	out := make(map[string]any, len(g))
	for k, v := range g {
		// id/ts are random; completed_at/abandoned_at are timestamps.
		if k == "id" || k == "ts" || k == "completed_at" || k == "abandoned_at" {
			continue
		}
		out[k] = v
	}
	return out
}
