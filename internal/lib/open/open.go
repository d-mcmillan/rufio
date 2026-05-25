// Package open implements the read-side bundle that backs `rufio open
// <subject>` — the cold-agent first-contact verb that consolidates the
// 4-5 reads every agent does when first encountering a topic. Read-dual
// of `attend`.
//
// Architecture: pure-read in-process orchestrator. Composes the existing
// recall / thought / attention / devhealth / fleet libs into a single
// OpenBundle shape. Renderer lives in internal/cli/open.go; the bundle
// itself never writes and never renders.
package open

import (
	"sort"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/devhealth"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
	"github.com/d-mcmillan/rufio/internal/lib/recall"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// Params controls Bundle's behavior. See internal/cli/open.go for the
// flag → Params mapping.
type Params struct {
	// Subject is required — Bundle does not validate; callers (the CLI
	// front door) validate via thought.ValidateSubject before invoking.
	Subject string

	// Topics is the optional CSV --topics filter forwarded server-side to
	// recall.Filter (#180 — server-side topic filter shipped 2026-05-21).
	// Nil/empty → no topic filter.
	Topics []string

	// Since is the recency floor; the CLI applies a 24h default when
	// unset. Zero means "no since filter".
	Since time.Duration

	// Scope filters recall output (empty / agent / deployment / fleet).
	// privacy.IsVisible (#147) is the actual floor and applies regardless
	// of Scope.
	Scope string

	// Limit caps rows per section. Default applied by the caller.
	Limit int

	// CurrentAgent is the resolved agent id, used by privacy.IsVisible
	// downstream. Empty means anonymous (firehose visibility).
	CurrentAgent string
}

// FleetRow is the open package's minimal projection of an engaged-peer
// agent. Source is live/attention/*.gdl (every agent who has declared
// current attention) — NOT the broader `rufio fleet` view that also
// unions outbox/inbox/summons/learned authorship. The narrower scope is
// deliberate: open's read-tax-reduction goal is best served by surfacing
// the agents who are CURRENTLY ENGAGED with the substrate; historical
// authors are reachable via `rufio fleet` when the cold agent wants the
// full audit view.
//
// Kept here (rather than imported) because internal/cli/fleet.go's
// fleetAgent is package-private; the bundle exposes only the columns
// rendered by `rufio open` (state-first cognition, not full activity
// counts). The corresponding `attention` slot lives separately in
// OpenBundle.Attention so JSON consumers can index attention by agent
// without joining.
type FleetRow struct {
	// Agent is the agent id (matches the @attention file basename when
	// HasAttention is true).
	Agent string

	// HasAttention is true when live/attention/<agent>.gdl exists.
	HasAttention bool

	// Intent is the agent's current @attention free-text intent — empty
	// when HasAttention=false.
	Intent string

	// Scope is the on-disk @attention scope (#125). Empty for legacy
	// records or non-attention rows.
	Scope string

	// LastSeen is the unioned RFC3339Nano timestamp across attention +
	// outbox + inbox + summons + learned. Used for the fleet section's
	// descending sort and for selecting the attention section's top-3.
	LastSeen string
}

// OpenBundle is the assembled read-bundle. Renderer in internal/cli/open.go
// formats this as labeled text sections OR a stable-keyed JSON object.
//
// Locked invariants:
//   - Every slice field is non-nil at return so JSON consumers can range
//     without nil checks (empty arrays, never null).
//   - The Daemon field is an object shape (`devhealth.StatusReport` —
//     extensible) rather than a bool, matching Task 10's locked schema.
//   - HiddenPrivateCount tracks records elided by the privacy floor; the
//     text renderer surfaces it as a single footer line, JSON exposes the
//     integer directly.
type OpenBundle struct {
	Subject            string                 `json:"subject"`
	Agent              string                 `json:"agent"`
	Daemon             devhealth.StatusReport `json:"daemon"`
	Recall             []recall.RecallRecord  `json:"recall"`
	Thoughts           []recall.RecallRecord  `json:"thoughts"`
	Fleet              []FleetRow             `json:"fleet"`
	Attention          []attention.Attention  `json:"attention"`
	HiddenPrivateCount int                    `json:"hidden_private_count"`
}

// Bundle assembles the OpenBundle by composing existing libs. Pure read,
// no writes. Caller resolves identity and passes Params.CurrentAgent so
// privacy.IsVisible (#147) can apply correctly.
func Bundle(root string, p Params) (OpenBundle, error) {
	b := OpenBundle{
		Subject: p.Subject,
		Agent:   p.CurrentAgent,
		// devhealth.Status fails closed: a missing/garbage heartbeat file
		// returns StateNotRunning, so a cold substrate produces a clean
		// "not running" signal rather than a fabricated "ok".
		Daemon:    devhealth.Status(root, time.Now()),
		Recall:    []recall.RecallRecord{},
		Thoughts:  []recall.RecallRecord{},
		Fleet:     []FleetRow{},
		Attention: []attention.Attention{},
	}

	if err := populateRecall(root, p, &b); err != nil {
		return b, err
	}
	if err := populateThoughts(root, p, &b); err != nil {
		return b, err
	}
	if err := populateFleet(root, p, &b); err != nil {
		return b, err
	}
	if err := populateAttention(root, &b); err != nil {
		return b, err
	}
	return b, nil
}

// attentionTopN is the cap on b.Attention. Locked at 3 per the
// cross-harness Run 3 spec — cold agents need the most-recently-engaged
// peers' current intent without re-introducing the read noise the bundle
// is meant to cut. The full engaged-peer list stays in b.Fleet (uncapped).
const attentionTopN = 3

// effectiveScope translates the caller-facing Params.Scope into the
// FilterParams.Scope value the recall pipeline expects.
//
// Critical: recall.Filter's scopePass interprets a non-empty Scope as
// "narrow to records visible AT THIS SCOPE LEVEL", which (for same-rank
// records) means "ONLY the caller's own records". That's correct for
// scope=agent (private query) but WRONG for scope=fleet — which agents
// say to mean "the broadest view I can have". For fleet we therefore
// clear Scope so recall.Filter falls through to privacy.IsVisible alone
// (the unconditional #147 floor: scope:agent records authored by
// another agent are never visible; everything else is).
//
// The two non-fleet values pass through unchanged: scope=agent narrows
// to the caller's own agent-scoped records, scope=deployment narrows to
// the caller's deployment.
func effectiveScope(s string) string {
	if s == "fleet" {
		return ""
	}
	return s
}

// populateRecall fills b.Recall from the corpus. Composition:
//
//  1. recall.Scan walks every namespace once (given/learned/live/...).
//  2. recall.Filter applies type/scope/since/topics/privacy gates per
//     Params (server-side --topics from #180; privacy floor from #147).
//  3. recall.Match enforces the subject — Params.Subject passes
//     thought.ValidateSubject so Match takes the exact-subject path.
//
// Privacy elisions are counted into b.HiddenPrivateCount by running the
// filter pipeline TWICE — once with CurrentAgent set (the live view)
// and once with CurrentAgent="" (the firehose view per privacy.IsVisible's
// empty-string semantic) — the diff IS the privacy-elided count for the
// caller's identity. We do this only for the recall path; populateThoughts
// runs the same way (Task 7 covers both).
//
// Limit is applied LAST so it caps the final visible set, not the
// pre-privacy scan. Trim-overflow does NOT inflate HiddenPrivateCount —
// that counter is reserved for privacy-floor elisions.
func populateRecall(root string, p Params, b *OpenBundle) error {
	records, err := recall.Scan(root, false)
	if err != nil {
		return err
	}
	base := recall.FilterParams{
		Types: []string{"thought", "observation"},
		ThoughtTypes: []string{
			"decision", "hypothesis", "focus", "question",
		},
		Topics:         p.Topics,
		Scope:          effectiveScope(p.Scope),
		Since:          p.Since,
		IncludeExpired: false,
		CurrentAgent:   p.CurrentAgent,
	}
	filtered := recall.Filter(records, base)
	matched := recall.Match(filtered, p.Subject)

	b.HiddenPrivateCount += countPrivacyElided(records, base, p.Subject)

	if p.Limit > 0 && len(matched) > p.Limit {
		matched = matched[:p.Limit]
	}
	b.Recall = matched
	return nil
}

// countPrivacyElided counts records that would pass every gate EXCEPT
// the privacy.IsVisible floor for the caller's identity. The count is
// surfaced as b.HiddenPrivateCount so the renderer can show a footer
// line ("N private records hidden by privacy floor"). An anonymous
// caller (Params.CurrentAgent=="") sees zero elision by design — the
// firehose path returns everything privacy.IsVisible touches.
//
// Implementation: filter records ignoring privacy (Scope="", CurrentAgent=""),
// then for each result that survives subject match, check privacy.IsVisible
// against the CALLER's identity. The records that fail are the elided set.
func countPrivacyElided(records []recall.RecallRecord, base recall.FilterParams, subject string) int {
	if base.CurrentAgent == "" {
		return 0
	}
	// Build a parallel params that DROPS privacy & scope so we can count
	// the universe of records the gate-pipeline would otherwise hide.
	// Privacy in recall.Filter only fires when Scope=="" — clearing
	// CurrentAgent here makes the gate permissive while preserving the
	// other filters (type/topics/since).
	openParams := base
	openParams.CurrentAgent = ""
	openParams.Scope = ""
	universe := recall.Filter(records, openParams)
	universe = recall.Match(universe, subject)

	hidden := 0
	for _, r := range universe {
		if !privacy.IsVisible(r, base.CurrentAgent) {
			hidden++
		}
	}
	return hidden
}

// populateThoughts fills b.Thoughts with the broader companion view to
// b.Recall — every @thought record on subject regardless of subtype
// (decision, hypothesis, focus, question, AND observation-subtype).
// Cold agents can see the full thought history on the subject without
// running a second recall call.
//
// Same privacy floor + Since gate as populateRecall (driven by
// recall.Filter). Subject filter via recall.Match's exact-subject path.
func populateThoughts(root string, p Params, b *OpenBundle) error {
	records, err := recall.Scan(root, false)
	if err != nil {
		return err
	}
	filtered := recall.Filter(records, recall.FilterParams{
		Types:          []string{"thought"},
		Topics:         p.Topics,
		Scope:          effectiveScope(p.Scope),
		Since:          p.Since,
		IncludeExpired: false,
		CurrentAgent:   p.CurrentAgent,
	})
	matched := recall.Match(filtered, p.Subject)
	if p.Limit > 0 && len(matched) > p.Limit {
		matched = matched[:p.Limit]
	}
	b.Thoughts = matched
	return nil
}

// populateFleet fills b.Fleet with engaged-peer agents (those who have a
// current @attention record). Sort: LastSeen DESCENDING so cold agents
// see the most-recently-engaged peers at the top. Comparison routes
// through versioning.CanonicalTS so trimmed-fraction RFC3339Nano values
// rank chronologically (mirrors internal/cli/fleet.go's bumpLastSeen
// rationale — same hazard, same fix).
//
// Missing live/attention/ directory → empty Fleet, nil error. Source is
// attention.ReadAll which already short-circuits on missing dirs.
func populateFleet(root string, p Params, b *OpenBundle) error {
	atts, err := attention.ReadAll(root)
	if err != nil {
		return err
	}
	rows := make([]FleetRow, 0, len(atts))
	for _, a := range atts {
		rows = append(rows, FleetRow{
			Agent:        a.Agent,
			HasAttention: true,
			Intent:       a.Intent,
			Scope:        a.Scope,
			LastSeen:     a.TS,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ci := versioning.CanonicalTS(rows[i].LastSeen)
		cj := versioning.CanonicalTS(rows[j].LastSeen)
		if ci != cj {
			return ci > cj // newer first
		}
		// Tie-break: agent id ascending so output stays deterministic.
		return rows[i].Agent < rows[j].Agent
	})
	b.Fleet = rows
	return nil
}

// JSONPayload converts an OpenBundle into the locked wire shape that
// both the CLI's `rufio open --json` renderer AND the MCP `open` tool
// emit byte-identical. The fidelity contract is load-bearing — agents
// using either transport must see the same JSON; making this a shared
// lib helper (rather than duplicating in cli + mcp) eliminates the
// "drift between transports" failure mode.
//
// Locked schema:
//
//	{
//	  "_type": "open",
//	  "_version": 1,
//	  "subject": "...",
//	  "agent": "...",
//	  "daemon": {"running": bool, "heartbeat": "RFC3339Nano|"},
//	  "fleet": [{agent, has_attention, intent, scope, last_seen}, ...],
//	  "attention": [{agent, intent, scope, entities, topics, ts}, ...],
//	  "recall": [<row>, ...],
//	  "thoughts": [<row>, ...],
//	  "hidden_private_count": 0
//	}
//
// Row shape (RECALL / THOUGHTS):
//
//	{
//	  "_type": "thought|observation|...",
//	  "type": "<thought subtype, empty for non-thought>",
//	  "id": "<full id>",
//	  "ts": "RFC3339Nano",
//	  "author": "...", "subject": "...",
//	  "predicate": "...", "object": "...",
//	  "content": "...", "scope": "agent|deployment|fleet|",
//	  "path": "<on-disk path>",
//	  "retracted": false,
//	  "topics": [...],
//	  "confirm_count": 0, "refute_count": 0, "promoted": false
//	}
//
// Empty sections serialize as `[]` (never null) so consumers can range
// without nil-checks. _version is locked at 1 for the first ship; bump
// on any breaking schema change.
//
// Bonus (security audit followup): root is now an explicit parameter
// threaded through RecallRowJSON so the path-relativisation uses
// filepath.Rel rather than a substring search. The CLI's runOpen and
// the MCP tool's registerOpen both already know the substrate root
// (they call open.Bundle with it) — passing it through here is a
// clean signature change with no production-data drift.
func JSONPayload(b OpenBundle, root string) map[string]interface{} {
	fleet := make([]map[string]interface{}, 0, len(b.Fleet))
	for _, f := range b.Fleet {
		fleet = append(fleet, fleetRowJSON(f))
	}
	attn := make([]map[string]interface{}, 0, len(b.Attention))
	for _, a := range b.Attention {
		attn = append(attn, attentionRowJSON(a))
	}
	rec := make([]map[string]interface{}, 0, len(b.Recall))
	for _, r := range b.Recall {
		rec = append(rec, RecallRowJSON(r, root))
	}
	tho := make([]map[string]interface{}, 0, len(b.Thoughts))
	for _, r := range b.Thoughts {
		tho = append(tho, RecallRowJSON(r, root))
	}
	return map[string]interface{}{
		"_type":                "open",
		"_version":             1,
		"subject":              b.Subject,
		"agent":                b.Agent,
		"daemon":               DaemonJSON(b),
		"fleet":                fleet,
		"attention":            attn,
		"recall":               rec,
		"thoughts":             tho,
		"hidden_private_count": b.HiddenPrivateCount,
	}
}

// fleetRowJSON projects a FleetRow into its locked map shape (lowercase
// keys; snake_case for the multi-word last_seen / has_attention slots).
func fleetRowJSON(f FleetRow) map[string]interface{} {
	return map[string]interface{}{
		"agent":         f.Agent,
		"has_attention": f.HasAttention,
		"intent":        f.Intent,
		"scope":         f.Scope,
		"last_seen":     f.LastSeen,
	}
}

// attentionRowJSON projects an attention.Attention into its locked map
// shape. Entities and Topics are always emitted as `[]` (never null) so
// consumers can range without nil checks.
func attentionRowJSON(a attention.Attention) map[string]interface{} {
	ents := a.Entities
	if ents == nil {
		ents = []string{}
	}
	tops := a.Topics
	if tops == nil {
		tops = []string{}
	}
	return map[string]interface{}{
		"agent":    a.Agent,
		"intent":   a.Intent,
		"scope":    a.Scope,
		"entities": ents,
		"topics":   tops,
		"ts":       a.TS,
	}
}

// RecallRowJSON projects a single recall.RecallRecord into the locked
// per-row map shape used by the recall AND thoughts sections of the open
// payload. Exported so internal/mcp/tools_open.go can share it; cross-
// transport fidelity = both surfaces call this one helper.
//
// Bonus (security audit followup): takes the substrate root so the
// path relativisation uses filepath.Rel (the explicit-root form)
// rather than a substring search across the absolute path.
func RecallRowJSON(r recall.RecallRecord, root string) map[string]interface{} {
	topics := r.Topics
	if topics == nil {
		topics = []string{}
	}
	// Security audit H2: emit root-relative POSIX path instead of the
	// server's absolute filesystem path. Shares the single relativiser
	// in recall so the open + recall JSON surfaces stay byte-shape
	// consistent across both transports (CLI --json + MCP tool result).
	return map[string]interface{}{
		"_type":         r.Type,
		"type":          r.ThoughtType,
		"id":            r.ID,
		"ts":            r.TS,
		"author":        r.Author,
		"subject":       r.Subject,
		"predicate":     r.Predicate,
		"object":        r.Object,
		"content":       r.Content,
		"scope":         r.Scope,
		"path":          recall.RelativisePath(r.Path, root),
		"retracted":     r.Retracted,
		"topics":        topics,
		"confirm_count": r.ConfirmCount,
		"refute_count":  r.RefuteCount,
		"promoted":      r.Promoted,
	}
}

// DaemonJSON projects b.Daemon (a devhealth.StatusReport) into the
// locked daemon sub-shape: {running: bool, heartbeat: string}. Object
// shape (not bool) so future fields (pid, uptime, version) can land
// without a breaking change. Stale daemons report running=false (a
// stale daemon is, for routing-correctness purposes, not running) but
// still emit the last-known heartbeat string so consumers can compute
// staleness if needed.
func DaemonJSON(b OpenBundle) map[string]interface{} {
	running := false
	heartbeat := ""
	switch b.Daemon.State.String() {
	case "running":
		running = true
		if !b.Daemon.LastTick.IsZero() {
			heartbeat = b.Daemon.LastTick.UTC().Format(time.RFC3339Nano)
		}
	case "stale":
		if !b.Daemon.LastTick.IsZero() {
			heartbeat = b.Daemon.LastTick.UTC().Format(time.RFC3339Nano)
		}
	}
	return map[string]interface{}{
		"running":   running,
		"heartbeat": heartbeat,
	}
}

// populateAttention fills b.Attention with the @attention records for
// the top-N fleet rows (by LastSeen). Must be called AFTER populateFleet
// — it walks b.Fleet (already sorted desc) and calls attention.LoadOne
// per agent. A NoAttentionError on any agent is swallowed (the row
// should not have been in Fleet if it lacked attention, but the lib is
// defensive: a vanished file between scans is best-effort, not fatal).
func populateAttention(root string, b *OpenBundle) error {
	limit := attentionTopN
	if len(b.Fleet) < limit {
		limit = len(b.Fleet)
	}
	out := make([]attention.Attention, 0, limit)
	for i := 0; i < limit; i++ {
		a, err := attention.LoadOne(root, b.Fleet[i].Agent)
		if err != nil {
			// NoAttentionError or read failure: skip this agent. The
			// fleet row is still surfaced via b.Fleet; only the per-agent
			// attention payload is elided. Don't error out the whole
			// bundle for one missing file.
			continue
		}
		out = append(out, a)
	}
	b.Attention = out
	return nil
}
