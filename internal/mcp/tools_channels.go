package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/summon"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// ---- summon ----

type summonIn struct {
	To     string `json:"to" jsonschema:"the agent id to summon (opens a private channel on accept)"`
	Topic  string `json:"topic" jsonschema:"channel topic (required); free token or entity form like customer:5821"`
	Intent string `json:"intent" jsonschema:"free-text reason for the summon (required)"`
}

// summonOut mirrors runSummon's --json payload keys EXACTLY (see
// internal/cli/summon.go): _type="summon", _version="1", id, from, to,
// topic, intent, ts, ttl (int seconds, summon.DefaultTTL=86400).
type summonOut struct {
	Type    string `json:"_type"`
	Version string `json:"_version"`
	ID      string `json:"id"`
	From    string `json:"from"`
	To      string `json:"to"`
	Topic   string `json:"topic"`
	Intent  string `json:"intent"`
	TS      string `json:"ts"`
	TTL     int    `json:"ttl"`
}

func registerSummon(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "summon",
		Description: "Open a private channel by summoning another agent (24h TTL).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in summonIn) (*mcp.CallToolResult, summonOut, error) {
		if err := summon.ValidateTopic(in.Topic); err != nil {
			return nil, summonOut{}, toolErr(err)
		}
		if err := summon.ValidateIntent(in.Intent); err != nil {
			return nil, summonOut{}, toolErr(err)
		}
		id, err := summon.GenerateID()
		if err != nil {
			return nil, summonOut{}, toolErr(err)
		}
		ts := versioning.NowISO()
		ttl := summon.DefaultTTL
		rec := summon.BuildSummonRecord(id, r.Agent, in.To, in.Topic, in.Intent, ts, ttl)
		if err := summon.WritePending(r.Root, id, rec); err != nil {
			return nil, summonOut{}, toolErr(err)
		}
		return nil, summonOut{
			Type: "summon", Version: "1", ID: id, From: r.Agent, To: in.To,
			Topic: in.Topic, Intent: in.Intent, TS: ts, TTL: ttl,
		}, nil
	})
}

// ---- accept ----

type acceptIn struct {
	SummonID string `json:"summon_id" jsonschema:"the pending summon id to accept (only the summon's target may accept)"`
}

// acceptOut mirrors runAccept's --json payload keys EXACTLY (see
// internal/cli/accept.go): _type="accept", _version="1",
// "summon-id" (HYPHENATED key), channel, by, ts.
type acceptOut struct {
	Type     string `json:"_type"`
	Version  string `json:"_version"`
	SummonID string `json:"summon-id"`
	Channel  string `json:"channel"`
	By       string `json:"by"`
	TS       string `json:"ts"`
}

func registerAccept(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "accept",
		Description: "Accept a pending summon and open the channel (writes channel meta + moves the summon).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in acceptIn) (*mcp.CallToolResult, acceptOut, error) {
		loaded, err := summon.LoadAnyState(r.Root, in.SummonID)
		if err != nil {
			return nil, acceptOut{}, toolErr(err)
		}
		if loaded.State != summon.StatePending {
			return nil, acceptOut{}, toolErr(&rufioerr.NoSuchSummonError{ID: in.SummonID})
		}
		if loaded.To != r.Agent {
			return nil, acceptOut{}, toolErr(&rufioerr.SummonAuthError{ID: in.SummonID, Target: loaded.To})
		}
		chID, err := channels.GenerateID()
		if err != nil {
			return nil, acceptOut{}, toolErr(err)
		}
		ts := versioning.NowISO()
		// Order (D15.6): channel meta FIRST, then state move.
		metaRec := channels.BuildMetaRecord(chID, loaded.From, loaded.To, loaded.Topic, loaded.Intent, ts)
		if err := channels.WriteMeta(r.Root, chID, metaRec); err != nil {
			return nil, acceptOut{}, toolErr(err)
		}
		if err := summon.MoveToAccepted(r.Root, in.SummonID, r.Agent, chID, ts); err != nil {
			return nil, acceptOut{}, toolErr(err)
		}
		return nil, acceptOut{
			Type: "accept", Version: "1", SummonID: in.SummonID,
			Channel: chID, By: r.Agent, TS: ts,
		}, nil
	})
}

