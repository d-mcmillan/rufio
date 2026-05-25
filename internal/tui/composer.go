// composer.go — static composer rendering for the v8 TUI.
//
// Faithful character-cell port of the jsx composer block from
// docs/design/tui-v8/reference/rufio-bubbletea-v8.jsx (lines 267-305),
// per handoff §7.5 (composer) and §9 ("what's faked").
//
// ADD-ONLY (PR-C): RenderComposer is consumed by the v8 app.go. (Was
// reachable only via the hidden RUFIO_TUI_PREVIEW=1 gate with the legacy
// internal/tui render path as the default; the G4 cutover, 2026-05-17,
// made v8 the unconditional `rufio tui` and deleted the gate + that
// legacy path.)
//
// ── px → cell mapping (same ≈2.6 px/cell ratio as chat.go) ────────────
//
// The jsx is browser CSS; chat.go fixes the conversion at
// `cells ≈ round(px / 2.6)` (handoff §6 "18px ≈ 2 cells").
//
// HORIZONTAL — the only px gap unique to this file is the hint row's
// `paddingLeft:22` (rufio-bubbletea-v8.jsx line 295): round(22 / 2.6) ≈
// 8 cells. So the hint row is left-padded by 8 cells, which lands it
// under the prompt + target-chip region of the composer row. This is
// the ONLY new horizontal indent introduced here and it derives from
// the single chat.go ratio. The composer's `padding:'10px 16px'`
// HORIZONTAL component (16px) is NOT applied here — it is already
// supplied by the chat panel's interior `chatPanelHPad=2` (app.go), so
// re-adding it would double-pad against the panel border.
//
// VERTICAL — the composer wrapper is `padding: '10px 16px'` +
// `display:flex; flexDirection:column; gap:6` (rufio-bubbletea-v8.jsx
// lines 268-272). Cells (vertical lineHeight ≈ the same ≈2.6 px/cell
// reasoning, rounded to whole rows since a terminal row is atomic):
//
//	padding 10px (top)  → round(10 / 2.6) ≈ 1 blank row  (composerPadV)
//	gap 6 (input↔hint)  → round(6  / 2.6) ≈ 1 blank row  (composerGapV)
//	padding 10px (btm)  → round(10 / 2.6) ≈ 1 blank row  (composerPadV)
//
// So the composer block is ~6 rows: top-rule, blank, input row, blank,
// hint row, blank — a roomy input region with real breathing room
// around the input line, matching the jsx (was a cramped flat 3-row
// strip with zero vertical padding). The blank rows are GENUINE empty
// lines so the chat panel's Panel bg fills them (the input region reads
// as filled space, NOT as `─` rules). These vertical pads derive from
// the SAME ≈2.6 ratio as the hint indent and chat.go — not re-derived.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// composerHintIndent is the left pad of the hint row. jsx
// `paddingLeft:22` (line 295) → round(22 / 2.6) ≈ 8 cells (see the
// px→cell note in the file doc comment).
const composerHintIndent = 8

// composerPadV / composerGapV are the composer block's VERTICAL
// breathing room, in blank rows, derived from the jsx composer wrapper
// `padding:'10px 16px'` + `gap:6` (see the VERTICAL px→cell note in the
// file doc comment): 10px ≈ 1 blank row top and bottom (composerPadV),
// gap:6 ≈ 1 blank row between the input row and the hint row
// (composerGapV). These give the composer real room so it reads as an
// input region rather than a cramped 3-row strip.
const (
	composerPadV = 1 // jsx padding:10px (vertical), each side
	composerGapV = 1 // jsx gap:6 (input row ↔ hint row)
)

// ComposerHeight is the fixed line count RenderComposer returns:
// top-rule + padV + input row + gapV + hint row + padV. app.go uses
// this to size the chat panel's composer region AND to keep the mesh
// rail's ROUTING rule aligned across columns with the composer's
// top-rule (the jsx cross-column detail) without re-counting lines.
const ComposerHeight = 1 + composerPadV + 1 + composerGapV + 1 + composerPadV

