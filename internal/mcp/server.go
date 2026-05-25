package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Serve resolves root+identity once, builds the server with the registered
// tool set, and runs it over stdio until the client disconnects.
//
// Resolution errors are returned BEFORE the stdio loop is entered, so the
// cobra RunE can print them like any other verb and exit. Once Run is
// entered, tool errors are mapped by toolErr and the server never exits.
func Serve(rootFlag, agentFlag, version string) error {
	r, err := resolve(rootFlag, agentFlag)
	if err != nil {
		return err // surfaced by the cobra RunE (stderr + exit), BEFORE the server loop
	}
	s := buildServer(r, version)
	return s.Run(context.Background(), &mcp.StdioTransport{})
}

// NewServerFor builds an MCP server for a SPECIFIC root + agent identity
// without running it. This is the per-request constructor used by the
// HTTPS transport (internal/lib/serve): each incoming HTTP request resolves
// the caller's identity from the Bearer token, then constructs a fresh
// MCP server bound to that identity. The same tool roster is registered
// either way — stdio (one server, one identity) and HTTPS (per-request
// server, per-request identity) share the registration code so the tool
// surface stays in lockstep across transports.
//
// agent MAY be empty: tools follow the "anonymous = firehose" rule for
// downstream privacy (see internal/lib/privacy). The serve package only
// calls NewServerFor with a resolved identity in production; the empty
// case exists for tests + the health probe.
func NewServerFor(root, agent, version string) *mcp.Server {
	return buildServer(Resolved{Root: root, Agent: agent}, version)
}

// buildServer is the shared server constructor. Every tool registration
// goes here so stdio + HTTPS expose the identical surface.
func buildServer(r Resolved, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "rufio", Version: version}, nil)

	// Canary (PR1).
	registerAttend(s, r)

	// Thought group (PR2): think, observe, reason, retract.
	registerThink(s, r)
	registerObserve(s, r)
	registerReason(s, r)
	registerRetract(s, r)

	// Verify group (PR2): confirm, refute.
	registerConfirm(s, r)
	registerRefute(s, r)

	// Recall (PR2): the read tool (not in any write/verify/channel/goal
	// group file — own file, own registration).
	registerRecall(s, r)

	// Channels group (PR2): summon, accept, decline, say, leave, close.
	registerSummon(s, r)
	registerAccept(s, r)
	registerDecline(s, r)
	registerSay(s, r)
	registerLeave(s, r)
	registerClose(s, r)

	// Goals group (PR2): goal, goals_list, goal_complete, goal_abandon.
	registerGoal(s, r)
	registerGoalsList(s, r)
	registerGoalComplete(s, r)
	registerGoalAbandon(s, r)

	// Listen (PR3): the bounded poll read tool over this agent's listen
	// surface (per-agent inbox + project-wide substrate; see
	// stream.ListenDirs — same source the CLI listen path uses, locking
	// MCP/CLI symmetry per the v1.0.3 PR #188 gate fix). stream.Poll is
	// a single bounded WalkDir + sort + slice (no fsnotify, no tail) so
	// it cannot block the stdio loop — context.Background() is
	// acceptable.
	registerListen(s, r)

	// Open (v1.2.0): the read-dual of attend. Bundles identity + daemon +
	// fleet + attention + recall + thoughts on subject. Fidelity contract
	// with `rufio open --json` — same lib (internal/lib/open), same JSON
	// shape on the wire.
	registerOpen(s, r)

	// Quickstart (v1.0.3, tool #21): the cold-start card for cold
	// agents. Pure read, no parameters. Mirrors `rufio quickstart
	// --json` via shared quickstart.CardV1 + CardVersion constants.
	registerQuickstart(s, r)

	// ServeStatus (v1.0.4, tool #22): read-only health probe for the
	// hosted server. Token mint/revoke are deliberately NOT exposed via
	// MCP — only the local operator may invoke them.
	registerServeStatus(s, r)

	return s
}
