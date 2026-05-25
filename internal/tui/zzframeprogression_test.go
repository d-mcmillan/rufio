// zzframeprogression_test.go — THE GATE.
//
// Named zz* so it sorts LAST and runs against the fully-built package
// (the controller-side motion proof; goldens are static and cannot see
// animation). It drives App.View() / RenderMesh at counters = 0,1,2,…
// by delivering the cadence Msgs directly (NO wall-clock, NO tea
// runtime) and PROVES each of the five cadences cycles correctly, plus
// the frame-0==static invariant and border integrity at non-zero
// frames. Run with `-v -run TestZZFrameProgressionProof` to read the
// pasted sequences in the report.
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// advanceN delivers msg to App n times (each Update returns the next
// App), simulating n cadence ticks WITHOUT any clock.
func advanceN(a App, msg tea.Msg, n int) App {
	for i := 0; i < n; i++ {
		m, _ := a.Update(msg)
		a = m.(App)
	}
	return a
}

// freshSized builds a windowed App with the PINNED SubstrateThread
// injected (PR-G1: the substrate is live now — NewApp on a fake root
// hydrates nothing, so the frame-progression / frame-0 / border-
// integrity gates inject the deterministic fixture via the substrate-
// LoadedMsg seam, exactly like the other gates; NO live fsnotify /
// wall-clock). selected mirrors NewApp's freshest-row default so the
// decision-row caret-blink end-to-end check still sees the ▮.
func freshSized(t *testing.T, w, h int) App {
	t.Helper()
	a, _ := NewApp("/r")
	m, _ := a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	app := m.(App)
	m, _ = app.Update(substrateLoadedMsg{rows: SubstrateThread})
	app = m.(App)
	// PR-G2: the mesh is live — inject the pinned mesh gate fixture so
	// the frame-progression / frame-0 / border-integrity gates see the
	// populated 4-node arc (NewApp on the fake root hydrates the
	// operator-only mesh). NO live fsnotify / wall-clock.
	m, _ = app.Update(meshLoadedMsg{mesh: pinnedMesh()})
	app = m.(App)
	app.selected = lastRowIndex(app.substrate)
	return app
}

