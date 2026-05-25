// zzrender_test.go — the PR-E structural RENDER-GATE.
//
// Named to sort LAST in the package so it runs against the fully-built
// package (the controller-side guard, not a golden). v8 goldens are
// REGRESSION-ONLY per the locked plan and explicitly NOT the fidelity
// gate, so the exact bug PR-E exists to fix — the substrate chat panel
// collapsing to one undivided block / the composer floating instead of
// being a framed region BELOW its top-rule — would otherwise have no
// real guard. This test re-derives the 3-section geometry from the
// rendered App.View() and FAILS if the composition regresses.
//
// It deliberately re-parses the rendered screen structurally (panel
// columns → interior rows → the TWO full-inner-width hairline rules →
// section membership) rather than sniffing for substrings anywhere on
// the screen: a single-undivided-block render (0 or 1 interior hairline,
// or composer text not strictly below the lower rule) makes this RED.
// See the red→green proof in the PR-E review reply.
//
// Ascii profile only (deterministic — color escapes stripped, so a
// hairline row's border-stripped interior is exactly "─"×innerWidth).
package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// chatPanelHairlineRows returns the indices (into `interior`) of every
// row that is a full-CONTENT-width interior hairline rule. `interior` is
// the chat panel's border-stripped rows, which still carry the panel's
// 2-cell interior h-pad (chatPanelHPad) on each side — so a section rule
// row is `"  " + "─"×contentWidth + "  "`. After trimming the h-pad
// spaces the remainder must be non-empty, consist SOLELY of the Line
// hairline glyph `─`, and span the full content width
// (innerWidth − 2·chatPanelHPad). Requiring the full content width
// rejects any incidental short dash run in real content, so this only
// matches the chrome strip's bottom-rule and the composer's top-rule.
// Under the Ascii profile color escapes are stripped, so these are the
// only such rows. The mesh rail's own hairline is excluded by
// construction because `interior` is the CHAT panel's column slice.
func chatPanelHairlineRows(interior []string, innerWidth int) []int {
	contentWidth := innerWidth - 2*chatPanelHPad
	var rows []int
	for i, body := range interior {
		trimmed := strings.TrimSpace(body)
		if trimmed == "" {
			continue
		}
		if strings.Trim(trimmed, "─") != "" {
			continue // contains non-hairline glyphs — not a rule row
		}
		if len([]rune(trimmed)) == contentWidth {
			rows = append(rows, i)
		}
	}
	return rows
}

// renderSubstrate drives a fresh App to (w,h) on the substrate tab under
// the Ascii profile and returns View(). Mirrors app_test.go's drive().
func renderSubstrate(t *testing.T, w, h int) string {
	t.Helper()
	return renderSubstrateProfile(t, termenv.Ascii, w, h)
}

// renderSubstrateProfile is renderSubstrate under an explicit termenv
// profile (the screen-bg fidelity fix only manifests under TrueColor —
// the Ascii profile cannot represent a background color, which is
// exactly why the Ascii self-checks missed it).
//
// PR-G1: injects the PINNED SubstrateThread (the deterministic gate
// fixture) via a substrateLoadedMsg so the structural render-gates see
// the populated thread, not the empty cold-start (NewApp on the fake
// root hydrates nothing). NO live fsnotify / wall-clock — the
// determinism contract.
func renderSubstrateProfile(t *testing.T, p termenv.Profile, w, h int) string {
	t.Helper()
	styles.SetProfile(p)
	a, err := NewApp("/tmp/fake-root")
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	m, _ := a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	app := m.(App)
	m, _ = app.Update(substrateLoadedMsg{rows: SubstrateThread})
	app = m.(App)
	// PR-G2: the mesh is live too — inject the pinned mesh gate fixture
	// (the same 4-node arc the fixture pinned) so the structural
	// render-gates see the populated mesh, not the operator-only
	// cold-start. NO live fsnotify / wall-clock — the determinism
	// contract, extended to the mesh.
	m, _ = app.Update(meshLoadedMsg{mesh: pinnedMesh()})
	app = m.(App)
	app.selected = lastRowIndex(app.substrate)
	return app.View()
}

