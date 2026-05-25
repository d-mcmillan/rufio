// Package cli — `rufio fleet`.
//
// Inspection command per D20.1. Enumerates every agent with ANY
// activity on the substrate (attention record, outbox author, inbox
// recipient, summons participant, or hand-authored learned/
// observation), unions them into one row per agent, and prints
// columnar (default) or JSONL (--json).
//
// Pure read. No locks, no identity resolution (any agent can inspect
// the fleet). Empty result → exit 0, stdout empty.
//
// Issue #115: pre-broadening, `fleet` only read live/attention/*.gdl,
// so agents who had written thoughts, received summons, or authored
// observations but had no CURRENT attention record were invisible.
// Two cold-start vet agents (s1-channel, s2-discover) both hit this
// wall — they could see peers via `ls live/inbox/` + `ls live/outbox/`
// but `rufio fleet` returned empty. The broadening makes `fleet` the
// canonical "who is on this substrate" command.
package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/devhealth"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/summon"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// fleetAgent is the per-agent projection rendered by `rufio fleet`.
// Identity is always present; attention fields are zero when the agent
// has no current @attention record. LastSeen is the latest timestamp
// across every source (attention.ts, outbox/inbox file mtime, summon
// ts, learned/ observation ts) — used for the descending sort and
// surfaced in the row so cold agents can tell who's recently active.
type fleetAgent struct {
	Agent string

	// Current-attention projection. HasAttention is false when no
	// live/attention/<agent>.gdl is present — render as "(no current
	// attention)" in columnar, intent="" + entities/topics=[] in JSON.
	HasAttention bool
	Intent       string
	Scope        string // #125: on-disk @attention scope; drives the
	// privacy floor in redactPrivateAttentionFields (scope:agent rows
	// are hidden from non-self callers).
	Entities    []string
	Topics      []string
	AttentionTS string

	// Activity counts (unioned across sources). Surfaced in both
	// columnar and JSON output so a cold agent can pick the "loudest"
	// peer to summon first.
	Thoughts        int // outbox/<agent>/*.gdl files
	InboxDeliveries int // inbox/<agent>/*.gdl files
	SummonsSent     int // summons where from:<agent>
	SummonsReceived int // summons where to:<agent>
	LearnedAuthored int // learned/**/*.gdlm where author:<agent> (skip auto-promote)

	// LastSeen is the latest ts/mtime seen across all sources for this
	// agent. RFC3339Nano string for deterministic sort + JSON shape.
	LastSeen string
}

