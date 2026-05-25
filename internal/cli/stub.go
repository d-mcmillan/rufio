package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// stubSpec maps an unimplemented command name to the milestone it is
// planned for. The tripwire test in
// test/integration/no_silent_stubs_test.go enforces that every command
// listed here actually exits 2 with the canonical
// "not implemented yet — planned for <target>" message.
//
// As each milestone ships, command entries move from this map to the
// implementedCommands list (real Cobra commands wired by NewRootCmd).
type stubSpec struct{ Name, Target string }

func allStubs() []stubSpec {
	// Empty as of v1.1: the mcp adapter shipped (it is a real MCP stdio
	// server now, see internal/mcp + internal/cli/mcp.go). stubSpec /
	// newStubCmd are kept intact for any future pre-implementation command.
	return []stubSpec{}
}

// newStubCmd returns a Cobra command for an unimplemented subcommand.
// Exits 2 with the canonical envelope so the tripwire test's regex
// (looking for "not implemented yet" + the planned target) passes.
func newStubCmd(name, target string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: "not implemented yet — planned for " + target,
		// FParseErrWhitelist allows unknown flags through to RunE — we want
		// the "not implemented" message to appear regardless of flags.
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(os.Stderr, "rufio %s — not implemented yet — planned for %s\n", name, target)
			fmt.Fprintln(os.Stderr, "See docs/v1-spec.md for the full specification.")
			os.Exit(2)
			return nil
		},
	}
}