// Composer glyphs / sample content.
//
// RE-SCOPE (2026-05-15, PR-D): the jsx's literal placeholder content
// (`@all-agents` / "hold on §3.2 …" / "broadcasting to 6 of 7
// (tester-h-12 idle)") was generic-prototype chrome AND referenced the
// dropped `§3.2`/governance concept (re-scope §1 drops governance) plus
// the fictional `tester-h-12` agent. The composer LAYOUT is unchanged
// (handoff §7.5); only the sample STRINGS are re-skinned to the
// customer:5821 churn arc so the composer reads as OUR substrate. Real
// input state is still PR-F (visual-only this phase). The counter is
// recomputed from the new sample so it stays honest.
const (
	composerPrompt = "›"
	composerTarget = "@fleet"
	// composerSample is the Rufio-domain placeholder input (re-scope §1).
	composerSample = "downgrade approved — log learned/customer:5821 and notify"
	// composerCaret is the single block glyph. jsx renders a 7×12px
	// block with the `r-blink 1s steps(1)` keyframe (lines 285-288);
	// handoff §9 maps this to "a single ▮ cell, toggled on a 500ms
	// tick". PR-F: blinks via caretCell (ON=▮, OFF=same-width space).
	composerCaret = "▮"
	// composerCounter is the char counter — len(composerSample) (57
	// runes) of the jsx's 2000 cap (jsx line 290 shape `N / 2000`).
	composerCounter = "57 / 2000"
	// composerStatus is the broadcast status line, re-skinned to the
	// churn-arc fleet (3 agents, all linked).
	composerStatus = "broadcasting to 3 of 3 (all linked)"
)

// composerHint is one (key, label) pair of the hint row (jsx lines
// 297-300). The key glyph renders in Accent; the label in Dim.
type composerHint struct {
	key   string
	label string
}

// composerHints is the ordered hint set, a verbatim port of the jsx
// hint spans (rufio-bubbletea-v8.jsx lines 297-300).
// v1.0.6.3 (Bundle F doc-nit): the `newline` hint surfaces BOTH bindings —
// `⇧⏎` (Shift+Enter, works in iTerm2 / Kitty / Alacritty / WezTerm /
// most modern terminals) and `⌃J` (Ctrl+J, the protocol-level newline
// byte that ALWAYS works, including macOS Terminal.app which cannot
// distinguish Shift+Enter from plain Enter at the protocol level). The
// composer Update path already accepts both keymaps (app.go ~1334); this
// surfaces the fallback so users in any terminal know at least one
// working combo.
var composerHints = []composerHint{
	{"⏎", "send"},
	{"⇧⏎/⌃J", "newline"},
	{"/", "command"},
	{"@", "target"},
}

// caretOn reports whether the blink caret is visible at the given
// 500ms toggle counter (anim.caret). It is ON when the counter is EVEN,
// OFF when ODD — so a full ON(500ms)+OFF(500ms) cycle is 1000ms at 50%
// duty, the faithful `r-blink 1s steps(1)` + `50% { opacity: 0 }`
// (rufio-styles.css 454) / handoff §9 "toggle on a 500ms tick".
//
// FRAME-0 INVARIANT: caretOn(0) == true. counter 0 is the fresh-App
// state every golden renders, and the jsx r-blink starts at opacity 1
// (visible) — so the caret is solid `▮` at counter 0, byte-identical
// to the pre-PR-F static render.
func caretOn(counter int) bool {
	return counter%2 == 0
}

// caretCell renders the blink caret as a STABLE-WIDTH 1-cell string:
// the `▮` block in `color` when on, a single space (no SGR) when off.
// The width is identical in both states (1 cell) so a blink NEVER
// reflows the composer / decision row and never breaks the panel
// border at any frame — the load-bearing width contract (caret OFF is
// a same-width space, the prompt's explicit requirement).
func caretCell(on bool, color lipgloss.Color) string {
	if !on {
		return " " // same 1-cell width as ▮ — no reflow, ever.
	}
	return lipgloss.NewStyle().Foreground(color).Render(composerCaret)
}

