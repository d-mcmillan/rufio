// fixtures.go — the Rufio customer:5821 churn-arc fixture set.
//
// RE-SCOPE (2026-05-15, PR-D): the prior fictional `V8_THREAD` mock
// (runner-h-prime / gemini-2-fde rolling back a staging deploy) is
// REPLACED with a coherent Rufio scenario — the canonical customer:5821
// churn investigation. Per docs/plans/2026-05-15-tui-v8-rebuild.md
// "PR-D — Rufio-domain re-map" §1 and docs/design/tui-v8-data-mapping.md
// §0 "Resolved by the 2026-05-15 re-scope", every screen now reads as
// OUR substrate and the scenario doubles as the launch story.
//
// The struct SHAPES (ThreadMsg / Quorum) are unchanged so chat.go (PR-B)
// keeps consuming them; only the DATA changed. New exported structs
// (FleetAgent / Channel / GoalCard / MemoryEntry / DecisionLineage) feed
// the new fleet / channels / goals / memory tabs and the lineage
// drill-down (PR-D §1).
//
// STATIC fixtures — live `rufio stream` / filesystem wiring landed in
// the PR-G arc (docs/design/tui-v8-data-mapping.md); these constants now
// also serve as the deterministic gate/golden injection data. (Was
// reachable only via the RUFIO_TUI_PREVIEW=1 gate with the legacy
// internal/tui path as the default; the G4 cutover, 2026-05-17, made v8
// the unconditional `rufio tui` and deleted the gate + legacy path.)
package tui

// Quorum mirrors the jsx `quorum: { yes: [...], total: N }` shape
// (rufio-bubbletea-v8.jsx line 49/58). Yes is the list of agent ids that
// have confirmed; Total is the quorum denominator. Quorum-dot POSITION
// is driven by QuorumOrder, not by the order of Yes (handoff §7.4: "the
// dot's position identifies the agent — don't sort by who voted").
//
// Re-scope note (resolves V8B-DATA1 / OPEN-2 FOR THE FIXTURE only): the
// decision row's Quorum uses Total:3 = Rufio's real AutoPromoteHandler
// threshold (≥3 distinct confirmers). The LIVE-data denominator (auto-
// promote /3 vs linked-agents /N) remains a PR-G product call — see
// docs/design/tui-v8-data-mapping.md §0 OPEN-2.
type Quorum struct {
	Yes   []string
	Total int
}

// ThreadMsg mirrors one entry of the old jsx `V8_THREAD` array shape
// (rufio-bubbletea-v8.jsx lines 43-59), now carrying Rufio substrate
// data. Field semantics:
//
//   - Who:    substrate agent id (drives the agent color via
//     styles.AgentColor). For op rows this is "operator".
//   - Role:   the Rufio thought TYPE / record kind (hypothesis,
//     observation, confirm, decision, …) — NOT a v8 prototype role
//     word. Rendered UPPERCASE in the row (data-mapping §0 OPEN-1).
//   - Time:   "HH:MM:SS" wall-clock string.
//   - Kind:   one of "op" | "plan" | "reply" (drives glyph/rail/indent).
//     A decision-type @thought is a "plan" row; observations/confirms
//     threaded under it are "reply" rows (data-mapping §1).
//   - Text:   the message body.
//   - Chips:  context-bundle refs / scoped entities (nil for non-plan
//     rows). For the decision row these are the @context-bundle refs.
//   - Quorum: optional quorum state (nil unless the decision requested
//     one); Total:3 = the AutoPromoteHandler threshold (see Quorum doc).
//   - Last:   true on the most recent row → renders a trailing caret.
//   - Lineage: optional drill-down payload, set ONLY on the decision row
//     (Role == "decision"). enter on that row opens the lineage overlay.
type ThreadMsg struct {
	Who     string
	Role    string
	Time    string
	Kind    string
	Text    string
	Chips   []string
	Quorum  *Quorum
	Last    bool
	Lineage *DecisionLineage
}

