// live_mesh.go — PR-G2: the live substrate-mesh read path.
//
// SCOPE (docs/plans/2026-05-15-tui-v8-rebuild.md "### PR-G", the G2
// slice): make the mesh LIVE from the real on-disk substrate —
// live/attention/ (agent nodes) + routing deliveries (outbox∩inbox
// edges) + a SYNTHESIZED operator hub (the OPEN-4 resolution). The mesh
// is BOTH the substrate right-rail AND the fleet-tab content (same
// renderer / same data, per the v8 design). channels/goals/memory/
// lineage tabs stay fixtures (G3). NO operator→agent send (later slice).
//
// This file is the BRIDGE between the retained on-disk substrate and the
// pure G0 projection — exactly mirroring live_substrate.go's role for the
// chat. It does NOT re-implement projection or placement: it REUSES the
// G0 functions VERBATIM —
//
//   - projectMeshNodes(attns)        (project_walk.go) — deterministic
//     9×36 placement of the agents that carry a live/attention/ record;
//   - deriveMeshEdgesLive(root)      (project_walk.go) — the outbox∩inbox
//     routing-delivery edges as agent-id pairs (OPEN-3-resolved).
//
// project.go / project_walk.go are CONSUMED VERBATIM (not modified —
// `git diff` on them is empty). G0 deliberately synthesizes NO operator
// node and NO presence (its file header: "OPEN-4 … is the G2 product
// decision"); G2 adds exactly that here, OUTSIDE the pure G0 layer.
//
// Everything is a pure function of `root` (the on-disk state) + the
// resolved operator id — NO time.Now(), NO fsnotify, NO rand here; the
// live fsnotify/tea wiring is in app.go's Init/Update only, and every
// test injects a deterministic meshLoadedMsg (the G1 pattern) rather
// than touching the watcher / wall-clock.
//
// OPEN-4 RESOLUTION (LOCKED 2026-05-16 — applied HERE, the synthesis
// layer): the OPERATOR node is synthesized from the resolved operator
// identity (identity.Resolve → a.me; default operatorFallbackID on
// NoIdentityError) and placed as the central hub (R:4 C:18 in the 9×36
// rail — the same hub cell the PR-D fixture used, MeshNodes[0]) with the
// heavier "◉" hub glyph (vs "●" agent spokes; jsx MESH_NODES hub glyph).
// The operator is ALWAYS present — they are running the TUI — so the
// mesh is never empty/void even with ZERO agent attention records.
// Agent nodes = ONLY those with a live/attention/ record (projectMeshNodes
// — REAL signal, never faked). Liveness/glyph is derived only from real
// signals (an agent has an attention record ⇒ it is a node; no synthetic
// "typing"/"present" — data-mapping §0/§3 OPEN-4 "do NOT fake it").
//
// OPERATOR DEDUPE (opdedupe — bug damon eyeballed on /tmp/g1-full): the
// operator may ALSO carry a live/attention/ record (the operator
// `attend`ed too). projectMeshNodes (identity-agnostic, OPEN-4-deferred)
// yields a node for it like any other agent; the OPEN-4 synthesis above
// ALSO adds the operator hub — two nodes for ONE identity. One identity =
// one node: when an attention-derived node's id == the resolved operator
// id, that node IS the operator IS the hub — its projectMeshNodes slot is
// SKIPPED (the hub at index 0 already represents this identity; the hub's
// placement/glyph/color win). Routing edges referencing the operator id
// remap to the single hub index (idx[me] == 0). With no operator
// attention record (the normal human-at-TUI case) nothing is skipped and
// behaviour is unchanged. Net: in BOTH cases exactly ONE operator node =
// the hub; never two.
package tui

import (
	"sort"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
)

// operatorHubR / operatorHubC / operatorHubGlyph are the synthesized
// operator node's placement + glyph (OPEN-4). The cell (4,18) is the
// 9×36 grid hub — the SAME coordinate the PR-D fixture pinned for
// `operator` (fixtures.go:341 `{ID:"operator", R:4, C:18, Glyph:"◉"}`),
// kept identical so the live mesh reads exactly like the fixture mesh
// damon already eyeballed (the v8 mesh has a central hub; the fixture put
// `operator` center). "◉" is the heavier hub glyph the jsx MESH_NODES
// uses for the hub vs "●" for spokes (mesh.go / fixtures.go:327).
const (
	operatorHubR     = 4
	operatorHubC     = 18
	operatorHubGlyph = "◉"
)

