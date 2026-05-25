package integration_test

import (
	"sort"
	"strings"
	"testing"
)

// The JSON-RPC client harness (mcpConn/startMCP/rpc/initialize) lives in
// mcp_helpers_test.go, shared by every MCP tool test.

// expectedToolRoster is the EXACT agent-participation tool set `rufio mcp`
// must expose: 22 tools total. v1.2.0 added `open` (read-dual of `attend`)
// → 20. v1.0.3 added `quickstart` (the cold-start card) → 21. v1.0.4
// added `serve_status` (hosted-server health probe) → 22. The symmetry
// contract (every CLI verb has an MCP tool) drives roster growth. This is
// a structural tripwire: a dropped registerXxx in internal/mcp/server.go
// (or an accidental extra tool, e.g. an excluded operator verb leaking
// in) fails this test loudly instead of silently shrinking the public
// MCP contract. Keep in sync with docs/mcp.md's tool table and
// internal/mcp/server.go's buildServer registrations.
var expectedToolRoster = []string{
	// cognition
	"attend", "think", "observe", "reason", "retract",
	// verification
	"confirm", "refute",
	// channels
	"summon", "accept", "decline", "say", "leave", "close",
	// coordination
	"goal", "goal_complete", "goal_abandon",
	// read
	"recall", "goals_list", "listen", "open",
	// onboarding (v1.0.3)
	"quickstart",
	// hosted-server health (v1.0.4)
	"serve_status",
}

func TestMCP_SkeletonHandshakeAndToolsList(t *testing.T) {
	root := initProject(t)
	c := startMCP(t, root, "agent-a")
	resp := c.rpc(t, "tools/list", map[string]any{})
	if _, bad := resp["error"]; bad {
		t.Fatalf("tools/list errored: %v", resp["error"])
	}
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		if m, ok := tl.(map[string]any); ok {
			names[m["name"].(string)] = true
		}
	}

	// Assert the FULL expected roster as an exact set (not just that the
	// canary `attend` is present). Bidirectional: every expected tool must
	// be registered, and no unexpected tool may appear.
	want := map[string]bool{}
	for _, n := range expectedToolRoster {
		want[n] = true
	}
	if len(names) != len(want) {
		got := make([]string, 0, len(names))
		for n := range names {
			got = append(got, n)
		}
		sort.Strings(got)
		t.Fatalf("tools/list returned %d tools, want exactly %d; got=%v",
			len(names), len(want), strings.Join(got, ","))
	}
	var missing, unexpected []string
	for n := range want {
		if !names[n] {
			missing = append(missing, n)
		}
	}
	for n := range names {
		if !want[n] {
			unexpected = append(unexpected, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	if len(missing) > 0 {
		t.Fatalf("tools/list missing expected tool(s): %v (a registerXxx may be unregistered in internal/mcp/server.go)", missing)
	}
	if len(unexpected) > 0 {
		t.Fatalf("tools/list has unexpected tool(s): %v (an excluded operator verb may have leaked into the MCP surface)", unexpected)
	}
}