// RenderComposer renders the composer block at the given width and the
// per jsx lines 267-305 and handoff §7.5. The layout is the jsx
// composer wrapper's `padding:'10px 16px'` + `gap:6` translated to
// rows (ComposerHeight lines total — was a cramped flat 3-row strip):
//
//	<top-rule, full width, Line color>                  ── jsx borderTop
//	<blank>                                              ── jsx padding 10px (top)
//	<› prompt> <@fleet chip> <sample text▮>   <57 / 2000>  ── input row
//	<blank>                                              ── jsx gap:6
//	<8-cell indent>⏎ send · ⇧⏎/⌃J newline · / command · @ target · <status>
//	<blank>                                              ── jsx padding 10px (btm)
//
// The blank rows are GENUINE empty lines so the chat panel's Panel bg
// fills them and the composer reads as a roomy input REGION (not a
// cramped strip, not `─` rules). (the chip / sample / counter strings
// are the PR-D Rufio re-skin — composerTarget/composerSample/
// composerCounter; the LAYOUT is the jsx's, now with its vertical
// padding faithfully reproduced.)
//
//   - The top-rule is a `─`×width hairline in Palette.Line (jsx
//     `borderTop: 1px solid Line`, line 269).
//   - The prompt `›` is Accent bold (jsx line 279).
//   - The composerTarget chip is Accent-colored TEXT with NO fill
//     and NO border (jsx lines 280-282 / handoff §7.5: "no fill, no
//     border — just colored text").
//   - The sample input is Fg (jsx line 283); the caret `▮` is Accent
//     and BLINKS at the 1000ms/50%-duty cadence via caretCell
//     (caretCounter; ON=▮, OFF=same-width space — no reflow).
//   - The char counter (composerCounter, `57 / 2000`) is VDim,
//     right-aligned (jsx line 290 shape `N / 2000`).
//   - The hint row is left-padded composerHintIndent cells, with each
//     key glyph in Accent and label in Dim, ` · ` separators, then a `·`
//     in Line, then the broadcast status in Dim (jsx lines 292-303).
//
// The `focused` parameter mirrors the jsx `composerFocus` gate (line
// 292) and is retained because PR-G wires real focus state through it.
// Per handoff §9 ("Hover states ... Doesn't apply — the composer is
// always focused in a TUI. Always show the hint row.") the hint row is
// rendered UNCONDITIONALLY regardless of `focused`; the parameter is
// intentionally accepted-but-not-branched-on. This is a deliberate
// handoff-§9 override of the jsx's hover gate, not an oversight.
//
// RenderComposer is the stable PR-C/D 2-arg entry point (composer_test.go
// pins it). It renders at caret counter 0 — the frame-0 state (ON,
// solid `▮`) — so every existing composer test + golden stays
// byte-identical. The animated path is renderComposer(width, focused,
// caretCounter), which app.go calls with App.anim.caret.
func RenderComposer(width int, focused bool) string {
	return renderComposer(width, focused, 0)
}

// composerPlaceholder is the Dim prompt shown on the input row when the
// live buffer is EMPTY and the composer is focused for typing (G-interact).
// It replaces the static composerSample placeholder in the LIVE path so an
// empty compose state reads as an actionable prompt, not fake content. The
// static composerSample is retained for RenderComposer (the PR-C/D unit
// tests + the read-only composer goldens pin it byte-for-byte).
const composerPlaceholder = "type to broadcast · @agent to direct · /cmd for actions"

