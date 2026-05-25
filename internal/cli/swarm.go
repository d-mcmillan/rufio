// Package cli — `rufio swarm` parent + `swarm spawn` subcommand.
//
// `swarm spawn --persona=<text> --count=<n>` is the demo-helper command
// that scaffolds N agent identities under a shared persona tag. It
// writes one @spawned record per agent to .rufio/swarm.local.gdl
// (gitignored under the .rufio/ umbrella) and emits the generated
// agent-ids — one per line in default mode, one JSONL object per line
// under --json. Subsequent invocations APPEND to the same file; new
// sequence numbers are picked off max(existing seq for persona)+1.
//
// The command does NOT execute as the spawned agents. PR #24's
// `rufio demo` orchestrator reads .rufio/swarm.local.gdl back to know
// which agent-ids to set in subprocess RUFIO_AGENT_ID env (D21.11).
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/swarm"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// NewSwarmCmd returns the `rufio swarm` parent Cobra command. It has
// no RunE — Cobra prints help when invoked without a subcommand.
// Subcommands hang off of it (today: `spawn`). Mirrors NewSummonsCmd's
// parent+subcommand layout (D21.1).
func NewSwarmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "swarm",
		Short: "Demo helpers for multi-agent scenarios",
	}
	cmd.AddCommand(newSwarmSpawnCmd())
	return cmd
}

// newSwarmSpawnCmd returns the `rufio swarm spawn` Cobra subcommand.
//
//	rufio swarm spawn --persona=<text> --count=<n> [--json] [--quiet] [--no-color]
//
// --persona and --count are required; both validate via the swarm
// package (InvalidPersonaError / InvalidCountError, both exit 2).
func newSwarmSpawnCmd() *cobra.Command {
	var personaFlag string
	var countFlag int
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "spawn",
		Short: "Scaffold N agent identities with a given persona",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runSwarmSpawn(cwd, personaFlag, countFlag, opts)
			}
			if err != nil {
				HandleError("swarm spawn", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&personaFlag, "persona", "", "persona tag for spawned agents (required, [a-z][a-z0-9-]*)")
	cmd.Flags().IntVar(&countFlag, "count", 0, "number of agents to spawn (required, 1..50)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// runSwarmSpawn is the pure logic for `rufio swarm spawn`. Validates
// the flags first (so usage errors surface before any filesystem work),
// resolves the project root, computes the next sequence number for the
// persona, generates the batch, and appends.
//
// Output:
//   - Default: one line per added agent-id (WriteData; --quiet ignored).
//   - --json:  one JSONL `_type=spawned-agent` object per added agent.
//   - Skipped ids (defensive — see swarm.Append docs): rendered to
//     stderr as a single `(skipped: <csv>)` line.
func runSwarmSpawn(cwd, persona string, count int, opts output.RenderOpts) error {
	if err := swarm.ValidatePersona(persona); err != nil {
		return err
	}
	if err := swarm.ValidateCount(count); err != nil {
		return err
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}

	existing, err := swarm.ReadAll(root)
	if err != nil {
		return err
	}
	nextSeq := swarm.NextSeq(existing, persona)
	batch := swarm.GenerateBatch(persona, count, nextSeq)

	ts := versioning.NowISO()
	added, skipped, err := swarm.Append(root, persona, batch, ts)
	if err != nil {
		return err
	}

	if opts.JSON {
		if err := renderSwarmSpawnJSON(added, persona, ts, opts); err != nil {
			return err
		}
	} else {
		renderSwarmSpawnColumnar(added, opts)
	}

	// Skipped ids surface on stderr regardless of format. Defensive
	// path (D21.7 + swarm.Append docs) — in normal flow NextSeq picks
	// fresh ids so this is empty. We still want operators to see it
	// when it fires because it signals hand-edits or a bypass caller.
	if len(skipped) > 0 {
		output.WriteErr(fmt.Sprintf("(skipped: %s)", strings.Join(skipped, ",")))
	}
	return nil
}

// renderSwarmSpawnColumnar prints one agent-id per line via WriteData
// (so --quiet does NOT suppress the rows — same convention as
// summons/fleet/recall).
func renderSwarmSpawnColumnar(added []string, opts output.RenderOpts) {
	for _, id := range added {
		output.WriteData(id, opts)
	}
}

// renderSwarmSpawnJSON emits one JSONL `spawned-agent` object per added
// id. Fields locked at: _type, _version, persona, agent, ts. Matches
// the on-disk record's field set so a downstream consumer can ingest
// either source.
func renderSwarmSpawnJSON(added []string, persona, ts string, opts output.RenderOpts) error {
	for _, id := range added {
		payload := map[string]interface{}{
			"_type":    "spawned-agent",
			"_version": "1",
			"persona":  persona,
			"agent":    id,
			"ts":       ts,
		}
		if err := output.WriteJSONL(payload, opts); err != nil {
			return err
		}
	}
	return nil
}
