package integration_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// recall is a READ tool. Its --json is JSONL (one object per record); the
// MCP tool returns {"records":[<same objects>]}. We seed an identical
// corpus into both roots (a thought with a type, plus an observation — the
// #89-fixed shape: conditional `type` key + populated observation id), run
// the CLI and the tool, then deep-equal the record sets after dropping the
// per-root-volatile keys (id/ts/path differ by root).

func dropRecallVolatile(rec map[string]any) map[string]any {
	out := make(map[string]any, len(rec))
	for k, v := range rec {
		// id/ts are inherently random; path is an absolute per-root path.
		if k == "id" || k == "ts" || k == "path" {
			continue
		}
		out[k] = v
	}
	return out
}

func TestMCP_Recall_FidelityVsCLI(t *testing.T) {
	seedCorpus := func(t *testing.T, root string) {
		t.Helper()
		env := map[string]string{"RUFIO_AGENT_ID": "agent-a"}
		// A decision thought → #89: --json must carry "type":"decision".
		if rr := testutil.RunCLI(t, []string{
			"think", "--type", "decision", "--subject", "customer:5821",
			"--content", "approve the refund", "--scope", "fleet",
		}, root, env); rr.Code != 0 {
			t.Fatalf("seed think exit=%d stderr=%q", rr.Code, rr.Stderr)
		}
		// An observation → #89: --json id must be the path stem (non-empty).
		if rr := testutil.RunCLI(t, []string{
			"observe", "--subject", "customer:5821", "--predicate", "prefers",
			"--object", "email", "--scope", "fleet",
		}, root, env); rr.Code != 0 {
			t.Fatalf("seed observe exit=%d stderr=%q", rr.Code, rr.Stderr)
		}
	}

	cliRoot := initProject(t)
	seedCorpus(t, cliRoot)
	r := testutil.RunCLI(t, []string{"recall", "--json"}, cliRoot,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if r.Code != 0 {
		t.Fatalf("CLI recall exit=%d stderr=%q", r.Code, r.Stderr)
	}
	var cliRecs []map[string]any
	for _, line := range strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("CLI recall --json line not parseable: %v\n%q", err, line)
		}
		cliRecs = append(cliRecs, dropRecallVolatile(m))
	}

	mcpRoot := initProject(t)
	seedCorpus(t, mcpRoot)
	c := startMCP(t, mcpRoot, "agent-a")
	mcpStructured := c.callTool(t, "recall", map[string]any{})
	rawRecords, ok := mcpStructured["records"].([]any)
	if !ok {
		t.Fatalf("recall structuredContent.records is not an array: %#v", mcpStructured)
	}
	var mcpRecs []map[string]any
	for _, rr := range rawRecords {
		m, ok := rr.(map[string]any)
		if !ok {
			t.Fatalf("recall record not an object: %#v", rr)
		}
		mcpRecs = append(mcpRecs, dropRecallVolatile(m))
	}

	// #89 assertions: the decision thought must carry type=="decision"; the
	// observation must have a non-empty id (path stem). Verify on the MCP
	// side (which is what agents consume) AND that the CLI agrees.
	assertHasDecisionType := func(recs []map[string]any, side string) {
		for _, m := range recs {
			if m["_type"] == "thought" {
				if m["type"] != "decision" {
					t.Fatalf("%s: decision thought missing type=decision (#89): %#v", side, m)
				}
			}
		}
	}
	assertHasDecisionType(cliRecs, "cli")
	assertHasDecisionType(mcpRecs, "mcp")

	if !reflect.DeepEqual(cliRecs, mcpRecs) {
		t.Fatalf("MCP recall records != CLI --json (volatile dropped):\n cli=%#v\n mcp=%#v", cliRecs, mcpRecs)
	}
}

func TestMCP_Recall_ObservationIDPopulated(t *testing.T) {
	// #89 (c): observations previously had id=="" in --json. After the fix
	// the recall tool must surface the path-stem id for observations.
	root := initProject(t)
	if rr := testutil.RunCLI(t, []string{
		"observe", "--subject", "customer:9", "--predicate", "is",
		"--object", "vip", "--scope", "agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"}); rr.Code != 0 {
		t.Fatalf("seed observe exit=%d stderr=%q", rr.Code, rr.Stderr)
	}
	c := startMCP(t, root, "agent-a")
	mcpStructured := c.callTool(t, "recall", map[string]any{"types": "observation"})
	recs, _ := mcpStructured["records"].([]any)
	if len(recs) != 1 {
		t.Fatalf("expected exactly one observation, got %d: %#v", len(recs), recs)
	}
	m := recs[0].(map[string]any)
	if id, _ := m["id"].(string); id == "" {
		t.Fatalf("observation id must be populated (#89), got empty: %#v", m)
	}
}