// Kind values for ThreadMsg.Kind. Named string consts (not iota) per the
// CLAUDE.md stack rule "no iota enums for user-facing string values".
const (
	kindOp    = "op"
	kindPlan  = "plan"
	kindReply = "reply"
)

// roleDecision is the ThreadMsg.Role value that marks a row as a
// `@thought type=decision` (data-mapping §0 OPEN-1). `enter` on a row
// with this Role opens the lineage drill-down (PR-D §4).
const roleDecision = "decision"

// DecisionLineage is the drill-down payload for the `@thought
// type=decision` substrate row. It is exactly what `rufio lineage <id>`
// renders: the decision header, its `@context-bundle` refs, and the
// numbered `@reason` chain (re-scope §1; PR-D §1/§4).
type DecisionLineage struct {
	ID        string   // decision thought-id, e.g. "1747-d29"
	Author    string   // agent that authored the decision
	Subject   string   // the decision subject, e.g. "customer:5821"
	Statement string   // the decision itself
	Time      string   // "HH:MM:SS" wall-clock
	Bundle    []string // @context-bundle refs (one per line in the overlay)
	Chain     []string // @reason chain (numbered list in the overlay)
}

// SubstrateThread is the canonical customer:5821 churn-arc conversation
// — the broadcast thought-stream rendered as the v8 chat (data-mapping
// §0 "substrate ↔ broadcast stream"). It REPLACES the old fictional
// V8Thread var (same []ThreadMsg type). The golden snapshot + the
// timestamp-suppression tests pin this exact rhythm.
//
// Arc: operator opens an investigation → claude-code hypothesizes churn
// risk → cursor + data-analyst observe → cursor confirms → claude-code
// decides (with quorum on auto-promote). The decision row carries the
// DecisionLineage drill-down payload.
var SubstrateThread = []ThreadMsg{
	{
		Who: "operator", Role: "operator", Time: "14:02:09", Kind: kindOp,
		Text: "investigate customer:5821 churn risk — rufio fleet",
	},
	{
		Who: "claude-code", Role: "hypothesis", Time: "14:02:11", Kind: kindPlan,
		Text:  "14-day silence, customer mentioned cancel — churn signals on customer:5821",
		Chips: []string{"customer:5821", "scope:fleet"},
	},
	{
		Who: "cursor", Role: "observation", Time: "14:02:12", Kind: kindReply,
		Text: "customer:5821 prefers email contact — prior preference on file",
	},
	{
		Who: "data-analyst", Role: "observation", Time: "14:02:13", Kind: kindReply,
		Text: "team usage 12→3 over 30d — contraction pattern, not hard churn",
	},
	{
		Who: "cursor", Role: "confirm", Time: "14:02:14", Kind: kindReply,
		Text: "confirming churn-risk hypothesis — aligns with email-only engagement",
	},
	{
		// chips: nil — DELIBERATE. v8 chips are plan SUB-TASK
		// decomposition tags (jsx: [snapshot][drain][revert][verify]).
		// Rufio decisions do not decompose into chip sub-tasks; the
		// context-bundle refs (given/refund-policy.md@v1,
		// learned/customer:5821) are NOT chips — they surface in the
		// lineage drill-down under "Context bundle:" via
		// DecisionLineage.Bundle below, which is where they belong.
		// Putting them here as long row-chips was both wrong (chips ≠
		// bundle refs) and obliterated the decision body under the PR-C
		// fit policy. Row chips are short entity/scope tags only (see the
		// hypothesis row's ["customer:5821","scope:fleet"]). Reconciled
		// in docs/design/tui-v8-data-mapping.md §3a (chips — RESOLVED).
		Who: "claude-code", Role: roleDecision, Time: "14:02:46", Kind: kindPlan,
		Text: "decision: offer downgrade, not churn-save discount — quorum on auto-promote",
		Quorum: &Quorum{
			Yes:   []string{"cursor", "data-analyst"},
			Total: 3,
		},
		Last:    true,
		Lineage: &decisionLineage5821,
	},
}

