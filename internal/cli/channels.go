// Cold-late-joiner read API for channels (#142). Adds two new top-level
// verbs:
//
//	rufio channels list [--active|--closed|--member-of=<agent>] [--json]
//	rufio channel show <ch-id> [--since=<duration>] [--json]
//
// Both commands gate on membership: a non-member cannot see other
// agents' channels (privacy floor mirrors the same model that gates
// say/leave). Closed channels are still readable by their past members
// — leave is not a hard delete, the audit trail remains accessible.
package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/recall"
)

// NewChannelsCmd returns the `rufio channels` parent Cobra command.
//
// H3c (#125): bare `rufio channels` aliases to `channels list`. See
// NewThoughtsCmd for the cluster rationale. The `channel show` shape
// (singular, separate parent) is independent of this change.
func NewChannelsCmd() *cobra.Command {
	listCmd := newChannelsListCmd()
	cmd := &cobra.Command{
		Use:   "channels",
		Short: "Inspect channels you are or were a member of",
		RunE:  listCmd.RunE,
	}
	cmd.Flags().AddFlagSet(listCmd.Flags())
	cmd.AddCommand(listCmd)
	return cmd
}

// NewChannelCmd returns the `rufio channel` parent Cobra command. The
// singular form mirrors the read verbs that act on one specific
// channel (today: `show`). Kept distinct from `rufio channels` (plural,
// for enumeration) so the verb shape matches the noun cardinality.
func NewChannelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "channel",
		Short: "Inspect a single channel by id",
	}
	cmd.AddCommand(newChannelShowCmd())
	return cmd
}

