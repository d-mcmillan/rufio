// Package cli — `rufio tui`.
//
// Substrate command per PR #22. Resolves the project root, builds the
// v8 style table, constructs the v8 tui.App, and runs the Bubble Tea
// program. The TUI watches live/attention/ + live/outbox/ and renders
// the v8 fleet view; channels/goals/lineage tabs render placeholder
// content until PR #23.
//
// v8 is the only TUI: `rufio tui` unconditionally launches tui.NewApp
// with the internal/tui/styles subpackage profile applied. There is no
// alternate path or env switch.
//
// Flags:
//   - --quiet / -q: retained no-op (the pre-run startup banner was
//     removed in #136 — it raced Bubble Tea's alt-screen startup;
//     the flag stays for compatibility / future quietable output).
//   - --no-color: force the Ascii termenv profile (no ANSI colour).
//
// No --json flag — a TUI is not a JSONL producer.
package cli

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/tui"
	styles "github.com/d-mcmillan/rufio/internal/tui/styles"
)

// NewTuiCmd returns the `rufio tui` Cobra command. Substrate version
// per PR #22 — fleet tab renders; channels/goals/lineage are
// placeholders.
func NewTuiCmd() *cobra.Command {
	var quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Launch the v8 TUI for inspecting the substrate",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runTui(cwd, opts)
			}
			if err != nil {
				HandleError("tui", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "retained no-op (pre-run startup banner removed, #136)")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// runTui is the pure logic for `rufio tui`. Resolves the project
// root, builds the v8 internal/tui/styles subpackage style table for
// the resolved colour profile (auto-detected unless --no-color),
// constructs the v8 tui.App, and runs the Bubble Tea program with the
// alt-screen + os.Stdin/Stdout wired up.
//
// v8 is the only TUI — there is no alternate path or env switch.
//
// Returns the underlying tea.Program.Run error (typically nil on a
// clean quit). Wrapped errors propagate to HandleError unchanged.
func runTui(cwd string, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}

	// No pre-run banner: a one-line stderr write here raced Bubble
	// Tea's alt-screen startup handshake and, on terminals where
	// alt-screen enter was ineffective, bottom-anchored the whole TUI
	// (#136). The TUI's own header already shows the project/substrate
	// context in-app, and the working case wiped this line instantly
	// anyway — it was only ever seen when broken. --quiet still exists
	// as a flag; the non-quiet path simply now also emits nothing here.

	// Build the v8 styles subpackage for the resolved profile, then
	// construct the v8 App. buildPreviewApp is the headlessly-testable
	// seam: tea.NewProgram / p.Run() stay inline here — they need a
	// real TTY.
	app, err := buildPreviewApp(root, opts)
	if err != nil {
		return err
	}

	p := tea.NewProgram(
		app,
		tea.WithAltScreen(),
		// #137: capture the mouse wheel so trackpad/wheel scroll moves the
		// substrate feed IN-PANE (reusing #134 scrollback) instead of
		// leaking into Terminal.app's native alt-screen scrollback (the
		// "gap on scroll" — shell history + a void above the frame).
		// CellMotion = button+wheel+drag; NOT AllMotion (that spams a
		// MouseMsg on every cursor move). Accepted tradeoff: native
		// text-selection now needs Option/Shift held (standard for TUIs).
		tea.WithMouseCellMotion(),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
	)
	_, err = p.Run()
	return err
}

// buildPreviewApp is the headlessly-testable seam between runTui and
// the v8 App: it builds the v8 styles subpackage for the resolved
// profile, then constructs the App. runTui calls this on the single
// (unconditional) v8 path; tea.NewProgram/p.Run() stay inline in runTui
// (they need a TTY).
//
// (The "Preview" in the name is historical — v8 is now the only TUI;
// the function name is retained for the regression guard that drives it
// and will be renamed in a later step.)
//
// The applyV8Profile call here is load-bearing: the internal/tui/styles
// subpackage has no init() by design, so without it the live binary
// renders unstyled (no border/bg/padding). A regression test drives
// THIS function (not applyV8Profile directly) so removing/moving that
// call out of the seam is caught.
func buildPreviewApp(root string, opts output.RenderOpts) (tui.App, error) {
	applyV8Profile(opts)
	return tui.NewApp(root)
}

// applyV8Profile builds the internal/tui/styles SUBPACKAGE style table
// (Panel border+bg, Tab/Footer/Hairline) using the resolved profile
// (--no-color → Ascii, else auto-detect honouring NO_COLOR). Called
// from buildPreviewApp.
//
// This exists because the subpackage has no init() by design, so the
// live binary would never build these styles and would render
// borderless/unstyled while every golden test passed (the goldens
// pre-build the subpackage). Idempotent: subpackage SetProfile also
// re-applies the global lipgloss profile.
func applyV8Profile(opts output.RenderOpts) {
	if opts.NoColor {
		styles.SetProfile(termenv.Ascii)
		return
	}
	styles.DetectAndApplyProfile()
}
