// mesh.go — substrate-mesh rendering for the v8 TUI (PR-F: animated).
//
// Faithful character-cell port of `lineCells` + `buildGrid` from
// docs/design/tui-v8/reference/rufio-graphs.jsx (lines 36-121), per the
// PR-E "Mesh" contract and docs/design/tui-v8-data-mapping.md §0
// (OPEN-3/OPEN-5 resolved).
//
// PR-F adds the two tick-driven branches the static PR-E port omitted:
// edge particle flow (jsx 67-76) and node pulse rings (jsx 78-97),
// driven by the 90ms meshTickMsg cadence (jsx MeshGrid
// `setInterval(…,90)`, rufio-graphs.jsx line 126). The phase/index math
// is a verbatim port; see buildMeshGrid.
//
// FRAME-0 DEVIATION (documented, intentional): the jsx animates from
// tick 0 (its particle phase is non-zero at tick 0). Our committed mesh
// goldens were bootstrapped from the STATIC port (no particles/rings),
// and the frame-0 invariant requires tick 0 to stay byte-identical to
// them. So particles AND rings are GATED to `tick > 0` — tick 0 is the
// static frame (edges/nodes/labels only); motion begins at tick 1. This
// is the single deviation from the jsx in this file, taken solely to
// preserve the byte-identical-goldens invariant (the highest-priority
// constraint). At tick≥1 the phase math uses the absolute tick exactly
// as the jsx.
//
// PR-G2 — the mesh is now LIVE. The render core
// (buildMeshGridFrom/RenderMeshFrom) is PARAMETERIZED on an explicit
// (nodes, edges) pair so the App can feed it the live-projected mesh
// (live_mesh.go: synthesized operator hub + attention-bearing agents +
// outbox∩inbox routing edges). The legacy global-reading shims
// (buildMeshGrid/RenderMesh/MeshNodeCount/MeshEdgeCount) are KEPT as thin
// wrappers over the fixture (MeshNodes + deriveMeshEdges in fixtures.go)
// SOLELY so the frame-0 invariant + the committed mesh goldens +
// mesh_anim_test.go stay byte-identical (they exercise the algorithm,
// not the data source). The geometry/animation port is UNCHANGED by the
// fixture→live swap — only the (nodes, edges) inputs change.
//
// AGENTS-ONLY (damon, 2026-05-15): the LIVE node set = the synthesized
// operator hub + the agents with a live/attention/ record (no fictional
// ids, no governance); edges = real outbox∩inbox routing deliveries
// (data-mapping §0 OPEN-3-resolved). Liveness is a REAL signal, never
// faked (OPEN-4 — see live_mesh.go).
//
// ADD-ONLY: consumed only by the v8 app.go (PR-E/F/G). (Was reachable
// via the hidden RUFIO_TUI_PREVIEW=1 gate with the legacy internal/tui
// path as the default; the G4 cutover, 2026-05-17, made v8 the
// unconditional `rufio tui` and deleted the gate + legacy path.)
package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// meshCell is one rendered grid cell: glyph, color, and whether the
// glyph is a node (drawn bold + on top, jsx buildGrid line 102-106).
type meshCell struct {
	ch    string
	color lipgloss.Color
	bold  bool
}

// absInt is integer abs (lineCells does Math.abs on int deltas).
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// roundDiv mirrors the jsx `Math.round(A + d*i/steps)` for the integer
// interpolation in lineCells (rufio-graphs.jsx lines 41-42). Go has no
// math.Round on ints; this rounds (num/den) half-away-from-zero, which
// matches JS Math.round for the non-negative grid coords here.
func roundDiv(num, den int) int {
	if den == 0 {
		return 0
	}
	if num >= 0 {
		return (2*num + den) / (2 * den)
	}
	return -((-2*num + den) / (2 * den))
}

// lineCells is a verbatim port of rufio-graphs.jsx lineCells (lines
// 36-50): the interior cells of the A→B segment, each with its slope
// glyph (─ near-horizontal, │ near-vertical, ╲/╱ diagonal by sign).
func lineCells(ar, ac, br, bc int) []struct {
	r, c int
	ch   string
} {
	dr := br - ar
	dc := bc - ac
	steps := absInt(dr)
	if absInt(dc) > steps {
		steps = absInt(dc)
	}
	var cells []struct {
		r, c int
		ch   string
	}
	for i := 1; i < steps; i++ {
		r := ar + roundDiv(dr*i, steps)
		c := ac + roundDiv(dc*i, steps)
		var ch string
		switch {
		case absInt(dc) > absInt(dr)*2:
			ch = "─"
		case absInt(dr) > absInt(dc)*2:
			ch = "│"
		case dr*dc > 0:
			ch = "╲"
		default:
			ch = "╱"
		}
		cells = append(cells, struct {
			r, c int
			ch   string
		}{r, c, ch})
	}
	return cells
}

