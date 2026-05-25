package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
)

// NewIdentityCmd returns the `rufio identity` Cobra command.
//
// Three modes (in order of preference):
//
//   - `rufio identity set <id>` (P1/R31): positional subcommand —
//     persists <id> to .rufio/identity.local.gdl. The CANONICAL shape per
//     the rest of the verb-pattern landscape (goal complete <id>,
//     summon <agent>, etc.). Cold agents reach for this shape first.
//
//   - `rufio identity --as=<id>`: legacy flag-form, kept for backward
//     compat. Identical effect to `identity set <id>`.
//
//   - `rufio identity` (no args / no subcommand) (#112): falls back to
//     whoami's behaviour — resolve env > .rufio/identity.local.gdl,
//     print the id (or surface NoIdentityError with the helpful
//     "run `rufio identity set <id>`" hint when neither is set).
//     Before this PR the no-args path errored `missing required flag
//     --as=<agent-id>` (exit 2), which was the #112 papercut.
//
// Inline RUFIO_AGENT_ID env-var doc is in the Long help so an agent
// reading `rufio identity --help` learns about the override without
// hitting a foot-gun (#112 fix).
func NewIdentityCmd() *cobra.Command {
	var asFlag string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Print or set the persisted agent identity for this project",
		Long: "Print or set the persisted agent identity for this project.\n\n" +
			"With no flags, prints the resolved identity (same as " +
			"`rufio whoami`).\nWith `set <id>`, persists the id to " +
			".rufio/identity.local.gdl.\nWith --as=<id> (legacy), same " +
			"effect as `set <id>`.\n\n" +
			"Examples:\n" +
			"  rufio identity              # print current identity\n" +
			"  rufio identity set bob      # persist agent:bob\n" +
			"  rufio identity --as=bob     # same effect (legacy form)\n\n" +
			"Environment variables:\n" +
			"  RUFIO_AGENT_ID   override the persisted agent identity for " +
			"this invocation\n" +
			"                   (wins over .rufio/identity.local.gdl)",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err != nil {
				HandleError("identity", err)
				return nil
			}
			// No --as → whoami-style read path (#112). The two flows
			// share zero behavioural overlap (one reads, one writes), so
			// we dispatch on flag presence at the top.
			if asFlag == "" {
				if err := runWhoami(cwd, opts); err != nil {
					HandleError("identity", err)
				}
				return nil
			}
			if err := runIdentity(cwd, asFlag, opts); err != nil {
				HandleError("identity", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&asFlag, "as", "", "agent id to persist (legacy; prefer `rufio identity set <id>`)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")

	// P1/R31: `identity set <id>` positional subcommand. Identical effect
	// to `identity --as=<id>`. The shared output flags are re-declared on
	// the subcommand (Cobra doesn't auto-inherit parent flags onto
	// children — and we want `identity set <id> --json` to work for
	// scripting parity with the rest of the write surface).
	setCmd := &cobra.Command{
		Use:   "set <id>",
		Short: "Persist <id> as the project agent identity",
		Long: "Persist <id> to .rufio/identity.local.gdl. Identical effect to " +
			"`rufio identity --as=<id>` (the legacy flag-form).\n\n" +
			"Examples:\n" +
			"  rufio identity set bob\n" +
			"  rufio identity set agent-7-claude",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err != nil {
				HandleError("identity", err)
				return nil
			}
			if err := runIdentity(cwd, args[0], opts); err != nil {
				HandleError("identity", err)
			}
			return nil
		},
	}
	cmd.AddCommand(setCmd)
	return cmd
}

func runIdentity(cwd, id string, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	if err := identity.WriteLocalFile(root, id); err != nil {
		return err
	}
	// Warn (stderr) if env will override the file we just wrote.
	if env := identity.EnvOverride(); env != "" && env != id {
		fmt.Fprintln(os.Stderr,
			"warning: RUFIO_AGENT_ID is set in env (resolves to "+env+
				"); this overrides .rufio/identity.local.gdl. unset to use file identity.")
	}
	if opts.JSON {
		return output.WriteJSONL(map[string]string{
			"_type":    "identity-set",
			"_version": "1",
			"agent":    id,
		}, opts)
	}
	output.WriteOut("identity set: "+id, opts)
	return nil
}
