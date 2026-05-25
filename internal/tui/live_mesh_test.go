// live_mesh_test.go — PR-G2: tests for the live mesh loader (loadMesh)
// + the OPEN-4 operator-hub synthesis + the App-level meshLoadedMsg fold
// and the three mesh cold-start states.
//
// Determinism: every loadMesh test writes synthetic on-disk records via
// the REAL lib writers (attention.BuildRecord / thought.Write) under
// t.TempDir() and reads them back through the loader — NO wall-clock, NO
// fsnotify, NO time.Now(). loadMesh is a pure function of `root` + the
// resolved operator id, exactly like loadSubstrate (G1) and project.go's
// pure layer. The App-level tests inject a PINNED meshLoadedMsg (the G1
// determinism pattern, extended to the mesh) — never the live watcher.
package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// writeMeshAttn writes live/attention/<agent>.gdl via the real
// attention.BuildRecord so the on-disk shape is the lib's exactly (the
// same files attention.ReadAll / projectMeshNodes consume).
func writeMeshAttn(t *testing.T, root, agent string) {
	t.Helper()
	rec := attention.BuildRecord(agent, "investigating customer:5821", "fleet",
		[]string{"customer:5821"}, nil, "2026-05-15T14:00:00Z")
	dir := filepath.Join(root, "live", "attention")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, agent+".gdl"),
		[]byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeMeshDelivery writes the outbox+inbox pair for one routing
