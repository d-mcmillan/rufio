// composer_edit_test.go — the composer-edit self-check.
//
// PROVES the v8 composer (COMPOSE mode) supports the standard
// readline/terminal line-editing set via the bubbles/textarea editing
// model, that Enter still parses+emits + the quit contract is exact,
// and that the v8 composer VISUAL + the FIXED ComposerHeight + the
// cross-column ROUTING-rule alignment are preserved.
//
// Every keystroke is injected as the EXACT tea.KeyMsg the bubbletea
// runtime delivers (so msg.String() matches the textarea KeyMap +
// app.go's intercepts) through App.Update — the real key path, no
// internal poking. Deterministic: no wall-clock, no fsnotify.
//
// HONEST Cmd-chord limitation (documented, asserted-by-design): macOS
// terminal emulators intercept Cmd-chords before a TUI sees them — a
// TUI can NEVER receive Cmd+Delete et al. The universal readline set
// proven below is what "delete the whole row" actually means in a
// console (Ctrl+U kill-to-start, Ctrl+K kill-to-end, Ctrl+W /
// Alt+Backspace delete-word-back).
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// composeApp is a windowed App in COMPOSE mode (the App default — the
// composer is focused). Keystrokes go to the live textarea, exactly the
// operator's experience.
func composeApp(t *testing.T) App {
	t.Helper()
	styles.SetProfile(termenv.Ascii)
	a, err := NewApp(t.TempDir())
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	m, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a = m.(App)
	if !a.composeMode {
		t.Fatalf("the App must start in COMPOSE mode (composer focused)")
	}
	return a
}

// send injects one key Msg through the real App.Update key path.
func send(t *testing.T, a App, k tea.KeyMsg) App {
	t.Helper()
	m, _ := a.Update(k)
	return m.(App)
}

