// live_substrate.go — PR-G1: the live substrate-chat read path.
//
// SCOPE (docs/plans/2026-05-15-tui-v8-rebuild.md "### PR-G"; the G1
// slice): wire the broadcast thought-stream → the v8 substrate chat
// panel (live, read-only) + resolve OPEN-2 at this render boundary. The
// mesh / channels / goals / memory tabs stay on fixtures.go (G2/G3);
// operator→agent SEND is a later slice.
//
// This file is the BRIDGE between the retained on-disk substrate and the
// pure G0 projection. It does NOT re-implement projection — it sources
// the flat ordered broadcast feed and the confirm tallies, then calls
// the EXISTING projectThread (project.go) and applies the locked OPEN-2
// denominator. Everything is a pure function of `root` (the on-disk
// state) + an injected `now` — NO time.Now(), NO fsnotify, NO rand here;
// the live fsnotify/tea.Tick wiring is in app.go's Init/Update only, and
// every test injects disk state + reads it back deterministically.
//
// FEED SOURCING (data-mapping §0 "substrate ↔ broadcast stream —
// RESOLVED" + §1 :113): the v8 substrate chat == the broadcast
// thought-stream. The canonical broadcast log is live/outbox/ (every
// agent's broadcast @thought/@observation/@reason; live/inbox/ is
// private routing copies — explicitly excluded, data-mapping §2). We
// read it through the SAME stream lib `rufio stream` uses
// (stream.EmitCatchUp, stream.go:163) — one stream.Event per record,
// exactly the shape G0's projectThread consumes — rather than
// re-deriving a parse. EmitCatchUp's filepath.WalkDir order is
// directory/lexical, NOT chronological, so we re-sort by the thought-id
// unix-millis prefix (the canonical write-path stamp, watch.go's
// stampFromID — D5.10) so the chat reads top-to-bottom in time order
// like the fixture (fixtures.go SubstrateThread rhythm).
//
// OPEN-2 RESOLUTION (LOCKED 2026-05-16 — applied HERE, the render
// layer; OPEN-2 follow-up #131, 2026-05-18 — see below): projectThread
// deliberately leaves Quorum.Total = 0 (G0 deferred the denominator as a
// product call — project.go:238-242, data-mapping §0 OPEN-2). G1 sets
// Quorum.Total = autopromote.MinDistinctConfirmers (the real
// auto-promote threshold — ≥3 distinct confirmers, autopromote.go:48;
// referenced as the constant, NEVER a literal 3) so a confirmed thought
// reads "confirms toward auto-promote" (e.g. ●●○ 2/3). Quorum.Yes is the
// sorted-deduped confirm tally projectThread already populated from
// confirm.ReadAll (confirm.go:120-121). This resolves data-mapping §0
// OPEN-2 / §1 :118 for live data (the PR-D fixture had pre-modelled /3;
// G1 makes the live path agree, via the constant not a copy of the
// literal).
//
// OPEN-2 FOLLOW-UP (#131, 2026-05-18): the original scoping rendered
// quorum dots on `type:decision` thoughts only. That was a PROJECTION
// choice, never an engine constraint — the auto-promote engine is
// type-agnostic (autopromote.MinDistinctConfirmers distinct confirmers,
// conf ≥0.85, on ANY thought). The projection now matches the engine:
// quorum dots render on ANY confirmed thought (focus/hypothesis/
// observation/question/decision). The ≥1-confirmer guard is RETAINED
// (loadConfirmTallies only registers ids with ≥1 confirm), so an
// unconfirmed thought of any type still shows NO dot row (no `0/3`
// clutter) — exactly as before. Glyphs/spacing/counter/colour and the
// auto-promote engine are UNCHANGED; only the set of rows that get a
// Quorum broadened. See data-mapping §0 OPEN-2 follow-up.
package tui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"sort"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/autopromote"
	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	"github.com/d-mcmillan/rufio/internal/lib/stream"
)

// substrateBroadcastDirs is the subtree the v8 substrate chat reads. ONLY
// live/outbox/ — the public broadcast log. live/inbox/ (private routing
// copies), learned/ (promoted observations) and live/promoted/ are
// deliberately EXCLUDED from the substrate chat per data-mapping §2 ("the
// v8 substrate screen is wired to the broadcast stream only"). This is a
// strict subset of `rufio stream`'s dirs (cli/stream.go:73) — the chat
// is the broadcast, not the full event firehose.
var substrateBroadcastDirs = []string{"live/outbox"}

// loadSubstrate is the pure G1 read path: it reads the on-disk broadcast
// log, orders it chronologically, tallies confirms for every decision,
// projects via the shared projectThread, and resolves OPEN-2. Returns
// the []ThreadMsg the substrate chat panel renders. A missing/empty
// substrate yields an empty slice (NOT nil-panic) — that empty result is
// itself the "fresh/empty" cold-start signal the App keys its setup hint
// off of. `now` is injected (never time.Now) so the render is
// deterministic in tests.
func loadSubstrate(root, operatorID string, now time.Time) []ThreadMsg {
	rows, _ := loadSubstrateWithIDs(root, operatorID, now)
	return rows
}

