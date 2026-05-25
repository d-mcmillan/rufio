// composer_test.go — unit tests for the v8 static composer.
//
// Asserts composer.go against the jsx spec
// (docs/design/tui-v8/reference/rufio-bubbletea-v8.jsx lines 267-305)
// and handoff §7.5/§9. Content assertions run under the Ascii termenv
// profile so ANSI escapes are stripped and the visible glyphs are
// deterministic; one TrueColor test asserts a 24-bit escape on a hint
// key.
//
// As in chat_test.go, every test calls the QUALIFIED styles.SetProfile
// (the NEW internal/tui/styles subpackage) — never the bare in-package
// SetProfile from the old internal/tui/styles.go.
package tui

import (
	"strings"
	"testing"

	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// TestComposerTopRule asserts the composer opens with a full-width
// horizontal hairline (jsx `borderTop: 1px solid Line`, line 269).
func TestComposerTopRule(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	out := RenderComposer(80, true)
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(first, "─") {
		t.Errorf("composer top-rule missing ─ run: %q", first)
	}
	// The rule should span the full width (80 box-drawing cells).
	if got := strings.Count(first, "─"); got != 80 {
		t.Errorf("top-rule width = %d ─ cells, want 80: %q", got, first)
	}
}

// TestComposerPromptAndTarget asserts the `›` prompt and the
// composerTarget chip (now `@fleet` after the PR-D re-skin), and that
// the chip has NO surrounding
// border/fill chars (handoff §7.5: "no fill, no border — just colored
// text").
func TestComposerPromptAndTarget(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	out := RenderComposer(80, true)
	if !strings.Contains(out, composerPrompt) {
		t.Errorf("composer missing %q prompt: %q", composerPrompt, out)
	}
	if !strings.Contains(out, composerTarget) {
		t.Errorf("composer missing %q target chip: %q", composerTarget, out)
	}
	// No bordered/filled chip: the chip is bare text, so none of the
	// box/bracket fill glyphs should hug the target chip.
	for _, bad := range []string{"[" + composerTarget, composerTarget + "]", "│" + composerTarget, "▌" + composerTarget} {
		if strings.Contains(out, bad) {
			t.Errorf("target chip must be bare text (no border/fill), found %q in %q", bad, out)
		}
	}
}

// TestComposerSampleAndCaret asserts the jsx placeholder input text and
// the static `▮` caret immediately after it (jsx lines 284-288;
// handoff §9 — static this PR, blink is PR-E).
func TestComposerSampleAndCaret(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	out := RenderComposer(120, true)
	if !strings.Contains(out, composerSample) {
		t.Errorf("composer missing sample input %q: %q", composerSample, out)
	}
	if !strings.Contains(out, composerSample+composerCaret) {
		t.Errorf("caret %q must immediately follow the sample text: %q", composerCaret, out)
	}
}

// TestComposerCounter asserts the right-aligned char counter
// (composerCounter, `57 / 2000` after the PR-D re-skin; jsx line 290
// shape `N / 2000`).
func TestComposerCounter(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	out := RenderComposer(120, true)
	if !strings.Contains(out, composerCounter) {
		t.Errorf("composer missing char counter %q: %q", composerCounter, out)
	}
	// The block is now ComposerHeight rows (top-rule, padV blank(s),
	// INPUT row, gapV blank(s), hint row, padV blank(s) — the jsx
	// padding:10px + gap:6). The counter is right-aligned on the INPUT
	// row, which sits at index 1+composerPadV.
	rows := strings.Split(out, "\n")
	if len(rows) != ComposerHeight {
		t.Fatalf("composer should have ComposerHeight=%d rows, got %d: %q",
			ComposerHeight, len(rows), out)
	}
	inputRow := rows[1+composerPadV]
	if !strings.HasSuffix(strings.TrimRight(inputRow, " "), composerCounter) {
		t.Errorf("counter should be right-aligned on the input row (idx %d): %q",
			1+composerPadV, inputRow)
	}
}

// TestComposerHintRowAlwaysShown asserts the hint row renders even when
// focused=false — handoff §9 overrides the jsx `composerFocus` hover
// gate ("the composer is always focused in a TUI. Always show the hint
// row.").
func TestComposerHintRowAlwaysShown(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	for _, focused := range []bool{true, false} {
		out := RenderComposer(120, focused)
		for _, h := range composerHints {
			if !strings.Contains(out, h.label) {
				t.Errorf("focused=%v: hint row missing label %q: %q", focused, h.label, out)
			}
		}
		if !strings.Contains(out, composerStatus) {
			t.Errorf("focused=%v: hint row missing broadcast status %q: %q", focused, composerStatus, out)
		}
		// Hint keys present.
		for _, h := range composerHints {
			if !strings.Contains(out, h.key) {
				t.Errorf("focused=%v: hint row missing key glyph %q: %q", focused, h.key, out)
			}
		}
	}
}

// TestComposerHintIndent asserts the hint row is left-padded by
// composerHintIndent cells (jsx paddingLeft:22 → round(22/2.6) ≈ 8
// cells, documented in composer.go). With the jsx vertical padding the
// hint row is at index 1+composerPadV+1+composerGapV (top-rule, padV,
// input, gapV, → HINT), NOT the last line (the last is a padV blank).
func TestComposerHintIndent(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	out := RenderComposer(120, true)
	rows := strings.Split(out, "\n")
	if len(rows) != ComposerHeight {
		t.Fatalf("composer should have ComposerHeight=%d rows, got %d: %q",
			ComposerHeight, len(rows), out)
	}
	hintRow := rows[1+composerPadV+1+composerGapV]
	if !strings.HasPrefix(hintRow, strings.Repeat(" ", composerHintIndent)) {
		t.Errorf("hint row should start with %d leading spaces, got %q", composerHintIndent, hintRow)
	}
	// And the first non-space content is the first hint key glyph.
	trimmed := strings.TrimLeft(hintRow, " ")
	if !strings.HasPrefix(trimmed, composerHints[0].key) {
		t.Errorf("first hint content should be %q, got %q", composerHints[0].key, trimmed)
	}
	// The block's last row is a genuine blank (jsx bottom padding) so
	// the chat panel's Panel bg fills it — NOT a `─` rule, NOT content.
	if last := rows[len(rows)-1]; strings.TrimSpace(last) != "" {
		t.Errorf("composer last row must be a blank padding line, got %q", last)
	}
}

// TestComposerHintKeysTrueColor asserts that under termenv.TrueColor a
// hint key glyph carries the 24-bit Accent foreground escape. Accent
// #a78bfa → rgb(167,139,250) → ESC[38;2;167;139;250m.
func TestComposerHintKeysTrueColor(t *testing.T) {
	styles.SetProfile(termenv.TrueColor)
	defer styles.SetProfile(termenv.Ascii)
	out := RenderComposer(120, true)
	const wantAccentFg = "38;2;167;139;250"
	if !strings.Contains(out, wantAccentFg) {
		t.Fatalf("composer missing 24-bit Accent fg %q (hint keys / prompt): %q", wantAccentFg, out)
	}
}

// TestComposerTinyWidth asserts a very narrow width does not panic and
// still returns the full ComposerHeight structural rows (top-rule, the
// jsx vertical padding/gap blanks, input row, hint row).
func TestComposerTinyWidth(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	out := RenderComposer(4, true)
	if out == "" {
		t.Fatal("RenderComposer(4) returned empty")
	}
	if n := strings.Count(out, "\n"); n != ComposerHeight-1 {
		t.Errorf("composer should have ComposerHeight=%d rows (%d newlines), got %d: %q",
			ComposerHeight, ComposerHeight-1, n, out)
	}
}