// typeRunes feeds ordinary text rune-by-rune (KeyRunes / KeySpace — the
// exact shapes the runtime delivers).
func typeRunes(t *testing.T, a App, s string) App {
	t.Helper()
	for _, r := range s {
		if r == ' ' {
			a = send(t, a, tea.KeyMsg{Type: tea.KeySpace})
		} else {
			a = send(t, a, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	return a
}

// kr is an alt-modified rune key (e.g. alt+b) — msg.String() == "alt+b".
func kr(r rune, alt bool) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: alt}
}

// ── (1) the readline editing set, per-binding, through App.Update ─────

// TestComposerEdit_Readline proves each standard binding edits the
// composer textarea correctly. The assertion seam is composeText()
// (the buffer) — and, for motion bindings, that subsequently typing
// inserts at the moved cursor (the only way to OBSERVE a pure cursor
// move at the value level).
func TestComposerEdit_Readline(t *testing.T) {
	// Ctrl+U — kill to line start (delete-before-cursor).
	t.Run("Ctrl+U kills to line start", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "hello world")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlU})
		if got := a.composeText(); got != "" {
			t.Errorf("Ctrl+U at end must kill the whole line, got %q", got)
		}
	})

	// Ctrl+A then Ctrl+K — go to line start, then kill to line end ⇒
	// empties the line (proves BOTH Ctrl+A motion and Ctrl+K kill).
	t.Run("Ctrl+A + Ctrl+K", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "abc def")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlA}) // → line start
		a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlK}) // kill to end
		if got := a.composeText(); got != "" {
			t.Errorf("Ctrl+A then Ctrl+K must empty the line, got %q", got)
		}
	})

	// Ctrl+A (line start) is observable: after it, typing inserts at the
	// FRONT.
	t.Run("Ctrl+A moves to line start (insert at front)", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "world")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlA})
		a = typeRunes(t, a, "X")
		if got := a.composeText(); got != "Xworld" {
			t.Errorf("Ctrl+A then type must insert at line start, got %q", got)
		}
	})

	// Ctrl+E (line end) is observable: from line start, Ctrl+E then type
	// inserts at the END.
	t.Run("Ctrl+E moves to line end (insert at end)", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "world")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlA}) // start
		a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlE}) // end
		a = typeRunes(t, a, "Z")
		if got := a.composeText(); got != "worldZ" {
			t.Errorf("Ctrl+E then type must insert at line end, got %q", got)
		}
	})

	// Ctrl+W — delete the previous word.
	t.Run("Ctrl+W deletes previous word", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "alpha beta gamma")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlW})
		if got := a.composeText(); got != "alpha beta " {
			t.Errorf("Ctrl+W must delete the previous word, got %q", got)
		}
	})

	// Alt+Backspace — also delete the previous word (the same readline
	// verb; the prompt asks for both spellings).
	t.Run("Alt+Backspace deletes previous word", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "one two three")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyBackspace, Alt: true})
		if got := a.composeText(); got != "one two " {
			t.Errorf("Alt+Backspace must delete the previous word, got %q", got)
		}
	})

	// Word motion back (Ctrl+Left AND Alt+B), then type ⇒ inserts at the
	// start of the last word.
	t.Run("Ctrl+Left word-back motion", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "foo bar")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlLeft})
		a = typeRunes(t, a, "Q")
		if got := a.composeText(); got != "foo Qbar" {
			t.Errorf("Ctrl+Left must move one word back, got %q", got)
		}
	})
	t.Run("Alt+B word-back motion", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "foo bar")
		a = send(t, a, kr('b', true)) // alt+b
		a = typeRunes(t, a, "Q")
		if got := a.composeText(); got != "foo Qbar" {
			t.Errorf("Alt+B must move one word back, got %q", got)
		}
	})

	// Word motion forward (Ctrl+Right AND Alt+F): from line start, one
	// word forward then type.
	t.Run("Ctrl+Right word-forward motion", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "foo bar")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlA})     // line start
		a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlRight}) // → after "foo"
		a = typeRunes(t, a, "Q")
		if got := a.composeText(); got != "fooQ bar" {
			t.Errorf("Ctrl+Right must move one word forward, got %q", got)
		}
	})
	t.Run("Alt+F word-forward motion", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "foo bar")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlA})
		a = send(t, a, kr('f', true)) // alt+f
		a = typeRunes(t, a, "Q")
		if got := a.composeText(); got != "fooQ bar" {
			t.Errorf("Alt+F must move one word forward, got %q", got)
		}
	})

	// Left/Right char motion, then type at the moved cursor.
	t.Run("left/right char motion", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "ac")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyLeft}) // between a and c
		a = typeRunes(t, a, "b")
		if got := a.composeText(); got != "abc" {
			t.Errorf("Left then type must insert mid-line, got %q", got)
		}
		a = send(t, a, tea.KeyMsg{Type: tea.KeyRight}) // past c
		a = typeRunes(t, a, "d")
		if got := a.composeText(); got != "abcd" {
			t.Errorf("Right then type must insert after, got %q", got)
		}
	})

	// Home/End motion.
	t.Run("home/end motion", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "mid")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyHome})
		a = typeRunes(t, a, "[")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyEnd})
		a = typeRunes(t, a, "]")
		if got := a.composeText(); got != "[mid]" {
			t.Errorf("Home/End motion wrong, got %q", got)
		}
	})

	// Backspace + Delete (char back / char forward).
	t.Run("backspace and delete", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "abcd")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyBackspace}) // drop d
		if got := a.composeText(); got != "abc" {
			t.Errorf("Backspace must delete the char before the cursor, got %q", got)
		}
		a = send(t, a, tea.KeyMsg{Type: tea.KeyLeft}) // between b and c
		a = send(t, a, tea.KeyMsg{Type: tea.KeyDelete})
		if got := a.composeText(); got != "ab" {
			t.Errorf("Delete must delete the char at the cursor, got %q", got)
		}
	})

	// ⇧⏎ inserts a newline (multi-line buffer). ctrl+j is the alternate
	// spelling some terminals deliver.
	t.Run("Shift+Enter inserts a newline", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "line one")
		a = send(t, a, keyMsg("shift+enter"))
		a = typeRunes(t, a, "line two")
		if got := a.composeText(); got != "line one\nline two" {
			t.Errorf("⇧⏎ must insert a newline (multi-line), got %q", got)
		}
	})
	t.Run("Ctrl+J also inserts a newline", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "a")
		a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlJ})
		a = typeRunes(t, a, "b")
		if got := a.composeText(); got != "a\nb" {
			t.Errorf("Ctrl+J must insert a newline, got %q", got)
		}
	})
}

