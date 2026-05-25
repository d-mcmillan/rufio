package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// ---- goal (write) ----

type goalIn struct {
	Statement string `json:"statement" jsonschema:"goal statement (required)"`
	By        string `json:"by,omitempty" jsonschema:"deadline (free-text in v1; e.g. 'EOW', '2026-06-01')"`
	Parent    string `json:"parent,omitempty" jsonschema:"parent goal id (optional)"`
	Scope     string `json:"scope,omitempty" jsonschema:"scope: agent|deployment|fleet (default fleet)"`
}

// goalOut mirrors runGoalWrite's --json payload keys EXACTLY (see
// internal/cli/goal.go): _type="goal", _version="1", id, author,
// statement, scope, ts; `by` and `parent` are ALWAYS present (the CLI
// sets them to nil when absent — *string, no omitempty, so they
// serialise as JSON null exactly like the CLI).
type goalOut struct {
	Type      string  `json:"_type"`
	Version   string  `json:"_version"`
	ID        string  `json:"id"`
	Author    string  `json:"author"`
	Statement string  `json:"statement"`
	Scope     string  `json:"scope"`
	TS        string  `json:"ts"`
	By        *string `json:"by"`
	Parent    *string `json:"parent"`
}

func registerGoal(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "goal",
		Description: "Declare a coordination goal (writes live/goals/active/<id>.gdl).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in goalIn) (*mcp.CallToolResult, goalOut, error) {
		// H3a (#125): scope defaults to "fleet" — matches the CLI's
		// unified write-verb default (was: "agent" pre-H3a).
		scope := in.Scope
		if scope == "" {
			scope = "fleet"
		}
		if err := goal.ValidateStatement(in.Statement); err != nil {
			return nil, goalOut{}, toolErr(err)
		}
		if err := thought.ValidateScope(scope); err != nil {
			return nil, goalOut{}, toolErr(err)
		}
		if err := thought.ValidateParent(in.Parent); err != nil {
			return nil, goalOut{}, toolErr(err)
		}
		id, err := goal.GenerateID()
		if err != nil {
			return nil, goalOut{}, toolErr(err)
		}
		ts := versioning.NowISO()
		statement := strings.TrimSpace(in.Statement)
		rec := goal.BuildGoalRecord(id, r.Agent, statement, in.By, in.Parent, scope, ts)
		if err := goal.WriteActive(r.Root, id, rec); err != nil {
			return nil, goalOut{}, toolErr(err)
		}
		out := goalOut{
			Type: "goal", Version: "1", ID: id, Author: r.Agent,
			Statement: statement, Scope: scope, TS: ts,
		}
		if in.By != "" {
			b := in.By
			out.By = &b
		}
		if in.Parent != "" {
			p := in.Parent
			out.Parent = &p
		}
		return nil, out, nil
	})
}

// ---- goals_list (read) ----

type goalsListIn struct {
	Scope string `json:"scope,omitempty" jsonschema:"filter to goals with this scope (agent|deployment|fleet); empty = no filter"`
	State string `json:"state,omitempty" jsonschema:"filter to goals in this state (active|completed|abandoned); empty = all"`
}

// goalsListOut wraps the JSONL `goals list --json` streams. goals_list is
// a read verb. The per-goal JSON shape (locked keys + conditional audit
// keys) is single-sourced in goal.RenderJSON — the SAME renderer the CLI
// uses (no parallel map that could drift). goals is always a non-nil
// array. (Same proven pattern as recall's recallOut over
// recall.RenderJSON.)
type goalsListOut struct {
	Goals []map[string]interface{} `json:"goals"`
}

func registerGoalsList(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "goals_list",
		Description: "List coordination goals across the project (read-only; mirrors `rufio goals list`).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in goalsListIn) (*mcp.CallToolResult, goalsListOut, error) {
		// Validate filters BEFORE touching the FS (mirrors runGoalsList).
		if in.Scope != "" {
			if err := thought.ValidateScope(in.Scope); err != nil {
				return nil, goalsListOut{}, toolErr(err)
			}
		}
		if in.State != "" {
			switch goal.State(in.State) {
			case goal.StateActive, goal.StateCompleted, goal.StateAbandoned:
				// ok
			default:
				return nil, goalsListOut{}, toolErr(&rufioerr.UsageError{
					Message: "invalid --state \"" + in.State + "\": must be one of [active completed abandoned]",
				})
			}
		}
		all, err := goal.ReadAll(r.Root)
		if err != nil {
			return nil, goalsListOut{}, toolErr(err)
		}
		// Filter pass — preserves ReadAll ordering (mirrors runGoalsList).
		// Privacy filter (#147) hides scope:agent goals authored by other
		// agents; r.Agent is always set on the MCP path (the server
		// resolves identity at startup), so the firehose branch is never
		// taken here.
		matched := make([]goal.Goal, 0, len(all))
		for _, g := range all {
			if !privacy.IsVisible(g, r.Agent) {
				continue
			}
			if in.Scope != "" && g.Scope != in.Scope {
				continue
			}
			if in.State != "" && string(g.State) != in.State {
				continue
			}
			matched = append(matched, g)
		}

		// Render through the SAME goal.RenderJSON the CLI uses, then decode
		// each JSONL line back into a generic object. This makes the
		// per-record shape byte-identical to `goals list --json` (no
		// parallel map that could drift), including the conditional
		// audit keys. (Same proven pattern as tools_recall.go.)
		var buf bytes.Buffer
		if err := goal.RenderJSON(&buf, matched); err != nil {
			return nil, goalsListOut{}, toolErr(err)
		}
		out := goalsListOut{Goals: []map[string]interface{}{}}
		for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				return nil, goalsListOut{}, toolErr(err)
			}
			out.Goals = append(out.Goals, m)
		}
		return nil, out, nil
	})
}