// NewFleetCmd returns the `rufio fleet` Cobra command. Lists every
// agent with ANY activity on the substrate (D20.1 + #115 broadening).
// Columnar default; JSONL via --json. Sorted by LastSeen descending so
// recently-active peers surface first; ties broken by agent id
// ascending for determinism.
func NewFleetCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "fleet",
		Short: "List agents on the substrate (any activity)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runFleet(cwd, opts)
			}
			if err != nil {
				HandleError("fleet", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// runFleet is the pure logic for `rufio fleet`. Resolves the project
// root, collects every agent with ANY activity, and dispatches to the
// renderer matching opts.JSON.
//
// Identity is best-effort: when set, the privacy filter (#147) blanks
// the per-row `entities` and `topics` for OTHER agents — those fields
// are private routing hints (R8 vet 2026-05-20 showed bob seeing
// alice's "entities:secret:internal" via fleet). The row, the intent
// summary, and the activity counts remain visible so the discovery
// purpose of fleet ("who is on this substrate") is unaffected.
// Anonymous callers (no identity) get the unredacted view, matching
// the stream.Match opt-in semantic.
func runFleet(cwd string, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	agents, err := collectAgents(root)
	if err != nil {
		return err
	}
	currentAgent, _, _ := identity.Resolve(root)
	agents = redactPrivateAttentionFields(agents, currentAgent)
	if opts.JSON {
		return renderFleetJSON(agents, opts)
	}
	// #154 daemon supervision: surface daemon liveness at the top of the
	// columnar output so cold agents have a discoverable health signal.
	// Never fails the fleet command — the header is purely advisory and
	// is suppressed under --quiet (chatter, not data) and --json (would
	// corrupt JSONL streams).
	renderDaemonHealthHeader(root, opts, time.Now)
	renderFleetColumnar(agents, opts)
	return nil
}

// renderDaemonHealthHeader emits a single advisory line describing the
// dev daemon's liveness (ok / STALE / not running). The line goes to
// STDERR — it's advisory chatter that surrounds the fleet rows, not
// fleet data itself, and stdout-purity matters because downstream
// callers parse `fleet` line-by-line. An attentive operator reading the
// terminal sees it next to the rows; pipes / wc -l are unaffected.
//
// Suppressed under --quiet (chatter rule) and --json (no equivalent
// JSON field — operators inspecting structured output should call
// `rufio dev --status`). The now argument is injected for deterministic
// testing of the age math.
func renderDaemonHealthHeader(root string, opts output.RenderOpts, now func() time.Time) {
	if opts.Quiet || opts.JSON {
		return
	}
	st := devhealth.Status(root, now())
	var line string
	switch st.State {
	case devhealth.StateNotRunning:
		line = "daemon: not running (no heartbeat)"
	case devhealth.StateStale:
		line = fmt.Sprintf(
			"daemon: STALE - last heartbeat %s ago; routing may be delayed",
			formatHeaderAge(st.LastTickAge),
		)
	default:
		line = fmt.Sprintf("daemon: ok (heartbeat %s ago)", formatHeaderAge(st.LastTickAge))
	}
	fmt.Fprintln(os.Stderr, line)
}

// formatHeaderAge mirrors dev.go's formatAge — whole-second precision —
// but lives here so fleet.go doesn't reach across files for a one-line
// helper. Sub-second granularity is noise for a daemon-health summary.
func formatHeaderAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Truncate(time.Second).String()
}

// redactPrivateAttentionFields enforces the fleet privacy floor:
//
//   - For NON-SELF rows: always blank Entities and Topics (private
//     routing hints; R8 vet 2026-05-20 showed they were leaking).
//   - For NON-SELF rows whose attention is scope:agent (#125): blank
//     the attention projection entirely (HasAttention/Intent/AttentionTS
//     too) so the row carries no signal from the private record. If
//     the agent has no OTHER substrate activity (Thoughts/inbox/
//     summons/learned == 0) the row is dropped from the output —
//     scope:agent + no other activity == invisible to other callers,
//     matching the privacy.IsVisible rule the rest of the surface
//     enforces.
//
// currentAgent="" (anonymous) is the firehose — return rows unchanged.
func redactPrivateAttentionFields(rows []fleetAgent, currentAgent string) []fleetAgent {
	if currentAgent == "" {
		return rows
	}
	out := make([]fleetAgent, 0, len(rows))
	for _, r := range rows {
		if r.Agent == currentAgent {
			out = append(out, r)
			continue
		}
		// Non-self: redact private hints regardless of scope.
		r.Entities = nil
		r.Topics = nil
		// scope:agent attention is private to its author (#125 +
		// privacy.IsVisible). Wipe the attention projection so non-self
		// callers see no signal from the private record.
		if r.Scope == "agent" && r.HasAttention {
			r.HasAttention = false
			r.Intent = ""
			r.Scope = ""
			r.AttentionTS = ""
			// If the row's ONLY presence on the substrate was this
			// (now-hidden) attention, drop the row entirely. activity-
			// only rows survive — fleet's broadened "who is on this
			// substrate" purpose (#115) is unaffected for non-attention
			// activity.
			if r.Thoughts == 0 && r.InboxDeliveries == 0 &&
				r.SummonsSent == 0 && r.SummonsReceived == 0 &&
				r.LearnedAuthored == 0 {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// collectAgents unions every agent visible on the substrate. Sources
// (#115):
//
//   - live/attention/<agent>.gdl       → current attention (+ intent etc.)
//   - live/outbox/<agent>/*.gdl        → outbox author
//   - live/inbox/<agent>/*.gdl         → inbox recipient
//   - live/summons/*/<id>.gdl          → from:/to: agents
//   - learned/**/<id>.gdlm             → author: (skipping "auto-promote")
//
// Returns a slice sorted by LastSeen descending; ties broken by Agent
// id ascending so output is deterministic.
//
// Per-source errors short-circuit (this command is read-only; a
// corrupted on-disk record means the user wants to know). Missing
// source directories are not errors — fresh projects have none.
func collectAgents(root string) ([]fleetAgent, error) {
	byID := make(map[string]*fleetAgent)

	// 1. Attention — the existing source. Carries intent/entities/topics
	// that we want to preserve on the rendered row.
	atts, err := attention.ReadAll(root)
	if err != nil {
		return nil, err
	}
	for _, a := range atts {
		ag := getOrCreate(byID, a.Agent)
		ag.HasAttention = true
		ag.Intent = a.Intent
		ag.Scope = a.Scope
		ag.Entities = a.Entities
		ag.Topics = a.Topics
		ag.AttentionTS = a.TS
		bumpLastSeen(ag, a.TS)
	}

	// 2. Outbox — every agent who has authored a thought/observation/
	// reason. Directory layout is live/outbox/<agent>/<id>.gdl, so the
	// immediate subdir of live/outbox/ IS the agent id. Per-file mtimes
	// are not required for the LastSeen sort — the on-disk record's
	// `ts:` field is more authoritative.
	if err := scanAgentDir(byID, filepath.Join(root, "live", "outbox"), func(ag *fleetAgent, fileTS string) {
		ag.Thoughts++
		bumpLastSeen(ag, fileTS)
	}); err != nil {
		return nil, err
	}

	// 3. Inbox — every agent who has received at least one routed file.
	if err := scanAgentDir(byID, filepath.Join(root, "live", "inbox"), func(ag *fleetAgent, fileTS string) {
		ag.InboxDeliveries++
		bumpLastSeen(ag, fileTS)
	}); err != nil {
		return nil, err
	}

	// 4. Summons — every `from:` and `to:` agent across all state dirs.
	if err := scanSummons(root, byID); err != nil {
		return nil, err
	}

	// 5. Learned/ — every observation author except the synthetic
	// "auto-promote" daemon writer (D13.7). Tracks hand-authored
	// observations only — auto-promoted observations carry the
	// originating author in their `origin:` field, but THAT agent
	// usually also has an outbox thought from the same hypothesis, so
	// they'll already be in byID via step 2.
	if err := scanLearned(root, byID); err != nil {
		return nil, err
	}

	out := make([]fleetAgent, 0, len(byID))
	for _, ag := range byID {
		out = append(out, *ag)
	}
	sort.SliceStable(out, func(i, j int) bool {
		// Compare via CanonicalTS so trimmed-fraction RFC3339Nano values
		// (e.g. "…01.1Z" vs "…01.15Z") rank chronologically rather than
		// by raw byte order. Mirrors bumpLastSeen — same rationale.
		ci := versioning.CanonicalTS(out[i].LastSeen)
		cj := versioning.CanonicalTS(out[j].LastSeen)
		if ci != cj {
			// Descending: newer first.
			return ci > cj
		}
		// Tie-break: agent id ascending for stable, predictable output.
		return out[i].Agent < out[j].Agent
	})
	return out, nil
}

// getOrCreate returns the fleetAgent for id, creating a zero value if
// absent. The Agent field is always populated on creation so callers
// can rely on the returned pointer being fully addressable.
func getOrCreate(m map[string]*fleetAgent, id string) *fleetAgent {
	if ag, ok := m[id]; ok {
		return ag
	}
	ag := &fleetAgent{Agent: id}
	m[id] = ag
	return ag
}

// bumpLastSeen updates ag.LastSeen to ts if ts is chronologically
// later. Comparison is routed through versioning.CanonicalTS because
// the wire format (time.RFC3339Nano via versioning.NowISO) trims
// trailing-zero fraction digits, so width is variable and a NAIVE
// lexical compare is NOT chronological (e.g. "…01.1Z" sorts AFTER
// "…01.15Z"). CanonicalTS reformats to a fixed 9-digit-fraction layout
// whose lexical order IS chronological — see versioning.go:81-87 for
// the rationale and PR3 / #97 for the root-cause history.
//
// Empty ts is a no-op so callers can pass an unparsed field without
// pre-checking. The stored value is the ORIGINAL wire ts (canonicalised
// form is the sort key, not the on-row value).
func bumpLastSeen(ag *fleetAgent, ts string) {
	if ts == "" {
		return
	}
	if versioning.CanonicalTS(ts) > versioning.CanonicalTS(ag.LastSeen) {
		ag.LastSeen = ts
	}
}

// scanAgentDir walks <baseDir>/<agent>/*.gdl and invokes onFile for
// every .gdl record under each agent's subdir. The agent id is the
// immediate subdir name. fileTS is the parsed `ts:` field of the first
// record in the file (empty when the file lacks a ts or the record
// is malformed — in which case LastSeen falls through to whatever
// other sources contributed).
//
// Missing baseDir → no-op. Per-file read/parse errors propagate.
func scanAgentDir(byID map[string]*fleetAgent, baseDir string, onFile func(ag *fleetAgent, fileTS string)) error {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		agentID := e.Name()
		agentDir := filepath.Join(baseDir, agentID)
		files, err := os.ReadDir(agentDir)
		if err != nil {
			return err
		}
		var seenAny bool
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".gdl") {
				continue
			}
			seenAny = true
			ag := getOrCreate(byID, agentID)
			ts, _ := firstRecordTS(filepath.Join(agentDir, f.Name()))
			onFile(ag, ts)
		}
		// If the agent dir exists but is empty (no .gdl files), still
		// register the agent — the dir's existence is itself a signal
		// that some peer addressed them. Use the dir mtime as a weak
		// LastSeen so they sort *somewhere* rather than to the very
		// bottom with empty ts. Format with time.RFC3339Nano (matches
		// NowISO's wire shape) — bumpLastSeen routes the compare through
		// versioning.CanonicalTS so width mismatches between trimmed
		// values and the mtime here can't mis-rank chronologically.
		if !seenAny {
			ag := getOrCreate(byID, agentID)
			if info, err := os.Stat(agentDir); err == nil {
				bumpLastSeen(ag, info.ModTime().UTC().Format(time.RFC3339Nano))
			}
		}
	}
	return nil
}

// scanSummons enumerates every from:/to: agent across all four summon
// state dirs (pending/accepted/declined/expired). The on-disk records
// carry both agent ids plus a ts:, so this is the cleanest source for
// "who has tried to open a channel with whom".
//
// Delegates to summon.ReadAll which already iterates the four state
// dirs, parses GDL, and returns typed []Summon — using it picks up the
// canonical parse-error context (`"summon: parse %s: %w"`) and removes
// a parallel state-dir-iteration codepath. Missing live/summons or any
// state dir is not an error (summon.ReadAll treats them as empty).
func scanSummons(root string, byID map[string]*fleetAgent) error {
	sums, err := summon.ReadAll(root)
	if err != nil {
		return err
	}
	for _, s := range sums {
		if s.From != "" {
			ag := getOrCreate(byID, s.From)
			ag.SummonsSent++
			bumpLastSeen(ag, s.TS)
		}
		if s.To != "" {
			ag := getOrCreate(byID, s.To)
			ag.SummonsReceived++
			bumpLastSeen(ag, s.TS)
		}
	}
	return nil
}

// scanLearned walks learned/**/*.gdlm and registers every distinct
// `author:` field as an agent. The synthetic "auto-promote" author is
// skipped (D13.7 — the daemon is the writer of crowd-confirmed facts,
// not a real agent). Per-record `ts:` feeds bumpLastSeen.
//
// Missing learned/ → no-op.
func scanLearned(root string, byID map[string]*fleetAgent) error {
	learnedDir := filepath.Join(root, "learned")
	return filepath.WalkDir(learnedDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return filepath.SkipDir
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".gdlm") {
			return nil
		}
		bs, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		records, err := gdl.ParseDocument(string(bs))
		if err != nil {
			return err
		}
		for _, r := range records {
			if r.Type != "observation" {
				continue
			}
			author := r.Get("author")
			if author == "" || author == "auto-promote" {
				continue
			}
			ag := getOrCreate(byID, author)
			ag.LearnedAuthored++
			bumpLastSeen(ag, r.Get("ts"))
		}
		return nil
	})
}

