package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/d-mcmillan/rufio/internal/lib/recall"
	"github.com/d-mcmillan/rufio/internal/lib/stream"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// listenIn mirrors `rufio listen`'s agent-relevant flags. The CLI flag is
// --types (CSV, parsed by recall.ValidateTypes) and --scope (validated by
// thought.ValidateScope) — see internal/cli/listen.go. `cursor`/`max` are
// the poll-specific additions (the CLI tails forever; the MCP tool is a
// bounded poll). --as/--catch-up have no MCP analogue: identity is resolved
// once at server start, and a bounded poll IS the catch-up. The walk
// surface (per-agent inbox + project-wide substrate dirs) is sourced from
// stream.ListenDirs so both transports walk identical paths — see
// registerListen below for the symmetry-lock rationale.
type listenIn struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"opaque resume cursor from a previous listen call; omit for the first poll"`
	Max    int    `json:"max,omitempty" jsonschema:"max events to return (default 100)"`
	Types  string `json:"types,omitempty" jsonschema:"optional CSV record-type filter (same values as rufio listen --types)"`
	Scope  string `json:"scope,omitempty" jsonschema:"optional scope filter: agent|deployment|fleet (same as rufio listen --scope)"`
}

// listenOut returns the bounded page plus the opaque next cursor. Events is
// the verbatim stream.Event schema (notification-ready: a future push
// transport reuses it unchanged). The events array is always non-nil per
// the doc.go Out discipline.
type listenOut struct {
	Events     []stream.Event `json:"events"`
	NextCursor string         `json:"next_cursor"`
}

// registerListen wires the bounded listen poll. It replicates the filter-
// build of internal/cli/listen.go (recall.ValidateTypes for --types,
// thought.ValidateScope for --scope) over the pre-resolved identity,
// then calls the stateless stream.Poll. Poll is a single bounded
// WalkDir + sort + slice — it never watches or blocks — so running it
// under the server's context.Background() cannot stall the stdio loop.
//
// Walk dirs are sourced from stream.ListenDirs(agent) — the SAME
// function the CLI listen path uses. This is the structural lock that
// keeps the two transports in symmetry; the PR #188 MCP gate caught a
// pre-existing divergence where MCP listen walked only the per-agent
// inbox while CLI listen walked the broader substrate (outbox,
// channels, summons, confirms, retracted, reasoning, promoted). The
// v1.0.3 addition of live/promoted/ for auto-promote events made the
// divergence user-visible. Sourcing both from stream.ListenDirs means
// a future dir addition automatically applies to both surfaces.
func registerListen(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "listen",
		Description: "Bounded-response poll of this agent's listen surface (inbox + project-wide substrate: outbox, channels, summons, confirms, retracted, reasoning, promoted). Returns events since `cursor` (opaque, monotonic) plus next_cursor. Poll repeatedly; an unchanged cursor means no new events.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listenIn) (*mcp.CallToolResult, listenOut, error) {
		// Validate flags BEFORE any FS work — matches runListen's discipline.
		types, err := recall.ValidateTypes(in.Types)
		if err != nil {
			return nil, listenOut{}, toolErr(err)
		}
		if in.Scope != "" {
			if err := thought.ValidateScope(in.Scope); err != nil {
				return nil, listenOut{}, toolErr(err)
			}
		}

		fp := stream.FilterParams{Types: types, Scope: in.Scope, CurrentAgent: r.Agent}
		dirs := stream.ListenDirs(r.Agent)

		evs, next, err := stream.Poll(r.Root, dirs, fp, in.Cursor, in.Max)
		if err != nil {
			return nil, listenOut{}, toolErr(err)
		}
		if evs == nil {
			evs = []stream.Event{}
		}
		return nil, listenOut{Events: evs, NextCursor: next}, nil
	})
}
