// project.go — PR-G0: the PURE, headless, decision-independent
// substrate→display projection layer.
//
// SCOPE (docs/plans/2026-05-15-tui-v8-rebuild.md "### PR-G", this is the
// G0 slice): these functions read the on-disk Rufio substrate and
// produce EXACTLY the existing v8 display structs declared in
// fixtures.go (ThreadMsg / Quorum / MemoryEntry / MeshNode / the
// deriveMeshEdges-shaped pair / DecisionLineage). They are NOT wired
// into App/Init/Update/View here — that fixture→projection cutover is
// the LATER G1 slice. G0 builds + unit-tests the shape reconciliation
// headless so the risky part is proven before any UI touches it.
//
// HARD CONSTRAINTS honoured here (handoff "Hard constraints"):
//
//   - PURE / deterministic: every function is a pure function of its
//     inputs (or a `root` path) plus an INJECTED `now time.Time`. There
//     is NO time.Now() / math/rand / wall-clock read anywhere in this
//     file — tests inject `now` so goldens are stable.
//   - ADD-ONLY (at the G0 slice): this file + project_test.go were the
//     only new files; no existing file (fixtures.go, app.go,
//     messages.go, internal/cli, the then-legacy TUI) was modified. (The
//     `rufio tui` default was later flipped to v8 at the G4 cutover,
//     2026-05-17 — that is outside this file's add-only scope.)
//   - Reuse the substrate libs (stream/attention/routing-format/confirm/
//     observation/lineage/versioning/paths) — record parsing is NOT
//     re-implemented; we go through gdl.ParseDocument and the libs'
//     own readers/types.
//
// DEFERRED PRODUCT DECISIONS (explicitly NOT decided here):
//
//   - OPEN-2 (quorum denominator): Quorum.Total is left as the raw
//     confirmer-relevant value 0 — the X/Y denominator (auto-promote /3
//     vs linked-agents /N) is an unresolved product call applied at
//     RENDER time in G1, NOT baked in here. See projectThread.
//   - OPEN-4 (operator/presence node policy): projectMeshNodes does NOT
//     synthesize an operator node or decide presence; it projects only
//     the agents that actually have an attention record. The operator-
//     node policy is the G2 product decision.
//
// data-mapping references throughout cite
// docs/design/tui-v8-data-mapping.md (§0 / §1) by section.
package tui

import (
	"sort"
	"strconv"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/lineage"
	"github.com/d-mcmillan/rufio/internal/lib/stream"
)

// ── Time formatters ───────────────────────────────────────────────────
//
// The on-disk `ts` field is RFC3339Nano (versioning.NowISO() =
// time.Now().UTC().Format(time.RFC3339Nano), versioning.go:77-79). The
// v8 display structs carry pre-formatted strings (fixtures.go: ThreadMsg
// .Time / DecisionLineage.Time are "HH:MM:SS"; MemoryEntry.Ago is a
// humanised "2m"/"1h"/"2h" — see fixtures.go:292-295). G0 owns the
// substrate-ts → display-string conversion so G1 is a pure swap.

// tsToClock parses an RFC3339Nano timestamp and returns the wall-clock
// "HH:MM:SS" string the v8 rows render (fixtures.go ThreadMsg.Time /
// DecisionLineage.Time, e.g. "14:02:46"). The instant is formatted in
// UTC because versioning.NowISO writes UTC (versioning.go:78) — the
// display is the substrate's own clock, not the viewer's local zone.
//
// Parse failure → "" (the empty string): the v8 row renderer treats an
// empty Time as "timestamp absent" and simply omits it (the §7.3
// suppressible-timestamp slot), so a malformed/missing ts degrades to a
// time-less row rather than leaking raw garbage into the rail. This
// matches how the fixture would behave if Time were "".
func tsToClock(rfc3339nano string) string {
	t, err := time.Parse(time.RFC3339Nano, rfc3339nano)
	if err != nil {
		return ""
	}
	return t.UTC().Format("15:04:05")
}

