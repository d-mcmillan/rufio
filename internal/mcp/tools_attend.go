package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

type attendIn struct {
	Intent   string   `json:"intent" jsonschema:"why the agent is attending (required, non-empty)"`
	Entities []string `json:"entities,omitempty" jsonschema:"entity ids this attention is about, namespace:local form e.g. customer:5821 (required, >=1)"`
	Topics   []string `json:"topics,omitempty" jsonschema:"topic tags for this attention"`
	// Scope mirrors the CLI's --scope flag (#125). Defaults to "fleet"
	// when omitted — attention is a broadcast primitive. Validated
	// against the canonical enum (agent|deployment|fleet).
	Scope string `json:"scope,omitempty" jsonschema:"visibility scope (agent|deployment|fleet), default fleet"`
}

// attendOut mirrors runAttend's --json payload keys EXACTLY (see
// internal/cli/attend.go runAttend: _type="attend-set", _version="1",
// agent, intent, entities, topics (always a non-nil array), ts). The plan's
// draft struct (_type="attend", "author") disagreed with the real CLI; the
// CLI is the source of truth, so this struct follows runAttend.
type attendOut struct {
	Type     string   `json:"_type"`
	Version  string   `json:"_version"`
	Agent    string   `json:"agent"`
	Intent   string   `json:"intent"`
	Scope    string   `json:"scope"`
	Entities []string `json:"entities"`
	Topics   []string `json:"topics"`
	TS       string   `json:"ts"`
}

// registerAttend wires the `attend` canary tool. It replicates runAttend's
// body (validate intent/entities/topics -> BuildRecord -> Write) using the
// pre-resolved root+agent; it does NOT call runAttend (which re-resolves
// root from cwd, writes to os.Stdout, and os.Exit's via HandleError).
func registerAttend(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "attend",
		Description: "Record that this agent is attending to something on the substrate.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in attendIn) (*mcp.CallToolResult, attendOut, error) {
		// runAttend trims intent before validating; mirror that exactly so
		// the written record is byte-identical to the CLI's.
		intent := strings.TrimSpace(in.Intent)
		ent := in.Entities
		if ent == nil {
			ent = []string{}
		}
		top := in.Topics
		if top == nil {
			top = []string{}
		}
		if err := attention.ValidateIntent(intent); err != nil {
			return nil, attendOut{}, toolErr(err)
		}
		if err := attention.ValidateEntities(ent); err != nil {
			return nil, attendOut{}, toolErr(err)
		}
		if err := attention.ValidateTopics(top); err != nil {
			return nil, attendOut{}, toolErr(err)
		}
		// #125: mirror runAttend's default — empty -> fleet — and
		// validate against the canonical enum so a malformed value
		// errors before the disk write.
		scope := strings.TrimSpace(in.Scope)
		if scope == "" {
			scope = "fleet"
		}
		if err := thought.ValidateScope(scope); err != nil {
			return nil, attendOut{}, toolErr(err)
		}
		ts := versioning.NowISO()
		rec := attention.BuildRecord(r.Agent, intent, scope, ent, top, ts)
		if err := attention.Write(r.Root, r.Agent, rec); err != nil {
			return nil, attendOut{}, toolErr(err)
		}
		return nil, attendOut{
			Type:     "attend-set",
			Version:  "1",
			Agent:    r.Agent,
			Intent:   intent,
			Scope:    scope,
			Entities: ent,
			Topics:   top,
			TS:       ts,
		}, nil
	})
}
