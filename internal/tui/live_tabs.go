// live_tabs.go — PR-G3: the live read path for the channels / goals /
// memory tabs (and the data side of the lineage drill-down's id carry).
//
// SCOPE (docs/plans/2026-05-15-tui-v8-rebuild.md "### PR-G", the G3
// slice): make the 4 remaining fixture-backed surfaces LIVE — the
// channels tab, the goals tab, the memory tab, and the per-decision
// lineage drill-down. The substrate chat (G1) and the mesh (G2) are
// already live and stay unbroken. NO operator→agent send / slash
// commands (later). (Was preview-only behind RUFIO_TUI_PREVIEW with the
// legacy tui.Model as the default; the cutover + old-TUI deletion
// happened later in G4, 2026-05-17 — v8 is now the unconditional
// `rufio tui`.)
//
// This file is the BRIDGE between the retained on-disk substrate and the
// v8 tab fixture-shaped display structs — exactly mirroring
// live_substrate.go's role for the chat and live_mesh.go's role for the
// mesh. It does NOT re-implement projection or record parsing:
//
//   - memory: REUSES the G0 walkLearned(root, now) VERBATIM
//     (project_walk.go) — the learned/ → []MemoryEntry projection,
//     `now` injected (never time.Now here);
//   - channels/goals: sourced via the RETAINED watcher's already-emitted
//     pane Msgs (ChannelMsg / ChannelMessageMsg / GoalMsg / InboxMsg —
//     watch_panes.go, BYTE-UNCHANGED) folded through the EXISTING
//     InitialWalkPanes(root, me) disk enumeration (the SAME canonical
//     full-disk read the old tui.go Model hydrates from, tui.go:157) and
//     mapped to the v8 fixture structs (Channel / ChannelSay / GoalCard)
//     field-identically so the tab renderers (tabs.go) are unchanged;
//   - lineage: the drill-down resolves via the G0 projectLineage(root,
//     <thought-id>) VERBATIM (project.go). The thought-id is carried on
//     the live decision ThreadMsg by annotateThoughtIDs at the G1 render
//     boundary (NOT inside G0 projectThread — project*.go is consumed
//     byte-unchanged; this is the data-mapping §1 :115 decision-row id,
//     threaded minimally exactly like applyQuorumThreshold annotates the
//     OPEN-2 denominator post-projection).
//
// project.go / project_walk.go are CONSUMED VERBATIM (`git diff` on them
// is EMPTY). watch.go / watch_panes.go are BYTE-UNCHANGED (the pane Msgs
// + InitialWalkPanes + the pane watches already exist — PR #23 / G0).
//
// Everything here is a pure function of `root` (the on-disk state) + the
// resolved operator id + an injected `now` — NO time.Now(), NO fsnotify,
// NO rand. The live fsnotify/tea wiring is in app.go's Init/Update only;
// every test injects a deterministic tabsLoadedMsg (the G1/G2 pattern)
// rather than touching the watcher / wall-clock. The memory tab's
// relative-time is deterministic because `now` is injected.
package tui

import (
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
	"github.com/d-mcmillan/rufio/internal/lib/stream"
)

// tabState is the live-projected state the channels/goals/memory tab
// renderers consume — the tab analogue of live_substrate.go's
// []ThreadMsg and live_mesh.go's meshState. It carries EXACTLY the v8
// fixture-shaped structs (Channel / GoalCard / MemoryEntry, fixtures.go)
// so tabs.go's renderers are fed live data unchanged (data-source-
// agnostic — the renderers do not know fixture from live). Produced by
// loadTabs, folded through the single tabsLoadedMsg seam, read by
// renderChannelsTab / renderGoalsTab / renderMemoryTab. The zero value
// (all nil) is a valid "nothing on disk yet" state (a fresh project) —
// the renderers already handle empty slices (tabs.go:91 "(no channels)").
type tabState struct {
	// Channels is the live private-channel set (active only — closed/
	// archived are excluded to match the old TUI, tui.go via
	// InitialWalkPanes which walks active/ only, watch_panes.go:292).
	// Sorted by CreatedAt descending (newest first) to match the old
	// TUI's filteredChannels (render_channels.go:57-59).
	Channels []Channel
	// Goals is the live coordination-goal set, sorted by the old TUI's
	// precedence (state: active<completed<abandoned, then ts descending —
	// render_goals.go:56-63) so the cards read in the same order.
	Goals []GoalCard
	// Memory is the live learned/ observation set (G0 walkLearned —
	// already sorted by (subject, ts, path) for golden determinism,
	// project_walk.go:117-125).
	Memory []MemoryEntry
}

