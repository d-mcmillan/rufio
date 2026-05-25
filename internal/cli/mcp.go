package cli

import (
	"github.com/spf13/cobra"

	mcpadapter "github.com/d-mcmillan/rufio/internal/mcp"
)

// NewMcpCmd returns the `rufio mcp` Cobra command: a long-lived MCP stdio
// (JSON-RPC) server exposing the agent-participation cognition verbs as
// tools. One server instance = one agent identity, resolved once at start.
//
// Startup errors (bad --root / unresolved identity) happen BEFORE the stdio
// loop and are printed + exited via HandleError, exactly like every other
// verb. Once the stdio loop is entered, tool errors are mapped to MCP tool
// errors by the adapter and the server never exits.
//
// The Long: description enumerates the 22 tools the server exposes so a
// cold MCP integrator (Claude Desktop / Cursor / their own MCP client)
// can SEE what they're wiring up without spinning up the server or
// reading docs/mcp.md (#156). The roster MUST stay in lockstep with the
// register* calls in internal/mcp/server.go — the
// TestMcpHelp_ListsAllTools test pins this.
//
// v1.2.0 added `open` — the read-dual of `attend` — bringing the roster
// to 20. v1.0.3 added `quickstart` (cold-start card transport mirror) for
// 21. v1.0.4 added `serve_status` (hosted-server health probe) for 22.
// The symmetry contract: every CLI verb has a matching MCP tool.
func NewMcpCmd(version string) *cobra.Command {
	var rootFlag, agentFlag string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP stdio server (agent-participation subset)",
		Long: `Exposes the agent-participation cognition verbs as MCP tools over stdio.
One server instance = one agent identity, resolved once at start.

Tools exposed (22):
  attend, think, observe, reason, retract,
  confirm, refute, recall,
  summon, accept, decline, say, leave, close,
  goal, goals_list, goal_complete, goal_abandon,
  listen, open, quickstart, serve_status

Each tool mirrors the corresponding rufio CLI verb. See docs/mcp.md for
the JSON-RPC schema and per-tool input/output contracts.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := mcpadapter.Serve(rootFlag, agentFlag, version); err != nil {
				HandleError("mcp", err) // never returns (os.Exit) — only reached on STARTUP error
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "substrate root (default: walk up from cwd)")
	cmd.Flags().StringVar(&agentFlag, "agent", "", "agent identity (default: RUFIO_AGENT_ID, then .rufio/identity.local.gdl)")
	return cmd
}