// decisionLineage5821 is the drill-down for SubstrateThread's decision
// row. ID/author/subject/statement/time mirror that row; Bundle = the
// @context-bundle refs; Chain = the numbered @reason chain `rufio
// lineage 1747-d29` would print (PR-D §1).
var decisionLineage5821 = DecisionLineage{
	ID:        "1747-d29",
	Author:    "claude-code",
	Subject:   "customer:5821",
	Statement: "offer downgrade, not churn-save discount",
	Time:      "14:02:46",
	Bundle: []string{
		"given/refund-policy.md@v1 (sha a3f8…)",
		"learned/customer:5821",
	},
	Chain: []string{
		"customer requested downgrade, not cancellation",
		"usage contraction confirmed by data-analyst (12→3/30d)",
		"policy: downgrade offers < $500 auto-approve",
		"decision: approve downgrade offer, no churn-save discount",
	},
}

// QuorumOrder is the canonical, stable agent order for quorum dots
// (handoff §7.4: the dot's POSITION identifies the agent, so rows
// iterate this slice — never q.Yes — so a confirmer that appears late
// in q.Yes still renders at its fixed slot). RE-SCOPE: these are the 3
// real Rufio agents in canonical position order (was the 6 fictional
// ids); the decision's Total:3 (AutoPromoteHandler threshold) means a
// full quorum fills all three slots.
var QuorumOrder = []string{
	"cursor", "data-analyst", "claude-code",
}

// ── New per-tab fixtures (PR-D §1) ────────────────────────────────────
//
// These feed the fleet / channels / goals / memory tabs. Shapes are
// deliberately simple, exported, and doc-commented; PR-G swaps them for
// live `live/attention` + `live/channels` + `live/goals` + `learned/`
// reads.

// FleetAgent is one row of the fleet tab: an agent with its current
// intent, the entities it is attending to, and when it was last seen.
// (Mesh visualisation of these is PR-E — not PR-D.)
type FleetAgent struct {
	ID       string // substrate agent id (drives the agent color)
	Intent   string // what the agent is currently doing
	Entities string // entities under attention, e.g. "customer:5821"
	LastSeen string // "HH:MM:SS" wall-clock of last activity
}

// FleetAgents is the customer:5821-arc fleet roster.
var FleetAgents = []FleetAgent{
	{ID: "claude-code", Intent: "churn investigation", Entities: "customer:5821", LastSeen: "14:02:46"},
	{ID: "cursor", Intent: "watching", Entities: "customer:5821", LastSeen: "14:02:14"},
	{ID: "data-analyst", Intent: "usage analysis", Entities: "customer:5821", LastSeen: "14:02:13"},
}

// ChannelSay is one `@say` message inside a private channel.
//
// NAME NOTE: the PR-D spec text names this `ChannelMsg`, but the OLD
// TUI's internal/tui/watch_panes.go already declares a (different-shape)
// `ChannelMsg` in this same Go package, and the add-only constraint
// forbids touching the old TUI. `ChannelSay` is the forced rename to
// avoid the same-package identifier collision (it also reads more
// precisely — a channel `@say` record). Reconciled in
// docs/design/tui-v8-data-mapping.md §3a (V8D-DATA1): a pure Go
// identifier disambiguation, no semantic divergence from the v8 design.
type ChannelSay struct {
	By   string // author agent id (drives the agent color)
	Text string // the message body
	Time string // "HH:MM:SS" wall-clock
}

// Channel is one Rufio private 1:1 channel (`summon`→`accept`→`say`),
// distinct from the broadcast substrate stream (data-mapping §2).
type Channel struct {
	ID     string       // channel id, e.g. "ch-1747-x1"
	Opener string       // the agent that summoned the channel
	Target string       // the agent that accepted
	Topic  string       // the channel topic / subject
	Msgs   []ChannelSay // the @say transcript
}