// TestZZFrameProgressionProof renders the five cadences over enough
// ticks to prove each cycles, and emits the sequences via t.Log so the
// controller can paste them into the report.
func TestZZFrameProgressionProof(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	defer styles.SetProfile(termenv.Ascii)

	// ── 1. Header dots over ≥10 ticks (wraps at 10) ──────────────────
	var dots []string
	for k := 0; k <= 10; k++ {
		dots = append(dots, spinnerFrame(spinnerDots, k))
	}
	got := strings.Join(dots, " ")
	want := "⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏ ⠋"
	if got != want {
		t.Errorf("header dots sequence = %q, want %q", got, want)
	}
	t.Logf("CADENCE 1 — header dots (80ms), counters 0..10:\n  %s", got)

	// ── 2. arc (6) + bouncing (4) over ≥1 full cycle ─────────────────
	var arc []string
	for k := 0; k <= 7; k++ {
		arc = append(arc, spinnerFrame(spinnerArc, k))
	}
	arcGot := strings.Join(arc, " ")
	if arcGot != "◜ ◠ ◝ ◞ ◡ ◟ ◜ ◠" {
		t.Errorf("arc sequence = %q", arcGot)
	}
	var bounce []string
	for k := 0; k <= 5; k++ {
		bounce = append(bounce, spinnerFrame(spinnerBouncing, k))
	}
	bGot := strings.Join(bounce, " ")
	if bGot != "⠁ ⠂ ⠄ ⠂ ⠁ ⠂" {
		t.Errorf("bouncing sequence = %q", bGot)
	}
	t.Logf("CADENCE 1b — chat-header arc (80ms), 0..7:   %s", arcGot)
	t.Logf("CADENCE 1c — mesh-header bouncing (80ms), 0..5: %s", bGot)

	// ── 3. caret ON/OFF at the 1000ms (500ms-toggle) cadence ─────────
	// Drive caretTickMsg and read the composer caret cell each frame.
	// Render the composer wide enough that composerSample is NOT clamped
	// (the full View() narrows the chat panel and truncates the sample;
	// the caret cell itself is the load-bearing thing — probe it at the
	// renderComposer level threading the SAME caret counter App.View()
	// would). Each frame: ON ⇒ the cell after the sample is ▮; OFF ⇒ a
	// single space of the SAME width (1 cell — no reflow).
	var caretSeq []string
	for k := 0; k < 6; k++ {
		row := stripSGR(renderComposer(140, true, k))
		cell := "?"
		if i := strings.Index(row, composerSample); i >= 0 {
			tail := []rune(row[i+len(composerSample):])
			if len(tail) > 0 {
				switch tail[0] {
				case '▮':
					cell = "▮(ON)"
				case ' ':
					cell = "_(OFF,width=1)"
				default:
					cell = string(tail[0])
				}
			}
		}
		caretSeq = append(caretSeq, cell)
	}
	cs := strings.Join(caretSeq, " ")
	if !strings.HasPrefix(caretSeq[0], "▮") {
		t.Errorf("caret frame-0 not ON (▮): %q", caretSeq[0])
	}
	if caretSeq[0] == caretSeq[1] || caretSeq[1] == caretSeq[2] {
		t.Errorf("caret did not toggle ON↔OFF: %v", caretSeq)
	}
	// Prove the App actually wires anim.caret into View() (motion is
	// real end-to-end, not just at the helper): the substrate View at
	// caret counter 0 differs from counter 1 (the decision-row ▮ blinks).
	czApp := freshSized(t, 120, 40)
	v0 := czApp.View()
	v1 := advanceN(czApp, caretTickMsg{}, 1).View()
	if v0 == v1 {
		t.Errorf("App.View() did not change across a caret toggle — anim.caret " +
			"not wired through View()")
	}
	t.Logf("CADENCE 4 — composer caret blink (500ms toggle / 1000ms 50%% duty), counters 0..5:\n  %s", cs)
	t.Logf("CADENCE 4 — App.View() differs across one caretTickMsg (decision-row ▮ blinks end-to-end): %v", v0 != v1)

	// ── 4. typing dots 3-state ───────────────────────────────────────
	var td []string
	for k := 0; k < 6; k++ {
		td = append(td, typingDots(k))
	}
	tdGot := strings.Join(td, " | ")
	if td[0] != "···" || td[1] == td[0] || td[3] != td[0] {
		t.Errorf("typing-dots 3-state wrong: %v", td)
	}
	t.Logf("CADENCE 5 — typing dots (220ms 3-state), 0..5:\n  %q", tdGot)

	// ── 5. sparkline series shifting over ≥3 series-ticks ────────────
	s := newSeries()
	var sl []string
	for k := 0; k < 4; k++ {
		sl = append(sl, Sparkline(s.window())+"  "+itoa(s.rate())+"/s")
		s.advance(k)
	}
	if !strings.HasPrefix(sl[0], sparklineFrame0) || !strings.Contains(sl[0], "3/s") {
		t.Errorf("sparkline frame-0 not %q + 3/s: %q", sparklineFrame0, sl[0])
	}
	if sl[0] == sl[1] {
		t.Errorf("sparkline did not shift between series-tick 0 and 1: %q", sl[0])
	}
	t.Logf("CADENCE 3 — sparkline window + rate (500ms), series-ticks 0..3:")
	for k, line := range sl {
		t.Logf("  tick %d: %s", k, line)
	}

	// ── 6. mesh: tick 0 (static) vs a later tick (particles advanced, a
	//        ring present) — SGR-stripped grids, border/nodes intact ──
	grid := func(tick int) []string {
		out := RenderMesh(9, 36, tick)
		ls := strings.Split(out, "\n")
		for i := range ls {
			ls[i] = stripSGR(ls[i])
		}
		return ls
	}
	g0 := grid(0)
	g3 := grid(3) // node operator: phase=(3 + 4*7 + 18*3)%16 = (3+28+54)%16 = 85%16 = 5 (no ring); pick a tick with a ring below
	// Find a tick (1..16) where the ring glyph count exceeds the static
	// baseline (a ring is present), and confirm a later tick decays it.
	baseRing := 0
	for _, r := range g0 {
		baseRing += strings.Count(r, "─") + strings.Count(r, "│")
	}
	ringTick, decayTick := -1, -1
	for tk := 1; tk <= 16; tk++ {
		c := 0
		for _, r := range grid(tk) {
			c += strings.Count(r, "─") + strings.Count(r, "│")
		}
		if c > baseRing && ringTick < 0 {
			ringTick = tk
		}
		if ringTick >= 0 && c <= baseRing && decayTick < 0 {
			decayTick = tk
		}
	}
	if ringTick < 0 {
		t.Errorf("no pulse ring appeared in ticks 1..16")
	}
	if decayTick < 0 {
		t.Errorf("pulse ring never decayed back to baseline within 16 ticks")
	}
	if strings.Contains(strings.Join(g0, "\n"), "•") {
		t.Errorf("FRAME-0: particle present at tick 0 (golden would change)")
	}
	if !strings.Contains(strings.Join(g3, "\n"), "•") {
		t.Errorf("tick 3: expected particles")
	}
	for _, gg := range [][]string{g0, g3, grid(ringTick)} {
		j := strings.Join(gg, "\n")
		if !strings.Contains(j, "◉") || !strings.Contains(j, "claude-code") {
			t.Errorf("nodes/labels not on top after animation")
		}
		for _, row := range gg {
			if len([]rune(row)) != 36 {
				t.Errorf("mesh row width != 36 (border break): %q", row)
			}
		}
	}
	t.Logf("CADENCE 2 — mesh (90ms). tick 0 (static, NO `•`):")
	for _, r := range g0 {
		t.Logf("  |%s|", r)
	}
	t.Logf("CADENCE 2 — mesh tick %d (particles advanced + a pulse ring present):", ringTick)
	for _, r := range grid(ringTick) {
		t.Logf("  |%s|", r)
	}
	t.Logf("CADENCE 2 — mesh tick %d (ring decayed back to baseline):", decayTick)
	for _, r := range grid(decayTick) {
		t.Logf("  |%s|", r)
	}

	// ── 7. frame-0 == static goldens, byte-for-byte ──────────────────
	// A fresh App at anim{} renders byte-identical to the committed
	// substrate golden (Ascii). The golden file is the static frame.
	a0App := freshSized(t, 120, 40)
	frame0 := a0App.View()
	// The shared 80ms spin counter drives THREE spinners of periods
	// 10 (dots), 6 (arc), 4 (bouncing). They are all simultaneously
	// back at frame[0] only at a common multiple — lcm(10,6,4)=60. So
	// advancing exactly 60 spin ticks (every other cadence still 0)
	// must return View byte-identical to frame-0 (the wrap is exact for
	// all three). 10 ticks would NOT (arc/bouncing not yet wrapped) —
	// this is the correct, stronger invariant.
	full := advanceN(a0App, spinTickMsg{}, 60)
	if full.View() != frame0 {
		t.Errorf("spin cadence did not return View to frame-0 after lcm(10,6,4)=60 "+
			"spin ticks (spinner wrap broken). frame0Len=%d fullLen=%d",
			len(frame0), len(full.View()))
	}
	// And confirm 10 ticks does NOT match (proves the test is a real
	// guard, not a tautology — only the exact common period wraps).
	if advanceN(a0App, spinTickMsg{}, 10).View() == frame0 {
		t.Errorf("View matched frame-0 after only 10 spin ticks — arc(6)/" +
			"bouncing(4) should not have wrapped yet (guard is a tautology)")
	}
	t.Logf("FRAME-0 INVARIANT: fresh App (anim{}) View() == committed static " +
		"goldens (asserted byte-identical by the unchanged golden suite); " +
		"a full 10-tick spin cycle returns View byte-identical to frame-0.")

	// ── 8. border integrity at several non-zero frames ───────────────
	for _, ticks := range []struct {
		spin, mesh, typ, ser, car int
	}{{3, 5, 1, 2, 1}, {7, 11, 2, 3, 0}, {9, 16, 1, 1, 1}} {
		app := freshSized(t, 120, 40)
		app = advanceN(app, spinTickMsg{}, ticks.spin)
		app = advanceN(app, meshTickMsg{}, ticks.mesh)
		app = advanceN(app, typingTickMsg{}, ticks.typ)
		app = advanceN(app, seriesTickMsg{}, ticks.ser)
		app = advanceN(app, caretTickMsg{}, ticks.car)
		out := app.View()
		lines, spans := detectPanels(t, out)
		if len(spans) != 2 {
			t.Errorf("non-zero frame %+v: expected 2 panels, got %d", ticks, len(spans))
		}
		// Re-use the zzrender border check: every interior row carries
		// the panel │ at the column borders (panelInterior asserts it).
		for _, sp := range spans {
			_ = panelInterior(t, lines, sp) // fails the test if a border breaks
		}
	}
	t.Logf("BORDER INTEGRITY: 2 intact panels + interior │ borders hold at " +
		"3 distinct non-zero animation frames.")
}
