package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
)

// NewWhoamiCmd returns the `rufio whoami` Cobra command. Resolves the
// current identity and prints it; --json includes the source ("env" or
// "file"). Identity output IS data, so --quiet does not suppress it.
func NewWhoamiCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Print the current agent identity",
		Long:  withIdentityEnvHelp("Print the current agent identity."),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runWhoami(cwd, opts)
			}
			if err != nil {
				HandleError("whoami", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter (no effect on identity line — that's data)")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runWhoami(cwd string, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	id, source, err := identity.Resolve(root)
	if err != nil {
		return err
	}
	if opts.JSON {
		return output.WriteJSONL(map[string]string{
			"_type":    "whoami",
			"_version": "1",
			"agent":    id,
			"source":   source,
		}, opts)
	}
	output.WriteData(id, opts)
	return nil
}
