package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewVersionCmd returns the `rufio version` Cobra subcommand. Prints the
// exact same string Cobra's built-in `--version` flag emits — the
// canonical form is `rufio version <version>` (e.g. "rufio version dev"
// for a dev build, "rufio version v1.0.6.2" for a release tag).
//
// Why a subcommand at all when --version already exists: field feedback
// (Joey #213) flagged that cold agents type `rufio version` by reflex
// (matching git, kubectl, helm, etc.) and got an "Unknown command" error
// where every comparable CLI just prints its version. Adding the
// subcommand is a conventional surface, not a spec change — it's an
// alias on top of the existing --version flag.
//
// Implementation: the subcommand reaches up to its parent (the root cmd,
// where Version is set in NewRootCmd) and inlines the format string
// "{name} version {version}\n" to match the byte-exact form Cobra's
// default --version emits by convention. Byte-equality with the --version
// flag is enforced by a parity test (TestVersionSubcommand_MatchesVersionFlag),
// so a future Cobra template change would surface there rather than silently
// drift.
func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the rufio version (alias for --version)",
		Long: "Print the rufio version. Output is identical to " +
			"`rufio --version`. Useful for environments / habits where " +
			"`rufio version` is the reflex (git, kubectl, helm, etc.).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Walk up to the root so we read the canonical Version
			// field set in NewRootCmd. Defensive nil-checks let us
			// degrade gracefully if a test composes us under a non-root
			// parent (we'd just print "dev" instead of crashing).
			root := cmd.Root()
			version := "dev"
			if root != nil && root.Version != "" {
				version = root.Version
			}
			// Match Cobra's default VersionTemplate exactly so the two
			// surfaces stay byte-identical: "<root.Name()> version <version>\n".
			name := "rufio"
			if root != nil && root.Name() != "" {
				name = root.Name()
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s version %s\n", name, version)
			return nil
		},
	}
}