// ---- decline ----

type declineIn struct {
	SummonID string `json:"summon_id" jsonschema:"the pending summon id to decline (only the summon's target may decline)"`
	Reason   string `json:"reason" jsonschema:"free-text reason for declining (required)"`
}

// declineOut mirrors runDecline's --json payload keys EXACTLY (see
// internal/cli/decline.go): _type="decline", _version="1",
// "summon-id" (HYPHENATED), by, reason, ts.
type declineOut struct {
	Type     string `json:"_type"`
	Version  string `json:"_version"`
	SummonID string `json:"summon-id"`
	By       string `json:"by"`
	Reason   string `json:"reason"`
	TS       string `json:"ts"`
}

func registerDecline(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "decline",
		Description: "Decline a pending summon addressed to this agent.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in declineIn) (*mcp.CallToolResult, declineOut, error) {
		reasonText := strings.TrimSpace(in.Reason)
		if reasonText == "" {
			return nil, declineOut{}, toolErr(&rufioerr.InvalidContentError{Field: "reason"})
		}
		loaded, err := summon.LoadAnyState(r.Root, in.SummonID)
		if err != nil {
			return nil, declineOut{}, toolErr(err)
		}
		if loaded.State != summon.StatePending {
			return nil, declineOut{}, toolErr(&rufioerr.NoSuchSummonError{ID: in.SummonID})
		}
		if loaded.To != r.Agent {
			return nil, declineOut{}, toolErr(&rufioerr.SummonAuthError{ID: in.SummonID, Target: loaded.To})
		}
		ts := versioning.NowISO()
		if err := summon.MoveToDeclined(r.Root, in.SummonID, r.Agent, reasonText, ts); err != nil {
			return nil, declineOut{}, toolErr(err)
		}
		return nil, declineOut{
			Type: "decline", Version: "1", SummonID: in.SummonID,
			By: r.Agent, Reason: reasonText, TS: ts,
		}, nil
	})
}

// ---- say ----

type sayIn struct {
	Channel string `json:"channel" jsonschema:"channel id (ch-...) to write to (required); must be a current member"`
	Content string `json:"content" jsonschema:"free-text message body (required)"`
}

// sayOut mirrors runSay's --json payload keys EXACTLY (see
// internal/cli/say.go): _type="channel-message", _version="1", id
// (message id), channel, by, content, ts. Issue #107: _type aligns
// with the on-disk record Type and recall.AllTypes — the CLI verb is
// still `say` but the structured output's _type token shifted to
// "channel-message" together with the writer.
type sayOut struct {
	Type    string `json:"_type"`
	Version string `json:"_version"`
	ID      string `json:"id"`
	Channel string `json:"channel"`
	By      string `json:"by"`
	Content string `json:"content"`
	TS      string `json:"ts"`
}

func registerSay(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "say",
		Description: "Write a message to a channel this agent is a current member of.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sayIn) (*mcp.CallToolResult, sayOut, error) {
		chID := strings.TrimSpace(in.Channel)
		if chID == "" {
			return nil, sayOut{}, toolErr(&rufioerr.InvalidContentError{Field: "channel"})
		}
		content := strings.TrimSpace(in.Content)
		if content == "" {
			return nil, sayOut{}, toolErr(&rufioerr.InvalidContentError{Field: "content"})
		}
		meta, err := channels.LoadMeta(r.Root, chID)
		if err != nil {
			return nil, sayOut{}, toolErr(err)
		}
		if meta.Closed {
			return nil, sayOut{}, toolErr(&rufioerr.NoSuchChannelError{ID: chID})
		}
		if !meta.IsCurrentMember(r.Agent) {
			return nil, sayOut{}, toolErr(&rufioerr.NotChannelMemberError{ID: chID, Agent: r.Agent})
		}
		msgID, err := channels.GenerateMessageID()
		if err != nil {
			return nil, sayOut{}, toolErr(err)
		}
		ts := versioning.NowISO()
		record := channels.BuildSayRecord(msgID, chID, r.Agent, content, ts)
		if err := channels.WriteMessage(r.Root, chID, msgID, record); err != nil {
			return nil, sayOut{}, toolErr(err)
		}
		return nil, sayOut{
			Type: "channel-message", Version: "1", ID: msgID, Channel: chID,
			By: r.Agent, Content: content, TS: ts,
		}, nil
	})
}