// loadSubstrateWithIDs is loadSubstrate plus the PR-G3 lineage-id carry:
// it also returns, parallel to the projected rows, the @thought `id` of
// each row's source stream.Event (substrateRowIDs, live_tabs.go — the
// data-mapping §1 :115 decision-row id). rows[i] and ids[i] are derived
// from the SAME ordered `events` slice in this one call so they CANNOT
// drift (projectThread emits exactly one row per event, in order —
// project.go:202-247). G0 projectThread is NOT modified (project*.go is
// byte-unchanged); the id is extracted post-projection via the G0
// thoughtID helper, exactly as applyQuorumThreshold sets the OPEN-2
// denominator post-projection — both are render-boundary annotations G0
// deliberately deferred. The App carries `ids` alongside a.substrate so
// `enter` on a decision row resolves projectLineage(root, ids[selected])
// VERBATIM. loadSubstrate keeps its original signature (callers that do
// not need the ids are unchanged).
func loadSubstrateWithIDs(root, operatorID string, now time.Time) ([]ThreadMsg, []string) {
	rows, ids, _ := loadSubstrateAll(root, operatorID, now)
	return rows, ids
}

// loadSubstrateAll is loadSubstrateWithIDs plus the G-interact subject
// carry: it also returns, parallel to the projected rows, each row's
// source @thought `subject` (substrateRowSubjects — the G0 rawField
// helper, NOT a second hand-rolled parse). rows[i], ids[i] and
// subjects[i] are all derived from the SAME ordered `events` slice in
// this one call so the three CANNOT drift (projectThread emits exactly
// one row per event, in order — project.go). G0 projectThread is NOT
// modified; the subject is a post-projection render-boundary annotation
// EXACTLY like applyQuorumThreshold (OPEN-2) and substrateRowIDs (the
// G3 lineage carry). The App carries `subjects` alongside a.substrate so
// resolveBroadcastSubject can default a free-text broadcast to the
// focused entity.
func loadSubstrateAll(root, operatorID string, now time.Time) ([]ThreadMsg, []string, []string) {
	events := loadSubstrateEvents(root)
	tallies := loadConfirmTallies(root, events)
	rows := projectThread(events, tallies, operatorID, now)
	applyQuorumThreshold(rows)
	return rows, substrateRowIDs(events), substrateRowSubjects(events)
}

// substrateRowSubjects returns, parallel to the projected rows, the
// `subject` field of each row's source stream.Event (rawField(ev,
// "subject") — the SAME G0 parse projectThread/substrateRowIDs use,
// project.go). One entry per event in order (projectThread emits one row
// per event) so it stays exactly parallel to a.substrate. A non-@thought
// record or a subject-less event yields "" at that index (handled by
// resolveBroadcastSubject's fallback chain).
func substrateRowSubjects(events []stream.Event) []string {
	subs := make([]string, len(events))
	for i, ev := range events {
		subs[i] = rawField(ev, "subject") // G0 helper (project.go) — same parse path
	}
	return subs
}

// loadSubstrateEvents reads live/outbox/ through stream.EmitCatchUp (the
// SAME lib `rufio stream`/`rufio listen` use — we do NOT hand-roll the
// record parse), decodes the JSONL into []stream.Event, and re-orders it
// chronologically by the thought-id unix-millis prefix (EmitCatchUp's
// walk order is directory/lexical, not time order). An empty/absent
// outbox → empty slice (EmitCatchUp silently skips a missing dir,
// stream.go:168-169 — a fresh project is not an error).
func loadSubstrateEvents(root string) []stream.Event {
	var buf bytes.Buffer
	// FilterParams is zero-value: the chat shows the WHOLE broadcast
	// (every @thought/@observation/@reason/@confirm record); the v8 row
	// renderer decides what each kind looks like. No scope/type filter
	// (Match passes everything on empty FilterParams, stream.go:61-82).
	if err := stream.EmitCatchUp(&buf, root, substrateBroadcastDirs, stream.FilterParams{}); err != nil {
		// EmitCatchUp aborts on a malformed file (one-shot replay
		// posture, stream.go:160-162). The TUI is a read-only console: a
		// single bad record must not blank the chat, so degrade to
		// whatever decoded cleanly so far rather than surface an error.
		// (The watcher re-reads on the next event; a transient bad file
		// self-heals.)
		return decodeEvents(buf.Bytes())
	}
	return decodeEvents(buf.Bytes())
}

