// keys_test.go — unit tests for the v8 keymap + help overlay (PR-D §4).
//
// Asserts the help overlay renders the keyMap verbatim (no drift between
// "what help says" and "what keys do"), the keymap covers the documented
// PR-D interactions, and the overlay stays inside its panel border.
package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// TestKeyMapCoversPRDInteractions asserts the keymap documents every
// interaction PR-D §4 requires (so the help overlay can't silently omit
// one).
func TestKeyMapCoversPRDInteractions(t *testing.T) {
	joined := ""
	for _, b := range keyMap {
		joined += b.key + " | " + b.label + "\n"
	}
	wants := []string{
		"1-5", "tab", "shift+tab", // nav
		"enter", "lineage", // drill-down
		"esc",       // close overlay
		"?",         // help
		"q", "quit", // quit
	}
	for _, w := range wants {
		if !strings.Contains(joined, w) {
			t.Errorf("keyMap missing documented PR-D interaction %q in:\n%s", w, joined)
		}
	}
}

// TestHelpOverlayRendersKeyMap asserts the help overlay renders the
// title + every keyMap row's key and label.
func TestHelpOverlayRendersKeyMap(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	out := renderHelpOverlay(120, 36)
	if !strings.Contains(out, helpTitle) {
		t.Errorf("help overlay missing title %q in:\n%s", helpTitle, out)
	}
	for _, b := range keyMap {
		if !strings.Contains(out, b.key) {
			t.Errorf("help overlay missing key %q in:\n%s", b.key, out)
		}
		if !strings.Contains(out, b.label) {
			t.Errorf("help overlay missing label %q in:\n%s", b.label, out)
		}
	}
	if !strings.Contains(out, "press ? or esc to close") {
		t.Errorf("help overlay missing dismissal hint in:\n%s", out)
	}
}

// TestHelpOverlayBorderIntact asserts the help overlay box is a closed
// rounded border (╭ … ╯) and no line exceeds the requested width.
func TestHelpOverlayBorderIntact(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	const w = 120
	out := renderHelpOverlay(w, 36)
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╯") {
		t.Errorf("help overlay must have a rounded border:\n%s", out)
	}
	for i, ln := range strings.Split(out, "\n") {
		if gw := lipgloss.Width(ln); gw > w {
			t.Errorf("help overlay line %d width = %d exceeds %d: %q", i, gw, w, ln)
		}
	}
}

// TestLineageOverlayBorderIntact asserts the lineage drill-down box is a
// closed rounded border and fits the requested width.
func TestLineageOverlayBorderIntact(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	const w = 120
	out := renderLineageOverlay(SubstrateThread[5].Lineage, w, 36)
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╯") {
		t.Errorf("lineage overlay must have a rounded border:\n%s", out)
	}
	for i, ln := range strings.Split(out, "\n") {
		if gw := lipgloss.Width(ln); gw > w {
			t.Errorf("lineage overlay line %d width = %d exceeds %d: %q", i, gw, w, ln)
		}
	}
}