// ── (2) Enter still parses+emits; quit contract exact ─────────────────

// TestComposerEdit_EnterStillEmits proves Enter routes the textarea's
// full text through the EXISTING emit path (broadcast / @agent / /slash)
// and clears the buffer — unchanged by the textarea swap.
func TestComposerEdit_EnterStillEmits(t *testing.T) {
	// plain text → broadcast operator @thought, buffer clears.
	t.Run("plain text → broadcast", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "ship it now")
		m, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
		a = m.(App)
		if cmd == nil {
			t.Fatalf("a successful broadcast must return the post-write reload cmd")
		}
		if a.composeText() != "" {
			t.Errorf("Enter must clear the buffer on a successful send, got %q", a.composeText())
		}
		if !strings.HasPrefix(a.composeNote, "broadcast ✓") {
			t.Errorf("Enter on plain text must broadcast, note=%q", a.composeNote)
		}
	})

	// /slash routes to runSlash (unknown verb ⇒ clean in-pane ✗, buffer
	// PRESERVED — proves Enter still hits the slash parser on Value()).
	t.Run("/slash → runSlash", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "/nope do a thing")
		m, _ := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
		a = m.(App)
		if !strings.HasPrefix(a.composeNote, "✗") {
			t.Errorf("/nope must render a clean ✗ note, got %q", a.composeNote)
		}
		if a.composeText() != "/nope do a thing" {
			t.Errorf("a bad command must PRESERVE the buffer, got %q", a.composeText())
		}
	})

	// @agent routes to runDirected (no channel ⇒ summon written; note
	// surfaced) — proves the @-path on Value() is intact.
	t.Run("@agent → runDirected", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "@claude-code look at this")
		m, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
		a = m.(App)
		if cmd == nil {
			t.Fatalf("a successful @directed must return the post-write reload cmd")
		}
		if !strings.Contains(a.composeNote, "summoned") {
			t.Errorf("@agent with no channel must summon, note=%q", a.composeNote)
		}
		if a.composeText() != "" {
			t.Errorf("Enter must clear the buffer on a successful @directed, got %q", a.composeText())
		}
	})

	// Enter on a MULTI-LINE buffer (⇧⏎) emits the WHOLE text (not just
	// the cursor's line) — the textarea.Value() is the full buffer.
	t.Run("Enter emits the whole multi-line buffer", func(t *testing.T) {
		a := composeApp(t)
		a = typeRunes(t, a, "first")
		a = send(t, a, keyMsg("shift+enter"))
		a = typeRunes(t, a, "second")
		m, _ := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
		a = m.(App)
		// Broadcast content is the trimmed whole buffer (multi-line
		// preserved in the record; here we just assert it sent + cleared).
		if a.composeText() != "" {
			t.Errorf("Enter must clear a multi-line buffer too, got %q", a.composeText())
		}
		if !strings.HasPrefix(a.composeNote, "broadcast ✓") {
			t.Errorf("Enter on a multi-line buffer must still broadcast, note=%q", a.composeNote)
		}
	})
}

// TestComposerEdit_QuitContract proves the EXACT quit contract is
// unchanged by the textarea swap: ctrl+c ALWAYS quits (compose mode);
// `q` in compose is a LITERAL (typed into the textarea, does NOT quit);
// esc → nav, then `q` quits.
func TestComposerEdit_QuitContract(t *testing.T) {
	// ctrl+c quits in compose mode.
	a := composeApp(t)
	if _, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Errorf("ctrl+c must ALWAYS quit (compose mode)")
	}

	// `q` in compose is a literal rune — it must NOT quit and must land
	// in the textarea.
	a = composeApp(t)
	m, qcmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	a = m.(App)
	if qcmd != nil {
		if _, ok := qcmd().(tea.QuitMsg); ok {
			t.Errorf("`q` in COMPOSE mode must NOT quit (documented literal exception)")
		}
	}
	if a.composeText() != "q" {
		t.Errorf("`q` in compose must be a literal in the buffer, got %q", a.composeText())
	}

	// esc → nav; then `q` quits.
	a = composeApp(t)
	m, _ = a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a = m.(App)
	if a.composeMode {
		t.Errorf("esc must drop COMPOSE → NAV")
	}
	if _, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); cmd == nil {
		t.Errorf("`q` in NAV mode must quit")
	}

	// esc PRESERVES the buffer (focus switch, not discard).
	a = composeApp(t)
	a = typeRunes(t, a, "draft kept")
	m, _ = a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a = m.(App)
	if a.composeText() != "draft kept" {
		t.Errorf("esc must PRESERVE the buffer (focus switch), got %q", a.composeText())
	}
}

