package integration_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestMCP_Quickstart_Tool_Registered pins that `quickstart` is on the
// MCP tool roster. v1.0.3 brings the count from 20 → 21 — the
// symmetry contract (every CLI verb has a matching MCP tool) is
// load-bearing for cross-harness integrators.
func TestMCP_Quickstart_Tool_Registered(t *testing.T) {
	root := initProject(t)
	c := startMCP(t, root, "agent-a")
	resp := c.rpc(t, "tools/list", map[string]any{})
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	found := false
	for _, ti := range tools {
		tm, _ := ti.(map[string]any)
		if name, _ := tm["name"].(string); name == "quickstart" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("MCP tools/list does not include 'quickstart'; got: %v", tools)
	}
}

// TestMCP_Quickstart_PassthroughFidelity pins the byte-structural
// identity between `rufio quickstart --json` (CLI) and the MCP
// quickstart tool's structured result. Drift between the two
// transports silently splits the contract; shared CardV1/CardVersion
// constants are the fidelity gate.
func TestMCP_Quickstart_PassthroughFidelity(t *testing.T) {
	cliRoot := initProject(t)
	mcpRoot := initProject(t)

	cliRes := testutil.RunCLI(t, []string{"quickstart", "--json"}, cliRoot, nil)
	if cliRes.Code != 0 {
		t.Fatalf("CLI quickstart --json: exit=%d stderr=%q", cliRes.Code, cliRes.Stderr)
	}
	var cli map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(cliRes.Stdout)), &cli); err != nil {
		t.Fatalf("CLI --json parse: %v\nstdout=%q", err, cliRes.Stdout)
	}

	c := startMCP(t, mcpRoot, "agent-a")
	mcp := c.callTool(t, "quickstart", map[string]any{})

	if !reflect.DeepEqual(mcp, cli) {
		t.Fatalf("MCP quickstart != CLI quickstart --json:\n MCP: %#v\n CLI: %#v", mcp, cli)
	}
}

// TestMcpHelp_IncludesQuickstart pins the inclusion of `quickstart` in
// the listed tools. The exact roster count is asserted by the more
// specific TestMcpHelp_ListsAllTools tripwire (currently 22 after v1.0.4
// added serve_status); this test is a focused presence check.
func TestMcpHelp_IncludesQuickstart(t *testing.T) {
	res := testutil.RunCLI(t, []string{"mcp", "--help"}, t.TempDir(), nil)
	if !strings.Contains(res.Stdout, "quickstart") {
		t.Errorf("mcp --help missing quickstart; got:\n%s", res.Stdout)
	}
}
