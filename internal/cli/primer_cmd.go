package cli

import (
	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// NewPrimerCmd returns the `rufio primer` Cobra command (#114).
//
// `rufio primer` prints the same primer that `rufio init` writes to
// RUFIO.md — to stdout, on demand, from ANYWHERE. No `rufio init`
// required, no substrate required, no identity required. It is the
// cold-start anchor: a fresh shell with the binary alone can pipe this
// into an agent context and the agent learns the substrate.
//
// Single source of truth: the primer body comes from buildPrimer() in
// primer.go — the same function init calls. If the primer regresses,
// `rufio init` and `rufio primer` regress together (that's the point of
// having one function feed both).
//
// Flags: deliberately minimal. --no-color is kept for consistency with
// every other verb (the primer is plain markdown so it ignores colour,
// but the flag is harmless and lets agents script verb invocations
// uniformly). --json / --quiet are NOT exposed: the primer's output IS
// markdown text, so wrapping it in JSON would just add an envelope; and
// --quiet would defeat the verb's only purpose. Structured per-verb
// docs are a separate proposal (`rufio explain`, #114 item 3).
func NewPrimerCmd() *cobra.Command {
	var noColorFlag bool
	cmd := &cobra.Command{
		Use:   "primer",
		Short: "Print the agent-onboarding substrate primer (no init required)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{NoColor: noColorFlag}
			runPrimer(opts)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output (the primer is plain markdown; flag kept for consistency)")
	return cmd
}

// runPrimer prints the primer body to stdout. It is byte-identical to
// the content `rufio init` writes to RUFIO.md (the test suite pins this
// equivalence; the only difference is the destination — file vs stdout).
//
// We deliberately use WriteData (NOT WriteOut) so the primer is treated
// as DATA, not chatter: --quiet (were we to add it) must not suppress
// it, because the entire reason to run the verb is to get this text.
func runPrimer(opts output.RenderOpts) {
	output.WriteData(buildPrimer(), opts)
}
