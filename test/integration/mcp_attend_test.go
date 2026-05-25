package integration_test

import (
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestNormaliseVolatile_MidRecord proves the shared normaliser (now in
// mcp_helpers_test.go) neutralises ts/id when they appear MID-record (not
// just trailing), so every PR2 fidelity test can trust it for record types
// that don't end with ts.
func TestNormaliseVolatile_MidRecord(t *testing.T) {
	// Crafted line: id and ts both appear mid-record, each followed by a
	// further field; ts value carries gdl-escaped colons like the real
	// renderer emits.
	in := `@thought|id:1747651909713-a1b2c3|agent:agent-a|ts:2026-05-19T10\:51\:49.713219Z|type:hypothesis|content:x`
	want := `@thought|id:ID|agent:agent-a|ts:TS|type:hypothesis|content:x`
	if got := normaliseVolatile(in); got != want {
		t.Fatalf("mid-record normalisation wrong:\n got=%q\nwant=%q", got, want)
	}

	// Trailing ts (the @attention shape) must still normalise correctly.
	inTrail := `@attention|agent:agent-a|intent:x y|entities:customer\:1|topics:audit|ts:2026-05-19T10\:51\:49.713219Z`
	wantTrail := `@attention|agent:agent-a|intent:x y|entities:customer\:1|topics:audit|ts:TS`
	if got := normaliseVolatile(inTrail); got != wantTrail {
		t.Fatalf("trailing normalisation regressed:\n got=%q\nwant=%q", got, wantTrail)
	}

	// Two records with identical inputs but different ts/id must compare
	// equal after normalisation (the property the fidelity tests rely on).
	a := `@thought|id:1747651909713-a1b2c3|ts:2026-05-19T10\:51\:49.000000Z|content:same`
	b := `@thought|id:1747651999999-z9y8x7|ts:2026-05-19T11\:22\:33.999999Z|content:same`
	if normaliseVolatile(a) != normaliseVolatile(b) {
		t.Fatalf("normalised records differ despite identical non-volatile content:\n a=%q\n b=%q",
			normaliseVolatile(a), normaliseVolatile(b))
	}
}

func TestMCP_Attend_FidelityVsCLI(t *testing.T) {
	// CLI side. attention.BuildRecord requires >=1 entity, so a valid
	// invocation must pass --entities (the plan's draft omitted it; the
	// CLI's real contract is the arbiter). --json so we can compare the
	// structured surface, not just the on-disk record.
	cliRoot := initProject(t)
	r := testutil.RunCLI(t, []string{
		"attend",
		"--intent", "dogfooding the substrate",
		"--entities", "customer:1",
		"--topics", "audit",
		"--json",
	}, cliRoot, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if r.Code != 0 {
		t.Fatalf("CLI attend exit=%d stderr=%q", r.Code, r.Stderr)
	}

	// MCP side.
	mcpRoot := initProject(t)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "attend", map[string]any{
		"intent":   "dogfooding the substrate",
		"entities": []string{"customer:1"},
		"topics":   []string{"audit"},
	})

	// (1) Structured-output fidelity: the MCP tool's structuredContent must
	// mirror the CLI --json object 1:1 (the whole point of the Out struct).
	assertStructuredFidelity(t, mcpStructured, r.Stdout)

	// (2) On-disk fidelity: the written substrate record must be
	// byte-identical (modulo the volatile ts) to the CLI's.
	cliRec := readSingleRecord(t, cliRoot, "live/attention/agent-a.gdl")
	mcpRec := readSingleRecord(t, mcpRoot, "live/attention/agent-a.gdl")
	if normaliseVolatile(cliRec) != normaliseVolatile(mcpRec) {
		t.Fatalf("MCP record not byte-identical to CLI:\n cli=%q\n mcp=%q", cliRec, mcpRec)
	}
}