// meshState is the live-projected mesh the App renders: the placed nodes
// (synthesized operator hub + the attention-bearing agents) and the
// derived edges as index pairs into Nodes. It is the mesh analogue of
// live_substrate.go's []ThreadMsg — produced by loadMesh, folded through
// the single meshLoadedMsg seam, read by meshPanel/renderMeshHeader/
// renderRoutingStrip. The zero value (no nodes, no edges) is a valid
// "nothing on disk yet" state, though loadMesh ALWAYS yields ≥1 node
// (the operator), so a hand-constructed zero meshState only occurs in a
// unit test that never calls loadMesh.
type meshState struct {
	// Nodes is the placed mesh: index 0 is ALWAYS the operator hub
	// (OPEN-4 — present even with zero agents); indices 1.. are the
	// attention-bearing agents in projectMeshNodes order (sorted-by-agent,
	// deterministic), EXCLUDING any whose id == the resolved operator id
	// (opdedupe — that identity IS the hub, so it appears ONCE at index 0,
	// never also as a spoke). Glyph "◉" for the operator hub, "●" for
	// agent spokes.
	Nodes []MeshNode
	// Edges are unique {lo,hi} index pairs into Nodes (the same
	// [][2]int shape buildMeshGrid/deriveMeshEdges use) so the mesh
	// renderer is unchanged by the fixture→live swap. Sorted +
	// de-duplicated for deterministic rendering/goldens.
	Edges [][2]int
}