// tsToAgo parses an RFC3339Nano timestamp and returns a humanised
// "time ago" string relative to `now` (the MemoryEntry.Ago shape:
// "2m" / "1h" / "2h" / "3d", fixtures.go:292-295). `now` is a PARAMETER
// — never time.Now() — so tests are deterministic (handoff hard
// constraint).
//
// Buckets (coarsest unit that fits, matching the terse fixture style):
//
//	< 60s            → "<unit>s"  (seconds, "5s"; "0s" for the instant)
//	< 60m            → "<unit>m"
//	< 24h            → "<unit>h"
//	>= 24h           → "<unit>d"
//
// A future or unparseable ts → "" (degrade gracefully like tsToClock;
// the row simply shows no age rather than a negative/garbage value).
func tsToAgo(rfc3339nano string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, rfc3339nano)
	if err != nil {
		return ""
	}
	d := now.Sub(t)
	if d < 0 {
		// ts is in the future relative to the injected now — there is no
		// sensible "ago" to show; omit rather than render "-3s".
		return ""
	}
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours())/24) + "d"
	}
}

// ── Thread projection ─────────────────────────────────────────────────

// projectThread projects the flat ordered substrate feed into the v8
// chat rows (data-mapping §1 :113-117). It is a PURE function of its
// inputs: the already-read+ordered stream events, the per-thought-id
// confirm tallies, the resolved operator id, and an injected `now`
// (unused for thread rows today but kept in the signature so G1 can pass
// the same clock it passes everywhere — and so a future "Nm ago" column
// needs no signature churn).
//
// `events` is the flat ordered feed the stream lib produces (one Event
// per @thought/@observation/@confirm/… record, stream.go:34-45). The
// caller (G1) builds it via stream.EmitCatchUp/WatchAndEmit decode; G0
// takes it as data so the projection is testable without a watcher.
//
// Kind derivation (data-mapping §1 mapping table, verbatim):
//
//   - author == operatorID                         → kindOp     (:114)
//   - @thought type=decision                       → kindPlan   (:115)
//   - record carries a parent:/decision: link to a
//     visible plan thought                         → kindReply  (:116)
//   - else                                          → kindPlan
//
// The "else → kindPlan" default mirrors the existing SubstrateThread
// fixture pattern (fixtures.go:115-119): the un-parented hypothesis that
// opens an investigation is a kindPlan row (it is the thing replies
// thread under), exactly like a decision. Only events that explicitly
// link to a visible plan become kindReply; a root non-decision thought
// with no parent stays a plan row. One nesting level only (handoff §14 /
// data-mapping §1 :116) — a reply's own children are NOT re-nested;
// every linked event is a single-level kindReply.
//
// Role = the Rufio record kind/type string the fixture uses
// (data-mapping §0 OPEN-1-resolved, fixtures.go:46-48): a @thought
// carries its `type` (hypothesis/observation/decision/…); a non-thought
// record (@observation/@confirm/…) uses its record type. Rendered
// uppercase by the row renderer — projection stores it verbatim.
//
// Quorum: populated on ANY @thought row that has a confirm tally — NOT
// decision-only (#131, 2026-05-18: the auto-promote engine is
// type-agnostic, so the projection follows it; focus/hypothesis/
// observation/question/decision all get a Quorum once confirmed). The
// real gate is "a tally exists for that thought-id"; loadConfirmTallies
// only registers ids with ≥1 confirm, so an unconfirmed thought of any
// type gets no Quorum (no `0/3` clutter — the guard is preserved
// transitively). Yes = confirm.Tally.Confirms (already sorted+deduped by
// confirm.ReadAll, confirm.go:120-121 — we copy defensively). Total is
// left 0 — the X/Y denominator (OPEN-2: auto-promote /3) is applied at
// RENDER time in G1 (applyQuorumThreshold), deliberately NOT baked in
// here (handoff hard constraint; data-mapping §0 OPEN-2 + the #131
// follow-up / §1 :118).
//
// Lineage is left nil here — the decision drill-down is built on demand
// via projectLineage (the lineage libs), not eagerly per row.
//
// Last is set on the freshest row (the final element of the ordered
// feed), matching the fixture (fixtures.go:151 sets Last on the last
// element).
func projectThread(events []stream.Event, tallies map[string]confirm.Tally, operatorID string, now time.Time) []ThreadMsg {
	_ = now // reserved: G1 passes the shared clock; no row needs it yet.

	// Pass 1: index the thought-ids that are PLAN rows so a child's
	// parent:/decision: link can be resolved to "links to a VISIBLE
	// plan" (data-mapping §1 :116 "a visible plan"). A row is a plan if
	// it is a decision OR a non-operator root thought (the else→plan
	// default). Operator rows are kindOp, never plan parents.
	planIDs := make(map[string]bool)
	for _, ev := range events {
		if ev.Type != "thought" {
			continue
		}
		id := thoughtID(ev)
		if id == "" {
			continue
		}
		author := recordAuthor(ev)
		if author == operatorID {
			continue // operator rows are kindOp, not plan anchors
		}
		// A decision is always a plan anchor; a non-decision thought is
		// a plan anchor only when it is a root (no parent/decision link)
		// — a linked non-decision thought is itself a reply.
		if thoughtType(ev) == roleDecision || parentLink(ev) == "" {
			planIDs[id] = true
		}
	}

	out := make([]ThreadMsg, 0, len(events))
	for _, ev := range events {
		author := recordAuthor(ev)
		role := rowRole(ev)
		kind := kindPlan // else→plan default (mirrors the fixture root rows)

		switch {
		case author == operatorID:
			// data-mapping §1 :114 — operator identity ⇒ kindOp.
			kind = kindOp
		case ev.Type == "thought" && thoughtType(ev) == roleDecision:
			// data-mapping §1 :115 — @thought type=decision ⇒ kindPlan.
			kind = kindPlan
		case parentLink(ev) != "" && planIDs[parentLink(ev)]:
			// data-mapping §1 :116 — a parent:/decision: link pointing
			// at a VISIBLE plan ⇒ kindReply (one nesting level only).
			kind = kindReply
		}

		msg := ThreadMsg{
			Who:  author,
			Role: role,
			Time: tsToClock(ev.TS),
			Kind: kind,
			Text: ev.Content,
		}

		// Quorum on ANY confirmed @thought (#131, 2026-05-18 — no longer
		// decision-only; the auto-promote engine is type-agnostic). The
		// real guard is the tally lookup: only a thought WITH a loaded
		// tally gets a Quorum, and loadConfirmTallies only registers ids
		// with ≥1 confirm — so an unconfirmed thought (of any type) gets
		// no Quorum here and no `0/3` dot row downstream (the no-clutter
		// property is preserved transitively). Total stays 0 — the
		// denominator is the G1 render-time product call
		// (applyQuorumThreshold), unchanged.
		if ev.Type == "thought" {
			if t, ok := tallies[thoughtID(ev)]; ok {
				yes := make([]string, len(t.Confirms))
				copy(yes, t.Confirms)
				// Defensive re-sort+dedupe: confirm.ReadAll already
				// guarantees this (confirm.go:120-121) but projection
				// must not depend on a caller honouring that.
				yes = sortedDedupe(yes)
				msg.Quorum = &Quorum{
					Yes: yes,
					// Total deliberately 0 — OPEN-2 denominator is a
					// G1 RENDER-time product call, NOT baked in here.
					Total: 0,
				}
			}
		}

		out = append(out, msg)
	}

	if len(out) > 0 {
		out[len(out)-1].Last = true // freshest row → trailing caret (fixtures.go:151)
	}
	return out
}