// goalStatePrecedence mirrors the old TUI's statePrecedence
// (render_goals.go:20-24) so the live goals tab orders cards exactly as
// the old `rufio tui` did (active first). Kept local (the old map is in
// render_goals.go which the add-only constraint forbids touching).
var goalStatePrecedence = map[goal.State]int{
	goal.StateActive:    0,
	goal.StateCompleted: 1,
	goal.StateAbandoned: 2,
}

// loadTabs is the pure G3 read path for the three list tabs: it does a
// full deterministic disk re-read (the SAME canonical enumeration the
// old tui.go Model hydrates from — InitialWalkPanes for channels/goals/
// inbox + the G0 walkLearned for memory) and maps the result to the v8
// fixture-shaped structs. A missing/empty substrate yields empty slices
// (NOT nil-panic) — the renderers handle empty. NEVER gated on a live
// daemon (reads on-disk channels/goals/learned directly, exactly like
// loadSubstrate / loadMesh). `now` is injected (never time.Now) so the
// memory tab's relative-time ("2m"/"1h") is deterministic in tests.
//
// Re-read (not incremental merge): disk is the single source of truth
// and channels.LoadMeta / goal.ReadAll / walkLearned are idempotent, so
// — exactly like loadSubstrate / loadMesh — there is no dup/order/merge
// bug class. The retained watcher's pane Msgs are the CHANGE SIGNAL that
// triggers a re-read; the records they carry are not merged in piecemeal
// (that is the old TUI's incremental-cache model; G1/G2/G3 are
// disk-truth-re-read).
func loadTabs(root, me string, now time.Time) tabState {
	walkMsgs := InitialWalkPanes(root, me) // the canonical full-disk pane read (watch_panes.go:289)
	return tabState{
		Channels: projectChannels(walkMsgs),
		Goals:    projectGoals(walkMsgs),
		Memory:   loadMemory(root, now),
	}
}

// loadMemory is the memory tab's live read: a thin best-effort WRAPPER
// that DELEGATES to / REUSES the G0 walkLearned(root, now) VERBATIM
// (project_walk.go) — the learned/<entity>/*.gdlm → []MemoryEntry
// projection (subject/predicate/object/author + tsToAgo(ts, now)). G0 is
// REUSED, not re-implemented (the per-record parse-skip best-effort lives
// INSIDE walkLearned; the verified output-identity is asserted by
// TestLoadTabs_MemoryReusesG0WalkLearnedVerbatim's reflect.DeepEqual).
// The wrapper adds exactly ONE thing: on a walk-level error it returns
// nil rather than propagating — the read-only-console degrade (a
// transient unreadable learned/ subtree must not blank the memory tab;
// the watcher re-reads on the next pane event and it self-heals),
// mirroring loadSubstrateEvents's identical best-effort posture. `now`
// is injected so the relative-time column is deterministic in tests.
func loadMemory(root string, now time.Time) []MemoryEntry {
	rows, err := walkLearned(root, now) // G0 reused VERBATIM (project_walk.go:60)
	if err != nil {
		return nil
	}
	return rows
}

