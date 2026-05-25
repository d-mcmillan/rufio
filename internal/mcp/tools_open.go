// Package mcp — `open` tool (v1.0.2).
//
// MCP-transport analogue of `rufio open <subject>`. The fidelity contract
// is load-bearing: the wire shape returned here MUST be byte-identical to
// what `rufio open --json` emits, so agents using either transport see
// the same substrate snapshot. Both surfaces share open.JSONPayload to
// guarantee this — never construct the payload locally in this file.
package mcp

import (
	"context"
	"regexp"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/open"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// openIn mirrors the CLI's `rufio open` flag set. Subject is required
// (namespace:local — thought-id-shaped values get redirected to lineage).
// Topics / Since / Scope / Limit are optional and default exactly as the
// CLI side does (see mcpOpenDefault* below).
type openIn struct {
	Subject string   `json:"subject" jsonschema:"the namespace:local subject to open (required); thought-id-shaped values are rejected with a hint at the lineage tool"`
	Topics  []string `json:"topics,omitempty" jsonschema:"optional topic-token list; server-side ANY-match against the recall and thoughts sections"`
	Since   string   `json:"since,omitempty" jsonschema:"recency floor as Go duration (e.g. 10m, 24h); default 24h"`
	Scope   string   `json:"scope,omitempty" jsonschema:"visibility scope (agent|deployment|fleet); default fleet — privacy.IsVisible is the floor regardless"`
	Limit   int      `json:"limit,omitempty" jsonschema:"max rows per section; default 50"`
}

// openOut is the typed wire shape. The field set + json tags MUST mirror
// what open.JSONPayload emits one-for-one so the strongly-typed MCP
// schema generator agrees with what the CLI's --json renderer produces.
// Sections are []map[string]interface{} (not typed structs) because the
// row schema lives in open.RecallRowJSON / fleetRowJSON — sharing those
// helpers is the fidelity gate.
type openOut struct {
	Type               string                   `json:"_type"`
	Version            int                      `json:"_version"`
	Subject            string                   `json:"subject"`
	Agent              string                   `json:"agent"`
	Daemon             map[string]interface{}   `json:"daemon"`
	Fleet              []map[string]interface{} `json:"fleet"`
	Attention          []map[string]interface{} `json:"attention"`
	Recall             []map[string]interface{} `json:"recall"`
	Thoughts           []map[string]interface{} `json:"thoughts"`
	HiddenPrivateCount int                      `json:"hidden_private_count"`
}

// mcpOpenThoughtIDPattern matches the canonical thought-id shape on the
// MCP front door so the cross-verb breadcrumb works for MCP clients too.
// Identical regex to internal/cli/open.go's thoughtIDPattern; duplicated
// (not imported) because internal/cli is the binary's front door, not a
// library — depending on it from internal/mcp would invert the import
// graph.
var mcpOpenThoughtIDPattern = regexp.MustCompile(`^[0-9]+-[a-z0-9]{6}$`)

// mcpOpenDefault* mirrors the CLI's openDefault* constants. The two
// surfaces share open.Bundle so the defaults MUST match exactly — keep
// these in sync with internal/cli/open.go if either side moves.
const (
	mcpOpenDefaultSince = 24 * time.Hour
	mcpOpenDefaultScope = "fleet"
	mcpOpenDefaultLimit = 50
)

// registerOpen wires the `open` tool. The handler returns a typed
// openOut populated from open.JSONPayload so the wire shape is
// byte-identical to `rufio open --json` while the MCP SDK still gets a
// concrete schema for tool introspection.
func registerOpen(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "open",
		Description: "Read-bundle the substrate state for a subject: identity, daemon, fleet, " +
			"attention, recall, thoughts. Read-dual of the attend tool. Pure read.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in openIn) (*mcp.CallToolResult, openOut, error) {
		// Subject validation: thought-id-shape redirects to the lineage
		// tool; otherwise the same namespace:local regex the write verbs
		// use. Keeps the cross-verb breadcrumb consistent across transports.
		if mcpOpenThoughtIDPattern.MatchString(in.Subject) {
			return nil, openOut{}, toolErr(&rufioerr.UsageError{Message: in.Subject + " looks like a thought id - try the lineage tool with that id for its audit trail"})
		}
		if err := thought.ValidateSubject(in.Subject); err != nil {
			return nil, openOut{}, toolErr(err)
		}

		since := mcpOpenDefaultSince
		if in.Since != "" {
			d, perr := time.ParseDuration(in.Since)
			if perr != nil || d <= 0 {
				return nil, openOut{}, toolErr(&rufioerr.UsageError{Message: "invalid since: " + in.Since + " (want positive Go duration, e.g. 10m, 24h)"})
			}
			since = d
		}
		scope := mcpOpenDefaultScope
		if in.Scope != "" {
			if err := thought.ValidateScope(in.Scope); err != nil {
				return nil, openOut{}, toolErr(err)
			}
			scope = in.Scope
		}
		limit := mcpOpenDefaultLimit
		if in.Limit > 0 {
			limit = in.Limit
		}

		bundle, err := open.Bundle(r.Root, open.Params{
			Subject:      in.Subject,
			Topics:       in.Topics,
			Since:        since,
			Scope:        scope,
			Limit:        limit,
			CurrentAgent: r.Agent,
		})
		if err != nil {
			return nil, openOut{}, toolErr(err)
		}

		return nil, projectOpenOut(bundle, r.Root), nil
	})
}

// projectOpenOut reshapes open.JSONPayload's map into the typed openOut
// struct the MCP SDK uses for schema generation. The payload IS the
// fidelity contract — projectOpenOut just re-types the section maps so
// the MCP schema can describe them. Any change to the wire shape
// belongs in open.JSONPayload, not here.
func projectOpenOut(b open.OpenBundle, root string) openOut {
	payload := open.JSONPayload(b, root)
	daemon, _ := payload["daemon"].(map[string]interface{})
	fleet, _ := payload["fleet"].([]map[string]interface{})
	attn, _ := payload["attention"].([]map[string]interface{})
	rec, _ := payload["recall"].([]map[string]interface{})
	tho, _ := payload["thoughts"].([]map[string]interface{})
	return openOut{
		Type:               "open",
		Version:            1,
		Subject:            b.Subject,
		Agent:              b.Agent,
		Daemon:             daemon,
		Fleet:              fleet,
		Attention:          attn,
		Recall:             rec,
		Thoughts:           tho,
		HiddenPrivateCount: b.HiddenPrivateCount,
	}
}