// renderComposerLive is the G-interact buffer-aware composer renderer. It
// is renderComposer with the static composerSample REPLACED by the live
// operator buffer `buf` (the value of the composer's bubbles/textarea —
// see app.go's composeTA) and the v8 blink-caret placed at the textarea's
// CURSOR position. LOAD-BEARING invariant: the rendered footprint is
// EXACTLY ComposerHeight rows and the top-rule is the lower interior
// hairline — IDENTICAL to renderComposer — regardless of buffer content,
// cursor position, or line count, so the structural gates (zzrender
// 3-section, per-panel border-integrity, ROUTING-rule alignment — which
// depends on the fixed ComposerHeight) stay green. A multi-line buffer
// (⇧⏎ newline) is real (the textarea holds N logical lines) but only the
// line the CURSOR is on is shown on the fixed single input row, prefixed
// with a Dim `↵ ` continuation marker when there are prior lines (the
// documented fixed-height contract: the composer never grows vertically —
// a growing textarea would shift the top-rule and break the
// routingBottomPad/ROUTING alignment + every structural gate; the prior
// hand buffer showed only its LAST line, this shows the CURSOR's line so
// editing on any logical line is visible — the faithful equivalent).
//
// CARET RECONCILIATION (the textarea has its OWN cursor; the v8 visual
// has its OWN blink-caret): the textarea's internal cursor RENDERING is
// never shown (its View() is never called — app.go feeds it keys and
// reads Value()+Line()+LineInfo() only). The visible caret is the v8
// blink-caret (caretCell, the `r-blink` 1000ms/50%-duty `▮`) placed AT
// the textarea cursor's column (`curCol`) on the cursor's logical line
// (`curLine`). It is the SAME stable-width cell (ON=▮, OFF=same-width
// space) so a blink never reflows the row / breaks the panel border at
// any cursor position — the v8 caret semantics are preserved exactly,
// now at the real edit point instead of always at end-of-line.
//
// `focused` mirrors the compose/nav mode (the hint row is still ALWAYS
// shown — handoff §9). caretCounter drives the same blink. When buf == ""
// a Dim composerPlaceholder is shown (no fake content) and the caret sits
// AFTER it — byte-identical to the pre-textarea empty render so the
// read-only substrate goldens do not move. The counter is
// len([]rune(buf)) / 2000 (honest whole-buffer live count).
func renderComposerLive(width int, focused bool, caretCounter int, buf string, curLine, curCol int) string {
	if width < 1 {
		width = 1
	}

	topRule := styles.SectionRule.Render(strings.Repeat("─", width))

	prompt := lipgloss.NewStyle().
		Foreground(styles.Palette.Accent).
		Bold(true).
		Render(composerPrompt)
	target := lipgloss.NewStyle().
		Foreground(styles.Palette.Accent).
		Render(composerTarget)

	caret := caretCell(caretOn(caretCounter), lipgloss.Color(styles.Palette.Accent))

	// The input glyph: the textarea line the CURSOR is on, with the v8
	// blink-caret at the cursor column. Empty buffer → the Dim placeholder
	// + caret AFTER it (byte-identical to the pre-textarea render — the
	// read-only substrate goldens pin this exact empty shape).
	var inputCells string
	runes := []rune(buf)
	if len(runes) == 0 {
		inputCells = lipgloss.NewStyle().
			Foreground(styles.Palette.Dim).
			Render(composerPlaceholder) + caret
	} else {
		lines := strings.Split(buf, "\n")
		// Clamp curLine/curCol defensively to the buffer (a stale cursor
		// must never index out of range — degrade, never panic).
		if curLine < 0 {
			curLine = 0
		}
		if curLine >= len(lines) {
			curLine = len(lines) - 1
		}
		lineRunes := []rune(lines[curLine])
		if curCol < 0 {
			curCol = 0
		}
		if curCol > len(lineRunes) {
			curCol = len(lineRunes)
		}
		fg := lipgloss.NewStyle().Foreground(styles.Palette.Fg)
		// Split the cursor's logical line at the cursor column and place
		// the v8 blink-caret between the halves (cursor at end-of-line ⇒
		// `before▮` with empty after — same shape as the prior
		// last-line+caret render; cursor mid-line ⇒ `before▮after`).
		before := fg.Render(string(lineRunes[:curCol]))
		after := fg.Render(string(lineRunes[curCol:]))
		shown := before + caret + after
		if len(lines) > 1 {
			// Multi-line buffer: a Dim `↵ ` marker shows there are prior
			// lines without growing the composer (fixed-height contract).
			cont := lipgloss.NewStyle().Foreground(styles.Palette.Dim).Render("↵ ")
			shown = cont + shown
		}
		inputCells = shown
	}

	// Honest live char counter: rune count of the WHOLE buffer / 2000 (the
	// jsx `N / 2000` shape; the cap mirrors composerCounter's 2000).
	counter := lipgloss.NewStyle().
		Foreground(styles.Palette.VDim).
		Render(itoa(len(runes)) + " / 2000")

	// The caret is now placed WITHIN inputCells (at the cursor column),
	// not appended after the whole row — so the left segment is just
	// prompt + chip + the cursor-bearing input.
	left := prompt + " " + target + " " + inputCells
	leftW := lipgloss.Width(left)
	counterW := lipgloss.Width(counter)

	var composerRow string
	if leftW+1+counterW <= width {
		pad := width - leftW - counterW
		composerRow = left + strings.Repeat(" ", pad) + counter
	} else {
		composerRow = clampLine(left, width)
	}

	keyStyle := lipgloss.NewStyle().Foreground(styles.Palette.Accent)
	labelStyle := lipgloss.NewStyle().Foreground(styles.Palette.Dim)
	sep := styles.Hairline.Render(" · ")

	var hint strings.Builder
	hint.WriteString(strings.Repeat(" ", composerHintIndent))
	for i, hh := range composerHints {
		if i > 0 {
			hint.WriteString(sep)
		}
		hint.WriteString(keyStyle.Render(hh.key))
		hint.WriteString(" ")
		hint.WriteString(labelStyle.Render(hh.label))
	}
	hint.WriteString(sep)
	// Mode whisper: `compose` (Accent2) when focused for typing, `nav`
	// (Dim) when in nav mode — the only focus affordance, replacing the
	// static composerStatus broadcast string in the LIVE path. Honors the
	// hint row contract (always shown) + tells the operator which mode
	// keystrokes go to (the documented focus model's visible signal).
	if focused {
		hint.WriteString(lipgloss.NewStyle().Foreground(styles.Palette.Accent2).Render("compose"))
	} else {
		hint.WriteString(labelStyle.Render("nav · press i / type to compose"))
	}
	hintRow := hint.String()

	rows := make([]string, 0, ComposerHeight)
	rows = append(rows, topRule)
	for i := 0; i < composerPadV; i++ {
		rows = append(rows, "")
	}
	rows = append(rows, composerRow)
	for i := 0; i < composerGapV; i++ {
		rows = append(rows, "")
	}
	rows = append(rows, hintRow)
	for i := 0; i < composerPadV; i++ {
		rows = append(rows, "")
	}
	return strings.Join(rows, "\n")
}

