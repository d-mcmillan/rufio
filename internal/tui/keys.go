// keys.go — v8 keybinding definitions and the help-overlay model.
//
// RE-SCOPE (2026-05-15, PR-D §4): nav interactivity is pulled forward
// (was PR-F) so the eyeball gate is judge-able — you must be able to
// move. This file centralises the keymap so app.go's Update stays a thin
// dispatcher and the help overlay renders the SAME table the keymap
// defines (no drift between "what the help says" and "what the keys do").
//
// (Was reachable only via the RUFIO_TUI_PREVIEW=1 gate with the legacy
// internal/tui path as the default; the G4 cutover, 2026-05-17, made v8
// the unconditional `rufio tui` and deleted the gate + legacy path.)
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// keyBinding is one row of the help overlay / footer-conceptual keymap.
type keyBinding struct {
	key   string // the key glyph(s), e.g. "1-5", "tab", "enter"
	label string // what it does
}

// keyMap is the canonical v8 keybinding table. The help overlay renders
// this verbatim; app.go's Update implements exactly these. Numeric tab
// switches (1-5) and tab/shift+tab cycling are listed once each.
// keyMap is the canonical v8 keybinding table. G-interact added the
// composer + the compose/nav focus model — the help reflects it so the
// rendered help never drifts from what the keys do. The substrate view
// has two modes; the other tabs are read-only lists (nav keys always
// work there).
var keyMap = []keyBinding{
	{"(type)", "compose: text → broadcast · @agent → direct · /cmd → action"},
	{"⏎ / ⇧⏎ / ⌃J", "compose: send · insert newline · insert newline (Terminal.app fallback)"},
	{"esc", "compose → nav (or close overlay / drill-down)"},
	{"i", "nav → compose"},
	{"c / r", "nav: confirm / refute the selected decision row"},
	{"1-5", "nav: switch tab (substrate·fleet·channels·goals·memory)"},
	{"tab / shift+tab", "nav: cycle tabs forward / back"},
	{"↑/k  ↓/j", "nav: move selection (substrate rows)"},
	{"enter", "nav: open lineage drill-down (on a decision row)"},
	{":", "command palette (coming soon)"},
	{"?", "toggle this help"},
	{"ctrl+c", "quit (always — every mode)"},
	{"q", "quit (nav mode; a literal char while composing)"},
}

// helpTitle is the help-overlay heading.
const helpTitle = "rufio · keybinds"

// renderHelpOverlay renders the help overlay: a rounded, Panel-backed
// bordered box listing keyMap, centered in the given width/height. The
// dismissal style matches PR-C / the old TUI's help (?/esc/any key
// closes — handled in app.go's Update). Keys render in Accent, labels in
// Dim, the title in Accent bold.
func renderHelpOverlay(width, height int) string {
	title := lipgloss.NewStyle().
		Foreground(styles.Palette.Accent).
		Bold(true).
		Render(helpTitle)

	keyStyle := lipgloss.NewStyle().Foreground(styles.Palette.Accent)
	labelStyle := lipgloss.NewStyle().Foreground(styles.Palette.Dim)

	// Widest key column so the labels align.
	keyW := 0
	for _, b := range keyMap {
		if w := lipgloss.Width(b.key); w > keyW {
			keyW = w
		}
	}

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")
	for i, bind := range keyMap {
		if i > 0 {
			b.WriteString("\n")
		}
		pad := keyW - lipgloss.Width(bind.key)
		b.WriteString(keyStyle.Render(bind.key))
		b.WriteString(strings.Repeat(" ", pad+2))
		b.WriteString(labelStyle.Render(bind.label))
	}
	b.WriteString("\n\n")
	b.WriteString(labelStyle.Render("press ? or esc to close"))

	box := styles.Panel.
		Padding(1, 3).
		Render(b.String())

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
