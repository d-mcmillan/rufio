// Package mcp — `quickstart` tool (v1.0.3, MCP tool #21).
//
// MCP-transport analogue of `rufio quickstart`. The fidelity contract
// is load-bearing: the wire shape returned here MUST be byte-identical
// to what `rufio quickstart --json` emits, so agents using either
// transport see the same cold-start card. Both surfaces share
// quickstart.CardV1 + CardVersion to guarantee this — never duplicate
// the prose in this file.
package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/d-mcmillan/rufio/internal/lib/quickstart"
)

// quickstartIn is the (empty) input schema for the tool. The card has
// no parameters — it's a constant snapshot returned verbatim. Future
// enrichments (e.g. --topic= to surface topic-specific quickstarts)
// would land additive fields here without changing the existing shape.
type quickstartIn struct{}

// quickstartOut is the typed wire shape. Field set + json tags MUST
// mirror runQuickstart's --json payload one-for-one so the MCP schema
// generator agrees with what the CLI emits. v1 schema is locked at
// {_type, _version, content, card_version}.
type quickstartOut struct {
	Type        string `json:"_type"`
	Version     int    `json:"_version"`
	Content     string `json:"content"`
	CardVersion int    `json:"card_version"`
}

// registerQuickstart wires the `quickstart` tool. Pure read; no
// project root or identity required — the tool exists precisely to
// onboard cold agents BEFORE they have either.
func registerQuickstart(s *mcp.Server, _ Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "quickstart",
		Description: "Return the locked cold-start card: the seven first-contact verbs, " +
			"quorum dynamics (≥3 confirmers at ≥0.85 → auto-promote), and the " +
			"subject-vs-topics distinction. Read-once, then participate. Pure read.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ quickstartIn) (*mcp.CallToolResult, quickstartOut, error) {
		return nil, quickstartOut{
			Type:        "quickstart",
			Version:     1,
			Content:     quickstart.CardV1,
			CardVersion: quickstart.CardVersion,
		}, nil
	})
}
