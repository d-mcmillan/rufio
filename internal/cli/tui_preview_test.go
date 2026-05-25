// tui_preview_test.go — post-cutover regression guard for the
// live ≠ golden v8 styling bug on the DEFAULT `rufio tui` path.
//
// THE BUG (the PR-D1 class, caught at the manual eyeball gate via
// screenshots): the v8 App renders with the internal/tui/styles
// SUBPACKAGE, which by design has no init() (no-side-effecting-init
// rule). Its Panel/Tab/Footer/Hairline style vars stay zero-value
// until SetProfile/DetectAndApplyProfile is called. The TUI
// construction path constructed tui.NewApp and ran it WITHOUT ever
// building the subpackage styles, so the live binary rendered with NO
// rounded border / bg / padding / footer styling — while every v8
// golden test passed because each golden test manually calls
// styles.SetProfile(termenv.Ascii) first. Live ≠ golden.
//
// Post-cutover state: v8 is the ONLY TUI. There is no preview path
// and no env switch — `rufio tui` (runTui) unconditionally constructs
// the v8 App through the single profile-init seam. (That seam helper
// is still historically named buildPreviewApp/applyV8Profile pending
// a separate deferred rename; the name is stale but the path is the
// real default production path.)
//
// WHAT IS (and isn't) GUARDED HERE:
//
//   - TestDefaultTuiAppViewHasRoundedBorder drives the SAME App
//     construction seam runTui uses for the default `rufio tui` and
//     never calls applyV8Profile or styles.SetProfile itself. So it
//     catches the REAL regression: the styles-init call being
//     removed/moved out of the seam (or runTui ceasing to route
//     through it). That is the missing-call-site class the original
//     PR-D1 bug belonged to.
//   - TestApplyV8ProfileBuildsSubpackageStyles is a narrow unit test
//     on the helper body only. It calls applyV8Profile directly, so
//     it does NOT see a removed call site — it only catches the
//     helper's internals being broken. Kept as a focused unit, not
//     the guard.
//
// Neither test manually pre-builds the subpackage (no styles.SetProfile
// in the test bodies), so the seam test reflects the live binary's true
// uninitialised starting state.
package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/tui"
	styles "github.com/d-mcmillan/rufio/internal/tui/styles"
)

// roundedTL / roundedBL are the rounded-border corner runes lipgloss
// emits for lipgloss.RoundedBorder() — the styles.Panel definition.
// They render even under the Ascii profile (the border glyphs are
// structural, not ANSI-colour), so a NoColor=true probe is
// CI-deterministic. A zero-value (unbuilt) Panel has no border, so its
// output cannot contain these; their presence is the precise signal
// "the v8 subpackage styles were built on this path".
const (
	roundedTL = "╭"
	roundedBL = "╰"
)

// TestDefaultTuiAppViewHasRoundedBorder is THE guard for the default
// `rufio tui` path post-cutover. It drives the exact App-construction
// seam runTui uses unconditionally (the profile-init seam still
// historically named buildPreviewApp pending a separate deferred
// rename), then sizes the App and asserts View() carries the rounded
// panel border. It deliberately does NOT call applyV8Profile or
// styles.SetProfile directly: the only thing that may build the
// subpackage styles here is the seam's own wiring. Therefore if the
// styles-init call is removed/moved out of the seam, or runTui stops
// routing through it, this test goes RED — the PR-D1 missing-call-site
// regression class. NoColor=true keeps it Ascii-deterministic / CI-
// safe.
func TestDefaultTuiAppViewHasRoundedBorder(t *testing.T) {
	app, err := buildPreviewApp(t.TempDir(), output.RenderOpts{NoColor: true})
	if err != nil {
		t.Fatalf("buildPreviewApp: %v", err)
	}
	m, _ := app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	view := m.(tui.App).View()

	if !strings.Contains(view, roundedTL) || !strings.Contains(view, roundedBL) {
		t.Fatalf("default v8 tui App.View() has no rounded panel border — "+
			"the construction seam did not build the styles subpackage "+
			"(live ≠ golden; caught at the manual eyeball gate). The "+
			"styles-init call was likely removed from the seam / "+
			"runTui. View:\n%s", view)
	}
}

// TestApplyV8ProfileBuildsSubpackageStyles is a narrow unit test on the
// helper body only — it asserts applyV8Profile, when called, builds the
// subpackage Panel. It does NOT guard the call site (it invokes the
// helper directly), so it stays green even if the seam/runTui stop
// calling it; TestDefaultTuiAppViewHasRoundedBorder is the test that
// catches that. Kept as a focused unit so a body break is pinpointed.
func TestApplyV8ProfileBuildsSubpackageStyles(t *testing.T) {
	applyV8Profile(output.RenderOpts{NoColor: true})

	probe := styles.Panel.Render("x")
	if !strings.Contains(probe, roundedTL) || !strings.Contains(probe, roundedBL) {
		t.Fatalf("applyV8Profile did not build a bordered v8 Panel — "+
			"helper body broken. styles.Panel.Render(\"x\") = %q", probe)
	}
}