// ChannelThreads is the customer:5821-arc channel set (one channel).
var ChannelThreads = []Channel{
	{
		ID: "ch-1747-x1", Opener: "claude-code", Target: "data-analyst",
		Topic: "customer:5821",
		Msgs: []ChannelSay{
			{By: "claude-code", Text: "14-day silence, mentioned cancel", Time: "14:03:01"},
			{By: "data-analyst", Text: "team usage 12→3 in 30d. contraction, not churn", Time: "14:03:20"},
			{By: "claude-code", Text: "got it — proposing downgrade", Time: "14:03:34"},
		},
	},
}

// GoalCard is one coordination goal. Overlap is the `@goal-overlap`
// notification line (empty if the goal does not overlap another).
type GoalCard struct {
	ID        string // author agent id (the goal's author)
	Author    string // author agent id (drives the agent color)
	Statement string // the goal statement
	State     string // goal state, e.g. "active"
	Time      string // "HH:MM:SS" wall-clock
	Overlap   string // @goal-overlap notification line ("" if none)
}

// GoalCards is the customer:5821-arc goal set (two overlapping goals).
var GoalCards = []GoalCard{
	{
		ID: "claude-code", Author: "claude-code",
		Statement: "resolve customer:5821 churn risk",
		State:     "active", Time: "14:02:50",
		Overlap: "overlaps cursor — shared entity customer:5821",
	},
	{
		ID: "cursor", Author: "cursor",
		Statement: "improve customer:5821 onboarding re-engagement",
		State:     "active", Time: "14:02:55",
		Overlap: "overlaps claude-code — shared entity customer:5821",
	},
}

// MemoryEntry is one durable `learned/<entity>/*.gdlm` observation
// (subject / predicate / object), with its author and how long ago it
// was recorded.
type MemoryEntry struct {
	Subject   string // the observed entity, e.g. "customer:5821"
	Predicate string // the relation, e.g. "prefers"
	Object    string // the value, e.g. "email"
	Author    string // who recorded it (drives the agent color)
	Ago       string // human "time ago", e.g. "2m"
}

// MemoryEntries is the customer:5821-arc durable knowledge base.
var MemoryEntries = []MemoryEntry{
	{Subject: "customer:5821", Predicate: "prefers", Object: "email", Author: "cursor", Ago: "2m"},
	{Subject: "customer:5821", Predicate: "usage-trend", Object: "contraction", Author: "data-analyst", Ago: "1m"},
	{Subject: "customer:5821", Predicate: "tier", Object: "standard", Author: "initial-import", Ago: "2h"},
}

// ── Mesh fixture (PR-E §"Mesh") ───────────────────────────────────────
//
// AGENTS-ONLY product decision (damon, 2026-05-15): the mesh nodes are
// the customer:5821-arc participants — `operator` + the three substrate
// agents (claude-code / cursor / data-analyst) = 4 nodes. NO governance,
// NO fictional ids; the graph reads as OUR fleet.
//
// DATA-DRIVEN: MeshNodes is the single source of node identity and
// placement; the edges are DERIVED from SubstrateThread by
// deriveMeshEdges (NOT a hand-kept parallel list that can drift from the
// thread). Adding 1-2 more agents later is a DATA-ONLY change: append a
// MeshNode (id + grid coord + glyph) and add their rows to
// SubstrateThread — no mesh layout/algorithm change (damon will extend
// the live preview to 4-5 agents to demonstrate this).
//
// Coords are the 9×36 LANDSCAPE rail (jsx `<MeshGrid rows={9} cols={36}>`,
// line 325). The raw `MESH_NODES` in rufio-graphs.jsx use a bigger
// (r≤12 / c≤44) grid for 7 fictional ids; per data-mapping §0 OPEN-5
// "the visual is truth over raw numbers" the 4 Rufio nodes are RE-PLACED
// sensibly inside 9×36: operator as the central hub, the three agents
// fanned around it so every derived edge reads without crossing through
// a node. Origin is top-left; r ∈ [0,8], c ∈ [0,35].

