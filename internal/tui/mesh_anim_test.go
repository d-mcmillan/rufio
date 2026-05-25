// mesh_anim_test.go — deterministic frame-progression tests for the
// PR-F animated mesh: the FRAME-0 invariant (tick 0 == the pre-PR-F
// static port, byte-identical, NO particles/rings), particle flow
// advancing along edges, node pulse rings appearing then decaying, and
// the draw-order / stable-width contract.
package tui

import (
	"strings"
	"testing"

	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// meshGridGlyphs returns the SGR-stripped rows×cols glyph grid for a
// given tick (one rune per cell — RenderMesh emits exactly cols cells
// per row).
func meshGridGlyphs(t *testing.T, tick int) []string {
	t.Helper()
	styles.SetProfile(termenv.Ascii)
	out := RenderMesh(9, 36, tick)
	lines := strings.Split(out, "\n")
	for i, ln := range lines {
		lines[i] = stripSGR(ln)
	}
	return lines
}

// TestMeshFrame0IsStaticPort is the load-bearing mesh frame-0 invariant:
// at tick 0 RenderMesh MUST be byte-identical to the static (no-tick)
// port — i.e. exactly the edges/nodes/labels with ZERO particles and
// ZERO pulse rings. The committed mesh goldens render at tick 0, so any
// particle/ring at tick 0 would change a golden. (Deliberate documented
// deviation from the jsx, which animates from tick 0; tick 0 is our
// static frame.)
func TestMeshFrame0IsStaticPort(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	got := RenderMesh(9, 36, 0)
	want := renderMeshStatic(9, 36) // edges+nodes+labels only, no anim
	if got != want {
		t.Errorf("FRAME-0 BROKEN: RenderMesh(tick=0) != static port.\n"+
			"--- static ---\n%s\n--- tick0 ---\n%s", want, got)
	}
	// And concretely: no particle glyph anywhere at tick 0.
	if strings.Contains(stripSGR(got), "•") {
		t.Errorf("FRAME-0 BROKEN: a `•` particle is present at tick 0 " +
			"(a mesh golden would change)")
	}
}

// TestMeshParticlesAdvance proves the 90ms particle cadence: `•`
// particles appear once ticking starts and move along edges between
// ticks (different cells over successive ticks). Verbatim phase port of
// rufio-graphs.jsx 67-76.
func TestMeshParticlesAdvance(t *testing.T) {
	// A particle is present at some tick>0.
	g1 := meshGridGlyphs(t, 1)
	joined1 := strings.Join(g1, "\n")
	if !strings.Contains(joined1, "•") {
		t.Fatalf("tick 1: expected ≥1 `•` particle on the edges, got none:\n%s", joined1)
	}

	// Particle positions differ across ticks (flow). Collect the set of
	// (r,c) cells holding a `•` at tick 1 vs a later tick; they must not
	// be identical (the particles advanced).
	pos := func(g []string) map[[2]int]bool {
		m := map[[2]int]bool{}
		for r, row := range g {
			for c, ch := range []rune(row) {
				if ch == '•' {
					m[[2]int{r, c}] = true
				}
			}
		}
		return m
	}
	p1 := pos(g1)
	p7 := pos(meshGridGlyphs(t, 7))
	if len(p7) == 0 {
		t.Fatalf("tick 7: expected particles, got none")
	}
	same := len(p1) == len(p7)
	if same {
		for k := range p1 {
			if !p7[k] {
				same = false
				break
			}
		}
	}
	if same {
		t.Errorf("particles did NOT advance between tick 1 and tick 7 (no flow): %v", p1)
	}

	// Determinism: same tick ⇒ same grid, always.
	if a, b := RenderMesh(9, 36, 5), RenderMesh(9, 36, 5); a != b {
		t.Errorf("RenderMesh non-deterministic at a fixed tick")
	}
}

// TestMeshPulseRingAppearsThenDecays proves the node-pulse-ring cadence
// (rufio-graphs.jsx 78-97): a non-dim node emits a ring (4 cells one
// step out, growing) when its phase < 4 of a 16-tick cycle, then the
// ring is gone (decayed) outside that window. We sweep ticks and assert
// the count of ring glyphs (`─`/`│` drawn in the Ring tone, i.e. cells
// that are ring-only) both rises above the static baseline and returns
// to it within one 16-tick cycle.
func TestMeshPulseRingAppearsThenDecays(t *testing.T) {
	base := meshGridGlyphs(t, 0) // static — no rings
	baseCount := 0
	for _, row := range base {
		baseCount += strings.Count(row, "─") + strings.Count(row, "│")
	}

	maxC, minC := baseCount, 1<<30
	for tk := 1; tk <= 16; tk++ {
		g := meshGridGlyphs(t, tk)
		c := 0
		for _, row := range g {
			c += strings.Count(row, "─") + strings.Count(row, "│")
		}
		if c > maxC {
			maxC = c
		}
		if c < minC {
			minC = c
		}
	}
	if maxC <= baseCount {
		t.Errorf("pulse rings never raised the ─/│ glyph count above the "+
			"static baseline %d (max seen %d) — rings not appearing", baseCount, maxC)
	}
	// Within one 16-tick cycle the ring count must come back down toward
	// the baseline at least once (the decay) — not stay permanently high.
	if minC > baseCount {
		t.Errorf("ring glyph count never returned toward baseline %d within a "+
			"16-tick cycle (min seen %d) — rings not decaying", baseCount, minC)
	}
}

// TestMeshNodesAndLabelsDrawnOnTop proves the jsx draw order survives
// the animation: nodes + labels are still present at an animated tick
// (particles/rings must not paint over a node — nodes are drawn last).
func TestMeshNodesAndLabelsDrawnOnTop(t *testing.T) {
	for _, tk := range []int{0, 3, 9, 14} {
		joined := strings.Join(meshGridGlyphs(t, tk), "\n")
		if !strings.Contains(joined, "◉") {
			t.Errorf("tick %d: operator hub glyph ◉ missing — a particle/ring "+
				"painted over a node (draw order broken)", tk)
		}
		if !strings.Contains(joined, "claude-code") {
			t.Errorf("tick %d: node label `claude-code` missing", tk)
		}
	}
}

// TestMeshStableWidthEveryFrame is the border-integrity contract: every
// rendered row at every tick is EXACTLY cols cells wide (SGR-stripped),
// so the mesh never reflows and the rail border stays intact at any
// animation frame.
func TestMeshStableWidthEveryFrame(t *testing.T) {
	for tk := 0; tk < 40; tk++ {
		for r, row := range meshGridGlyphs(t, tk) {
			if w := len([]rune(row)); w != 36 {
				t.Fatalf("tick %d row %d width = %d, want 36 (reflow → border break)",
					tk, r, w)
			}
		}
	}
}
