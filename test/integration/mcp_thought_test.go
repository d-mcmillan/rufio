package integration_test

import (
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// Each test runs the verb via the CLI into cliRoot and the MCP tool into
// mcpRoot, then asserts (1) the MCP structuredContent deep-equals the CLI
// --json (modulo volatile ts/id) and (2) the written .gdl is byte-identical
// (modulo volatile ts/id). The CLI is the source of truth.

func TestMCP_Think_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	r := testutil.RunCLI(t, []string{
		"think",
		"--type", "decision",
		"--subject", "customer:5821",
		"--content", "approve the refund",
		"--scope", "fleet",
		"--ttl", "3600",
		"--topics", "billing,audit",
		"--json",
	}, cliRoot, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if r.Code != 0 {
		t.Fatalf("CLI think exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "think", map[string]any{
		"type":    "decision",
		"subject": "customer:5821",
		"content": "approve the refund",
		"scope":   "fleet",
		"ttl":     "3600",
		"topics":  []string{"billing", "audit"},
	})
	assertStructuredFidelity(t, mcpStructured, r.Stdout)

	// decision → @thought + @context-bundle. Assert BOTH lines (multi-record).
	cliFile := globOne(t, cliRoot, "live/outbox/agent-a/*.gdl")
	mcpFile := globOne(t, mcpRoot, "live/outbox/agent-a/*.gdl")
	cliRecs := readAllLinesAbs(t, cliFile)
	mcpRecs := readAllLinesAbs(t, mcpFile)
	if len(cliRecs) != 2 || len(mcpRecs) != 2 {
		t.Fatalf("decision must write @thought+@context-bundle: cli=%d mcp=%d", len(cliRecs), len(mcpRecs))
	}
	for i := range cliRecs {
		// The @context-bundle's `decision:` field echoes the (volatile)
		// thought id, which differs per root — neutralise it too.
		norm := func(s string) string { return normaliseIDRefs(normaliseVolatile(s)) }
		if norm(cliRecs[i]) != norm(mcpRecs[i]) {
			t.Fatalf("think record[%d] not byte-identical:\n cli=%q\n mcp=%q", i, cliRecs[i], mcpRecs[i])
		}
	}
}

func TestMCP_Observe_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	r := testutil.RunCLI(t, []string{
		"observe",
		"--subject", "customer:5821",
		"--predicate", "prefers",
		"--object", "email contact",
		"--scope", "fleet",
		"--confidence", "0.8",
		"--topics", "crm",
		"--json",
	}, cliRoot, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if r.Code != 0 {
		t.Fatalf("CLI observe exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "observe", map[string]any{
		"subject":    "customer:5821",
		"predicate":  "prefers",
		"object":     "email contact",
		"scope":      "fleet",
		"confidence": "0.8",
		"topics":     []string{"crm"},
	})
	assertStructuredFidelity(t, mcpStructured, r.Stdout)

	cliFile := globOne(t, cliRoot, "learned/customer/5821/*.gdlm")
	mcpFile := globOne(t, mcpRoot, "learned/customer/5821/*.gdlm")
	if normaliseVolatile(readSingleFromAbs(t, cliFile)) != normaliseVolatile(readSingleFromAbs(t, mcpFile)) {
		t.Fatalf("observe record not byte-identical:\n cli=%q\n mcp=%q",
			readSingleFromAbs(t, cliFile), readSingleFromAbs(t, mcpFile))
	}
}

func TestMCP_Reason_FidelityVsCLI(t *testing.T) {
	cliRoot := initProject(t)
	r := testutil.RunCLI(t, []string{
		"reason",
		"--content", "weighed the tradeoffs",
		"--json",
	}, cliRoot, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if r.Code != 0 {
		t.Fatalf("CLI reason exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "reason", map[string]any{
		"content": "weighed the tradeoffs",
	})
	assertStructuredFidelity(t, mcpStructured, r.Stdout)

	cliFile := globOne(t, cliRoot, "live/reasoning/agent-a/*.gdl")
	mcpFile := globOne(t, mcpRoot, "live/reasoning/agent-a/*.gdl")
	if normaliseVolatile(readSingleFromAbs(t, cliFile)) != normaliseVolatile(readSingleFromAbs(t, mcpFile)) {
		t.Fatalf("reason record not byte-identical:\n cli=%q\n mcp=%q",
			readSingleFromAbs(t, cliFile), readSingleFromAbs(t, mcpFile))
	}
}

func TestMCP_Retract_FidelityVsCLI(t *testing.T) {
	// retract needs an existing thought authored by the same agent. Seed it
	// via the CLI in BOTH roots (the thought id differs per root, so we
	// retract whatever id each root produced).
	seedThought := func(t *testing.T, root string) string {
		t.Helper()
		rr := testutil.RunCLI(t, []string{
			"think", "--type", "hypothesis", "--subject", "customer:1",
			"--content", "seed", "--scope", "agent", "--json",
		}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
		if rr.Code != 0 {
			t.Fatalf("seed think exit=%d stderr=%q", rr.Code, rr.Stderr)
		}
		f := globOne(t, root, "live/outbox/agent-a/*.gdl")
		return idFromGlobPath(f)
	}

	cliRoot := initProject(t)
	cliID := seedThought(t, cliRoot)
	r := testutil.RunCLI(t, []string{
		"retract", cliID, "--reason", "superseded by newer analysis", "--json",
	}, cliRoot, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if r.Code != 0 {
		t.Fatalf("CLI retract exit=%d stderr=%q", r.Code, r.Stderr)
	}

	mcpRoot := initProject(t)
	mcpID := seedThought(t, mcpRoot)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "retract", map[string]any{
		"thought_id": mcpID,
		"reason":     "superseded by newer analysis",
	})
	// target differs by id between roots; drop it alongside volatile keys.
	cliJSON := r.Stdout
	assertStructuredFidelityIgnoring(t, mcpStructured, cliJSON, "target")

	cliRec := readSingleRecord(t, cliRoot, "live/retracted/"+cliID+".gdl")
	mcpRec := readSingleRecord(t, mcpRoot, "live/retracted/"+mcpID+".gdl")
	// normalise the (different) target ids too — same shape otherwise.
	norm := func(s string) string { return normaliseIDRefs(normaliseVolatile(s)) }
	if norm(cliRec) != norm(mcpRec) {
		t.Fatalf("retract record not byte-identical (modulo target id):\n cli=%q\n mcp=%q", cliRec, mcpRec)
	}
}
