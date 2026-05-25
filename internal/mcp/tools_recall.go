package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/d-mcmillan/rufio/internal/lib/recall"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

type recallIn struct {
	Query          string `json:"query,omitempty" jsonschema:"optional query: an entity id (exact subject match) or space-separated AND substrings"`
	Scope          string `json:"scope,omitempty" jsonschema:"visibility scope filter: agent|deployment|fleet"`
	Types          string `json:"types,omitempty" jsonschema:"CSV of record types to include (default: all): given,learned,thought,observation,reason,summon,channel-message,goal"`
	Since          string `json:"since,omitempty" jsonschema:"include only records younger than this Go duration (e.g. 24h)"`
	AsOf           string `json:"as_of,omitempty" jsonschema:"RFC3339 timestamp; exclude records newer than this"`
	IncludeExpired bool   `json:"include_expired,omitempty" jsonschema:"include retracted/expired records in the corpus"`
}

// recallOut wraps the JSONL the CLI's `recall --json` streams to stdout.
// recall is a READ verb: runRecall emits one JSON object PER record via
// recall.RenderJSON (line-delimited). The MCP tool returns the SAME
// objects (produced by the SAME RenderJSON, so byte-identical including
// the #89-fixed conditional `type` key and the populated observation
// `id`) collected into a non-nil `records` array.
type recallOut struct {
	Records []map[string]interface{} `json:"records"`
}

func registerRecall(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "recall",
		Description: "Scan the corpus across given/learned/live and return matching records (read-only).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in recallIn) (*mcp.CallToolResult, recallOut, error) {
		// Parse + validate flags BEFORE touching the FS (mirrors runRecall).
		types, err := recall.ValidateTypes(in.Types)
		if err != nil {
			return nil, recallOut{}, toolErr(err)
		}
		if in.Scope != "" {
			if err := thought.ValidateScope(in.Scope); err != nil {
				return nil, recallOut{}, toolErr(err)
			}
		}
		since, err := recall.ParseSince(in.Since)
		if err != nil {
			return nil, recallOut{}, toolErr(err)
		}
		asof, err := recall.ParseAsOf(in.AsOf)
		if err != nil {
			return nil, recallOut{}, toolErr(err)
		}

		// runRecall treats identity as best-effort (CurrentAgent="" when
		// unresolved). The MCP server resolved identity once at startup, so
		// r.Agent is always the resolved agent — equivalent to the CLI's
		// happy path; recall never errors on identity.
		currentAgent := r.Agent

		records, err := recall.Scan(r.Root, true)
		if err != nil {
			return nil, recallOut{}, toolErr(err)
		}
		filtered := recall.Filter(records, recall.FilterParams{
			Types: types, Scope: in.Scope, Since: since, AsOf: asof,
			IncludeExpired: in.IncludeExpired, CurrentAgent: currentAgent,
		})
		matched := recall.Match(filtered, in.Query)

		// Render through the SAME recall.RenderJSON the CLI uses, then
		// decode each JSONL line back into a generic object. This makes the
		// per-record shape byte-identical to `recall --json` (no parallel
		// struct that could drift), including the conditional `type` key.
		var buf bytes.Buffer
		if err := recall.RenderJSON(&buf, r.Root, matched); err != nil {
			return nil, recallOut{}, toolErr(err)
		}
		out := recallOut{Records: []map[string]interface{}{}}
		for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				return nil, recallOut{}, toolErr(err)
			}
			out.Records = append(out.Records, m)
		}
		return nil, out, nil
	})
}