// rowRole returns the Role string the v8 row renders (data-mapping §0
// OPEN-1-resolved): a @thought row uses its `type` field
// (hypothesis/observation/decision/question/focus); any other record
// uses its record type verbatim (observation/confirm/reason/…). Mirrors
// the fixture's Role values (fixtures.go:116/121/129/145).
func rowRole(ev stream.Event) string {
	if ev.Type == "thought" {
		if t := thoughtType(ev); t != "" {
			return t
		}
	}
	return ev.Type
}

// thoughtType extracts the @thought `type` field from a stream Event's
// raw GDL line. stream.Event does not surface `type`/`id`/`parent`
// (stream.go:34-45 only projects a fixed column set), so we re-parse the
// Raw line via the gdl lib — we do NOT hand-roll the record parse.
func thoughtType(ev stream.Event) string { return rawField(ev, "type") }

// thoughtID extracts the @thought `id` field (the thought-id used as the
// confirm-tally key and the parent-link target).
func thoughtID(ev stream.Event) string { return rawField(ev, "id") }

// parentLink returns the reply linkage: the `parent` field if present,
// else the `decision` field (data-mapping §1 :116 — "a `parent:` or
// `decision:` link"). @observation/@reason records link via `decision`;
// @thought replies link via `parent`.
func parentLink(ev stream.Event) string {
	if p := rawField(ev, "parent"); p != "" {
		return p
	}
	return rawField(ev, "decision")
}

