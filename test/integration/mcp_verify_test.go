package integration_test

import (
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// confirm/refute need an existing target thought. Seed one per root and
// confirm/refute whatever id that root produced; the `target` key is
// per-root-volatile so it is dropped from the structured comparison and
// the on-disk target id is neutralised.

func seedThoughtForVerify(t *testing.T, root, agent string) string {
	t.Helper()
	// scope=deployment so the verify-side non-author can confirm/refute
	// (#147: scope:agent is non-author-writeable). The CLI<->MCP fidelity
	// tests are about output-shape parity, not the authz check itself —
	// which is covered by privacy_cross_surface_test.go.
	rr := testutil.RunCLI(t, []string{
		"think", "--type", "hypothesis", "--subject", "customer:1",
		"--content", "seed", "--scope", "deployment", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if rr.Code != 0 {
		t.Fatalf("seed think exit=%d stderr=%q", rr.Code, rr.Stderr)
	}
	return idFromGlobPath(globOne(t, root, "live/outbox/"+agent+"/*.gdl"))
}

func TestMCP_Confirm_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	cliID := seedThoughtForVerify(t, cliRoot, "agent-a")
	r := testutil.RunCLI(t, []string{
		"confirm", cliID, "--evidence", "reproduced in staging", "--json",
	}, cliRoot, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if r.Code != 0 {
		t.Fatalf("CLI confirm exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	mcpID := seedThoughtForVerify(t, mcpRoot, "agent-a")
	c := startMCP(t, mcpRoot, "agent-b")
	mcpStructured := c.callTool(t, "confirm", map[string]any{
		"thought_id": mcpID,
		"evidence":   "reproduced in staging",
	})
	assertStructuredFidelityIgnoring(t, mcpStructured, r.Stdout, "target")

	norm := func(s string) string { return normaliseIDRefs(normaliseVolatile(s)) }
	cliRec := readSingleRecord(t, cliRoot, "live/confirms/"+cliID+".gdl")
	mcpRec := readSingleRecord(t, mcpRoot, "live/confirms/"+mcpID+".gdl")
	if norm(cliRec) != norm(mcpRec) {
		t.Fatalf("confirm record not byte-identical:\n cli=%q\n mcp=%q", cliRec, mcpRec)
	}
}

func TestMCP_Confirm_NoEvidence_OmitsKey(t *testing.T) {
	// Evidence is a CONDITIONAL key — absent from --json when empty. The
	// Out struct must omit it too (no spurious "evidence":"").
	cliRoot := initProject(t)
	cliID := seedThoughtForVerify(t, cliRoot, "agent-a")
	r := testutil.RunCLI(t, []string{
		"confirm", cliID, "--json",
	}, cliRoot, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if r.Code != 0 {
		t.Fatalf("CLI confirm exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	mcpID := seedThoughtForVerify(t, mcpRoot, "agent-a")
	c := startMCP(t, mcpRoot, "agent-b")
	mcpStructured := c.callTool(t, "confirm", map[string]any{"thought_id": mcpID})
	if _, ok := mcpStructured["evidence"]; ok {
		t.Fatalf("evidence key must be absent when empty, got %v", mcpStructured["evidence"])
	}
	assertStructuredFidelityIgnoring(t, mcpStructured, r.Stdout, "target")
}

func TestMCP_Refute_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	cliID := seedThoughtForVerify(t, cliRoot, "agent-a")
	r := testutil.RunCLI(t, []string{
		"refute", cliID, "--reason", "contradicted by prod logs",
		"--evidence", "trace 4821", "--json",
	}, cliRoot, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if r.Code != 0 {
		t.Fatalf("CLI refute exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	mcpID := seedThoughtForVerify(t, mcpRoot, "agent-a")
	c := startMCP(t, mcpRoot, "agent-b")
	mcpStructured := c.callTool(t, "refute", map[string]any{
		"thought_id": mcpID,
		"reason":     "contradicted by prod logs",
		"evidence":   "trace 4821",
	})
	assertStructuredFidelityIgnoring(t, mcpStructured, r.Stdout, "target")

	norm := func(s string) string { return normaliseIDRefs(normaliseVolatile(s)) }
	cliRec := readSingleRecord(t, cliRoot, "live/confirms/"+cliID+".gdl")
	mcpRec := readSingleRecord(t, mcpRoot, "live/confirms/"+mcpID+".gdl")
	if norm(cliRec) != norm(mcpRec) {
		t.Fatalf("refute record not byte-identical:\n cli=%q\n mcp=%q", cliRec, mcpRec)
	}
}