// MeshNode is one node of the substrate mesh: an agent id (drives the
// node color via styles.AgentColor), its grid coordinate, and its glyph.
// Big marks the hub (operator) with a heavier glyph (jsx MESH_NODES
// `glyph:'◉'` for the hub vs `'●'` for spokes).
type MeshNode struct {
	ID    string // agent id (color via styles.AgentColor)
	R, C  int    // 9×36 grid coordinate (row, col), top-left origin
	Glyph string // node glyph: "◉" hub / "●" spoke
}

// MeshNodes is the agents-only substrate mesh (operator hub + the
// customer:5821-arc agents). Append here to extend the live preview —
// edges re-derive from SubstrateThread automatically (data-only).
//
// Placement (9 rows r∈[0,8], 36 cols c∈[0,35]): operator is the central
// hub; the three agents form a triangle around it so every derived edge
// (op↔each agent + the claude-code→confirmers quorum edges) reads as a
// clean radial fan. Node rows (1/4/7) are kept distinct from their label
// rows (2/3/6) so labels never sit on a node, and labels win over edge
// glyphs (buildMeshGrid) so the ids stay legible in the slim rail.
var MeshNodes = []MeshNode{
	{ID: "operator", R: 4, C: 18, Glyph: "◉"},     // central hub
	{ID: "claude-code", R: 1, C: 18, Glyph: "●"},  // top (hypothesis/decision)
	{ID: "cursor", R: 7, C: 5, Glyph: "●"},        // lower-left (observation/confirm)
	{ID: "data-analyst", R: 7, C: 31, Glyph: "●"}, // lower-right (observation)
}

// meshNodeIndex maps an agent id to its MeshNodes slot (or -1). Used by
// deriveMeshEdges so a SubstrateThread author with no mesh node simply
// produces no edge (defensive — the fixture keeps them in sync).
func meshNodeIndex(id string) int {
	for i, n := range MeshNodes {
		if n.ID == id {
			return i
		}
	}
	return -1
}

// deriveMeshEdges derives the mesh edges from SubstrateThread (data-
// mapping §0 OPEN-3 "edges derived from routing deliveries" — the
// fixture encoding: every reply/confirm threads back to the operator who
// opened the investigation, and the decision author links to each
// confirmer). It returns unique {a,b} index pairs into MeshNodes so the
// graph is a pure projection of the thread — no drift-prone parallel
// list. Concretely for the customer:5821 arc:
//
//   - operator opened the thread (op row) → every agent that posted
//     under it links to operator (the broadcast hub edge).
//   - the decision author (claude-code) links to each of its quorum
//     confirmers (cursor, data-analyst) — the quorum relationship.
//
// Self-edges and duplicates are dropped. The result is stable-ordered
// (operator hub edges first, then quorum edges) for deterministic
// rendering / goldens.
func deriveMeshEdges() [][2]int {
	var operatorIdx = -1
	for _, m := range SubstrateThread {
		if m.Kind == kindOp {
			if i := meshNodeIndex(m.Who); i >= 0 {
				operatorIdx = i
			}
			break
		}
	}

	seen := map[[2]int]bool{}
	var edges [][2]int
	add := func(a, b int) {
		if a < 0 || b < 0 || a == b {
			return
		}
		if a > b {
			a, b = b, a
		}
		key := [2]int{a, b}
		if seen[key] {
			return
		}
		seen[key] = true
		edges = append(edges, key)
	}

	// 1. Broadcast hub edges: every non-operator author that posted
	//    under the operator's thread links to the operator.
	if operatorIdx >= 0 {
		for _, m := range SubstrateThread {
			if m.Kind == kindOp {
				continue
			}
			add(operatorIdx, meshNodeIndex(m.Who))
		}
	}

	// 2. Quorum edges: the decision author links to each confirmer that
	//    voted in the decision's quorum.
	for _, m := range SubstrateThread {
		if m.Role != roleDecision || m.Quorum == nil {
			continue
		}
		author := meshNodeIndex(m.Who)
		for _, voter := range m.Quorum.Yes {
			add(author, meshNodeIndex(voter))
		}
	}
	return edges
}