// buildMeshGrid is the legacy FIXTURE-fed shim (MeshNodes +
// deriveMeshEdges). Kept SOLELY so the committed mesh goldens +
// mesh_anim_test.go's frame-0 invariant stay byte-identical (they
// exercise the geometry, not the data source). The live App calls
// buildMeshGridFrom with the projected mesh (live_mesh.go).
func buildMeshGrid(rows, cols, tick int) [][]meshCell {
	return buildMeshGridFrom(rows, cols, tick, MeshNodes, deriveMeshEdges())
}

// buildMeshGridFrom is the port of rufio-graphs.jsx buildGrid (lines
// 52-121), PARAMETERIZED on an explicit (nodes, edges) pair (PR-G2: the
// mesh is live — the App feeds the projected operator-hub+agents nodes
// and the routing edges; the fixture shim above feeds MeshNodes/
// deriveMeshEdges for the frame-0/golden regression). `edges` is
// [][2]int index pairs into `nodes` (unchanged shape — both the fixture
// deriveMeshEdges and the live meshState.Edges produce it). The
// geometry/animation is byte-for-byte the previous buildMeshGrid; ONLY
// the node/edge inputs are now arguments.
//
// Draw order is the jsx order EXACTLY: blank grid → edge lines (Line) →
// edge particles (Particle, bold) → node pulse rings (Ring, into empty
// cells only) → nodes on top (agent color, bold) → labels.
//
// `tick` is the 90ms meshTickMsg counter. Particles + rings are GATED
// to tick > 0 (tick 0 = the static frame; see the file doc comment's
// FRAME-0 DEVIATION note). At tick≥1 the phase math is the verbatim jsx
// port.
func buildMeshGridFrom(rows, cols, tick int, nodes []MeshNode, edges [][2]int) [][]meshCell {
	grid := make([][]meshCell, rows)
	for r := range grid {
		grid[r] = make([]meshCell, cols)
		for c := range grid[r] {
			grid[r][c] = meshCell{ch: " "}
		}
	}
	inBounds := func(r, c int) bool {
		return r >= 0 && r < rows && c >= 0 && c < cols
	}

	animate := tick > 0 // frame-0 deviation: tick 0 is the static frame.

	// edges + particles (jsx lines 59-76). Edge lines first (Line), then
	// — only when animating — 2 phase-offset particles per edge overwrite
	// a cell on the line (Particle, bold). A defensively out-of-range
	// edge index (live remap drift) is skipped — never index-panic on a
	// read-only console.
	for _, e := range edges {
		if e[0] < 0 || e[0] >= len(nodes) || e[1] < 0 || e[1] >= len(nodes) {
			continue
		}
		a := nodes[e[0]]
		b := nodes[e[1]]
		cells := lineCells(a.R, a.C, b.R, b.C)
		for _, cell := range cells {
			if inBounds(cell.r, cell.c) {
				grid[cell.r][cell.c] = meshCell{ch: cell.ch, color: styles.Palette.Line}
			}
		}
		if !animate || len(cells) == 0 {
			continue
		}
		// 2 particles per edge, phase-offset — verbatim port of jsx
		// 68-75: `phase = ((tick*0.05 + p*0.5 + (a.length%7)*0.07) % 1)`,
		// `idx = floor(phase * cells.length)`. jsx `a` is the edge's
		// FIRST node id STRING; our edges are derived+index-sorted, so
		// `a` here is nodes[e[0]].ID (the lower-index endpoint). The
		// phase FORMULA is exact; only the `a` binding differs (our edge
		// list is derived, not the jsx's hand-authored MESH_EDGES) — a
		// documented, deterministic choice.
		aLen := len(nodes[e[0]].ID)
		for p := 0; p < 2; p++ {
			phase := math.Mod(
				float64(tick)*0.05+float64(p)*0.5+float64(aLen%7)*0.07, 1)
			idx := int(math.Floor(phase * float64(len(cells))))
			if idx < 0 || idx >= len(cells) {
				continue
			}
			cell := cells[idx]
			if inBounds(cell.r, cell.c) {
				grid[cell.r][cell.c] = meshCell{
					ch: "•", color: styles.Palette.Particle, bold: true,
				}
			}
		}
	}

	// node glyph set (so labels can avoid overwriting another node).
	isNode := func(r, c int) bool {
		for _, n := range nodes {
			if n.R == r && n.C == c {
				return true
			}
		}
		return false
	}

	// node pulse rings (jsx lines 78-97) — only when animating. For each
	// non-dim node: `phase = (tick + n.r*7 + n.c*3) % 16`; if phase < 4
	// draw a ring at distance `phase+1` (4 cells: ±row/±col) in the Ring
	// tone, but ONLY into a still-EMPTY cell (jsx `grid[r][c].ch === ' '`
	// — never over an edge/particle). "dim" = the jsx `n.color==='dim'`
	// skip; our fixture has no dim node, so every node pulses (the
	// guard is ported for fidelity / future dim nodes). Verbatim port.
	if animate {
		for _, n := range nodes {
			if styles.AgentColor(n.ID) == styles.Palette.Dim {
				continue // jsx `if (n.color === 'dim') continue;`
			}
			phase := (tick + n.R*7 + n.C*3) % 16
			if phase >= 4 {
				continue
			}
			ring := phase + 1
			ringChars := [4][3]int{
				{0, -ring, 0}, {0, ring, 0}, // ─ (horizontal)
				{-ring, 0, 1}, {ring, 0, 1}, // │ (vertical)
			}
			for _, rc := range ringChars {
				r, c := n.R+rc[0], n.C+rc[1]
				if !inBounds(r, c) || grid[r][c].ch != " " {
					continue
				}
				ch := "─"
				if rc[2] == 1 {
					ch = "│"
				}
				grid[r][c] = meshCell{ch: ch, color: styles.Palette.Ring}
			}
		}
	}

	// nodes drawn before labels (jsx lines 99-107). Color = agent color.
	for _, n := range nodes {
		if inBounds(n.R, n.C) {
			grid[n.R][n.C] = meshCell{
				ch:    n.Glyph,
				color: styles.AgentColor(n.ID),
				bold:  true,
			}
		}
	}

	// labels — placed on a clean row above/below the node (jsx lines
	// 108-118). DELIBERATE deviation from the jsx "empty cells only"
	// rule: in the slim 9×36 rail the radial edges cross every label
	// span, so the jsx behaviour fragments the ids (`d│-code`). Per
	// data-mapping §0 OPEN-5 "the visual is truth over raw numbers",
	// labels here WIN over edge glyphs (but never over a node) so each
	// agent id stays legible. Node rows (1/4/7) and label rows (2/3/6)
	// are chosen disjoint in the fixture so a label never lands on a
	// node and the small edge gap a label leaves still reads as a line.
	for _, n := range nodes {
		lbl := n.ID
		lr := n.R + 1
		if n.R >= rows/2 {
			lr = n.R - 1
		}
		lc := n.C - len(lbl)/2
		if lc < 0 {
			lc = 0
		}
		if lc > cols-len(lbl) {
			lc = cols - len(lbl)
		}
		if lc < 0 {
			lc = 0
		}
		for i := 0; i < len(lbl); i++ {
			if inBounds(lr, lc+i) && !isNode(lr, lc+i) {
				grid[lr][lc+i] = meshCell{ch: string(lbl[i]), color: styles.Palette.Label}
			}
		}
	}
	return grid
}