// TrueColor 24-bit SGR fragments for the v8 fidelity contract. These are
// the EXACT escapes lipgloss/termenv emit for the palette tokens under
// termenv.TrueColor (empirically verified). screen/panel bg: no
// off-by-one — 0x13/0x11/0x1c = 19/17/28, 0x1a/0x17/0x26 = 26/23/38,
// Accent 0xa7/0x8b/0xfa = 167/139/250. Ring #4a4470 = rgb(74,68,112)
// round-trips with the documented termenv off-by-one to 73;68;112 (R
// 74→73) — empirically verified.
const (
	sgrScreenBg = "48;2;19;17;28"      // Bg     #13111c (forced screen paint — REMOVED PR-E.1)
	sgrPanelBg  = "48;2;26;23;38"      // Panel  #1a1726 (forced panel fill — REMOVED PR-E.1)
	sgrMarkerFg = "1;38;2;167;139;250" // ▸ marker: bold + Accent FG, NO bg
	sgrRingFg   = "38;2;73;68;112"     // Ring   #4a4470 (panel border + section rules, PR-E.1)
)

// TestZZNoForcedBackgroundTrueColor is the PR-E.1 INVERTED fidelity
// gate (it supersedes the deleted TestZZScreenBackgroundPaintedTrueColor,
// which asserted the now-removed forced two-tone bg). Ascii cannot
// represent a bg color, so the forced-bg removal only manifests under
// TrueColor — exactly why this gate renders App.View() under
// termenv.TrueColor at 120×40 AND 200×55 and asserts:
//
//	(no screen bg)  — the forced screen Bg #13111c (sgrScreenBg
//	                   "48;2;19;17;28") appears NOWHERE in the render.
//	(no panel bg)   — the forced Panel bg #1a1726 (sgrPanelBg
//	                   "48;2;26;23;38") appears NOWHERE in the render.
//	(visible border)— the panel border carries the Ring foreground SGR
//	                   (sgrRingFg "38;2;73;68;112", the documented
//	                   termenv off-by-one round-trip of #4a4470) so the
//	                   structure reads on the native terminal bg.
//	(marker clean)  — the selected row's ▸ marker stays a clean
//	                   FOREGROUND glyph with NO background SGR (carried
//	                   over from the old Fix-3 — now trivially true with
//	                   no forced bg anywhere, but kept as a guard).
//
// REGRESSION SEMANTICS — this is a genuine guard, NOT a tautology:
// re-adding `.Background(styles.Palette.Bg)` to View() OR
// `.Background(Palette.Panel)` to styles.Panel makes the corresponding
// sgr* string reappear → RED (proven in the PR-E.1 reply). Removing it
// → GREEN. Dropping the Ring BorderForeground (border invisible again)
// → the sgrRingFg assertion goes RED. Also dumps the rendered screens
// to /tmp for the controller's grep proof.
func TestZZNoForcedBackgroundTrueColor(t *testing.T) {
	defer styles.SetProfile(termenv.Ascii)

	for _, sz := range [][2]int{{120, 40}, {200, 55}} {
		w, h := sz[0], sz[1]
		out := renderSubstrateProfile(t, termenv.TrueColor, w, h)
		_ = os.WriteFile(
			"/tmp/v8-tc-nobg-"+itoa(w)+"x"+itoa(h)+".txt", []byte(out), 0o644)

		// (no screen bg) — the forced screen Bg #13111c must not appear
		// ANYWHERE: not on header/footer, not in a gutter, not in the
		// inter-panel gap, not on any void row. The terminal's native
		// bg shows through instead (theme-portable, not patchy).
		if strings.Contains(out, sgrScreenBg) {
			t.Errorf("%dx%d: forced screen-bg SGR %q (#13111c) STILL present — "+
				"the PR-E.1 native-bg removal regressed (a Background() wrapper "+
				"is back in View())", w, h, sgrScreenBg)
		}
		// (no panel bg) — the forced Panel bg #1a1726 must not appear
		// ANYWHERE either: the panels no longer fill a bg block.
		if strings.Contains(out, sgrPanelBg) {
			t.Errorf("%dx%d: forced panel-bg SGR %q (#1a1726) STILL present — "+
				"styles.Panel.Background() regressed (panels must use the "+
				"native terminal bg)", w, h, sgrPanelBg)
		}

		// (visible border) — the panel border must carry the Ring
		// foreground SGR so the two bordered panels read on the native
		// bg (the structure is now carried by the border, not bg
		// contrast). detectPanels confirms both panels' ╭╮│╰╯ exist;
		// the Ring fg SGR must be on the rendered screen.
		ascii := renderSubstrate(t, w, h)
		_, spans := detectPanels(t, ascii)
		if len(spans) != 2 {
			t.Fatalf("%dx%d: expected 2 substrate panels, got %d", w, h, len(spans))
		}
		if !strings.Contains(out, "\x1b["+sgrRingFg+"m") {
			t.Errorf("%dx%d: panel border / section rules missing the Ring fg "+
				"SGR %q (#4a4470) — the border is invisible on the native bg "+
				"(BorderForeground/SectionRule regressed)", w, h, sgrRingFg)
		}
		// Stronger: the panel TOP-border row (the ╭…╮ rule) must itself
		// carry the Ring fg SGR — proving the border glyphs are
		// Ring-colored, not just some Ring text elsewhere.
		alines := strings.Split(out, "\n")
		topBorderRow := ""
		for _, ln := range alines {
			if strings.ContainsRune(stripSGR(ln), '╭') {
				topBorderRow = ln
				break
			}
		}
		if topBorderRow == "" {
			t.Fatalf("%dx%d: no ╭ top-border row in TrueColor render", w, h)
		}
		if !strings.Contains(topBorderRow, "\x1b["+sgrRingFg+"m") {
			t.Errorf("%dx%d: panel top-border row not Ring-colored (%q): %q",
				w, h, sgrRingFg, topBorderRow)
		}
	}

	// (marker clean) — the selected row's ▸ marker stays a clean
	// FOREGROUND glyph with NO background SGR. Default selection is the
	// last (decision) row. (Carried from the old Fix-3 — now trivially
	// true with no forced bg, kept as a standing guard.)
	out := renderSubstrateProfile(t, termenv.TrueColor, 120, 40)
	if !strings.Contains(out, "▸") {
		t.Fatalf("selection marker ▸ not present in truecolor render")
	}
	if !strings.Contains(out, "\x1b["+sgrMarkerFg+"m▸") {
		t.Errorf("▸ marker not rendered as clean fg SGR %q (selection marker "+
			"styling regressed)", sgrMarkerFg)
	}
	for _, seg := range strings.Split(out, "▸") {
		if i := strings.LastIndex(seg, "\x1b["); i >= 0 {
			tail := seg[i:]
			if j := strings.IndexByte(tail, 'm'); j >= 0 {
				sgr := tail[:j]
				if strings.Contains(sgr, "48;2;") {
					t.Errorf("▸ marker preceded by a background SGR %q — a "+
						"bg-filled-block regressed", sgr)
				}
			}
		}
	}
}

