// Package cli — `rufio attention <agent-id>`.
//
// Inspector for a single agent's @attention record per D20.2.
// Distinct from `rufio attend` (writer); spec §200-201 keeps the noun
// (`attention`, read-side) and verb (`attend`, write-side) on separate
// command names so tab completion is unambiguous.
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
)

// NewAttentionCmd returns the `rufio attention <agent-id>` Cobra
// command. Reads live/attention/<agent-id>.gdl and pretty-prints the
// parsed @attention record. Missing file → *NoAttentionError (exit 1)
// per D20.5.
func NewAttentionCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "attention <agent-id>",
		Short: "Pretty-print an agent's attention record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runAttention(cwd, args[0], opts)
			}
			if err != nil {
				HandleError("attention", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// runAttention is the pure logic for `rufio attention <agent>`. Loads
// the single record and dispatches to the renderer matching opts.JSON.
func runAttention(cwd, agentID string, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	att, err := attention.LoadOne(root, agentID)
	if err != nil {
		return err
	}
	if opts.JSON {
		return renderAttentionJSON(att, opts)
	}
	renderAttentionBlock(att, opts)
	return nil
}

// renderAttentionBlock prints the labelled multi-line block from D20.2:
//
//	Agent: <agent>
//	Intent: <intent>
//	Entities: <csv or "(none)">
//	Topics: <csv or "(none)">
//	Updated: <ts>
//
// "(none)" surfaces explicitly when entities or topics are absent so
// the consumer can distinguish "we don't know" from "no entries". Each
// line goes through WriteData (not WriteOut) so --quiet doesn't
// suppress what is the user's primary output.
func renderAttentionBlock(a attention.Attention, opts output.RenderOpts) {
	output.WriteData(fmt.Sprintf("Agent: %s", a.Agent), opts)
	output.WriteData(fmt.Sprintf("Intent: %s", a.Intent), opts)
	output.WriteData(fmt.Sprintf("Entities: %s", csvOrNone(a.Entities)), opts)
	output.WriteData(fmt.Sprintf("Topics: %s", csvOrNone(a.Topics)), opts)
	output.WriteData(fmt.Sprintf("Updated: %s", a.TS), opts)
}

// renderAttentionJSON emits a single JSONL object. Fields locked at:
// _type, _version, agent, intent, entities, topics, ts. entities/topics
// always render as arrays (never null) — matches fleet's JSON shape.
func renderAttentionJSON(a attention.Attention, opts output.RenderOpts) error {
	entities := a.Entities
	if entities == nil {
		entities = []string{}
	}
	topics := a.Topics
	if topics == nil {
		topics = []string{}
	}
	payload := map[string]interface{}{
		"_type":    "attention",
		"_version": "1",
		"agent":    a.Agent,
		"intent":   a.Intent,
		"entities": entities,
		"topics":   topics,
		"ts":       a.TS,
	}
	return output.WriteJSONL(payload, opts)
}

// csvOrNone returns the comma-joined slice, or the literal "(none)"
// when the slice is empty/nil. Used only by the columnar renderer
// (the JSON shape returns []string{} for emptiness, never "(none)").
func csvOrNone(xs []string) string {
	if len(xs) == 0 {
		return "(none)"
	}
	return strings.Join(xs, ",")
}