// projectChannels maps the pane-walk's ChannelMsg/ChannelMessageMsg
// stream (watch_panes.go — the SAME Msgs the live watcher emits) to the
// v8 Channel/ChannelSay structs field-identically (fixtures.go:223-237)
// so renderChannelsTab (tabs.go:90) is unchanged. The transcript is
// assembled per-channel, de-duplicated on message id (the old TUI's
// appendChannelMessage idempotence, tui.go:463-471) and ordered by the
// @say ts then id so the transcript reads top-to-bottom deterministically
// (golden-stable — InitialWalkPanes's os.ReadDir order is lexical, not
// chronological). Closed/archived channels are already excluded because
// InitialWalkPanes walks live/channels/active/ ONLY (watch_panes.go:292
// "closed channels are out of scope per D23.10") — matching the old TUI.
// List order is CreatedAt descending (newest first) to match the old
// TUI's filteredChannels (render_channels.go:57-59).
func projectChannels(walkMsgs []tea.Msg) []Channel {
	type acc struct {
		ch   channels.Channel
		msgs []ChannelMessage
		seen map[string]bool
	}
	byID := map[string]*acc{}
	var order []string // channel-id first-seen order (stable before the final sort)

	for _, msg := range walkMsgs {
		switch v := msg.(type) {
		case ChannelMsg:
			a := byID[v.Channel.ID]
			if a == nil {
				a = &acc{seen: map[string]bool{}}
				byID[v.Channel.ID] = a
				order = append(order, v.Channel.ID)
			}
			a.ch = v.Channel
		case ChannelMessageMsg:
			a := byID[v.ChannelID]
			if a == nil {
				a = &acc{seen: map[string]bool{}}
				byID[v.ChannelID] = a
				order = append(order, v.ChannelID)
			}
			if v.Message.ID != "" && a.seen[v.Message.ID] {
				continue // idempotent on message id (tui.go:464-469)
			}
			if v.Message.ID != "" {
				a.seen[v.Message.ID] = true
			}
			a.msgs = append(a.msgs, v.Message)
		}
	}

	out := make([]Channel, 0, len(order))
	for _, id := range order {
		a := byID[id]
		// Deterministic transcript order: ts then id (a re-read's ReadDir
		// order is lexical, not chronological — the same determinism
		// posture as sortEventsChrono / walkLearned's sort).
		sort.SliceStable(a.msgs, func(i, j int) bool {
			if a.msgs[i].TS != a.msgs[j].TS {
				return a.msgs[i].TS < a.msgs[j].TS
			}
			return a.msgs[i].ID < a.msgs[j].ID
		})
		says := make([]ChannelSay, 0, len(a.msgs))
		for _, m := range a.msgs {
			says = append(says, ChannelSay{
				By:   m.By,
				Text: m.Content,
				Time: tsToClock(m.TS), // G0 helper (project.go:74) — "HH:MM:SS"
			})
		}
		out = append(out, Channel{
			ID:     a.ch.ID,
			Opener: a.ch.Opener,
			Target: a.ch.Target,
			Topic:  a.ch.Topic,
			Msgs:   says,
		})
	}
	// CreatedAt descending (newest first) — match the old TUI
	// (render_channels.go:57-59). Stable + id tie-break so a missing/
	// equal CreatedAt never reorders between renders (golden determinism).
	sort.SliceStable(out, func(i, j int) bool {
		ci := byID[out[i].ID].ch.CreatedAt
		cj := byID[out[j].ID].ch.CreatedAt
		if ci != cj {
			return ci > cj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// projectGoals maps the pane-walk's GoalMsg/InboxMsg stream to the v8
// GoalCard structs field-identically (fixtures.go:254-261) so
// renderGoalsTab (tabs.go:127) is unchanged. The overlap line is
// formatted from the STRUCTURED InboxOverlap record (NOT a pre-rendered
// string) keyed by the TARGET-goal-id with a SourceGoalID fallback —
// EXACTLY the old TUI's keying (tui.go:431-443 "we store under
// target-goal because that's the goal the recipient typically owns"). A
// goal's first matching overlap becomes its GoalCard.Overlap (one line —
// the v8 card shows a single ⤷ overlap line, tabs.go:140-142). Order
// matches the old TUI's filteredGoals (state precedence then ts
// descending — render_goals.go:56-63).
func projectGoals(walkMsgs []tea.Msg) []GoalCard {
	var goals []goal.Goal
	// overlaps keyed exactly like the old TUI's m.inboxOverlaps
	// (tui.go:438-442): target-goal-id, falling back to source-goal-id.
	overlapsByGoal := map[string][]InboxOverlap{}

	for _, msg := range walkMsgs {
		switch v := msg.(type) {
		case GoalMsg:
			goals = append(goals, v.Goal)
		case InboxMsg:
			o := v.Overlap
			if o.SourceGoalID == "" && o.TargetGoalID == "" {
				continue
			}
			key := o.TargetGoalID
			if key == "" {
				key = o.SourceGoalID
			}
			overlapsByGoal[key] = appendUniqueOverlap(overlapsByGoal[key], o)
		}
	}

	// Old-TUI sort: state precedence, then ts descending
	// (render_goals.go:56-63). Stable + id tie-break for golden
	// determinism (ReadAll's traversal order is dir-lexical).
	sort.SliceStable(goals, func(i, j int) bool {
		pi := goalStatePrecedence[goals[i].State]
		pj := goalStatePrecedence[goals[j].State]
		if pi != pj {
			return pi < pj
		}
		if goals[i].TS != goals[j].TS {
			return goals[i].TS > goals[j].TS
		}
		return goals[i].ID < goals[j].ID
	})

	out := make([]GoalCard, 0, len(goals))
	for _, g := range goals {
		card := GoalCard{
			ID:        g.Author,
			Author:    g.Author,
			Statement: g.Statement,
			State:     string(g.State),
			Time:      tsToClock(g.TS), // G0 helper — "HH:MM:SS"
			Overlap:   formatOverlapLine(overlapsByGoal[g.ID]),
		}
		out = append(out, card)
	}
	return out
}

// formatOverlapLine renders the v8 GoalCard.Overlap string FROM the
// structured InboxOverlap record (data-mapping: the v8 card expects a
// single pre-rendered overlap line; the structured record is the source
// of truth). Empty when the goal has no overlap (the card shows no ⤷
// line, tabs.go:140). The phrasing mirrors the v8 fixture's intent
// ("overlaps <peer> — shared entity <entity>", fixtures.go:269) so the
// live render reads identically to the eyeballed fixture; the peer is
// the overlap's `from` (the other agent) and the entity is the shared
// `entity` (the @goal-overlap record fields, watch_panes.go:274-281). If
// multiple overlaps exist the FIRST (deterministically ordered by
// appendUniqueOverlap insertion) is shown — the v8 card is one line.
func formatOverlapLine(overlaps []InboxOverlap) string {
	if len(overlaps) == 0 {
		return ""
	}
	o := overlaps[0]
	var b strings.Builder
	b.WriteString("overlaps ")
	b.WriteString(o.From)
	if o.Entity != "" {
		b.WriteString(" — shared entity ")
		b.WriteString(o.Entity)
	}
	return b.String()
}

// substrateRowIDs is the lineage drill-down's id carry, computed at the
// G1 render boundary (NOT inside G0 projectThread — project*.go is
// consumed byte-unchanged; ThreadMsg/fixtures.go is NOT modified). It
// mirrors applyQuorumThreshold (live_substrate.go:203) in spirit: a
// post-projection derivation the live path needs but G0 deliberately
// left for the render layer. It returns, per substrate row, the @thought
// `id` of that row's source stream.Event (data-mapping §1 :115 — the
// decision-row id) so `enter` on a decision row can resolve
// projectLineage(root, id) VERBATIM. The slice is parallel to the
// projected []ThreadMsg: projectThread emits EXACTLY one row per event,
// in order (project.go:202-247), so index i of this slice is the id of
// substrate row i. The id is extracted via the G0 thoughtID helper
// (project.go:277 — the SAME parse projectThread uses internally), never
// a second hand-rolled split. A non-@thought event yields "" at its
// index (the drill-down only opens on a decision row, whose source IS a
// @thought, so a "" there is impossible by construction; the guard in
// app.go handles "" defensively regardless). The App carries this
// alongside a.substrate so the carry lives ENTIRELY in the live path
// (app.go + live_substrate.go + live_tabs.go) — no ThreadMsg field, no
// fixtures.go touch. Built from the SAME ordered events loadSubstrate
// projects (live_substrate.go) so rows and ids cannot drift.
func substrateRowIDs(events []stream.Event) []string {
	ids := make([]string, len(events))
	for i, ev := range events {
		ids[i] = thoughtID(ev) // G0 helper (project.go:277) — same parse projectThread uses
	}
	return ids
}