// stripSGR removes all CSI SGR escape sequences (ESC[…m) so a row can be
// tested for "is this visually blank / what glyphs are here" independent
// of color. Minimal hand-roll (no regexp dep); good enough for the
// deterministic test renders.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// TestZZSubstrateThreeSectionChatPanel is the PR-E structural
// render-gate (the Major). For the substrate screen at a realistic size
// AND a wide size it independently re-derives the chat-panel geometry
// from the rendered View() and asserts the 3-section structure:
//
//	chrome strip  (◆ #substrate)        ── ABOVE the upper hairline
//	── full-inner-width hairline ──        (chrome strip bottom border)
//	thread / messages                   ── strictly BETWEEN the rules
//	── full-inner-width hairline ──        (composer top-rule)
//	composer (› prompt + ⏎ send hint)   ── strictly BELOW the lower rule
//
// REGRESSION SEMANTICS (this is the whole point — see the red→green
// proof in the PR-E reply): if renderChatPanel ever collapses to a
// single undivided block, there are 0 or 1 interior hairlines (not
// exactly 2) → the `len(rules) != 2` assertion FAILS. If the composer
// is rendered as floating text instead of a framed region below its
// top-rule, the `›`/`⏎ send` tokens are NOT strictly below the lower
// rule → those assertions FAIL. A substring sniff anywhere on screen
// could not distinguish these; structural section membership does.
func TestZZSubstrateThreeSectionChatPanel(t *testing.T) {
	for _, sz := range [][2]int{{120, 40}, {200, 55}} {
		w, h := sz[0], sz[1]
		out := renderSubstrate(t, w, h)
		lines, spans := detectPanels(t, out)
		if len(spans) != 2 {
			t.Fatalf("%dx%d: substrate must be TWO panels (chat panel + mesh rail), got %d",
				w, h, len(spans))
		}

		chat := spans[0] // the flexible left bordered panel
		interior := panelInterior(t, lines, chat)
		innerWidth := chat.r - chat.l - 1

		// (2) the TWO full-inner-width interior hairline rules.
		rules := chatPanelHairlineRows(interior, innerWidth)
		if len(rules) != 2 {
			t.Fatalf("%dx%d: chat panel must have EXACTLY 2 full-width interior "+
				"hairlines (chrome bottom-rule + composer top-rule); found %d. "+
				"0 or 1 ⇒ the panel collapsed to a single undivided block — the "+
				"exact PR-E regression this gate guards. interior:\n%s",
				w, h, len(rules), strings.Join(interior, "\n"))
		}
		upper, lower := rules[0], rules[1]
		if !(upper < lower) {
			t.Fatalf("%dx%d: hairline ordering broken (upper=%d lower=%d)",
				w, h, upper, lower)
		}

		joinRange := func(lo, hi int) string { // [lo,hi) interior rows
			if lo < 0 {
				lo = 0
			}
			if hi > len(interior) {
				hi = len(interior)
			}
			if lo >= hi {
				return ""
			}
			return strings.Join(interior[lo:hi], "\n")
		}
		chrome := joinRange(0, upper)                 // ABOVE the upper rule
		messages := joinRange(upper+1, lower)         // BETWEEN the rules
		composer := joinRange(lower+1, len(interior)) // BELOW the lower rule

		// (3a) chrome strip marker ABOVE the upper hairline.
		if !strings.Contains(chrome, "◆ #substrate") {
			t.Errorf("%dx%d: chrome strip marker `◆ #substrate` not ABOVE the "+
				"upper hairline. chrome section:\n%s", w, h, chrome)
		}
		// It must NOT appear elsewhere (would mean the rule is below, not
		// above, the chrome — i.e. wrong section ordering).
		if strings.Contains(messages, "◆ #substrate") || strings.Contains(composer, "◆ #substrate") {
			t.Errorf("%dx%d: `◆ #substrate` leaked below the upper hairline — "+
				"section ordering regressed", w, h)
		}

		// (3b) thread/messages content strictly BETWEEN the two rules.
		// The operator row is the first thread row; OPERATOR is its
		// uppercased role tag (chat.go). Use it as the messages-section
		// witness — it must be between, not in chrome or composer.
		if !strings.Contains(messages, "OPERATOR") {
			t.Errorf("%dx%d: thread content (OPERATOR row) not strictly "+
				"BETWEEN the two hairlines. messages section:\n%s", w, h, messages)
		}
		if strings.Contains(chrome, "OPERATOR") || strings.Contains(composer, "OPERATOR") {
			t.Errorf("%dx%d: thread content leaked into chrome/composer — "+
				"messages section not bounded by the two rules", w, h)
		}

		// (3c) composer content strictly BELOW the lower hairline AND
		// inside the panel border: the `›` prompt AND the hint-row token
		// `⏎ send` (jsx composer + hint row). Floating-text regression =
		// these NOT below the lower rule.
		if !strings.Contains(composer, "›") {
			t.Errorf("%dx%d: composer `›` prompt not strictly BELOW the "+
				"composer top-rule (floating-composer regression). composer "+
				"section:\n%s", w, h, composer)
		}
		if !strings.Contains(composer, "⏎ send") {
			t.Errorf("%dx%d: composer hint token `⏎ send` not strictly BELOW "+
				"the composer top-rule. composer section:\n%s", w, h, composer)
		}
		// And the composer must be INSIDE the panel — panelInterior
		// already asserted every interior row carries the `│` border at
		// chat.l/chat.r, so a non-empty composer slice here is, by
		// construction, inside the chat panel border.
		if strings.TrimSpace(composer) == "" {
			t.Errorf("%dx%d: composer section empty — composer not rendered "+
				"as a framed region below its top-rule", w, h)
		}
	}
}