// ── (3) the v8 composer VISUAL + fixed ComposerHeight + ROUTING align ─

// TestComposerEdit_V8VisualPreserved proves the rendered composer still
// looks like the v8 composer (SGR-stripped 120x40): framed region, `›`
// prompt, the `@fleet` chip, the `N / 2000` counter, the hint row, the
// edited text + caret at the cursor — and that the chrome is the v8
// frame, NOT bubbles/textarea default chrome (no line numbers, no
// thick-border prompt).
func TestComposerEdit_V8VisualPreserved(t *testing.T) {
	a := composeApp(t)
	a = typeRunes(t, a, "downgrade approved")
	out := stripSGR(a.View())

	for _, want := range []string{
		"›",             // the v8 prompt
		"@fleet",        // the v8 target chip
		"⏎ send",        // the hint row
		"⇧⏎/⌃J newline", // the hint row (v1.0.6.3: surfaces Ctrl+J fallback for Terminal.app)
		"/ command",
		"@ target",
		" / 2000",            // the N / 2000 counter shape
		"downgrade approved", // the edited buffer text
		"▮",                  // the v8 blink-caret cell (frame-0 ON)
	} {
		if !strings.Contains(out, want) {
			t.Errorf("v8 composer visual missing %q:\n%s", want, out)
		}
	}
	// The honest-equivalent caret: at end-of-line the caret sits
	// immediately after the typed text (the v8 `text▮` shape).
	if !strings.Contains(out, "downgrade approved▮") {
		t.Errorf("the v8 blink-caret must sit at the cursor (end of typed text): %q", out)
	}
	// NOT bubbles/textarea default chrome: no line-number gutter, no
	// thick-border prompt glyph (the textarea's View() is never shown).
	if strings.Contains(out, "┃") {
		t.Errorf("bubbles/textarea thick-border prompt leaked into the v8 render:\n%s", out)
	}

	// The counter is the WHOLE-buffer rune count: "downgrade approved" is
	// 18 runes ⇒ "18 / 2000".
	if !strings.Contains(out, "18 / 2000") {
		t.Errorf("counter must be the honest whole-buffer rune count (18 / 2000): %q", out)
	}
}