// renderComposer is the caret-aware composer renderer. `caretCounter`
// is the 500ms blink-toggle counter (App.anim.caret); caretCounter 0 ==
// ON (the frame-0 / pre-PR-F static solid caret — RenderComposer's
// delegation keeps the goldens byte-identical).
func renderComposer(width int, focused bool, caretCounter int) string {
	// focused is intentionally not branched on — handoff §9 mandates the
	// hint row is always shown in a TUI. Reference it so the parameter is
	// not flagged unused while keeping the signature stable.
	_ = focused

	if width < 1 {
		width = 1
	}

	// 1. Top-rule: a full-width section rule (jsx borderTop 1px). PR-E.1:
	// Ring tone (not Hairline/Line) so it reads on the native terminal
	// bg now that the forced panel bg is removed — same glyph, same
	// width; only the color changes. The inline ` · ` dot separators in
	// the hint row below stay Hairline.
	topRule := styles.SectionRule.Render(strings.Repeat("─", width))

	// 2. Composer row: prompt · target chip · sample+caret ... counter.
	prompt := lipgloss.NewStyle().
		Foreground(styles.Palette.Accent).
		Bold(true).
		Render(composerPrompt)

	target := lipgloss.NewStyle().
		Foreground(styles.Palette.Accent).
		Render(composerTarget)

	sample := lipgloss.NewStyle().
		Foreground(styles.Palette.Fg).
		Render(composerSample)

	// Blink caret — ON (▮ in Accent) at even caretCounter, OFF (a
	// same-width space) at odd. caretCounter 0 ⇒ ON ⇒ byte-identical to
	// the pre-PR-F static solid caret (frame-0 invariant). Width is 1
	// cell either way, so the input row never reflows.
	caret := caretCell(caretOn(caretCounter), lipgloss.Color(styles.Palette.Accent))

	counter := lipgloss.NewStyle().
		Foreground(styles.Palette.VDim).
		Render(composerCounter)

	// Left segment: prompt + space + chip + space + sample + caret.
	left := prompt + " " + target + " " + sample + caret
	leftW := lipgloss.Width(left)
	counterW := lipgloss.Width(counter)

	// Right-align the counter. Pad between the left segment and the
	// counter so the counter sits flush against the right edge. If the
	// terminal is too narrow to fit both, drop the counter rather than
	// produce a negative pad (tiny-terminal safety).
	var composerRow string
	if leftW+1+counterW <= width {
		pad := width - leftW - counterW
		composerRow = left + strings.Repeat(" ", pad) + counter
	} else {
		composerRow = left
	}

	// 3. Hint row (ALWAYS shown — handoff §9 overrides the jsx hover
	// gate). 8-cell indent, key glyphs in Accent, labels in Dim, ` · `
	// separators in Line, then a `·` in Line, then status in Dim.
	keyStyle := lipgloss.NewStyle().Foreground(styles.Palette.Accent)
	labelStyle := lipgloss.NewStyle().Foreground(styles.Palette.Dim)
	sep := styles.Hairline.Render(" · ")

	var hint strings.Builder
	hint.WriteString(strings.Repeat(" ", composerHintIndent))
	for i, h := range composerHints {
		if i > 0 {
			hint.WriteString(sep)
		}
		hint.WriteString(keyStyle.Render(h.key))
		hint.WriteString(" ")
		hint.WriteString(labelStyle.Render(h.label))
	}
	hint.WriteString(sep)
	hint.WriteString(labelStyle.Render(composerStatus))
	hintRow := hint.String()

	// Assemble the block with the jsx vertical padding/gap as GENUINE
	// blank rows (empty strings — the chat panel's Panel bg fills them,
	// so the composer reads as a roomy input region). Order: top-rule,
	// padV, input row, gapV, hint row, padV (ComposerHeight rows).
	rows := make([]string, 0, ComposerHeight)
	rows = append(rows, topRule)
	for i := 0; i < composerPadV; i++ {
		rows = append(rows, "")
	}
	rows = append(rows, composerRow)
	for i := 0; i < composerGapV; i++ {
		rows = append(rows, "")
	}
	rows = append(rows, hintRow)
	for i := 0; i < composerPadV; i++ {
		rows = append(rows, "")
	}
	return strings.Join(rows, "\n")
}