// ---- leave ----

type leaveIn struct {
	Channel string `json:"channel" jsonschema:"the channel id to leave (both opener and target may leave)"`
}

// leaveOut mirrors runLeave's --json payload keys EXACTLY (see
// internal/cli/leave.go): _type="channel-leave", _version="1", channel,
// by, ts.
type leaveOut struct {
	Type    string `json:"_type"`
	Version string `json:"_version"`
	Channel string `json:"channel"`
	By      string `json:"by"`
	TS      string `json:"ts"`
}

func registerLeave(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "leave",
		Description: "Leave a channel (audit trail preserved; idempotent).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in leaveIn) (*mcp.CallToolResult, leaveOut, error) {
		chID := in.Channel
		meta, err := channels.LoadMeta(r.Root, chID)
		if err != nil {
			return nil, leaveOut{}, toolErr(err)
		}
		if meta.Closed {
			return nil, leaveOut{}, toolErr(&rufioerr.NoSuchChannelError{ID: chID})
		}
		// D16.14: both opener and target may leave; anyone else is a
		// non-member. (Deliberately not IsCurrentMember — a re-leave by an
		// already-left member must flow into the idempotent AppendLeave.)
		if r.Agent != meta.Opener && r.Agent != meta.Target {
			return nil, leaveOut{}, toolErr(&rufioerr.NotChannelMemberError{ID: chID, Agent: r.Agent})
		}
		ts := versioning.NowISO()
		if err := channels.AppendLeave(r.Root, chID, r.Agent, ts); err != nil {
			return nil, leaveOut{}, toolErr(err)
		}
		return nil, leaveOut{
			Type: "channel-leave", Version: "1", Channel: chID,
			By: r.Agent, TS: ts,
		}, nil
	})
}

// ---- close ----

type closeIn struct {
	Channel string `json:"channel" jsonschema:"the channel id to close (opener only; archives to closed/)"`
}

// closeOut mirrors runClose's --json payload keys EXACTLY (see
// internal/cli/close.go): _type="channel-close", _version="1", channel,
// by, ts.
type closeOut struct {
	Type    string `json:"_type"`
	Version string `json:"_version"`
	Channel string `json:"channel"`
	By      string `json:"by"`
	TS      string `json:"ts"`
}

func registerClose(s *mcp.Server, r Resolved) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "close",
		Description: "Close a channel (opener only; appends @channel-close and archives active/→closed/).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in closeIn) (*mcp.CallToolResult, closeOut, error) {
		chID := in.Channel
		meta, err := channels.LoadMeta(r.Root, chID)
		if err != nil {
			return nil, closeOut{}, toolErr(err)
		}
		if meta.Closed {
			return nil, closeOut{}, toolErr(&rufioerr.NoSuchChannelError{ID: chID})
		}
		if r.Agent != meta.Opener {
			return nil, closeOut{}, toolErr(&rufioerr.NotChannelOpenerError{ID: chID, Agent: r.Agent, Opener: meta.Opener})
		}
		ts := versioning.NowISO()
		if err := channels.AppendClose(r.Root, chID, r.Agent, ts); err != nil {
			return nil, closeOut{}, toolErr(err)
		}
		return nil, closeOut{
			Type: "channel-close", Version: "1", Channel: chID,
			By: r.Agent, TS: ts,
		}, nil
	})
}