// firstRecordTS reads path, parses it as GDL, and returns the `ts:`
// field of the first record. Empty string + nil error when the file is
// empty, has no records, or the first record has no ts field.
//
// Parse errors surface to the caller — a malformed on-disk file
// matters for downstream commands too, so silently skipping would mask
// corruption.
func firstRecordTS(path string) (string, error) {
	bs, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return "", err
	}
	for _, r := range records {
		if ts := r.Get("ts"); ts != "" {
			return ts, nil
		}
	}
	return "", nil
}

// renderFleetColumnar prints one tab-separated line per agent. Empty
// input produces zero output (no header) — caller detects "no rows"
// by exit-0 + empty stdout.
//
// Line shape (locked D20.1 + #115 broadening):
//
//	<agent>\tintent:"<intent>"\tentities:<csv>\ttopics:<csv>\tts:<last-seen>\tactivity:<csv>
//
// For agents without a current @attention record the intent column
// renders as `intent:"(no current attention)"` so cold agents can
// distinguish "currently attending" from "historically active".
// The activity column lists non-zero counts (e.g. `thoughts:3,inbox:1`)
// so callers can spot the loudest peer without parsing JSON.
func renderFleetColumnar(rows []fleetAgent, opts output.RenderOpts) {
	// H1b: render the ts field as a relative-time so a fleet listing
	// fits readably in 80 cols. We keep the labelled `ts:` token (the
	// L work-item owns the broader fleet.agent→id rename, so we
	// preserve the existing label set here).
	now := time.Now()
	for _, a := range rows {
		intent := a.Intent
		if !a.HasAttention {
			intent = "(no current attention)"
		}
		line := fmt.Sprintf(
			"%s\tintent:%q\tentities:%s\ttopics:%s\tts:%s\tactivity:%s",
			a.Agent,
			intent,
			strings.Join(a.Entities, ","),
			strings.Join(a.Topics, ","),
			output.Dim(output.RenderRelTime(a.LastSeen, now), opts),
			formatActivity(a),
		)
		// WriteData (not WriteOut) so --quiet does NOT suppress rows —
		// rows are the user's primary output; chatter is the only thing
		// --quiet kills. Matches summons/recall conventions.
		output.WriteData(line, opts)
	}
}