// ---- goal_complete ----

type goalCompleteIn struct {
	GoalID  string `json:"goal_id" jsonschema:"the active goal id to complete (author-only)"`
	Outcome string `json:"outcome" jsonschema:"outcome description (required)"`
}

// goalCompleteOut mirrors runGoalComplete's --json payload keys EXACTLY
// (see internal/cli/goal_complete.go): _type="goal-complete",
// _version="1", id, by, outcome, ts.
type goalCompleteOut struct {
	Type    string `json:"_type"`
	Version string `json:"_version"`
	ID      string `json:"id"`
	By      string `json:"by"`
	Outcome string `json:"outcome"`
	TS      string `json:"ts"`
}

func registerGoalComplete(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "goal_complete",
		Description: "Mark an active goal as completed (author-only; archives to completed/).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in goalCompleteIn) (*mcp.CallToolResult, goalCompleteOut, error) {
		outcome := strings.TrimSpace(in.Outcome)
		if outcome == "" {
			return nil, goalCompleteOut{}, toolErr(&rufioerr.InvalidContentError{Field: "outcome"})
		}
		loaded, err := goal.LoadAnyState(r.Root, in.GoalID)
		if err != nil {
			return nil, goalCompleteOut{}, toolErr(err)
		}
		if loaded.State != goal.StateActive {
			return nil, goalCompleteOut{}, toolErr(&rufioerr.NoSuchGoalError{ID: in.GoalID})
		}
		if loaded.Author != r.Agent {
			return nil, goalCompleteOut{}, toolErr(&rufioerr.GoalAuthError{ID: in.GoalID, Author: loaded.Author})
		}
		ts := versioning.NowISO()
		if err := goal.MoveToCompleted(r.Root, in.GoalID, r.Agent, outcome, ts); err != nil {
			return nil, goalCompleteOut{}, toolErr(err)
		}
		return nil, goalCompleteOut{
			Type: "goal-complete", Version: "1", ID: in.GoalID,
			By: r.Agent, Outcome: outcome, TS: ts,
		}, nil
	})
}

// ---- goal_abandon ----

type goalAbandonIn struct {
	GoalID string `json:"goal_id" jsonschema:"the active goal id to abandon (author-only)"`
	Reason string `json:"reason" jsonschema:"reason description (required)"`
}

// goalAbandonOut mirrors runGoalAbandon's --json payload keys EXACTLY
// (see internal/cli/goal_abandon.go): _type="goal-abandon",
// _version="1", id, by, reason, ts.
type goalAbandonOut struct {
	Type    string `json:"_type"`
	Version string `json:"_version"`
	ID      string `json:"id"`
	By      string `json:"by"`
	Reason  string `json:"reason"`
	TS      string `json:"ts"`
}

func registerGoalAbandon(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "goal_abandon",
		Description: "Abandon an active goal (author-only; archives to abandoned/).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in goalAbandonIn) (*mcp.CallToolResult, goalAbandonOut, error) {
		reasonText := strings.TrimSpace(in.Reason)
		if reasonText == "" {
			return nil, goalAbandonOut{}, toolErr(&rufioerr.InvalidContentError{Field: "reason"})
		}
		loaded, err := goal.LoadAnyState(r.Root, in.GoalID)
		if err != nil {
			return nil, goalAbandonOut{}, toolErr(err)
		}
		if loaded.State != goal.StateActive {
			return nil, goalAbandonOut{}, toolErr(&rufioerr.NoSuchGoalError{ID: in.GoalID})
		}
		if loaded.Author != r.Agent {
			return nil, goalAbandonOut{}, toolErr(&rufioerr.GoalAuthError{ID: in.GoalID, Author: loaded.Author})
		}
		ts := versioning.NowISO()
		if err := goal.MoveToAbandoned(r.Root, in.GoalID, r.Agent, reasonText, ts); err != nil {
			return nil, goalAbandonOut{}, toolErr(err)
		}
		return nil, goalAbandonOut{
			Type: "goal-abandon", Version: "1", ID: in.GoalID,
			By: r.Agent, Reason: reasonText, TS: ts,
		}, nil
	})
}