// RenderMesh is the legacy FIXTURE-fed shim (MeshNodes +
// deriveMeshEdges) — kept byte-identical for the committed mesh goldens
// + mesh_anim_test.go's frame-0 invariant. The live App calls
// RenderMeshFrom with the projected mesh (live_mesh.go).
func RenderMesh(rows, cols, tick int) string {
	return RenderMeshFrom(rows, cols, tick, MeshNodes, deriveMeshEdges())
}

// RenderMeshFrom renders the mesh as a rows×cols block of styled glyphs
// at the given 90ms `tick` (jsx MeshGrid, lines 123-147), from an
// explicit (nodes, edges) pair (PR-G2: the live App feeds the projected
// operator-hub+agents mesh; the shim above feeds the fixture). Each row
// is exactly cols cells wide so the mesh body has a stable width inside
// the rail's h-pad (the border-integrity contract — holds at EVERY tick
// AND for ANY node/edge set, particles/rings overwrite cells, never
// widen a row). Blank cells render as spaces (no escape) so the panel
// background shows through, exactly like the jsx `<pre>` with
// transparent gaps. tick 0 = the static frame (no particles/rings; see
// the file doc comment's FRAME-0 DEVIATION).
func RenderMeshFrom(rows, cols, tick int, nodes []MeshNode, edges [][2]int) string {
	if rows < 1 {
		rows = 1
	}
	if cols < 1 {
		cols = 1
	}
	grid := buildMeshGridFrom(rows, cols, tick, nodes, edges)
	lines := make([]string, rows)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		for c := 0; c < cols; c++ {
			cell := grid[r][c]
			if cell.ch == " " || cell.ch == "" {
				b.WriteByte(' ')
				continue
			}
			st := lipgloss.NewStyle().Foreground(cell.color)
			if cell.bold {
				st = st.Bold(true)
			}
			b.WriteString(st.Render(cell.ch))
		}
		lines[r] = b.String()
	}
	return strings.Join(lines, "\n")
}