// formatActivity renders the non-zero activity counts as a compact CSV
// like "thoughts:3,inbox:1,summons-sent:1". Returns "-" when the agent
// has zero counted activity (attention-only). Used by the columnar
// renderer to keep the line scannable.
func formatActivity(a fleetAgent) string {
	var parts []string
	if a.Thoughts > 0 {
		parts = append(parts, fmt.Sprintf("thoughts:%d", a.Thoughts))
	}
	if a.InboxDeliveries > 0 {
		parts = append(parts, fmt.Sprintf("inbox:%d", a.InboxDeliveries))
	}
	if a.SummonsSent > 0 {
		parts = append(parts, fmt.Sprintf("summons-sent:%d", a.SummonsSent))
	}
	if a.SummonsReceived > 0 {
		parts = append(parts, fmt.Sprintf("summons-received:%d", a.SummonsReceived))
	}
	if a.LearnedAuthored > 0 {
		parts = append(parts, fmt.Sprintf("learned:%d", a.LearnedAuthored))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

// renderFleetJSON emits one JSONL object per agent. Fields are
// ADDITIVE over the original D20.1 set (locked: _type, _version,
// agent, intent, entities, topics, ts) — broadening adds
// `has_attention`, `last_seen`, and the `activity` object. entities/
// topics still render as arrays (never null) so JSON consumers can
// iterate without nil-checks.
//
// L4 (R26 LOW finding): adds `id` as the canonical identity key,
// matching every other --json surface (channels list, channel show,
// summons, recall). The legacy `agent` key is kept as a DEPRECATED
// alias for one version so existing consumers don't break — both keys
// carry the same value. New code should read `.id`; the deprecation
// window ends in a future release.
func renderFleetJSON(rows []fleetAgent, opts output.RenderOpts) error {
	for _, a := range rows {
		entities := a.Entities
		if entities == nil {
			entities = []string{}
		}
		topics := a.Topics
		if topics == nil {
			topics = []string{}
		}
		// ts retains its original D20.1 semantics for attention agents
		// (the @attention record's ts). For non-attention agents the
		// field is empty — last_seen carries the unioned timestamp.
		payload := map[string]interface{}{
			"_type":    "fleet-agent",
			"_version": "1",
			// L4: `id` is the canonical key (matches every other --json
			// surface). `agent` is a DEPRECATED alias kept for one
			// version; both fields carry the same value during the
			// deprecation window. New consumers should read `.id`.
			"id":            a.Agent,
			"agent":         a.Agent,
			"intent":        a.Intent,
			"entities":      entities,
			"topics":        topics,
			"ts":            a.AttentionTS,
			"has_attention": a.HasAttention,
			"last_seen":     a.LastSeen,
			"activity": map[string]int{
				"thoughts":         a.Thoughts,
				"inbox":            a.InboxDeliveries,
				"summons_sent":     a.SummonsSent,
				"summons_received": a.SummonsReceived,
				"learned":          a.LearnedAuthored,
			},
		}
		if err := output.WriteJSONL(payload, opts); err != nil {
			return err
		}
	}
	return nil
}
