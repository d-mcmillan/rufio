// caret_typing_test.go — deterministic tests for the 1000ms caret
// blink (50% duty) and the 220ms typing-dots 3-state cadence, including
// the frame-0 invariant and the stable-cell-width contract (a blinking
// caret / animating dots must NEVER reflow the row or break a border).
package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// TestCaretBlinkPhase pins the blink: ON when the 500ms toggle counter
// is EVEN, OFF when ODD. counter 0 (frame-0) MUST be ON (the jsx
// r-blink starts at opacity 1; the pre-PR-F static caret was solid
// `▮`). A full ON+OFF cycle = 2 toggles = 1000ms, 50% duty.
func TestCaretBlinkPhase(t *testing.T) {
	if !caretOn(0) {
		t.Errorf("FRAME-0 BROKEN: caretOn(0) = false, want true (caret must be " +
			"solid ▮ at counter 0 — the pre-PR-F static render)")
	}
	for k := 0; k < 8; k++ {
		want := k%2 == 0
		if caretOn(k) != want {
			t.Errorf("caretOn(%d) = %v, want %v (even=ON, odd=OFF; 50%% duty)",
				k, caretOn(k), want)
		}
	}
}

// TestCaretCellStableWidth is the load-bearing width contract: the ON
// caret and the OFF caret occupy the EXACT same visible cell width
// (1), so a blink never reflows the composer/decision row and never
// breaks the panel border at any frame.
func TestCaretCellStableWidth(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	on := caretCell(true, lipgloss.Color(styles.Palette.Accent))
	off := caretCell(false, lipgloss.Color(styles.Palette.Accent))
	if lipgloss.Width(on) != 1 {
		t.Errorf("ON caret width = %d, want 1 (%q)", lipgloss.Width(on), on)
	}
	if lipgloss.Width(off) != 1 {
		t.Errorf("OFF caret width = %d, want 1 (%q) — a reflow/border break",
			lipgloss.Width(off), off)
	}
	if stripSGR(on) != "▮" {
		t.Errorf("ON caret glyph = %q, want ▮", stripSGR(on))
	}
	if stripSGR(off) != " " {
		t.Errorf("OFF caret glyph = %q, want a single space (same width)", stripSGR(off))
	}
}

// TestComposerCaretBlinks proves RenderComposer threads the caret phase:
// at an ON counter the `▮` is present; at an OFF counter it is gone but
// the rendered block width per line is unchanged (no reflow).
func TestComposerCaretBlinks(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	onView := renderComposer(80, true, 0)  // counter 0 → ON
	offView := renderComposer(80, true, 1) // counter 1 → OFF

	if !strings.Contains(stripSGR(onView), "▮") {
		t.Errorf("composer at caret counter 0 (ON) has no ▮:\n%s", stripSGR(onView))
	}
	// Frame-0 invariant: counter 0 must equal the pre-PR-F static
	// composer (solid caret) — assert the input row still ends with the
	// sample text immediately followed by ▮ (byte-shape unchanged).
	if !strings.Contains(stripSGR(onView), composerSample+"▮") {
		t.Errorf("FRAME-0 BROKEN: composer counter-0 input row is not "+
			"`%s▮` (static caret shape changed):\n%s", composerSample, stripSGR(onView))
	}
	// OFF frame: every line is the SAME visible width as the ON frame
	// (the caret OFF is a same-width space — no reflow).
	onLines := strings.Split(stripSGR(onView), "\n")
	offLines := strings.Split(stripSGR(offView), "\n")
	if len(onLines) != len(offLines) {
		t.Fatalf("composer line count changed ON=%d OFF=%d (reflow)",
			len(onLines), len(offLines))
	}
	for i := range onLines {
		if lipgloss.Width(onLines[i]) != lipgloss.Width(offLines[i]) {
			t.Errorf("composer line %d width changed ON=%d OFF=%d (caret blink "+
				"reflowed the row — border-integrity risk)", i,
				lipgloss.Width(onLines[i]), lipgloss.Width(offLines[i]))
		}
	}
}

// TestTypingDots3State pins the 220ms 3-state cycle and its frame-0
// invariant: counter 0 MUST be `···` (the pre-PR-F static 3-dot
// string) so the chat golden stays byte-identical; the cycle then
// advances through 3 distinct states and every state is EXACTLY 3
// cells wide (stable — no reflow).
func TestTypingDots3State(t *testing.T) {
	if got := typingDots(0); got != "···" {
		t.Errorf("FRAME-0 BROKEN: typingDots(0) = %q, want %q (the pre-PR-F "+
			"static `···` — the chat golden would change)", got, "···")
	}
	seen := map[string]bool{}
	for k := 0; k < 9; k++ { // 3 full cycles
		d := typingDots(k)
		if w := lipgloss.Width(d); w != 3 {
			t.Errorf("typingDots(%d) width = %d, want 3 (stable cell width — "+
				"a reflow): %q", k, w, d)
		}
		seen[d] = true
		// The cycle is period-3 and deterministic.
		if typingDots(k) != typingDots(k+3) {
			t.Errorf("typingDots not period-3: f(%d)=%q f(%d)=%q",
				k, typingDots(k), k+3, typingDots(k+3))
		}
	}
	if len(seen) != 3 {
		t.Errorf("typing-dots cycle is not 3-state: saw %d distinct states %v",
			len(seen), seen)
	}
}

// TestRenderTypingIndicatorAnimates proves RenderTypingIndicator threads
// the typing counter and stays width-stable (the chat-panel
// border-integrity contract holds at every typing frame).
func TestRenderTypingIndicatorAnimates(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	f0 := stripSGR(renderTypingIndicatorAt("data-analyst", 0))
	f1 := stripSGR(renderTypingIndicatorAt("data-analyst", 1))
	if !strings.HasSuffix(f0, "···") {
		t.Errorf("FRAME-0 BROKEN: typing indicator counter-0 must end `···`: %q", f0)
	}
	if lipgloss.Width(f0) != lipgloss.Width(f1) {
		t.Errorf("typing indicator width changed across frames (%d vs %d) — "+
			"reflow / border-integrity risk", lipgloss.Width(f0), lipgloss.Width(f1))
	}
	if f0 == f1 {
		t.Errorf("typing indicator did not animate between counter 0 and 1: %q", f0)
	}
}