// decodeEvents parses the JSONL EmitCatchUp wrote (one stream.Event per
// line, stream.go:164) and returns the chronologically-ordered feed.
// Ordering: by the thought-id unix-millis prefix (stampFromID, the
// canonical write-path stamp — watch.go), tie-broken by Path so the feed
// is fully deterministic for goldens even for prefix-less / same-stamp
// ids. A blank/garbled line is skipped (the same best-effort posture as
// the rest of the read path).
func decodeEvents(jsonl []byte) []stream.Event {
	var out []stream.Event
	sc := bufio.NewScanner(bytes.NewReader(jsonl))
	// stream.Event.Raw can be long; raise the scanner buffer well past
	// the default 64KB so a big record line is never silently dropped.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev stream.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // skip a garbled line; never blank the whole chat
		}
		out = append(out, ev)
	}
	sortEventsChrono(out)
	return out
}

// sortEventsChrono orders events by the @thought-id unix-millis prefix
// ascending (oldest first → the chat reads top-to-bottom in time, like
// SubstrateThread). Stable + Path tie-break so prefix-less ids (stamp 0)
// and same-millisecond writes never reorder between renders (golden
// determinism). The id lives on the raw GDL line (stream.Event projects
// a fixed column set that omits `id`, stream.go:34-45), reused via
// rawField (project.go) — NOT a second hand-rolled parse.
func sortEventsChrono(evs []stream.Event) {
	sort.SliceStable(evs, func(i, j int) bool {
		si := stampFromID(thoughtID(evs[i]))
		sj := stampFromID(thoughtID(evs[j]))
		if si != sj {
			return si < sj
		}
		return evs[i].Path < evs[j].Path
	})
}

// loadConfirmTallies builds the per-thought-id confirm Tally map
// projectThread consumes (project.go:230). It tallies EVERY thought-id
// present in the feed — NOT decision-only. #131 (2026-05-18): the
// auto-promote ENGINE is type-agnostic (autopromote.MinDistinctConfirmers
// distinct confirmers on ANY thought — focus/hypothesis/observation/
// question/decision), so the quorum-dot PROJECTION follows it: any
// confirmed thought gets a tally, not just decisions. The
// `ev.Type != "thought"` guard stays — confirms target @thought ids
// only; a non-@thought record (@observation/@confirm/…) is not a tally
// key. confirm.ReadAll returns a sorted+deduped Tally and treats a
// missing file as an empty Tally (confirm.go:86-93) — so a thought with
// no confirms simply gets no map entry and projectThread leaves its
// Quorum nil (history renders with no dots; never gated).
func loadConfirmTallies(root string, events []stream.Event) map[string]confirm.Tally {
	tallies := make(map[string]confirm.Tally)
	for _, ev := range events {
		if ev.Type != "thought" {
			continue
		}
		id := thoughtID(ev)
		if id == "" {
			continue
		}
		if _, done := tallies[id]; done {
			continue
		}
		t, err := confirm.ReadAll(root, id)
		if err != nil {
			continue // unreadable tally → no dots for this row (best-effort)
		}
		// ≥1-confirm guard (RETAINED verbatim under #131): only register
		// a tally with at least one confirmer — an empty Tally would make
		// projectThread render a 0/3 dot row for a thought nobody has
		// confirmed yet. data-mapping §1 :118: the dots are confirmers
		// ACCUMULATING toward the threshold; zero confirmers ⇒ no quorum
		// affordance (Quorum stays nil, like a thought with no confirms
		// file at all). Broadening the projection to ANY confirmed
		// thought does NOT relax this — an unconfirmed focus/hypothesis/
		// question shows NO dot row, exactly as an unconfirmed decision
		// did (no `0/3` clutter).
		if len(t.Confirms) == 0 {
			continue
		}
		tallies[id] = t
	}
	return tallies
}

// applyQuorumThreshold is the OPEN-2 resolution (LOCKED 2026-05-16),
// applied at the render boundary. projectThread leaves Quorum.Total = 0
// (G0 deferred the denominator — project.go:238-242). For EVERY row that
// carries a Quorum — i.e. ANY confirmed thought, NOT decision-only since
// #131 (2026-05-18) — set Total = autopromote.MinDistinctConfirmers (the
// real auto-promote threshold — autopromote.go:48; referenced as the
// CONSTANT, never a hardcoded 3) so the dot row reads "confirms toward
// auto-promote" (e.g. ●●○ 2/3). The auto-promote engine is type-agnostic
// (the same threshold gates any confirmed thought), so the denominator
// is the same for a confirmed focus/hypothesis/question as for a
// decision. Quorum.Yes is left exactly as projectThread set it (the
// sorted-deduped confirm tally). Rows without a Quorum (an unconfirmed
// thought of any type — loadConfirmTallies only registers ids with ≥1
// confirm, so projectThread leaves them nil) are untouched: the
// `Quorum == nil` guard preserves the no-`0/3`-clutter property
// transitively (the threshold is meaningless without confirmers).
func applyQuorumThreshold(rows []ThreadMsg) {
	for i := range rows {
		if rows[i].Quorum == nil {
			continue
		}
		rows[i].Quorum.Total = autopromote.MinDistinctConfirmers
	}
}