// newChannelsListCmd returns `rufio channels list`. Defaults to active
// channels visible to the current identity; --closed flips to closed;
// --member-of filters to channels the named agent is a member of
// (still subject to the caller's own visibility floor).
func newChannelsListCmd() *cobra.Command {
	var activeFlag, closedFlag, jsonFlag, quietFlag, noColorFlag bool
	var memberOfFlag string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List channels (defaults to active channels you are a member of)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runChannelsList(cwd, activeFlag, closedFlag, memberOfFlag, opts)
			}
			if err != nil {
				HandleError("channels", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&activeFlag, "active", false, "list active channels only (default)")
	cmd.Flags().BoolVar(&closedFlag, "closed", false, "list closed channels only")
	cmd.Flags().StringVar(&memberOfFlag, "member-of", "", "filter to channels the named agent is a member of")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// runChannelsList is the pure logic for `rufio channels list`.
//
// Filter pipeline:
//
//  1. Visibility (privacy floor): the caller MUST be opener or target
//     (ever-member) of every channel returned. A non-member querying
//     for someone else's channels does NOT get a back-door enumeration.
//  2. State filter: --closed → only closed; otherwise → only active.
//     The two are mutually exclusive in spec; we let --closed win if
//     both are passed (a closed-only --active makes no sense).
//  3. --member-of: AFTER the visibility floor, restrict to channels
//     where the named agent is also a member. Used to inspect "what
//     channels does X share with me".
func runChannelsList(cwd string, activeFlag, closedFlag bool, memberOfFlag string, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	me, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	all, err := channels.ReadAll(root)
	if err != nil {
		return err
	}

	// State filter: default is active-only. --closed flips it. (--active
	// is the explicit form of the default; we accept it for symmetry.)
	wantClosed := closedFlag

	filtered := make([]channels.Channel, 0, len(all))
	for _, c := range all {
		// Privacy floor: caller must be ever-member.
		if !c.IsEverMember(me) {
			continue
		}
		// State filter.
		if wantClosed && !c.Closed {
			continue
		}
		if !wantClosed && c.Closed {
			continue
		}
		// --member-of filter.
		if memberOfFlag != "" && !c.IsEverMember(memberOfFlag) {
			continue
		}
		filtered = append(filtered, c)
	}

	if opts.JSON {
		return renderChannelsListJSON(filtered, opts)
	}
	renderChannelsListColumnar(filtered, opts)
	return nil
}

// renderChannelsListColumnar prints one tab-separated row per channel.
// Empty input → zero output (no header) so the caller can detect
// "no rows" by exit-0 + empty stdout, matching `summons list`.
//
// Line shape:
//
//	<state>\t<ts>\t<id>\topener:<a>\tmembers:<a,b>\ttopic:"<t>"
func renderChannelsListColumnar(rows []channels.Channel, opts output.RenderOpts) {
	now := time.Now()
	for _, c := range rows {
		state := "active"
		ts := c.CreatedAt
		if c.Closed {
			state = "closed"
			ts = c.ClosedAt
		}
		members := strings.Join(allEverMembers(c), ",")
		// H1a/b: bold state, dim reltime, cyan short-id. Active/closed
		// surfaces via BoldState's word-driven colour map so a fleet
		// listing's status column scans at a glance.
		line := fmt.Sprintf(
			"%s\t%s\t%s\topener:%s\tmembers:%s\ttopic:%s",
			output.BoldState(state, opts),
			output.Dim(output.RenderRelTime(ts, now), opts),
			output.Cyan(output.FormatID(c.ID), opts),
			c.Opener, members, quoteAndTruncate(c.Topic),
		)
		output.WriteData(line, opts)
	}
}

// renderChannelsListJSON emits one JSON object per channel. Fields:
// _type, _version, id, opener, target, topic, state, created_ts,
// closed_ts (null on active), members[].
func renderChannelsListJSON(rows []channels.Channel, opts output.RenderOpts) error {
	for _, c := range rows {
		state := "active"
		if c.Closed {
			state = "closed"
		}
		members := allEverMembers(c)
		mIfaces := make([]interface{}, 0, len(members))
		for _, m := range members {
			mIfaces = append(mIfaces, m)
		}
		var closedTS interface{}
		if c.Closed {
			closedTS = c.ClosedAt
		}
		payload := map[string]interface{}{
			"_type":      "channel",
			"_version":   "1",
			"id":         c.ID,
			"opener":     c.Opener,
			"target":     c.Target,
			"topic":      c.Topic,
			"state":      state,
			"created_ts": c.CreatedAt,
			"closed_ts":  closedTS,
			"members":    mIfaces,
		}
		if err := output.WriteJSONL(payload, opts); err != nil {
			return err
		}
	}
	return nil
}

// allEverMembers returns the canonical {opener, target} pair (in that
// order) for the channel. Used by the list rendering — we surface the
// FULL membership ledger, not just current members, because the read
// view is for after-the-fact reconstruction. CurrentMembers excludes
// Left agents which would silently hide the cold-agent's own row when
// they've left a channel they're trying to enumerate.
func allEverMembers(c channels.Channel) []string {
	if c.Opener == c.Target {
		return []string{c.Opener}
	}
	return []string{c.Opener, c.Target}
}

// newChannelShowCmd returns `rufio channel show <ch-id>`. Authorised
// for ever-members (opener or target, regardless of Left/Closed) per
// #142 — leave doesn't burn the audit trail.
//
// L3 (R26 MED finding): --json defaults to messages-only — every
// emitted JSONL line is a @channel-message record. --with-header opts
// into the legacy header-first shape (one @channel object on line 1,
// messages thereafter). The default change is BACKWARD-INCOMPATIBLE for
// existing consumers that parsed the header; they must add
// --with-header to restore the previous shape.
func newChannelShowCmd() *cobra.Command {
	var sinceFlag string
	var jsonFlag, withHeaderFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "show <ch-id>",
		Short: "Render a channel's metadata and message history",
		Long: `Render a channel's metadata and message history.

--json default (L3): emits ONLY @channel-message records, one per JSONL
line. Cleaner SDK contract — no mixed-type stream split, no need for
` + "`jq 'select(._type == \"channel-message\")'`" + ` on every consumer.

--json --with-header: emits the @channel header object as the FIRST
JSONL line, then one @channel-message per line (the legacy shape
preserved as opt-in).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runChannelShow(cwd, args[0], sinceFlag, withHeaderFlag, opts)
			}
			if err != nil {
				HandleError("channel", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&sinceFlag, "since", "", "include only messages younger than the Go duration (e.g. 10m, 1h)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output (defaults to messages-only; see --with-header)")
	cmd.Flags().BoolVar(&withHeaderFlag, "with-header", false, "include the @channel header object as the first JSONL line (--json only)")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// runChannelShow is the pure logic for `rufio channel show <ch-id>`.
//
// Steps:
//
//  1. Resolve project root + identity.
//  2. Parse --since (Go duration). Empty → no filter.
//  3. Load channel meta. Missing → NoSuchChannelError (exit 1).
//  4. Authorise: caller must be ever-member of the channel. Looser
//     than IsCurrentMember on purpose (#142): leave doesn't revoke
//     read access; the audit trail is still available to past members.
//  5. Read messages, filter by since, sort chronological-ascending
//     (oldest first — humans read top-to-bottom).
//  6. Render header + each message.
func runChannelShow(cwd, chID, sinceRaw string, withHeader bool, opts output.RenderOpts) error {
	chID = strings.TrimSpace(chID)
	if chID == "" {
		return &rufioerr.InvalidContentError{Field: "channel-id"}
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	me, _, err := identity.Resolve(root)
	if err != nil {
		return err
	}

	since, err := recall.ParseSince(sinceRaw)
	if err != nil {
		return err
	}

	meta, err := channels.LoadMeta(root, chID)
	if err != nil {
		return err
	}
	if !meta.IsEverMember(me) {
		return &rufioerr.ChannelShowNotAuthorizedError{ID: chID, Agent: me}
	}

	msgs, err := channels.ReadMessages(root, chID)
	if err != nil {
		return err
	}

	if since > 0 {
		cutoff := time.Now().Add(-since)
		filtered := msgs[:0]
		for _, m := range msgs {
			ts, parseErr := time.Parse(time.RFC3339Nano, m.TS)
			if parseErr != nil {
				// Conservative: include unparseable TS rows so torn-state
				// rows aren't silently elided. The operator deserves to
				// see the corruption.
				filtered = append(filtered, m)
				continue
			}
			if ts.After(cutoff) {
				filtered = append(filtered, m)
			}
		}
		msgs = filtered
	}

	if opts.JSON {
		return renderChannelShowJSON(meta, msgs, withHeader, opts)
	}
	renderChannelShowColumnar(meta, msgs, opts)
	return nil
}

// renderChannelShowColumnar emits a single header line followed by one
// message per line. Header carries id/opener/target/topic/state; each
// message line is `<reltime> <by>: <content>` (free-form reader-friendly).
//
// H1a/b: header state word is bold-coloured (active→green, closed→red);
// message timestamps render as relative-time so the channel transcript
// stays scannable. The channel id field keeps its full form because
// `channel show <id>` is the canonical way to reference one — losing
// the leading bytes would defeat the purpose.
func renderChannelShowColumnar(meta channels.Channel, msgs []channels.Message, opts output.RenderOpts) {
	state := "active"
	if meta.Closed {
		state = "closed"
	}
	header := fmt.Sprintf(
		"channel:%s\topener:%s\ttarget:%s\ttopic:%s\tstate:%s",
		meta.ID, meta.Opener, meta.Target, quoteAndTruncate(meta.Topic),
		output.BoldState(state, opts),
	)
	output.WriteData(header, opts)
	now := time.Now()
	for _, m := range msgs {
		reltime := output.Dim(output.RenderRelTime(m.TS, now), opts)
		output.WriteData(fmt.Sprintf("%s\t%s: %s", reltime, m.By, m.Content), opts)
	}
}

// renderChannelShowJSON emits zero or one header objects on line 1
// followed by one message-record per line. L3 default (withHeader=false):
// messages-only — the header is suppressed so consumers don't need to
// `jq 'select(._type == "channel-message")'` on every read. L3 opt-in
// (withHeader=true): legacy header-first shape, kept available for
// callers that want the metadata inline.
func renderChannelShowJSON(meta channels.Channel, msgs []channels.Message, withHeader bool, opts output.RenderOpts) error {
	if withHeader {
		state := "active"
		if meta.Closed {
			state = "closed"
		}
		members := allEverMembers(meta)
		mIfaces := make([]interface{}, 0, len(members))
		for _, m := range members {
			mIfaces = append(mIfaces, m)
		}
		var closedTS interface{}
		if meta.Closed {
			closedTS = meta.ClosedAt
		}
		header := map[string]interface{}{
			"_type":      "channel",
			"_version":   "1",
			"id":         meta.ID,
			"opener":     meta.Opener,
			"target":     meta.Target,
			"topic":      meta.Topic,
			"intent":     meta.Intent,
			"state":      state,
			"created_ts": meta.CreatedAt,
			"closed_ts":  closedTS,
			"members":    mIfaces,
		}
		if err := output.WriteJSONL(header, opts); err != nil {
			return err
		}
	}
	for _, m := range msgs {
		row := map[string]interface{}{
			"_type":    "channel-message",
			"_version": "1",
			"id":       m.ID,
			"channel":  m.Channel,
			"by":       m.By,
			"content":  m.Content,
			"ts":       m.TS,
		}
		if err := output.WriteJSONL(row, opts); err != nil {
			return err
		}
	}
	return nil
}