// delivery (the EXACT on-disk shape deriveMeshEdgesLive's outbox∩inbox
// derivation reads): live/outbox/<from>/<id>.gdl via the real
// thought.Write + a hand-built live/inbox/<to>/<id>.gdl carrying the
// @route|from line (deliverToInbox is unexported — same convention as
// project_test.go's writeInbox).
func writeMeshDelivery(t *testing.T, root, from, to, id string) {
	t.Helper()
	rec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID: id, Author: from, Type: "hypothesis", Subject: "customer:5821",
		Content: "c", Scope: "fleet", TS: "2026-05-15T14:00:00Z",
	})
	if err := thought.Write(root, from, id, []gdl.Record{rec}); err != nil {
		t.Fatalf("thought.Write: %v", err)
	}
	idir := filepath.Join(root, "live", "inbox", to)
	if err := os.MkdirAll(idir, 0o755); err != nil {
		t.Fatal(err)
	}
	route := gdl.Record{Type: "route", Fields: []gdl.RecordField{
		{Key: "to", Value: to}, {Key: "from", Value: from},
		{Key: "ts", Value: "2026-05-15T14:00:01Z"}}}
	if err := os.WriteFile(filepath.Join(idir, id+".gdl"),
		[]byte(gdl.RenderLine(rec)+"\n"+gdl.RenderLine(route)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadMesh_OperatorSynthesisAndAgents is the OPEN-4 core: the
// operator hub is ALWAYS index 0 (central, ◉) even with agents present,
// and the agents come from projectMeshNodes (verbatim G0) — all "●"
// spokes, deterministic.
func TestLoadMesh_OperatorSynthesisAndAgents(t *testing.T) {
	root := t.TempDir()
	for _, ag := range []string{"cursor", "claude-code", "data-analyst"} {
		writeMeshAttn(t, root, ag)
	}
	ms := loadMesh(root, "operator")

	if len(ms.Nodes) != 4 {
		t.Fatalf("want 4 nodes (operator + 3 agents), got %d: %#v", len(ms.Nodes), ms.Nodes)
	}
	op := ms.Nodes[0]
	if op.ID != "operator" || op.R != operatorHubR || op.C != operatorHubC || op.Glyph != operatorHubGlyph {
		t.Errorf("node[0] must be the synthesized operator hub %s(%d,%d) got %s(%d,%d)%s",
			operatorHubGlyph, operatorHubR, operatorHubC, op.Glyph, op.R, op.C, op.ID)
	}
	// Agents follow in projectMeshNodes order (sorted-by-agent): the G0
	// fn is reused verbatim, so the agent nodes here MUST equal it.
	atts, _ := attention.ReadAll(root)
	wantAgents := projectMeshNodes(atts)
	gotAgents := ms.Nodes[1:]
	if !reflect.DeepEqual(gotAgents, wantAgents) {
		t.Errorf("agent nodes must be projectMeshNodes (G0 verbatim):\n got %#v\nwant %#v",
			gotAgents, wantAgents)
	}
	for _, n := range gotAgents {
		if n.Glyph != "●" {
			t.Errorf("agent %q glyph %q want ● (only the operator is the ◉ hub)", n.ID, n.Glyph)
		}
	}
}

// TestLoadMesh_EmptyIsOperatorOnly is OPEN-4's "always present": zero
// attention records → exactly the synthesized operator hub (the
// graceful minimal cold-start, never a void/crash). NEVER gated on a
// daemon (loadMesh reads disk directly).
func TestLoadMesh_EmptyIsOperatorOnly(t *testing.T) {
	ms := loadMesh(t.TempDir(), "operator")
	if len(ms.Nodes) != 1 || ms.Nodes[0].ID != "operator" || ms.Nodes[0].Glyph != operatorHubGlyph {
		t.Fatalf("empty substrate must yield ONLY the operator hub, got %#v", ms.Nodes)
	}
	if len(ms.Edges) != 0 {
		t.Fatalf("operator-only mesh must have no edges, got %#v", ms.Edges)
	}
}

// TestLoadMesh_ResolvedOperatorIdIsTheHub: the synthesized hub id is the
// RESOLVED operator identity (a.me — identity.Resolve / fallback), not a
// hardcoded "operator" literal.
func TestLoadMesh_ResolvedOperatorIdIsTheHub(t *testing.T) {
	ms := loadMesh(t.TempDir(), "alice-op")
	if ms.Nodes[0].ID != "alice-op" {
		t.Errorf("hub id must be the resolved operator id, got %q", ms.Nodes[0].ID)
	}
}

// TestLoadMesh_SingleAgentCollisionGuard: G0's projectMeshNodes
// centre-places a lone agent at (4,18) — EXACTLY the operator hub cell.
// G2 must nudge it so the two never stack (the mesh-placement
// invariant), WITHOUT modifying G0.
func TestLoadMesh_SingleAgentCollisionGuard(t *testing.T) {
	root := t.TempDir()
	writeMeshAttn(t, root, "claude-code")
	ms := loadMesh(root, "operator")
	if len(ms.Nodes) != 2 {
		t.Fatalf("want operator + 1 agent, got %#v", ms.Nodes)
	}
	op, ag := ms.Nodes[0], ms.Nodes[1]
	if op.R == ag.R && op.C == ag.C {
		t.Fatalf("collision guard failed: operator %v and agent %v stack", op, ag)
	}
	if op.R != operatorHubR || op.C != operatorHubC {
		t.Errorf("the operator hub must WIN the centre (%d,%d), got (%d,%d)",
			operatorHubR, operatorHubC, op.R, op.C)
	}
}

// TestLoadMesh_EdgesFromRoutingRemappedToIndices proves the G0→G2 shape
// bridge: deriveMeshEdgesLive returns agent-id pairs (verbatim G0); G2
// remaps them to Nodes index pairs, dropping any endpoint with no node.
func TestLoadMesh_EdgesFromRoutingRemappedToIndices(t *testing.T) {
	root := t.TempDir()
	writeMeshAttn(t, root, "claude-code")
	writeMeshAttn(t, root, "cursor")
	writeMeshAttn(t, root, "data-analyst")
	// claude-code → cursor AND claude-code → data-analyst (two edges).
	writeMeshDelivery(t, root, "claude-code", "cursor", "1-aaa001")
	writeMeshDelivery(t, root, "claude-code", "data-analyst", "1-aaa002")
	// A delivery to an agent with NO attention record (no node) → the
	// edge must be DROPPED (an edge needs two endpoints in the graph).
	writeMeshDelivery(t, root, "claude-code", "ghost-agent", "1-aaa003")

	ms := loadMesh(root, "operator")
	// Nodes: operator(0) claude-code(1) cursor(2) data-analyst(3) (sorted).
	idx := map[string]int{}
	for i, n := range ms.Nodes {
		idx[n.ID] = i
	}
	if _, ok := idx["ghost-agent"]; ok {
		t.Fatalf("ghost-agent has no attention record → must NOT be a node: %#v", ms.Nodes)
	}
	// Expected edges (as id pairs, then mapped): cc-cursor, cc-data-analyst.
	want := map[[2]int]bool{
		ordered(idx["claude-code"], idx["cursor"]):       true,
		ordered(idx["claude-code"], idx["data-analyst"]): true,
	}
	if len(ms.Edges) != len(want) {
		t.Fatalf("want %d edges (ghost dropped), got %d: %#v", len(want), len(ms.Edges), ms.Edges)
	}
	for _, e := range ms.Edges {
		if !want[[2]int{e[0], e[1]}] {
			t.Errorf("unexpected edge %v (not a routing delivery between two nodes)", e)
		}
	}
}

func ordered(a, b int) [2]int {
	if a > b {
		return [2]int{b, a}
	}
	return [2]int{a, b}
}

// TestLoadMesh_OperatorAttentionRecordIsTheHubNotADuplicate is the
// opdedupe core (the bug damon eyeballed on /tmp/g1-full): when the
// resolved operator ALSO carries a live/attention/ record (the operator
// `attend`ed too), projectMeshNodes (G0, verbatim) yields a node for it
// JUST like any other attention-bearing agent. G2 must NOT then ALSO
// synthesize a second operator hub — one identity = one node. The single
// operator node IS the central hub (◉ at the hub cell), and the live
// counts decrement accordingly (4 attention records ⇒ 4 nodes, NOT 5).
func TestLoadMesh_OperatorAttentionRecordIsTheHubNotADuplicate(t *testing.T) {
	root := t.TempDir()
	// The /tmp/g1-full scenario: operator ALSO attended, plus 3 agents.
	for _, ag := range []string{"operator", "claude-code", "cursor", "data-analyst"} {
		writeMeshAttn(t, root, ag)
	}
	ms := loadMesh(root, "operator")

	// Exactly ONE node with the operator id (never two).
	opCount, opIdx := 0, -1
	for i, n := range ms.Nodes {
		if n.ID == "operator" {
			opCount++
			opIdx = i
		}
	}
	if opCount != 1 {
		t.Fatalf("one identity = one node: want EXACTLY 1 operator node, got %d: %#v",
			opCount, ms.Nodes)
	}
	// That one operator node IS the hub: index 0, hub cell, ◉ glyph
	// (placement/glyph win even though the operator also has an attention
	// record — the attention node is folded into the hub).
	op := ms.Nodes[opIdx]
	if opIdx != 0 || op.R != operatorHubR || op.C != operatorHubC || op.Glyph != operatorHubGlyph {
		t.Errorf("the operator node must BE the hub %s(%d,%d) at index 0, got %s(%d,%d) at index %d",
			operatorHubGlyph, operatorHubR, operatorHubC, op.Glyph, op.R, op.C, opIdx)
	}
	// MeshNodeCount == attention-record-count (4), NOT +1 (the bug was 5).
	atts, _ := attention.ReadAll(root)
	if len(ms.Nodes) != len(atts) {
		t.Errorf("dedupe count: want %d nodes (== attention records, operator folded "+
			"into the hub), got %d: %#v", len(atts), len(ms.Nodes), ms.Nodes)
	}
	if len(ms.Nodes) != 4 {
		t.Errorf("the /tmp/g1-full scenario must read 4 nodes (not 5), got %d", len(ms.Nodes))
	}
	// The non-operator agents are still projectMeshNodes' "●" spokes.
	for _, n := range ms.Nodes[1:] {
		if n.ID == "operator" {
			t.Errorf("operator must not appear as a spoke alongside the hub: %#v", ms.Nodes)
		}
		if n.Glyph != "●" {
			t.Errorf("agent %q glyph %q want ● (only the operator is the ◉ hub)", n.ID, n.Glyph)
		}
	}
}

// TestLoadMesh_OperatorAttendsEdgesCollapseOntoTheHub: a routing edge
// that references the operator id (operator delivered to / received from
// an agent) must remap to the SINGLE operator node (the hub, index 0) —
// no dangling/duplicate endpoint, no lost edge — even though the operator
// also carries an attention record.
func TestLoadMesh_OperatorAttendsEdgesCollapseOntoTheHub(t *testing.T) {
	root := t.TempDir()
	for _, ag := range []string{"operator", "claude-code", "cursor"} {
		writeMeshAttn(t, root, ag)
	}
	// operator → claude-code and cursor → operator (both touch the hub id).
	writeMeshDelivery(t, root, "operator", "claude-code", "1-op00001")
	writeMeshDelivery(t, root, "cursor", "operator", "1-op00002")

	ms := loadMesh(root, "operator")
	idx := map[string]int{}
	for i, n := range ms.Nodes {
		if _, dup := idx[n.ID]; dup {
			t.Fatalf("duplicate node id %q — dedupe failed: %#v", n.ID, ms.Nodes)
		}
		idx[n.ID] = i
	}
	if idx["operator"] != 0 {
		t.Fatalf("operator must be the single hub at index 0, got %d (%#v)", idx["operator"], ms.Nodes)
	}
	want := map[[2]int]bool{
		ordered(idx["operator"], idx["claude-code"]): true,
		ordered(idx["operator"], idx["cursor"]):      true,
	}
	if len(ms.Edges) != len(want) {
		t.Fatalf("want %d edges both collapsed onto the single operator hub, got %d: %#v",
			len(want), len(ms.Edges), ms.Edges)
	}
	for _, e := range ms.Edges {
		if !want[[2]int{e[0], e[1]}] {
			t.Errorf("unexpected edge %v — operator edges must map to the hub index 0", e)
		}
		if e[0] != 0 && e[1] != 0 {
			t.Errorf("edge %v references the operator but neither endpoint is the hub (index 0)", e)
		}
	}
}

// TestLoadMesh_NoOperatorAttention_UnchangedHub is the regression guard
// (the normal human-at-TUI case): the operator has NO attention record,
// so behaviour is UNCHANGED from the pre-dedupe G2 — exactly one
// synthesized hub at index 0, agent count == attention-record-count + 1.
func TestLoadMesh_NoOperatorAttention_UnchangedHub(t *testing.T) {
	root := t.TempDir()
	for _, ag := range []string{"claude-code", "cursor", "data-analyst"} {
		writeMeshAttn(t, root, ag)
	}
	ms := loadMesh(root, "operator")

	atts, _ := attention.ReadAll(root)
	if len(ms.Nodes) != len(atts)+1 {
		t.Fatalf("no operator attention: hub is synthesized → want %d nodes "+
			"(agents + 1 hub), got %d: %#v", len(atts)+1, len(ms.Nodes), ms.Nodes)
	}
	op := ms.Nodes[0]
	if op.ID != "operator" || op.R != operatorHubR || op.C != operatorHubC || op.Glyph != operatorHubGlyph {
		t.Errorf("synthesized hub unchanged: want operator %s(%d,%d) at index 0, got %s(%d,%d)",
			operatorHubGlyph, operatorHubR, operatorHubC, op.Glyph, op.R, op.C)
	}
	// The agent nodes are still projectMeshNodes (G0 verbatim) exactly.
	if !reflect.DeepEqual(ms.Nodes[1:], projectMeshNodes(atts)) {
		t.Errorf("no-attention path must keep agent nodes == projectMeshNodes (G0 verbatim)")
	}
}

// TestLoadMesh_NonOperatorNamedAgentUnaffected: only the RESOLVED
// operator id collides/merges with the hub. An attention-bearing agent
// that is NOT the resolved operator is unaffected — it stays a normal
// spoke even if it has a routing edge, and the hub is still synthesized.
func TestLoadMesh_NonOperatorNamedAgentUnaffected(t *testing.T) {
	root := t.TempDir()
	for _, ag := range []string{"claude-code", "cursor"} {
		writeMeshAttn(t, root, ag)
	}
	// Resolved operator is "alice-op" (NOT one of the attention agents).
	ms := loadMesh(root, "alice-op")

	if ms.Nodes[0].ID != "alice-op" || ms.Nodes[0].Glyph != operatorHubGlyph {
		t.Fatalf("hub must be the synthesized resolved operator alice-op, got %#v", ms.Nodes[0])
	}
	atts, _ := attention.ReadAll(root)
	if len(ms.Nodes) != len(atts)+1 {
		t.Fatalf("non-operator agents must NOT merge into the hub: want %d nodes, got %d: %#v",
			len(atts)+1, len(ms.Nodes), ms.Nodes)
	}
	for _, n := range ms.Nodes[1:] {
		if n.Glyph != "●" {
			t.Errorf("agent %q must stay a ● spoke (only the resolved operator is the hub)", n.ID)
		}
	}
}

// TestLoadMesh_Deterministic: same disk → byte-identical mesh (golden
// stability — the live render is a pure fn of disk state).
func TestLoadMesh_Deterministic(t *testing.T) {
	root := t.TempDir()
	for _, ag := range []string{"data-analyst", "claude-code", "cursor"} {
		writeMeshAttn(t, root, ag)
	}
	writeMeshDelivery(t, root, "claude-code", "cursor", "1-zzz001")
	a := loadMesh(root, "operator")
	b := loadMesh(root, "operator")
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("loadMesh not deterministic:\n a=%#v\n b=%#v", a, b)
	}
}

// TestMeshLoadedMsg_FoldsWithoutRearmingWatcher proves the drain
// invariant for the mesh seam: a meshLoadedMsg folds the mesh but MUST
// NOT re-arm the watcher (it is produced by the one-shot loadMeshCmd /
// a test inject, NOT the drain — re-arming here would double-drain), and
// an AttentionMsg DOES re-arm exactly once (never zero: stream stops).
func TestMeshLoadedMsg_FoldsWithoutRearmingWatcher(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a, _ := NewApp("/tmp/fake-root")
	m, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app := m.(App)

	armed := 0
	app.watcherCmd = tea.Cmd(func() tea.Msg { armed++; return nil })

	// AttentionMsg → re-arm (exactly once) + a mesh re-read one-shot.
	_, cmd := app.Update(AttentionMsg{Agent: "claude-code"})
	if cmd == nil {
		t.Fatalf("AttentionMsg must return a re-arm cmd (drain must not stop)")
	}

	// meshLoadedMsg folds the mesh but MUST NOT re-arm the watcher.
	pinned := pinnedMesh()
	m2, cmd2 := app.Update(meshLoadedMsg{mesh: pinned})
	app = m2.(App)
	if cmd2 != nil {
		t.Errorf("meshLoadedMsg MUST NOT re-arm the watcher (drain-invariant: "+
			"double-re-arm) — got non-nil cmd %T", cmd2)
	}
	if !reflect.DeepEqual(app.mesh, pinned) {
		t.Errorf("meshLoadedMsg must fold the projected mesh into a.mesh")
	}
}

// TestMeshLive_HeaderAndRoutingCountsAreLive: the mesh header
// `N nodes · N links` + the ROUTING `N linked` reflect the LIVE
// projected counts (len(mesh.Nodes)/len(mesh.Edges)), never the fixture.
func TestMeshLive_HeaderAndRoutingCountsAreLive(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a, _ := NewApp("/tmp/fake-root")
	m, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app := m.(App)

	// Inject a 3-node / 1-edge mesh (≠ the 4-node/5-edge fixture).
	live := meshState{
		Nodes: []MeshNode{
			{ID: "operator", R: operatorHubR, C: operatorHubC, Glyph: operatorHubGlyph},
			{ID: "claude-code", R: 1, C: 18, Glyph: "●"},
			{ID: "cursor", R: 7, C: 5, Glyph: "●"},
		},
		Edges: [][2]int{{0, 1}},
	}
	m, _ = app.Update(meshLoadedMsg{mesh: live})
	app = m.(App)
	m, _ = app.Update(substrateLoadedMsg{rows: pinnedThread()})
	app = m.(App)
	out := stripSGR(app.View())

	if !strings.Contains(out, "3 nodes · 1 links") {
		t.Errorf("mesh header must show the LIVE counts `3 nodes · 1 links` "+
			"(not the 4-node/5-edge fixture):\n%s", out)
	}
	if !strings.Contains(out, "3 linked") {
		t.Errorf("ROUTING strip must show the LIVE `3 linked` node count:\n%s", out)
	}
}

// TestMeshLive_SubstrateRailAndFleetTabSameMesh proves the v8 contract:
// the SAME live mesh renders in BOTH the substrate right-rail AND the
// fleet tab (same renderer, same a.mesh data).
func TestMeshLive_SubstrateRailAndFleetTabSameMesh(t *testing.T) {
	a := driveInjected(t, 200, 55, SubstrateThread) // injects pinnedMesh too
	railOut := stripSGR(a.View())                   // substrate tab → right rail
	m, _ := a.Update(keyMsg("2"))                   // → fleet tab
	fleetOut := stripSGR(m.(App).View())

	for _, want := range []string{"◆ MESH", "operator", "data-analyst", "ROUTING"} {
		if !strings.Contains(railOut, want) {
			t.Errorf("substrate right-rail mesh missing %q", want)
		}
		if !strings.Contains(fleetOut, want) {
			t.Errorf("fleet-tab mesh missing %q (must be the SAME live mesh)", want)
		}
	}
	// Same node/edge counts in both (same a.mesh).
	if !strings.Contains(railOut, "4 nodes · 5 links") ||
		!strings.Contains(fleetOut, "4 nodes · 5 links") {
		t.Errorf("rail + fleet must show the SAME live counts (same a.mesh)")
	}
}

// TestGoldenLiveMeshInjected is the deterministic live-mesh golden (the
// fleet tab — the mesh at full panel width). PR-G2 originally pinned the
// static pinnedMesh() fixture here; opdedupe rewires it to drive the REAL
// loadMesh against a t.TempDir() seeded with the EXACT /tmp/g1-full shape
// that exposed the duplicate-operator bug damon eyeballed: an `operator`
// live/attention/ record (the operator `attend`ed too) PLUS the three
// arc agents + the routing deliveries. Pre-fix this rendered FIVE nodes
// with `operator` twice (the synthesized hub AND an attention-derived
// spoke nudged off the hub cell); post-fix it is FOUR nodes, one operator
// = the hub — so this golden now genuinely PROVES the dedupe end-to-end
// through the real read path, not a hand-built fixture that never had the
// bug. Determinism: a real t.TempDir() root, records written via the lib
// writers, loadMesh is a pure fn of disk state (sorted iteration, no
// fsnotify / wall-clock / time.Now); the substrate (ROUTING quorum) stays
// the pinned SubstrateThread injection. Regenerate via TEATEST_UPDATE=1.
func TestGoldenLiveMeshInjected(t *testing.T) {
	styles.SetProfile(termenv.Ascii) // pin the profile (goldenFor convention)

	// Seed the /tmp/g1-full shape: operator ALSO attended + the 3 arc
	// agents, with the same edge topology as the canonical arc (operator
	// broadcast-hub edges + the two claude-code→confirmer quorum links).
	root := t.TempDir()
	for _, ag := range []string{"operator", "claude-code", "cursor", "data-analyst"} {
		writeMeshAttn(t, root, ag)
	}
	writeMeshDelivery(t, root, "operator", "claude-code", "1-glm0001")
	writeMeshDelivery(t, root, "operator", "cursor", "1-glm0002")
	writeMeshDelivery(t, root, "operator", "data-analyst", "1-glm0003")
	writeMeshDelivery(t, root, "claude-code", "cursor", "1-glm0004")
	writeMeshDelivery(t, root, "claude-code", "data-analyst", "1-glm0005")

	// The REAL read path (dedupe lives here): 4 nodes, one operator = hub.
	live := loadMesh(root, "operator")
	if len(live.Nodes) != 4 {
		t.Fatalf("opdedupe golden precondition: the /tmp/g1-full shape must "+
			"yield 4 nodes (operator folded into the hub, not duplicated), "+
			"got %d: %#v", len(live.Nodes), live.Nodes)
	}

	a, err := NewApp("/tmp/fake-root")
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	m, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app := m.(App)
	m, _ = app.Update(substrateLoadedMsg{rows: SubstrateThread})
	app = m.(App)
	m, _ = app.Update(meshLoadedMsg{mesh: live}) // the real deduped mesh
	app = m.(App)
	m, _ = app.Update(keyMsg("esc")) // compose → nav (golden harness convention)
	app = m.(App)
	m, _ = app.Update(keyMsg("2")) // fleet tab = the mesh
	app = m.(App)
	goldenFromView(t, "tui-v8-live-mesh.txt", app.View())
}
