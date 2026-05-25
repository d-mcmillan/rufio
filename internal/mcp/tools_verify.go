package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
	"github.com/d-mcmillan/rufio/internal/lib/retract"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// mcpTargetRef adapts (author, scope) to the privacy.Record interface
// for the MCP confirm/refute authz check — parallel to targetRef in
// internal/cli/confirm.go. Kept package-private to MCP because confirm
// and refute are the only callers here.
type mcpTargetRef struct{ author, scope string }

func (t mcpTargetRef) GetAuthor() string { return t.author }
func (t mcpTargetRef) GetScope() string  { return t.scope }

// ---- confirm ----

type confirmIn struct {
	ThoughtID string `json:"thought_id" jsonschema:"the id of the thought to confirm (anyone may confirm any thought)"`
	Evidence  string `json:"evidence,omitempty" jsonschema:"optional free-text evidence supporting the confirmation"`
}

// confirmOut mirrors runConfirm's --json payload keys EXACTLY (see
// internal/cli/confirm.go): _type="confirm", _version="1", target, by,
// ts; `evidence` is CONDITIONAL — present only when non-empty (the CLI
// only sets payload["evidence"] when evidence != ""), so it is a pointer
// with omitempty to match the absent-key semantics byte-for-byte.
type confirmOut struct {
	Type     string  `json:"_type"`
	Version  string  `json:"_version"`
	Target   string  `json:"target"`
	By       string  `json:"by"`
	TS       string  `json:"ts"`
	Evidence *string `json:"evidence,omitempty"`
}

func registerConfirm(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "confirm",
		Description: "Confirm another agent's thought (or your own). Appends to live/confirms/<id>.gdl.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in confirmIn) (*mcp.CallToolResult, confirmOut, error) {
		// Verify the target exists AND apply the privacy gate (#147).
		// scope:agent thoughts are non-author-writeable; broader-scope
		// thoughts continue to admit crowd validation from any peer.
		author, scope, err := retract.LookupTarget(r.Root, in.ThoughtID)
		if err != nil {
			return nil, confirmOut{}, toolErr(err)
		}
		if !privacy.CanWriteAgainst(mcpTargetRef{author: author, scope: scope}, r.Agent) {
			return nil, confirmOut{}, toolErr(&rufioerr.PrivateRecordAuthzError{
				Verb: "confirm", ID: in.ThoughtID, Author: author,
			})
		}
		ts := versioning.NowISO()
		rec := confirm.BuildConfirm(in.ThoughtID, r.Agent, in.Evidence, ts)
		if err := confirm.Append(r.Root, in.ThoughtID, rec); err != nil {
			return nil, confirmOut{}, toolErr(err)
		}
		out := confirmOut{
			Type: "confirm", Version: "1", Target: in.ThoughtID,
			By: r.Agent, TS: ts,
		}
		if in.Evidence != "" {
			e := in.Evidence
			out.Evidence = &e
		}
		return nil, out, nil
	})
}

// ---- refute ----

type refuteIn struct {
	ThoughtID string `json:"thought_id" jsonschema:"the id of the thought to refute (anyone may refute any thought)"`
	Reason    string `json:"reason" jsonschema:"free-text reason for the refutation (required)"`
	Evidence  string `json:"evidence,omitempty" jsonschema:"optional free-text evidence supporting the refutation"`
}

// refuteOut mirrors runRefute's --json payload keys EXACTLY (see
// internal/cli/refute.go): _type="refute", _version="1", target, reason,
// by, ts; `evidence` conditional, same semantics as confirmOut.
type refuteOut struct {
	Type     string  `json:"_type"`
	Version  string  `json:"_version"`
	Target   string  `json:"target"`
	Reason   string  `json:"reason"`
	By       string  `json:"by"`
	TS       string  `json:"ts"`
	Evidence *string `json:"evidence,omitempty"`
}

func registerRefute(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "refute",
		Description: "Refute another agent's thought (or your own). Appends to live/confirms/<id>.gdl.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in refuteIn) (*mcp.CallToolResult, refuteOut, error) {
		reasonText := strings.TrimSpace(in.Reason)
		if reasonText == "" {
			return nil, refuteOut{}, toolErr(&rufioerr.InvalidContentError{Field: "reason"})
		}
		author, scope, err := retract.LookupTarget(r.Root, in.ThoughtID)
		if err != nil {
			return nil, refuteOut{}, toolErr(err)
		}
		if !privacy.CanWriteAgainst(mcpTargetRef{author: author, scope: scope}, r.Agent) {
			return nil, refuteOut{}, toolErr(&rufioerr.PrivateRecordAuthzError{
				Verb: "refute", ID: in.ThoughtID, Author: author,
			})
		}
		ts := versioning.NowISO()
		rec := confirm.BuildRefute(in.ThoughtID, r.Agent, reasonText, in.Evidence, ts)
		if err := confirm.Append(r.Root, in.ThoughtID, rec); err != nil {
			return nil, refuteOut{}, toolErr(err)
		}
		out := refuteOut{
			Type: "refute", Version: "1", Target: in.ThoughtID,
			Reason: reasonText, By: r.Agent, TS: ts,
		}
		if in.Evidence != "" {
			e := in.Evidence
			out.Evidence = &e
		}
		return nil, out, nil
	})
}
