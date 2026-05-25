package styles

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Composed v8 styles. These are the building blocks PR-B/C/D consume;
// PR-A does not render anything with them.
//
// The table is populated by buildStyles and re-built on every SetProfile
// call so a profile change (NO_COLOR / explicit override) takes effect
// immediately even if a caller already cached a copy of a style. Styles
// are NEVER built in init() — explicit SetProfile / DetectAndApplyProfile
// only, mirroring the no-side-effecting-init discipline of the old
// internal/tui/styles.go (PR #22) and the CLAUDE.md stack rule.
var (
	// Panel wraps the chat / mesh panels: a rounded border in the Ring
	// structural tone (handoff §6 "Border radius"). PR-E.1: the forced
	// Panel background (#1a1726) is REMOVED — the v8 prototype paints
	// `background: p.panel` but Rufio deliberately uses the terminal's
	// native background for theme portability (the forced two-tone bg
	// renders patchy in real terminals and fights non-dark themes;
	// matches the old/default `rufio tui` treatment). With no bg the
	// faint Line border was invisible, so the border now carries the
	// structure in the visible-but-quiet Ring tone (#4a4470).
	Panel lipgloss.Style
	// RoleTag is the UPPERCASE role tag. Bold only — the caller sets
	// .Foreground(AgentColor(id)) so the tag carries the agent's color.
	RoleTag lipgloss.Style
	// AgentName is the muted mixed-case agent name (handoff §7.2).
	AgentName lipgloss.Style
	// BodyPlan is full-intensity body text for orchestrator plan rows.
	BodyPlan lipgloss.Style
	// BodyReply is muted body text for reply rows (handoff §7.2).
	BodyReply lipgloss.Style
	// TabActive is the selected top tab: Accent, bold (handoff §7.1).
	TabActive lipgloss.Style
	// TabInactive is a non-selected top tab: Dim grey.
	TabInactive lipgloss.Style
	// TabPlaceholder is the not-yet-implemented `rules` tab: VDim.
	TabPlaceholder lipgloss.Style
	// Footer is the bottom keybind / attribution row: Dim.
	Footer lipgloss.Style
	// Hairline is a 1px Line separator — the quiet inline ` · ` dot
	// separators (tab strip, footer, chrome). Line #2d2742 is
	// deliberately faint; it stays subtle on the native terminal bg.
	Hairline lipgloss.Style
	// SectionRule is the FULL-WIDTH interior section divider (chrome
	// bottom-rule, composer top-rule, mesh body→ROUTING rule). PR-E.1:
	// these structural rules use the visible-but-quiet Ring tone
	// (#4a4470) so they read on the native terminal bg — without also
	// brightening the quiet inline Hairline ` · ` dot separators.
	SectionRule lipgloss.Style
	// GovLabel is the `GOV` strip label: Label color, bold (handoff §7.7).
	GovLabel lipgloss.Style
)

// detectProfile resolves the active termenv profile from the process
// environment AND honours NO_COLOR (matching the old internal/tui
// styles.go and the standard NO_COLOR spec at https://no-color.org).
//
// termenv's own Profile() honours NO_COLOR=1 but treats an empty
// NO_COLOR as a non-trigger; we tighten that to "any non-empty NO_COLOR
// forces Ascii".
func detectProfile() termenv.Profile {
	if v, ok := os.LookupEnv("NO_COLOR"); ok && v != "" {
		return termenv.Ascii
	}
	return termenv.NewOutput(os.Stdout).Profile
}

// SetProfile applies p to the lipgloss default renderer and rebuilds the
// composed style table so a profile change actually takes effect. Call
// this BEFORE rendering anything. Safe to call repeatedly; each call
// overwrites the package-level style values.
func SetProfile(p termenv.Profile) {
	lipgloss.SetColorProfile(p)
	buildStyles()
}

// buildStyles initialises every exported lipgloss.Style from the v8
// Palette. Split out from SetProfile so the rebuild is explicit and
// testable. NOT called from init() — only from SetProfile.
func buildStyles() {
	// PR-E.1: no forced Panel bg (native terminal bg, theme-portable);
	// the border carries the structure in the visible Ring tone.
	Panel = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Palette.Ring)

	RoleTag = lipgloss.NewStyle().Bold(true)

	AgentName = lipgloss.NewStyle().Foreground(Palette.FgMute)

	BodyPlan = lipgloss.NewStyle().Foreground(Palette.Fg)
	BodyReply = lipgloss.NewStyle().Foreground(Palette.FgMute)

	TabActive = lipgloss.NewStyle().Foreground(Palette.Accent).Bold(true)
	TabInactive = lipgloss.NewStyle().Foreground(Palette.Dim)
	TabPlaceholder = lipgloss.NewStyle().Foreground(Palette.VDim)

	Footer = lipgloss.NewStyle().Foreground(Palette.Dim)
	Hairline = lipgloss.NewStyle().Foreground(Palette.Line)
	SectionRule = lipgloss.NewStyle().Foreground(Palette.Ring)

	GovLabel = lipgloss.NewStyle().Foreground(Palette.Label).Bold(true)
}

// DetectAndApplyProfile resolves the terminal profile via the process
// environment (honouring NO_COLOR), applies it to the lipgloss renderer,
// rebuilds the style table, and returns the resolved profile so a CLI
// wrapper can log or override it. The package does NOT use an init() so
// callers retain explicit control (CLAUDE.md no-side-effecting-init).
func DetectAndApplyProfile() termenv.Profile {
	p := detectProfile()
	SetProfile(p)
	return p
}
