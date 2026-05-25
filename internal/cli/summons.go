package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/summon"
)

// intentTruncateLen caps the intent column in columnar output. Longer
// intents render as `"<first N chars>..."` so a single summon line stays
// readable in a 120-col terminal. Quoted form follows recall.go's
// column convention.
const intentTruncateLen = 60

// NewSummonsCmd returns the `rufio summons` parent Cobra command.
//
// H3c (#125): bare `rufio summons` aliases to `summons list`. See
// NewThoughtsCmd for the cluster rationale.
func NewSummonsCmd() *cobra.Command {
	listCmd := newSummonsListCmd()
	cmd := &cobra.Command{
		Use:   "summons",
		Short: "Inspect summons sent or received by an agent",
		RunE:  listCmd.RunE,
	}
	cmd.Flags().AddFlagSet(listCmd.Flags())
	cmd.AddCommand(listCmd)
	return cmd
}

// newSummonsListCmd returns the `rufio summons list` Cobra subcommand.
// Reads live/summons/{pending,accepted,declined,expired}/, filters by
// the resolved agent and state, and prints columnar or JSONL output.
//
//	rufio summons list [--as=<id>] [--pending|--all] [--json]
func newSummonsListCmd() *cobra.Command {
	var asFlag string
	var pendingFlag, allFlag, jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List summons sent or received by an agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runSummonsList(cwd, asFlag, allFlag, opts)
			}
			if err != nil {
				HandleError("summons", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&asFlag, "as", "", "filter to summons involving this agent (default: current identity)")
	cmd.Flags().BoolVar(&pendingFlag, "pending", false, "show only pending summons (default)")
	cmd.Flags().BoolVar(&allFlag, "all", false, "show all states (overrides --pending)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// runSummonsList is the pure logic for `rufio summons list`. It resolves
// the viewing identity (D15.18: --as wins, else current identity), reads
// every summon from disk via summon.ReadAll, filters down to records
// where the viewer is from-or-to, and finally collapses to pending-only
// unless --all is set.
func runSummonsList(cwd, asFlag string, allFlag bool, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}

	// D15.18: --as overrides identity. With no --as, fall back to the
	// resolved current identity; surface NoIdentityError verbatim so the
	// caller sees the standard envelope.
	viewer := asFlag
	if viewer == "" {
		viewer, _, err = identity.Resolve(root)
		if err != nil {
			return err
		}
	}

	all, err := summon.ReadAll(root)
	if err != nil {
		return err
	}

	// Filter to summons involving viewer, then by state. The state
	// filter is the second pass so --all preserves the
	// state-precedence ordering ReadAll gives us (pending → accepted
	// → declined → expired).
	matched := make([]summon.Summon, 0, len(all))
	for _, s := range all {
		if s.From != viewer && s.To != viewer {
			continue
		}
		if !allFlag && s.State != summon.StatePending {
			continue
		}
		matched = append(matched, s)
	}

	if opts.JSON {
		return renderSummonsJSON(matched, opts)
	}
	renderSummonsColumnar(matched, opts)
	return nil
}

// renderSummonsColumnar prints one tab-separated line per summon.
// Empty input produces zero output (no header) per the spec — the
// caller can still detect "no rows" by exit-0 + empty stdout.
//
// Line shape:
//
//	<state>\t<ts>\t<id>\tfrom:<from>\tto:<to>\ttopic:<topic>\tintent:"<intent>"[\tchannel:<ch>|\treason:"<r>"]
//
// Per #140: accepted rows append `channel:<ch-id>` so a cold agent who
// lost scrollback can recover the channel without grepping live/.
// Declined rows append `reason:"<r>"` for the same audit-join reason.
func renderSummonsColumnar(rows []summon.Summon, opts output.RenderOpts) {
	now := time.Now()
	for _, s := range rows {
		// H1a/b: bold state word, dim reltime, cyan short-id. The
		// channel id (when present) is also shortened for symmetry with
		// the leading id column.
		state := output.BoldState(string(s.State), opts)
		reltime := output.Dim(output.RenderRelTime(s.TS, now), opts)
		id := output.Cyan(output.FormatID(s.ID), opts)
		line := fmt.Sprintf(
			"%s\t%s\t%s\tfrom:%s\tto:%s\ttopic:%s\tintent:%s",
			state, reltime, id, s.From, s.To, s.Topic, quoteAndTruncate(s.Intent),
		)
		switch s.State {
		case summon.StateAccepted:
			if s.Channel != "" {
				line += "\tchannel:" + output.FormatID(s.Channel)
			}
		case summon.StateDeclined:
			if s.DeclineReason != "" {
				line += "\treason:" + quoteAndTruncate(s.DeclineReason)
			}
		}
		// WriteData (not WriteOut) so --quiet does NOT suppress rows.
		// Rows are the user's primary output; chatter is the only
		// thing --quiet kills.
		output.WriteData(line, opts)
	}
}

// quoteAndTruncate wraps the intent in double quotes and truncates the
// inner content (not the quotes) to intentTruncateLen. Longer values
// surface as `"<first N chars>..."`.
func quoteAndTruncate(s string) string {
	if len(s) <= intentTruncateLen {
		return `"` + s + `"`
	}
	return `"` + s[:intentTruncateLen] + `..."`
}

// renderSummonsJSON emits one JSONL object per matched summon. Fields
// locked at: _type, _version, id, from, to, topic, intent (full —
// JSON consumers don't need the columnar truncation), ts, ttl, state.
//
// Per #140: `channel` and `decline_reason` are ALWAYS present (null
// when absent) for shape stability across states. A consumer parsing
// the JSON can rely on the key being present and only branch on null
// vs string. The values are projected from the @accept/@decline audit
// records joined onto the row at summon.ReadAll time.
func renderSummonsJSON(rows []summon.Summon, opts output.RenderOpts) error {
	for _, s := range rows {
		payload := map[string]interface{}{
			"_type":          "summon",
			"_version":       "1",
			"id":             s.ID,
			"from":           s.From,
			"to":             s.To,
			"topic":          s.Topic,
			"intent":         s.Intent,
			"ts":             s.TS,
			"ttl":            s.TTL,
			"state":          string(s.State),
			"channel":        nil,
			"decline_reason": nil,
		}
		if s.Channel != "" {
			payload["channel"] = s.Channel
		}
		if s.DeclineReason != "" {
			payload["decline_reason"] = s.DeclineReason
		}
		if err := output.WriteJSONL(payload, opts); err != nil {
			return err
		}
	}
	return nil
}