// TestComposerEdit_FixedHeightAndRouting is the load-bearing structural
// proof: a real textarea (single line, multi-line, cursor mid-line) must
// NOT change the composer's rendered footprint — ComposerHeight stays
// fixed and the mesh rail's ROUTING hairline stays on the SAME screen
// row as the chat composer's top-rule (the cross-column alignment that
// depends on the fixed ComposerHeight). Compared against the EMPTY
// baseline.
func TestComposerEdit_FixedHeightAndRouting(t *testing.T) {
	styles.SetProfile(termenv.Ascii)

	// lastRule finds the LAST full-content-width hairline inside a panel
	// (chat panel ⇒ the composer top-rule; rail ⇒ the ROUTING rule).
	lastRule := func(t *testing.T, lines []string, p panelSpan) int {
		row := -1
		for ri := p.top + 1; ri < p.bot; ri++ {
			rr := []rune(lines[ri])
			if p.r >= len(rr) {
				continue
			}
			body := strings.TrimSpace(string(rr[p.l+1 : p.r]))
			if body != "" && strings.Trim(body, "─") == "" {
				row = ri
			}
		}
		return row
	}

	// rulesAligned renders an App, returns (totalLines, composerRuleRow,
	// routingRuleRow) so callers can assert the footprint + alignment.
	probe := func(t *testing.T, a App) (int, int, int) {
		t.Helper()
		out := a.View()
		lines, spans := detectPanels(t, out)
		if len(spans) != 2 {
			t.Fatalf("expected 2 substrate panels, got %d", len(spans))
		}
		chat, rail := spans[0], spans[1]
		cr := lastRule(t, lines, chat)
		rr := lastRule(t, lines, rail)
		if cr < 0 || rr < 0 {
			t.Fatalf("could not locate composer rule (%d) / routing rule (%d)", cr, rr)
		}
		return len(strings.Split(out, "\n")), cr, rr
	}

	// Baseline: empty composer.
	base := composeApp(t)
	bN, bC, bR := probe(t, base)
	if bC != bR {
		t.Fatalf("BASELINE BROKEN: ROUTING rule (row %d) must align with the "+
			"composer top-rule (row %d) on an empty buffer", bR, bC)
	}

	cases := []struct {
		name   string
		mutate func(App) App
	}{
		{"single line typed", func(a App) App { return typeRunes(t, a, "a single short line") }},
		{"very long line (clamp path)", func(a App) App {
			return typeRunes(t, a, strings.Repeat("verylongword ", 40))
		}},
		{"multi-line buffer (3 ⇧⏎ lines)", func(a App) App {
			a = typeRunes(t, a, "one")
			a = send(t, a, keyMsg("shift+enter"))
			a = typeRunes(t, a, "two")
			a = send(t, a, keyMsg("shift+enter"))
			a = typeRunes(t, a, "three")
			return a
		}},
		{"cursor moved mid-line", func(a App) App {
			a = typeRunes(t, a, "abcdef")
			a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlA}) // cursor to start
			return a
		}},
		{"cursor on an earlier logical line", func(a App) App {
			a = typeRunes(t, a, "top")
			a = send(t, a, keyMsg("shift+enter"))
			a = typeRunes(t, a, "bottom")
			a = send(t, a, tea.KeyMsg{Type: tea.KeyCtrlP}) // cursor up a line
			return a
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := c.mutate(composeApp(t))
			n, cr, rr := probe(t, a)
			if n != bN {
				t.Errorf("%s: total rendered line count changed %d → %d "+
					"(the composer grew/shrank — fixed-height contract broken)",
					c.name, bN, n)
			}
			if cr != bC {
				t.Errorf("%s: composer top-rule moved %d → %d (ComposerHeight "+
					"not fixed — would break the structural goldens)", c.name, bC, cr)
			}
			if cr != rr {
				t.Errorf("%s: ROUTING rule (row %d) must stay aligned with the "+
					"composer top-rule (row %d) across columns (jsx 327-336 — "+
					"depends on the fixed ComposerHeight)", c.name, rr, cr)
			}
		})
	}
}

// TestComposerEdit_NavRegressionIntact proves the NAV keymap + the
// read-only stack are untouched by the textarea swap (regression).
func TestComposerEdit_NavRegressionIntact(t *testing.T) {
	styles.SetProfile(termenv.Ascii)

	// 1-5 / tab / ? still work in nav mode (drive() drops to nav).
	for _, c := range []struct {
		key  string
		want AppView
	}{{"1", viewSubstrate}, {"2", viewFleet}, {"3", viewChannels}, {"4", viewGoals}, {"5", viewMemory}} {
		if got := drive(t, 120, 40, c.key); got.view != c.want {
			t.Errorf("nav key %s: view=%q want %q", c.key, got.view, c.want)
		}
	}
	if got := drive(t, 120, 40, "tab"); got.view != viewFleet {
		t.Errorf("nav `tab` must cycle to fleet, got %q", got.view)
	}
	if got := drive(t, 120, 40, "?"); got.overlay != overlayHelp {
		t.Errorf("nav `?` must open help, got overlay=%q", got.overlay)
	}
	// `i` returns nav → compose.
	if got := drive(t, 120, 40, "i"); !got.composeMode {
		t.Errorf("nav `i` must return to COMPOSE mode")
	}

	// The read-only v8 stack still renders (chat + mesh + routing + the
	// composer hint) intact.
	out := stripSGR(drive(t, 120, 40).View())
	for _, want := range []string{"◆ #substrate", "◆ MESH", "ROUTING", "⏎ send"} {
		if !strings.Contains(out, want) {
			t.Errorf("the read-only v8 frame must stay intact, missing %q:\n%s", want, out)
		}
	}
}