// recordAuthor returns the record author. stream.Event.Author is the
// projected `author` field (stream.go:41); fall back to the raw line for
// record kinds whose author lives under a different key historically —
// but @thought/@observation both use `author`, so this is just defence.
func recordAuthor(ev stream.Event) string {
	if ev.Author != "" {
		return ev.Author
	}
	return rawField(ev, "author")
}

// ── Lineage drill-down projection ─────────────────────────────────────

// projectLineage builds the DecisionLineage drill-down payload for a
// decision thought-id, using the lineage libs exactly as
// internal/cli/lineage.go:56-67 does (LookupDecision → ResolveBundleRefs
// → WalkReasoning) and formatting Bundle/Chain EXACTLY like
// renderLineageColumnar (internal/cli/lineage.go:112-134) so the v8
// overlay reads identically to `rufio lineage <id>`.
//
// Field mapping (DecisionLineage, fixtures.go:90-98):
//
//   - ID        = decision.ID
//   - Author    = decision.Author
//   - Subject   = decision.Subject
//   - Statement = decision.Content (the decision body — fixtures.go
//     decisionLineage5821.Statement is the decision sentence)
//   - Time      = tsToClock(decision.TS) ("HH:MM:SS", fixtures.go:165)
//   - Bundle    = one string per resolved ref, formatted EXACTLY as the
//     CLI's columnar render: "<path>@v<ver> (sha: <8>)" for resolved,
//     "(unknown sha: <sha>)" for unresolved (cli/lineage.go:112-122).
//   - Chain     = the @reason chain Content in audit order, one entry
//     per step (cli/lineage.go:130-133 numbers them at render; the v8
//     overlay numbers DecisionLineage.Chain itself — fixtures.go:170-175
//     stores the bare sentences, so we store Content verbatim).
//
// Errors (NoSuchDecisionError / NotADecisionError / IO) propagate so the
// caller can decide whether the drill-down is unavailable; G0 does not
// swallow them (matches cli/lineage.go which lets them reach
// HandleError).
func projectLineage(root, decisionID string) (*DecisionLineage, error) {
	d, err := lineage.LookupDecision(root, decisionID)
	if err != nil {
		return nil, err
	}
	refs, err := lineage.ResolveBundleRefs(root, d.Bundle)
	if err != nil {
		return nil, err
	}
	chain, err := lineage.WalkReasoning(root, d.ID)
	if err != nil {
		return nil, err
	}

	dl := &DecisionLineage{
		ID:        d.ID,
		Author:    d.Author,
		Subject:   d.Subject,
		Statement: d.Content,
		Time:      tsToClock(d.TS),
	}
	for _, r := range refs {
		if r.Resolved {
			short := r.SHA256
			if len(short) > 8 {
				short = short[:8]
			}
			// EXACT cli/lineage.go:118 columnar format.
			dl.Bundle = append(dl.Bundle, r.Path+"@v"+strconv.Itoa(r.Version)+" (sha: "+short+")")
		} else {
			// EXACT cli/lineage.go:120 unresolved format.
			dl.Bundle = append(dl.Bundle, "(unknown sha: "+r.SHA256+")")
		}
	}
	for _, step := range chain {
		dl.Chain = append(dl.Chain, step.Content)
	}
	return dl, nil
}

// The learned/ walker, live mesh-edge derivation, and mesh-node
// projection live in project_walk.go (split for review locality).

// ── small shared helpers ──────────────────────────────────────────────

// rawField re-parses ev.Raw (the original GDL line, stream.go:44/150)
// via the gdl parser and returns the named field, or "". stream.Event
// only surfaces a fixed column set (stream.go:34-45) so fields like
// `type`/`id`/`parent` must come from the raw line — through
// gdl.ParseLine, never a hand-rolled split (handoff: reuse the libs,
// don't re-implement record parsing). A malformed/blank Raw → "".
func rawField(ev stream.Event, key string) string {
	rec, err := gdl.ParseLine(ev.Raw)
	if err != nil || rec == nil {
		return ""
	}
	return rec.Get(key)
}

// sortedDedupe returns a sorted, de-duplicated copy. Used to make the
// quorum Yes list deterministic independent of caller guarantees.
func sortedDedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
