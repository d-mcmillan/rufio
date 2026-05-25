// Package styles holds the v8 TUI design tokens: the 16-color palette,
// the per-agent color map, and the composed lipgloss styles that PR-B…E
// consume. It is the single source of truth for color in the v8 rebuild.
//
// This package is currently dead-but-compiled: nothing wires it to
// `rufio tui` yet. The old flat internal/tui/styles.go remains the
// default until the PR-F cutover; this subpackage only ADDS the new v8
// token layer per docs/plans/2026-05-15-tui-v8-rebuild.md (PR-A).
//
// No init() side effects: styles are built lazily by SetProfile via
// buildStyles(), never in init(), preserving the CLAUDE.md stack rule
// and the no-side-effecting-init discipline carried from PR #22.
package styles

import "github.com/charmbracelet/lipgloss"

// Palette is the v8 24-bit truecolor token set. The hex values are the
// literal source of truth from the design handoff §6 and the prototype
// `BT_V8` object in docs/design/tui-v8/reference/rufio-bubbletea-v8.jsx
// (lines 14-31).
//
// Handoff token count: 16. NOTE — the handoff §6 Go sketch struct lists
// only 15 fields (it omits Particle). Particle (#c4b5fd) IS present in
// the jsx BT_V8 object at line 30 (`particle: '#c4b5fd'`) and the mesh
// particle flow (PR-D/E) needs it, so it is INCLUDED here. This one-field
// delta between the §6 sketch and the jsx is intentional and is the only
// divergence from the handoff's Go sketch shape.
//
// EXTENSION (post-handoff): Steel (#9aa6b8, steel grey) is a 17th token
// that is NOT from the handoff §6 / jsx BT_V8 source of truth — it is a
// dedicated launch-demo identity token added so codex-cli (the 4th demo
// harness) renders distinct from the purple-ish body text. It is grouped
// last, after the handoff tokens, so the 16 handoff values above remain
// the verbatim design-token set asserted by TestPaletteTokens.
var Palette = struct {
	Bg, Panel, Panel2     lipgloss.Color
	Fg, FgMute, Dim, VDim lipgloss.Color
	Label, Accent         lipgloss.Color
	Accent2, Accent3      lipgloss.Color
	Good, Warm, Line      lipgloss.Color
	Ring, Particle        lipgloss.Color
	Steel                 lipgloss.Color
}{
	Bg:       "#13111c", // app background
	Panel:    "#1a1726", // chat / mesh panel
	Panel2:   "#1f1c2e", // reserved, not used in v8
	Fg:       "#ece9f5", // primary text
	FgMute:   "#a39db8", // muted body text, agent names in chat
	Dim:      "#7d7798", // captions, secondary
	VDim:     "#4a4665", // unvoted quorum dots, timestamps
	Label:    "#c4b5fd", // lavender labels
	Accent:   "#a78bfa", // violet — primary brand
	Accent2:  "#8ab4f8", // soft lavender-blue
	Accent3:  "#d8b4fe", // light mauve (operator)
	Good:     "#a8e6a3", // green — verify
	Warm:     "#f5b78a", // peach — navigation / crawl
	Line:     "#2d2742", // hairlines, separators
	Ring:     "#4a4470", // mesh pulse rings
	Particle: "#c4b5fd", // mesh particle flow (jsx BT_V8.particle, line 30)
	Steel:    "#9aa6b8", // steel grey — codex-cli (4th demo harness)
}