// loadMesh is the pure G2 read path: read the live attention set, project
// the agent nodes (G0 projectMeshNodes — VERBATIM), SYNTHESIZE the
// operator hub (OPEN-4 — always present, central), derive the routing
// edges (G0 deriveMeshEdgesLive — VERBATIM), and map the returned
// agent-id pairs onto Nodes indices for the grid. A missing/empty
// substrate yields a mesh with JUST the operator hub (the graceful
// minimal cold-start — never a void/crash; the operator is running the
// TUI so they are always a node). NEVER gated on a live daemon — it
// reads on-disk attention/routing directly, exactly like loadSubstrate.
//
// `me` is the resolved operator identity (a.me — identity.Resolve, default
// operatorFallbackID). It is the synthesized hub's id (drives its color
// via styles.AgentColor and its label).
func loadMesh(root, me string) meshState {
	// 1. Agent nodes — G0 projectMeshNodes, VERBATIM (project_walk.go).
	//    attention.ReadAll returns the agents sorted-by-agent (a missing
	//    live/attention/ dir → empty slice, attention.go:159-161 — a
	//    fresh project is not an error); projectMeshNodes deterministically
	//    places them in the 9×36 rail. G0 synthesizes NO operator and all
	//    "●" spokes (its contract) — G2 adds the operator below.
	atts, err := attention.ReadAll(root)
	if err != nil {
		// attention.ReadAll only errors on a real IO/parse failure (not a
		// missing dir). The TUI is a read-only console: a transient bad
		// attention file must not blank the mesh — degrade to operator-only
		// (the watcher re-reads on the next event; it self-heals). Mirrors
		// loadSubstrateEvents's best-effort posture.
		atts = nil
	}
	agentNodes := projectMeshNodes(atts) // G0 verbatim — may be nil/empty.

	// 2. OPEN-4: synthesize the operator hub at index 0. ALWAYS present
	//    (they are running the TUI) — so even with zero agent attention
	//    records the mesh renders the operator (graceful minimal state,
	//    never a void). Central hub cell + heavier "◉" glyph.
	nodes := make([]MeshNode, 0, len(agentNodes)+1)
	nodes = append(nodes, MeshNode{
		ID: me, R: operatorHubR, C: operatorHubC, Glyph: operatorHubGlyph,
	})

	// 2a. Operator dedupe (opdedupe — the bug damon eyeballed on
	//     /tmp/g1-full): G0's projectMeshNodes is identity-agnostic and
	//     OPEN-4-deferred — it yields a node for EVERY live/attention/
	//     record, INCLUDING one whose agent id == the resolved operator id
	//     if the operator also `attend`ed (a perfectly valid on-disk
	//     state: live/attention/operator.gdl). Without a guard that
	//     attention-derived node + the synthesized hub above are TWO nodes
	//     for ONE identity — the duplicate "operator" damon saw (5 nodes:
	//     hub + 4 attention incl. operator). One identity = one node: that
	//     attention record IS the operator, and the operator IS the hub —
	//     so SKIP its projectMeshNodes node entirely (the hub at index 0
	//     already represents this identity, and the hub's placement/glyph/
	//     color/label win — data-mapping §0 OPEN-4 "the operator is the
	//     central hub"). projectMeshNodes is still consumed VERBATIM with
	//     the FULL attention set (the operator's record stays part of the
	//     radial-fan N so the OTHER agents' placement is byte-identical to
	//     G0's on-disk projection — we drop only the operator's own slot,
	//     never re-project a filtered input). When the operator has NO
	//     attention record (the normal human-at-TUI case) nothing is
	//     skipped and behaviour is UNCHANGED (exactly one synthesized hub).
	//
	// 2b. Collision guard: G0's projectMeshNodes is hub-agnostic (OPEN-4
	//     deferred), so for N==1 it centre-places that single agent at
	//     (4,18) — EXACTLY the operator hub cell — and a rare rounding
	//     collision is also possible. Two nodes must never stack (the
	//     mesh-placement invariant project_test.go asserts for agents).
	//     Resolve deterministically in G2 (NOT by modifying G0): any agent
	//     node landing on an OCCUPIED cell is nudged to the first free
	//     cell in a row-major scan — the same deterministic nudge strategy
	//     projectMeshNodes itself uses internally (project_walk.go:368-382),
	//     applied here against the operator-inclusive occupancy so the hub
	//     wins the centre.
	used := map[[2]int]bool{{operatorHubR, operatorHubC}: true}
	for _, n := range agentNodes {
		if n.ID == me {
			// One identity = one node: this attention record IS the
			// operator, which is already the hub at index 0. Do NOT add a
			// second node (the duplicate). Edges referencing the operator
			// id remap to the single hub index below (idx[me] == 0).
			continue
		}
		if used[[2]int{n.R, n.C}] {
			n.R, n.C = nextFreeCell(used)
		}
		used[[2]int{n.R, n.C}] = true
		nodes = append(nodes, n) // keep G0's "●" spoke glyph verbatim
	}

	// 3. Edges — G0 deriveMeshEdgesLive, VERBATIM (project_walk.go): the
	//    outbox∩inbox routing-delivery agent-id pairs (OPEN-3-resolved).
	//    Map each id-pair → Nodes index pair (the documented G0→G2 shape
	//    bridge: G0 returns ids because it has no node table; G2 owns the
	//    node table so it indexes here). A pair referencing an id with no
	//    node (an agent that routed but has no attention record, so no
	//    node) is DROPPED — an edge needs two endpoints in the graph.
	idPairs, err := deriveMeshEdgesLive(root)
	if err != nil {
		idPairs = nil // best-effort: unreadable routing → no edges, not a crash
	}
	idx := make(map[string]int, len(nodes))
	for i, n := range nodes {
		idx[n.ID] = i
	}
	seen := make(map[[2]int]bool)
	var edges [][2]int
	for _, p := range idPairs {
		ai, aok := idx[p[0]]
		bi, bok := idx[p[1]]
		if !aok || !bok || ai == bi {
			continue // endpoint not a node (no attention record) — drop
		}
		if ai > bi {
			ai, bi = bi, ai
		}
		key := [2]int{ai, bi}
		if seen[key] {
			continue
		}
		seen[key] = true
		edges = append(edges, key)
	}
	// Deterministic edge order (golden stability) — deriveMeshEdgesLive is
	// already id-sorted, but the id→index remap can reorder, so re-sort on
	// the final index pairs.
	sort.Slice(edges, func(i, j int) bool {
		if edges[i][0] != edges[j][0] {
			return edges[i][0] < edges[j][0]
		}
		return edges[i][1] < edges[j][1]
	})

	return meshState{Nodes: nodes, Edges: edges}
}

// nextFreeCell returns the first grid cell (row-major, 9×36) not in
// `used`. Bounded (≤ rows*cols). Mirrors projectMeshNodes's internal
// collision-nudge scan (project_walk.go:368-382) so the live mesh's
// collision handling is consistent with G0's. The grid is the SAME 9×36
// MeshNode coordinate space (fixtures.go:324-327 / meshGridRows×
// meshGridCols). Falls back to (0,0) only if the entire grid is full
// (impossible at v1 fleet scale — defence so the scan always terminates).
func nextFreeCell(used map[[2]int]bool) (int, int) {
	for r := 0; r < meshGridRows; r++ {
		for c := 0; c < meshGridCols; c++ {
			if !used[[2]int{r, c}] {
				return r, c
			}
		}
	}
	return 0, 0
}