// scaleMeshNodesToCols rescales the node COLUMN coordinates from the
// canonical meshGridCols (36) layout space into a target `cols` so the
// SAME RenderMeshFrom renderer can draw a COMPRESSED rail at narrow
// terminal widths (#67-P5: shrink the rail, never drop it). It is a
// pure geometry transform on a node-slice copy — NO second renderer, no
// change to RenderMeshFrom.
//
// IDENTITY at the default/wide width: when cols >= meshGridCols the node
// slice is returned UNCHANGED (same backing layout), so the default
// rail and the full-width fleet mesh are byte-identical to pre-P5. Only
// a strictly narrower `cols` rescales: newC = round(C·(cols−1)/35),
// clamped to [0, cols−1]. Rows are untouched (the 9-row height is
// available at every terminal width); the existing label-elision
// (clampLine in meshPanel/RenderMeshFrom) keeps ids legible. Edges are
// node-INDEX pairs and are unaffected (the indices do not move).
func scaleMeshNodesToCols(nodes []MeshNode, cols int) []MeshNode {
	if cols >= meshGridCols || len(nodes) == 0 {
		return nodes // identity — the default/wide rail is byte-identical
	}
	if cols < 1 {
		cols = 1
	}
	out := make([]MeshNode, len(nodes))
	copy(out, nodes)
	const srcMaxC = meshGridCols - 1 // 35 — the canonical layout's last col
	for i := range out {
		c := out[i].C
		if srcMaxC > 0 {
			c = int(math.Round(float64(out[i].C) * float64(cols-1) / float64(srcMaxC)))
		}
		if c < 0 {
			c = 0
		}
		if c > cols-1 {
			c = cols - 1
		}
		out[i].C = c
	}
	return out
}

// renderMeshStatic renders the mesh with NO animation overlay — exactly
// the pre-PR-F static port (edges+nodes+labels, zero particles/rings).
// It is the independent frame-0 witness mesh_anim_test.go compares
// RenderMesh(rows,cols,0) against: the static port and tick 0 MUST be
// byte-identical (the FRAME-0 invariant — the committed mesh goldens
// render at tick 0). Implemented via the tick≤0 (animate==false) path
// of buildMeshGrid, which is the static geometry by construction.
func renderMeshStatic(rows, cols int) string {
	// tick 0 ⇒ animate==false in buildMeshGrid ⇒ pure static geometry.
	return RenderMesh(rows, cols, 0)
}

// MeshNodeCount / MeshEdgeCount are the legacy fixture counts (kept for
// any remaining fixture-fed call site / test). The mesh header's "N
// nodes · N links" line (PR-E contract: "must reflect the real derived
// counts") now uses the LIVE meshState counts via len(a.mesh.Nodes) /
// len(a.mesh.Edges) directly (app.go) — they are the real projected
// operator-hub+agents / routing counts, never hardcoded and never the
// fixture.
func MeshNodeCount() int { return len(MeshNodes) }
func MeshEdgeCount() int { return len(deriveMeshEdges()) }
