// Package cli — `rufio quickstart`.
//
// The cold-start onboarding verb (v1.0.3). Prints the locked sub-200-
// token-ish card from internal/lib/quickstart to stdout, so a cold
// agent can run a single command before they know what else exists
// and learn the seven first-contact verbs + the quorum math + the
// subject-vs-topics distinction. Pure print; no project root required.
//
// MCP tool #21 (`quickstart`) ships the same JSON shape; symmetry
// contract — every CLI verb has a matching MCP tool.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/quickstart"
)

// NewQuickstartCmd returns the `rufio quickstart` Cobra command.
//
//	rufio quickstart            # print card text
//	rufio quickstart --json     # emit {"_type":"quickstart","_version":1,...}
//
// Deliberately accepts no arguments and requires no project root —
// the verb's job is to bootstrap an agent BEFORE they have one.
func NewQuickstartCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "quickstart",
		Short: "Print the cold-start card (read once, then participate)",
		Long: "Print the locked cold-start card. Teaches the seven first-contact " +
			"verbs, the quorum math (≥3 confirmers at ≥0.85 → auto-promote), " +
			"and the load-bearing subject-vs-topics distinction.\n\n" +
			"Designed for cold agents to run BEFORE they know what else " +
			"exists. No project root required. Same content is folded into " +
			"CLAUDE.md / .cursorrules / AGENTS.md by `rufio init`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			if err := runQuickstart(opts); err != nil {
				HandleError("quickstart", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit a single JSON object instead of the card text")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter (no effect on the card — that's data)")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// runQuickstart is the pure logic. JSON shape is locked at
// {_type:"quickstart",_version:1,content:<card>,card_version:1}. The MCP
// tool ships the byte-identical shape — the fidelity contract is enforced
// by sharing quickstart.CardV1 + CardVersion, not by duplicating prose.
func runQuickstart(opts output.RenderOpts) error {
	if opts.JSON {
		payload := map[string]interface{}{
			"_type":        "quickstart",
			"_version":     1,
			"content":      quickstart.CardV1,
			"card_version": quickstart.CardVersion,
		}
		return output.WriteJSONL(payload, opts)
	}
	output.WriteData(quickstart.CardV1, opts)
	return nil
}
