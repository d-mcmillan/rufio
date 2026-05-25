package integration_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestMCP_Open_Tool_Exists pins that `open` is registered in the MCP
// tool roster — the symmetry contract (every CLI verb has an MCP tool)
// is load-bearing for cross-harness integrators.
func TestMCP_Open_Tool_Exists(t *testing.T) {
	root := initProject(t)
	c := startMCP(t, root, "agent-a")
	resp := c.rpc(t, "tools/list", map[string]any{})
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	found := false
	for _, ti := range tools {
		tm, _ := ti.(map[string]any)
		if name, _ := tm["name"].(string); name == "open" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("MCP tools/list does not include 'open'; got: %v", tools)
	}
}

// TestMCP_Open_Tool_PassthroughFidelity pins the wire-shape fidelity
// contract: the MCP `open` tool's structuredContent must match
// `rufio open --json` modulo the inherently-random fields (heartbeat
// timestamps depend on the daemon, etc). Empty substrate → both
// transports return the same locked keyset + values.
func TestMCP_Open_Tool_PassthroughFidelity(t *testing.T) {
	// We use two parallel projects: one for the CLI invocation, one for
	// the MCP server. The structural keyset must match; only the
	// substrate path differs.
	cliRoot := initProject(t)
	mcpRoot := initProject(t)

	cliRes := testutil.RunCLI(t, []string{"open", "test:1", "--json"}, cliRoot,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if cliRes.Code != 0 {
		t.Fatalf("CLI open --json: exit=%d stderr=%q", cliRes.Code, cliRes.Stderr)
	}
	var cli map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(cliRes.Stdout)), &cli); err != nil {
		t.Fatalf("CLI --json parse: %v\nstdout=%q", err, cliRes.Stdout)
	}

	c := startMCP(t, mcpRoot, "agent-a")
	mcp := c.callTool(t, "open", map[string]any{"subject": "test:1"})

	// Drop the heartbeat (timestamp varies even on empty substrate when
	// devhealth ever existed). The structural assertion checks the
	// keyset + the deterministic values.
	dropHeartbeat := func(m map[string]any) map[string]any {
		out := make(map[string]any, len(m))
		for k, v := range m {
			if k == "daemon" {
				if dv, ok := v.(map[string]any); ok {
					ddrop := make(map[string]any, len(dv))
					for dk, vv := range dv {
						if dk != "heartbeat" {
							ddrop[dk] = vv
						}
					}
					out[k] = ddrop
					continue
				}
			}
			out[k] = v
		}
		return out
	}
	gotMCP := dropHeartbeat(mcp)
	gotCLI := dropHeartbeat(cli)

	if !reflect.DeepEqual(gotMCP, gotCLI) {
		t.Fatalf("MCP open != CLI open --json (heartbeat dropped):\n MCP: %#v\n CLI: %#v", gotMCP, gotCLI)
	}
}

// TestMCP_Open_Tool_RejectsThoughtID pins the cross-verb breadcrumb
// also applies on the MCP transport. A thought-id-shaped subject must
// produce a tool error whose message mentions `lineage`.
func TestMCP_Open_Tool_RejectsThoughtID(t *testing.T) {
	root := initProject(t)
	c := startMCP(t, root, "agent-a")
	resp := c.rpc(t, "tools/call", map[string]any{
		"name":      "open",
		"arguments": map[string]any{"subject": "1779345848015-cxkzz1"},
	})
	// The tool must surface an isError result OR a protocol-level error.
	// Either is acceptable; both must carry the `lineage` hint.
	if protoErr, hasProto := resp["error"]; hasProto {
		errBytes, _ := json.Marshal(protoErr)
		if !strings.Contains(string(errBytes), "lineage") {
			t.Errorf("MCP open protocol-error should mention lineage; got %s", errBytes)
		}
		return
	}
	result, _ := resp["result"].(map[string]any)
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Fatalf("MCP open on thought-id should be an error; got %v", result)
	}
	// The MCP tool errors land in result.content[].text per the SDK.
	contentBytes, _ := json.Marshal(result["content"])
	if !strings.Contains(string(contentBytes), "lineage") {
		t.Errorf("MCP open tool error should mention lineage; got %s", contentBytes)
	}
}
