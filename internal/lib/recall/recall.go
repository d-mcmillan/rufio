// Package recall implements `rufio recall` — the first read-side
// cognitive primitive. Scans the project corpus across given/learned/
// outbox/reasoning/retracted namespaces, filters by type/scope/timestamp,
// substring-matches against the query, and renders columnar or JSON
// output.
//
// Per design §C: scan-at-query-time in v1 (index deferred to v1.1).
package recall

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// AllTypes is the canonical type enum per design line 150.
//
// K2 / R28 extension: confirm, refute, retract added so an agent can
// `--types=confirm` etc. and so the search index covers their content
// fields. summon was already on the enum but unindexed in Scan; the
// new scanSummons walker closes that gap.
//
// v1.0.3 extension: auto-promote added so the enriched live/promoted/
// audit records flow through `rufio listen` (and through any --types=
// filter). recall itself still surfaces promotion state via the
// [PROMOTED] state-join on thought records (recall.go ~995), so the
// auto-promote enum value is mainly for the stream/listen surface.
//
// LISTEN-ONLY TYPES (no recall.Scan walker): `channel-message` and
// `auto-promote` are emitted by the stream package's live walkers
// (internal/lib/stream/*.go) but NOT by recall's retrospective scan —
// channel-messages live under live/channels/active/<ch>/messages/
// (gated by membership, see channel_privacy.go; see also
// `rufio channel show <id>` for per-channel retrospection) and
// auto-promote records are an audit surface for the live stream.
// `rufio recall --types=channel-message` therefore returns empty by
// design; use `rufio listen --catch-up --types=channel-message` or
// `rufio channel show` instead. The token stays in AllTypes so
// `--types=` validation accepts it uniformly across listen surfaces.
var AllTypes = []string{"given", "learned", "thought", "observation", "reason", "summon", "confirm", "refute", "retract", "channel-message", "goal", "auto-promote"}

// RecallRecord is the unified record shape across all source namespaces.
//
// ID is the canonical, actionable thought-id — the exact token
// `confirm`/`refute`/`retract`/`think --parent` consume. It is the file
// basename stem of live/outbox/<author>/<id>.gdl (see idFromPath), which
// is precisely what retract.Lookup globs against. It is set ONLY for
// kinds that have a verb-consumable id (thoughts); observation/reason/
// given have no such id and leave it empty.
//
// WHY this field exists: a real 3-harness dogfood (Claude+Gemini+Cursor
// coordinating via rufio) surfaced that an agent who ran `recall` to find
// a peer's thought had no clean way to obtain the id that
// confirm/refute/--parent require — plain output never printed it and
// --json only embedded it inside `path`. Every dogfood agent had to
// `ls live/outbox/` or string-parse the path. ID makes it first-class.
type RecallRecord struct {
	Type string
	// ThoughtType is the on-disk @thought `type:` (decision|hypothesis|
	// observation|…); empty for non-thoughts.
	ThoughtType string
	ID          string
	TS          string
	Author      string
	Subject     string
	Predicate   string
	Object      string
	Content     string
	Scope       string
	Path        string
	Retracted   bool

	// TTL (#149) — integer-seconds expiry from the on-disk @thought
	// `ttl:` field. 0 = never expire (D5.1). Populated only by
	// scanOutbox (the only namespace that writes `ttl:` today); zero
	// for non-thought records. Filter uses this to mark TTL-expired
	// records as hidden in the default view and surface them under
	// --include-expired, symmetric with Retracted.
	TTL int

	// Provenance (#76) — populated ONLY for promoted @observation records
	// (those the auto-promote engine wrote with origin/confirmed-by/source
	// fields). Empty/nil for every other record, including hand-authored
	// observations, so non-promoted rendering is byte-identical.
	//
	//   - Origin      = the author of the originating thought.
	//   - ConfirmedBy = the distinct confirmer ids (already sorted/deduped
	//                    on disk by confirm.ReadAll; preserved as-read).
	//   - Source      = the source thought-id.
	Origin      string
	ConfirmedBy []string
	Source      string

	// State-join surface (H2 / R24). Populated by Scan for thought
	// records only — observation/reason/given have no social-validation
	// surface so these stay zero/false on them. Drives the inline
	// `+N/-M` and `[PROMOTED]` markers in RenderColumnar and the
	// promoted_* keys in RenderJSON.
	ConfirmCount          int
	RefuteCount           int
	Promoted              bool
	PromotedAt            string
	PromotedBy            string
	PromotedObservationID string

	// K2 / R28 — cognition-vocabulary record types. Each carries the
	// load-bearing free-text from its on-disk record so recall's
	// substring search can index them uniformly:
	//   - Intent: populated for @summon records.
	//   - Evidence: populated for @confirm AND @refute records.
	//   - Reason: populated for @refute, @retract, @decline records.
	//   - Target: populated for @confirm/@refute/@retract — the
	//     thought-id the social-validation record acts against.
	//     RESERVED FOR THOUGHT-IDS. @summon's addressee lives in `To`
	//     instead so SDK consumers can dereference `target` as
	//     "thought-id" uniformly without per-type-switching.
	//   - To: populated for @summon — the addressee agent id (the
	//     on-disk @summon `to:` field). Distinct from Target so the
	//     two field semantics never collide in a mixed-type recall.
	// Empty for every other record kind.
	Intent   string
	Evidence string
	Reason   string
	Target   string
	To       string

	// Topics (#180) — the on-disk CSV `topics:` field parsed into a
	// slice. Populated for @thought and @observation records (the only
	// kinds the write verbs tag with --topics=). Nil/empty for every
	// other record kind, AND for records whose writer omitted --topics=
	// (the field is elided entirely on disk per the D4.9 contract).
	//
	// The Filter --topics= predicate uses this slot directly: a
	// non-empty FilterParams.Topics ANY-matches against r.Topics, and
	// records with nil/empty r.Topics are excluded under the filter
	// (no implicit "all topics" match for unlabeled records).
	Topics []string

	// Confidence (v1.0.4 bug #1 expanded scope) — the on-disk @observation
	// `confidence:` field, parsed to float64. Range [0, 1] (the write-path
	// validator). Populated by scanLearned for promoted observations; zero
	// for every other record kind. Used by the auto-promote engine
	// upstream (already on-disk via observation.BuildRecord) but
	// previously never surfaced to recall consumers — the mirror snapshot
	// path was the load-bearing victim (file-native fidelity broken).
	//
	// Stored as float64 (not a stricter typed wrapper) to keep JSON
	// marshaling cheap. Pre-fix the on-disk confidence: rendered cleanly
	// (e.g. "0.85") but recall's JSON dropped it; this slot closes the
	// loop.
	Confidence float64

	// Parent (v1.0.4 bug #1 expanded scope) — the on-disk `parent:` field
	// on @thought and @reason records. Optional on both; omitted entirely
	// on disk when empty (D5.x / R31 contract). Without surfacing it,
	// the lineage chain a thought participates in (e.g. a follow-up
	// hypothesis pointing at a parent decision) is lost through any
	// JSON-mediated round-trip — including mirror snapshot.
	Parent string

	// Decision (v1.0.4 bug #1 expanded scope) — the on-disk `decision:`
	// field on @reason records, pinning the reason to a specific decision
	// thought. Optional. The RenderJSON path already derives the decision
	// id from the file path for reason rows (the H1c #134 short-id
	// surface); this field carries the explicit on-disk value when
	// present, so reconstructed GDL lines (mirror snapshot) preserve
	// what the writer originally wrote.
	Decision string
}

// GetScope satisfies privacy.Record. Returns the on-disk `scope:` field
// (agent|deployment|fleet) or empty for records that pre-date scope
// (e.g. given/ files, which are project-wide by design — D9.3).
func (r RecallRecord) GetScope() string { return r.Scope }

// GetAuthor satisfies privacy.Record. Returns the on-disk `author:`
// field. For given/ files the "unknown" sentinel is preserved; the
// privacy gate only fires for scope=="agent", so given/ records flow
// through unaffected (their Scope is "" anyway).
func (r RecallRecord) GetAuthor() string { return r.Author }

// idFromPath derives the canonical record id from a corpus file path: the
// basename with its extension stripped (e.g.
// .../live/outbox/agent-a/1747000000000-ab12cd.gdl → 1747000000000-ab12cd).
//
// This is THE id confirm/refute/retract/--parent accept: retract.Lookup
// globs live/outbox/*/<id>.gdl, i.e. it matches on exactly this stem
// (internal/lib/retract/retract.go Lookup). thought.Write names the file
// <id>.gdl from thought.GenerateID's <unix-millis>-<rand6>
// (internal/lib/thought/thought.go Write/GenerateID), and the same stem
// is used for retraction-target matching in Scan below — so the
// path-derived id is the single authoritative key, even if an in-record
// `id` field were ever stale.
func idFromPath(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// splitConfirmedBy converts the comma-joined `confirmed-by` value back to
// a slice. Returns nil for empty input so a non-promoted observation's
// ConfirmedBy stays zero-valued (and JSON renders [] not a fabricated
// element). The on-disk order is preserved exactly as written by the
// auto-promote engine, which sourced it from confirm.Tally.Confirms
// (already deduped + sorted) — we do NOT re-sort here.
func splitConfirmedBy(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

// splitTopics converts the comma-joined on-disk `topics:` field value
// back to a slice. Mirrors the write-side encoding: thought.BuildThought
// Record / observation.BuildObservationRecord / attention.BuildRecord
// all use strings.Join(in.Topics, ",") and elide the key entirely when
// the slice is empty (D4.9). Empty input → nil so #180's
// "record without topics: field is excluded under --topics=" rule lights
// up uniformly via len(r.Topics)==0.
//
// Tokens are NOT re-trimmed: validators on the write side (attention.
// ValidateTopics + the [a-z0-9][a-z0-9_.-]* regex) already enforce a
// strict shape, and any whitespace inside a token would be a writer-side
// bug we don't want to silently paper over here.
func splitTopics(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

// RelativisePath converts an absolute substrate path into the canonical
// root-relative POSIX form emitted by recall/open JSON. Security audit
// H2 (v1.0.4): pre-fix, RenderJSON shipped the server's absolute path
// to every authenticated caller, leaking the operator's home directory
// + substrate root layout. The fix relativises at the serialization
// edge so internal consumers (the export verb's os.ReadFile, the
// retraction-marker prefix check) keep working off absolute paths.
//
// Exported so the open package can call it from JSONPayload and keep
// the recall+open wire-shape symmetry contract intact (same
// relativisation function, same output shape).
//
// Bonus (security audit followup): root is now an EXPLICIT parameter
// (was previously inferred via substring search for /given/ /learned/
// /live/, which mis-sliced when the operator's substrate root itself
// contained one of those tokens — e.g. /srv/live-prod/ leaked
// "live-prod/.rufio/live/outbox/..."). Threading root through means
// filepath.Rel resolves correctly regardless of root layout.
//
// Best-effort fallbacks (defensive — should never happen with the
// current scanners): empty absPath, empty root, or filepath.Rel
// failure all pass the input through unchanged so a future caller
// that hasn't been wired up stays visible rather than silently
// losing the slot.
func RelativisePath(absPath, root string) string {
	if absPath == "" {
		return ""
	}
	if root == "" {
		return absPath
	}
	rel, err := filepath.Rel(root, absPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return absPath
	}
	return filepath.ToSlash(rel)
}

// parseFloatOrZero parses a confidence-style float value from a GDL
// field. Returns 0 on empty or malformed input — the write path
// (observation.ParseConfidence) already validates the [0,1] range
// upstream, so a malformed on-disk value is a legacy/edge case rather
// than a hot path.
func parseFloatOrZero(raw string) float64 {
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0
	}
	return v
}

// thoughtSubtypes is the set of thought-subtypes that previously
// collided with --types= (P3/R31). They're NOT record-types — they're
// the `type:` field on @thought records — so passing them to --types=
// returns *ThoughtTypeAsRecordTypeError with the corrected shape.
//
// Kept local to recall (not exported from thought) to avoid pulling the
// thought package's full enum into the import path; the small
// duplication is justified by the disambiguation it buys.
var thoughtSubtypes = map[string]bool{
	"decision":   true,
	"hypothesis": true,
	"focus":      true,
	"question":   true,
	// `observation` is INTENTIONALLY OMITTED here: it's both a
	// record-type (durable SPO under learned/) AND a thought-subtype.
	// Resolving the ambiguity to RECORD-type semantics matches the
	// brief: `--types=observation` MUST return ONLY learned/ SPOs, not
	// @thought type:observation records. Agents who want the latter
	// must go via `--types=thought --thought-types=observation`.
}

// ValidateTypes parses a CSV --types value; returns the parsed slice or
// *InvalidTypesError on any unknown token. Empty input → all types.
//
// P3/R31: if any token is a thought-SUBTYPE (decision|hypothesis|
// focus|question — but NOT observation, which is also a record-type),
// returns *ThoughtTypeAsRecordTypeError with a helpful redirect to
// `--types=thought --thought-types=<subtype>` instead of the generic
// "must be from <enum>" dump.
func ValidateTypes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		// Return a copy so callers can't mutate AllTypes.
		out := make([]string, len(AllTypes))
		copy(out, AllTypes)
		return out, nil
	}
	allowed := make(map[string]bool, len(AllTypes))
	for _, t := range AllTypes {
		allowed[t] = true
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		tok := strings.TrimSpace(p)
		if !allowed[tok] {
			// P3/R31: redirect on thought-subtype collision.
			if thoughtSubtypes[tok] {
				return nil, &rufioerr.ThoughtTypeAsRecordTypeError{Value: tok}
			}
			return nil, &rufioerr.InvalidTypesError{Value: tok}
		}
		out = append(out, tok)
	}
	return out, nil
}

// allowedThoughtTypes is the canonical thought-subtype enum mirrored
// from thought.allowedTypes. Used by ValidateThoughtTypes (P3/R31) to
// validate --thought-types=<csv> tokens.
//
// Sync gate: if thought.allowedTypes changes, this must change too.
// We don't import the thought package to avoid the cycle (recall is
// imported by thought-adjacent code). Tests pin the symmetry.
var allowedThoughtTypes = []string{"decision", "hypothesis", "observation", "focus", "question"}

// ValidateThoughtTypes parses a CSV --thought-types value; returns the
// parsed slice or *InvalidThoughtTypesError on any unknown token.
// Empty input → nil (no thought-subtype filter; recall returns all
// thought subtypes when --thought-types is omitted).
//
// P3/R31: this is the new flag that closes the recall namespace
// ambiguity. --types= selects RECORD types; --thought-types= filters
// WITHIN --types=thought by the on-disk @thought `type:` field.
func ValidateThoughtTypes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	allowed := make(map[string]bool, len(allowedThoughtTypes))
	for _, t := range allowedThoughtTypes {
		allowed[t] = true
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		tok := strings.TrimSpace(p)
		if !allowed[tok] {
			return nil, &rufioerr.InvalidThoughtTypesError{Value: tok}
		}
		out = append(out, tok)
	}
	return out, nil
}

// ParseSince parses --since as a Go duration. Empty → zero duration (no
// filter). Returns *InvalidDurationError on parse fail or non-positive
// duration.
func ParseSince(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, &rufioerr.InvalidDurationError{Raw: raw}
	}
	return d, nil
}

// ParseAsOf parses --as-of as RFC3339 (with or without sub-second
// precision). Empty → zero time (no filter). Returns
// *InvalidTimestampError on parse fail.
func ParseAsOf(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	// Try RFC3339Nano first (also accepts RFC3339).
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, &rufioerr.InvalidTimestampError{Raw: raw}
	}
	return t, nil
}

// looksBinary reports whether buf contains any NUL byte. This is the
// classic heuristic for distinguishing text from binary blobs (matches
// `grep -I`, `file --mime`, etc.).
func looksBinary(buf []byte) bool {
	for _, b := range buf {
		if b == 0x00 {
			return true
		}
	}
	return false
}

// scanGiven walks <root>/given/ recursively for any file. For each file:
//   - Subject = path relative to <root> in POSIX form ("given/policy.md")
//   - Content = file body as string (skipped for binary files per heuristic)
//   - TS = latest-live-ref ts from .rufio/refs/<path>.gdl; falls back to
//     file ModTime() in RFC3339Nano if no ref exists
//   - Author = latest-live-ref author; "unknown" if no ref
//   - Type = "given"; Scope = "" (given/ records are project-wide per D9.3)
//   - Path = absolute path
//
// Missing given/ dir → (nil, nil) — empty corpus is fine.
func scanGiven(root string) ([]RecallRecord, error) {
	givenDir := filepath.Join(root, "given")
	info, err := os.Stat(givenDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}

	var out []RecallRecord
	walkErr := filepath.WalkDir(givenDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// POSIX-form subject relative to root.
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		subject := filepath.ToSlash(rel)

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := ""
		probe := body
		if len(probe) > 512 {
			probe = probe[:512]
		}
		if !looksBinary(probe) {
			content = string(body)
		}

		// Derive ts/author from .rufio/refs/<subject>.gdl when present.
		ts := ""
		author := "unknown"
		refs, refErr := versioning.ReadRefs(root, subject)
		if refErr != nil {
			return refErr
		}
		if latest := versioning.LatestRefByStage(refs, versioning.StageLive); latest != nil {
			ts = latest.Timestamp
			author = latest.Author
		}
		if ts == "" {
			fi, statErr := os.Stat(path)
			if statErr != nil {
				return statErr
			}
			ts = fi.ModTime().UTC().Format(time.RFC3339Nano)
		}

		out = append(out, RecallRecord{
			Type:    "given",
			TS:      ts,
			Author:  author,
			Subject: subject,
			Content: content,
			Path:    path,
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// scanLearned walks <root>/learned/ recursively for *.gdlm files and
// parses each via gdl.ParseDocument. For each @observation record found,
// emit a RecallRecord with Type="observation" and TS/Author/Subject/
// Predicate/Object/Scope copied from the record's fields. Content is
// empty — observations are subject/predicate/object structured data, not
// free-text content (PR #6 design).
//
// Non-@observation records in .gdlm files are skipped (no other record
// types live there in v1).
//
// Missing learned/ dir → (nil, nil).
func scanLearned(root string) ([]RecallRecord, error) {
	dir := filepath.Join(root, "learned")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	var out []RecallRecord
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".gdlm" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		records, err := gdl.ParseDocument(string(data))
		if err != nil {
			return err
		}
		for _, r := range records {
			if r.Type != "observation" {
				continue
			}
			out = append(out, RecallRecord{
				Type: "observation",
				// ID is the file-basename-stem (#89: observations
				// previously left ID==""). idFromPath, NOT r.Get("id"),
				// consistent with how scanOutbox sets thought ids.
				ID:        idFromPath(path),
				TS:        r.Get("ts"),
				Author:    r.Get("author"),
				Subject:   r.Get("subject"),
				Predicate: r.Get("predicate"),
				Object:    r.Get("object"),
				Scope:     r.Get("scope"),
				Path:      path,
				// Topics (#180) — surface the on-disk `topics:` slice so
				// the recall --topics= filter can ANY-match it. Nil when
				// the writer omitted --topics= (the field is elided per
				// D4.9), which under #180 correctly excludes the row
				// from any --topics=<csv> filter.
				Topics: splitTopics(r.Get("topics")),
				// Confidence (v1.0.4 bug #1 expanded scope) — surface the
				// on-disk `confidence:` value so JSON consumers (mirror
				// snapshot, JSONL export, jq pipelines) preserve the
				// observation's epistemic weight. Parse failure (e.g.
				// legacy record predating the field) yields 0 silently;
				// upstream observation.ParseConfidence already gates
				// writes so a malformed value on disk is rare.
				Confidence: parseFloatOrZero(r.Get("confidence")),
				// Provenance (#76). r.Get returns "" for absent keys, so a
				// non-promoted observation yields zero-valued provenance
				// (splitConfirmedBy("") → nil) — non-promoted rows
				// unchanged. Extra unknown keys on the @observation are
				// ignored by gdl.Get (additive, non-breaking).
				Origin:      r.Get("origin"),
				ConfirmedBy: splitConfirmedBy(r.Get("confirmed-by")),
				Source:      r.Get("source"),
			})
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// scanOutbox walks <root>/live/outbox/<author>/ recursively for *.gdl
// files and parses each. For every @thought record emit a RecallRecord
// with Type="thought" and fields copied from the record. Sibling
// @context-bundle records (decision metadata, not recall-surface) are
// skipped.
//
// Missing outbox dir → (nil, nil).
func scanOutbox(root string) ([]RecallRecord, error) {
	dir := filepath.Join(root, "live", "outbox")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	var out []RecallRecord
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".gdl" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		records, err := gdl.ParseDocument(string(data))
		if err != nil {
			return err
		}
		for _, r := range records {
			if r.Type != "thought" {
				continue
			}
			// TTL (#149) — integer-seconds expiry. Parse defensively: a
			// missing or unparseable ttl: field treats the record as
			// non-expiring (TTL=0), matching the pre-#149 behavior
			// (records never hid for ttl reasons). Negative values are
			// clamped to 0 too — Filter only acts on positive ttl.
			ttl := 0
			if v := r.Get("ttl"); v != "" {
				if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
					ttl = n
				}
			}
			out = append(out, RecallRecord{
				Type: "thought",
				// ThoughtType carries the on-disk @thought `type:`
				// (decision|hypothesis|observation|…). #89: previously
				// r.Get("type") was never called here so the on-disk
				// thought type was dropped entirely from --json.
				ThoughtType: r.Get("type"),
				// ID is the file-basename-stem — the exact token
				// confirm/refute/retract/--parent consume (retract.Lookup
				// globs live/outbox/*/<id>.gdl). This closes the dogfood
				// gap: recall now hands the agent the id it needs to act.
				ID:        idFromPath(path),
				TS:        r.Get("ts"),
				Author:    r.Get("author"),
				Subject:   r.Get("subject"),
				Predicate: r.Get("predicate"),
				Object:    r.Get("object"),
				Content:   r.Get("content"),
				Scope:     r.Get("scope"),
				Path:      path,
				TTL:       ttl,
				// Topics (#180) — surface the on-disk `topics:` slice so
				// the recall --topics= filter can ANY-match it. Nil when
				// the writer omitted --topics= (per D4.9 the field is
				// elided entirely on disk); #180's rule then correctly
				// excludes the row from any --topics=<csv> filter.
				Topics: splitTopics(r.Get("topics")),
				// Parent (v1.0.4 bug #1 expanded scope) — surface the
				// on-disk `parent:` so the lineage chain survives
				// JSON-mediated round-trip (mirror snapshot). Empty when
				// the writer omitted --parent.
				Parent: r.Get("parent"),
			})
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// scanReasoning walks <root>/live/reasoning/<author>/[<decision>/]
// recursively for *.gdl files and parses each @reason record into a
// RecallRecord with Type="reason". Reason records have no subject/
// predicate/object/scope per PR #7 D7.6 — only content/author/ts.
//
// Missing reasoning dir → (nil, nil).
func scanReasoning(root string) ([]RecallRecord, error) {
	dir := filepath.Join(root, "live", "reasoning")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	var out []RecallRecord
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".gdl" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		records, err := gdl.ParseDocument(string(data))
		if err != nil {
			return err
		}
		for _, r := range records {
			if r.Type != "reason" {
				continue
			}
			out = append(out, RecallRecord{
				Type: "reason",
				// ID is the file-basename-stem so reason rows surface
				// their own actionable id (R25 #134: text recall used to
				// drop both id and decision linkage; H1c restores both).
				ID:      idFromPath(path),
				TS:      r.Get("ts"),
				Author:  r.Get("author"),
				Content: r.Get("content"),
				Path:    path,
				// v1.0.4 bug #1 expanded scope — reason records on disk
				// carry subject (P2/R31), scope (#125), topics, parent,
				// and decision (R31). scanReasoning previously dropped
				// every one of them, breaking the snapshot mirror's
				// file-native fidelity claim for reason chains.
				Subject:  r.Get("subject"),
				Scope:    r.Get("scope"),
				Topics:   splitTopics(r.Get("topics")),
				Parent:   r.Get("parent"),
				Decision: r.Get("decision"),
			})
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// scanSummons walks <root>/live/summons/{pending,accepted,declined,
// expired}/ for *.gdl files and emits one RecallRecord per @summon
// record. Intent is populated so the substring search indexes it
// (R28: agent-cx's core claim lived inside summon --intent="…").
//
// Topic is rendered as Subject so existing entity-id-exact-match
// semantics in Match work for callers who pass a topic literally.
// State (pending/accepted/declined/expired) is captured via the
// source directory — exposed in Scope (free-form repurpose) is wrong;
// summons don't carry an agent/deployment/fleet scope on disk. We
// leave Scope empty so the privacy gate is permissive (summons are
// project-wide by design, like given/), and rely on the existing
// from/to identity fields for visibility downstream.
//
// Missing summons dir → (nil, nil).
func scanSummons(root string) ([]RecallRecord, error) {
	base := filepath.Join(root, "live", "summons")
	if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []RecallRecord
	for _, state := range []string{"pending", "accepted", "declined", "expired"} {
		dir := filepath.Join(base, state)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			records, err := gdl.ParseDocument(string(data))
			if err != nil {
				return nil, err
			}
			for _, r := range records {
				if r.Type != "summon" {
					continue
				}
				out = append(out, RecallRecord{
					Type:    "summon",
					ID:      idFromPath(path),
					TS:      r.Get("ts"),
					Author:  r.Get("from"),
					Subject: r.Get("topic"),
					Intent:  r.Get("intent"),
					Content: r.Get("intent"), // mirror to Content so existing
					// `Match` substring search hits without
					// having to special-case Intent. Cheap.
					To: r.Get("to"), // the addressee. Lives in `To`, not
					// `Target`, so a mixed-type recall
					// (summon + refute) doesn't collide on
					// `target` (thought-id for refute,
					// agent-id for summon). RenderJSON emits
					// `to` only for summons.
					Path: path,
				})
			}
		}
	}
	return out, nil
}

// scanConfirms walks <root>/live/confirms/<target>.gdl for *.gdl files
// and emits one RecallRecord per @confirm AND per @refute record.
// (Both kinds share the same file; ReadRecords does the split.) The
// load-bearing free text — @confirm.evidence, @refute.reason,
// @refute.evidence — is mirrored to Content so the substring search
// indexes it uniformly. Target carries the thought-id the record
// acts against; Subject is left empty (these records don't have a
// subject of their own — they're metadata on the target).
//
// Privacy: scope is intentionally empty on confirm/refute records
// because the writer side (confirm.go / refute.go) enforces the
// privacy.CanWriteAgainst gate against the TARGET's scope. Reading
// the records does not require re-gating beyond that — if the
// target's scope:agent floor was respected at write time, the record
// is itself project-visible metadata.
//
// Missing confirms dir → (nil, nil).
func scanConfirms(root string) ([]RecallRecord, error) {
	dir := filepath.Join(root, "live", "confirms")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []RecallRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		target := idFromPath(path)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		records, err := gdl.ParseDocument(string(data))
		if err != nil {
			return nil, err
		}
		for _, r := range records {
			switch r.Type {
			case "confirm":
				evidence := r.Get("evidence")
				out = append(out, RecallRecord{
					Type:     "confirm",
					TS:       r.Get("ts"),
					Author:   r.Get("by"),
					Target:   target,
					Evidence: evidence,
					Content:  evidence,
					Path:     path,
				})
			case "refute":
				reason := r.Get("reason")
				evidence := r.Get("evidence")
				// Mirror BOTH reason and evidence into Content so the
				// substring search indexes the load-bearing free text
				// without per-field branches. Space separator preserves
				// word boundaries for multi-word AND semantics.
				combined := reason
				if reason != "" && evidence != "" {
					combined = reason + " " + evidence
				} else if reason == "" {
					combined = evidence
				}
				out = append(out, RecallRecord{
					Type:     "refute",
					TS:       r.Get("ts"),
					Author:   r.Get("by"),
					Target:   target,
					Reason:   reason,
					Evidence: evidence,
					Content:  combined,
					Path:     path,
				})
			}
		}
	}
	return out, nil
}

// scanRetracts walks <root>/live/retracted/ for *.gdl files and emits
// one RecallRecord per @retract record. Reason is mirrored to Content
// for the substring search. Target carries the thought-id the retract
// acts against.
//
// Note: scanRetracted (different function, still kept) returns a
// target-id set used to mark related records as Retracted=true. The
// two scanners serve different purposes: scanRetracted feeds the
// IncludeExpired gate; scanRetracts adds @retract records as
// first-class recall rows so the reason text is searchable.
//
// Retract records are themselves "expired" from the default-view
// perspective in the sense that the thought they target is hidden by
// default — recall behavior gates these via IncludeExpired so a stale
// retract reason doesn't pollute the default view. With
// --include-expired the rows surface.
func scanRetracts(root string) ([]RecallRecord, error) {
	dir := filepath.Join(root, "live", "retracted")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []RecallRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		records, err := gdl.ParseDocument(string(data))
		if err != nil {
			return nil, err
		}
		for _, r := range records {
			if r.Type != "retract" {
				continue
			}
			reason := r.Get("reason")
			out = append(out, RecallRecord{
				Type:    "retract",
				TS:      r.Get("ts"),
				Author:  r.Get("by"),
				Target:  r.Get("target"),
				Reason:  reason,
				Content: reason,
				Path:    path,
				// Retract records describe expired state by definition —
				// pre-mark them so the default-view filter hides them
				// (preserves the existing "stale retract reasons don't
				// pollute the default view" property; --include-expired
				// surfaces them).
				Retracted: true,
			})
		}
	}
	return out, nil
}

// scanRetracted walks <root>/live/retracted/ for *.gdl files and returns
// the set of target thought-ids (the `target` field of each @retract
// record). Callers use this to mark related records as Retracted=true.
//
// Missing retracted dir → (nil, nil).
func scanRetracted(root string) (map[string]bool, error) {
	dir := filepath.Join(root, "live", "retracted")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	out := make(map[string]bool)
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".gdl" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		records, err := gdl.ParseDocument(string(data))
		if err != nil {
			return err
		}
		for _, r := range records {
			if r.Type != "retract" {
				continue
			}
			if target := r.Get("target"); target != "" {
				out[target] = true
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return out, nil
}

// scanGoals walks <root>/live/goals/{active,completed,abandoned}/ for
// *.gdl files and emits one RecallRecord per @goal record. F2
// (v1.0.6.2): pre-fix the recall AllTypes enum advertised "goal" as a
// valid --types value but no scanner was wired in — `recall
// --types=goal` silently returned zero rows even when goals existed.
// Cold agents wrote a goal, tried to recall it, and concluded the
// write had failed.
//
// Statement is mirrored to BOTH Subject and Content so:
//   - a positional substring query (`recall "ship v1.0.7"`) hits the
//     statement via the Match free-text search (uses Subject + Content)
//   - an entity-id-shaped query that happens to match the statement
//     exactly hits via the entity-id-exact-match fast path in Match.
//
// Privacy: Scope and Author are populated from the on-disk fields so
// the existing Filter privacy gate (privacy.IsVisible against
// p.CurrentAgent) fires automatically — same floor `goals list`
// enforces (#147 contract). No additional gating is needed here; the
// Filter is the single chokepoint.
//
// Audit records (@goal-complete, @goal-abandon) live in the same file
// as the @goal record for completed/abandoned goals; they are NOT
// surfaced as their own RecallRecord rows (they are metadata about
// the @goal, like @auto-promote is metadata about a thought). Their
// state IS implicitly captured by which directory the file lives in.
//
// Missing goals dir → (nil, nil). Individual missing state subdirs are
// tolerated — fresh projects scaffold all three, but a project that
// has only ever written active goals still works.
func scanGoals(root string) ([]RecallRecord, error) {
	base := filepath.Join(root, "live", "goals")
	if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var out []RecallRecord
	for _, state := range []string{"active", "completed", "abandoned"} {
		dir := filepath.Join(base, state)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			records, err := gdl.ParseDocument(string(data))
			if err != nil {
				return nil, err
			}
			for _, r := range records {
				// Only the @goal record itself becomes a RecallRecord;
				// audit records (@goal-complete, @goal-abandon) are
				// metadata for the goal and are not their own surface.
				if r.Type != "goal" {
					continue
				}
				statement := r.Get("statement")
				out = append(out, RecallRecord{
					Type:    "goal",
					ID:      idFromPath(path),
					TS:      r.Get("ts"),
					Author:  r.Get("author"),
					Subject: statement, // free-text statement — Match uses Subject in its haystack
					Content: statement, // mirror so the substring search hits uniformly
					Scope:   r.Get("scope"),
					Parent:  r.Get("parent"), // optional — empty when absent on disk
					Path:    path,
				})
			}
		}
	}
	return out, nil
}

// Scan walks all relevant source paths under root and returns the full
// corpus as []RecallRecord. Filters (type/scope/since/as-of/query) are
// NOT applied here — that's the renderer/filter layer's job.
//
// Sources walked: given/, learned/, live/outbox/, live/reasoning/,
// live/summons/, live/confirms/, live/retracted/, and
// live/goals/{active,completed,abandoned}/ (F2). When includeRetracted
// is true, live/retracted/ is also scanned for marker purposes and any
// record whose file basename (stem) matches a retracted target-id is
// marked Retracted=true.
//
// Errors from individual scanners propagate.
func Scan(root string, includeRetracted bool) ([]RecallRecord, error) {
	var all []RecallRecord

	given, err := scanGiven(root)
	if err != nil {
		return nil, err
	}
	all = append(all, given...)

	learned, err := scanLearned(root)
	if err != nil {
		return nil, err
	}
	all = append(all, learned...)

	outbox, err := scanOutbox(root)
	if err != nil {
		return nil, err
	}
	all = append(all, outbox...)

	reasoning, err := scanReasoning(root)
	if err != nil {
		return nil, err
	}
	all = append(all, reasoning...)

	// K2 / R28 — cognition-vocabulary record kinds. Each scanner is
	// best-effort, missing-dir-tolerant, and adds rows with the
	// load-bearing free text mirrored to Content so the existing
	// Match substring search indexes them uniformly.
	summons, err := scanSummons(root)
	if err != nil {
		return nil, err
	}
	all = append(all, summons...)

	confirms, err := scanConfirms(root)
	if err != nil {
		return nil, err
	}
	all = append(all, confirms...)

	retracts, err := scanRetracts(root)
	if err != nil {
		return nil, err
	}
	all = append(all, retracts...)

	// F2 (v1.0.6.2) — close the advertised-vs-implemented gap on the
	// goals record kind. AllTypes has listed "goal" since #147 but no
	// scanner existed; cold agents lost trust in the substrate when
	// `recall --types=goal` returned empty on goals they had just
	// written. scanGoals walks live/goals/{active,completed,abandoned}/
	// uniformly; privacy enforcement is handled by Filter via the
	// populated Scope+Author fields (same gate `goals list` uses).
	goals, err := scanGoals(root)
	if err != nil {
		return nil, err
	}
	all = append(all, goals...)

	if includeRetracted {
		retracted, err := scanRetracted(root)
		if err != nil {
			return nil, err
		}
		if len(retracted) > 0 {
			liveMarker := filepath.Join(root, "live") + string(filepath.Separator)
			for i := range all {
				// Only records from live/ can be retraction targets. given/ and
				// learned/ files happen to share basename patterns occasionally
				// but are not retractable. This guards against cross-source
				// collisions.
				if !strings.HasPrefix(all[i].Path, liveMarker) {
					continue
				}
				if retracted[idFromPath(all[i].Path)] {
					all[i].Retracted = true
				}
			}
		}
	}

	// H2 / R24 — state-join markers for thoughts. Join confirm tallies
	// and @auto-promote audit records per-thought into the unified
	// RecallRecord shape so RenderColumnar/RenderJSON can surface
	// social state inline. R24 found agents otherwise burn 6 commands
	// (recall → lineage → confirms → refutes → retracts → ...) per id.
	//
	// Confined to thought records: observation/reason/given have no
	// confirm/refute/promote surface — joining them is a noop. Both
	// joins are fail-soft (read errors → zero/false on the row, not a
	// scan-wide abort) for the same reason as the retract join above.
	for i := range all {
		if all[i].Type != "thought" || all[i].ID == "" {
			continue
		}
		if tally, terr := readConfirmTally(root, all[i].ID); terr == nil {
			all[i].ConfirmCount = len(tally.Confirms)
			all[i].RefuteCount = len(tally.Refutes)
		}
		if pr, perr := readPromoteAudit(root, all[i].ID); perr == nil && pr.Present {
			all[i].Promoted = true
			all[i].PromotedAt = pr.TS
			all[i].PromotedBy = pr.By
			all[i].PromotedObservationID = pr.ObservationID
		}
	}

	return all, nil
}

// promoteAudit is the projection of an @auto-promote record at
// live/promoted/<id>.gdl used by the recall state-join (H2). Present
// stays false for @promote-skipped markers — a skipped promotion never
// reached learned/, so emitting [PROMOTED] would mislead.
type promoteAudit struct {
	Present       bool
	TS            string
	By            string
	ObservationID string
}

// readConfirmTally is the recall package's read-side dependency on
// live/confirms/<id>.gdl. We deliberately inline a slim parser here
// rather than import internal/lib/confirm to avoid a cycle (confirm
// could plausibly depend on recall in the future). Shape matches
// confirm.ReadAll exactly — deduped + sorted agent sets per kind.
// Missing file → empty tally + nil err.
func readConfirmTally(root, targetID string) (confirmTally, error) {
	path := filepath.Join(root, "live", "confirms", targetID+".gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return confirmTally{}, nil
		}
		return confirmTally{}, err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return confirmTally{}, err
	}
	confirmSet := make(map[string]bool)
	refuteSet := make(map[string]bool)
	for _, r := range records {
		by := r.Get("by")
		if by == "" {
			continue
		}
		switch r.Type {
		case "confirm":
			confirmSet[by] = true
		case "refute":
			refuteSet[by] = true
		}
	}
	t := confirmTally{}
	for a := range confirmSet {
		t.Confirms = append(t.Confirms, a)
	}
	for a := range refuteSet {
		t.Refutes = append(t.Refutes, a)
	}
	return t, nil
}

// confirmTally is the deduped-agent-set projection of live/confirms/
// used by the recall state-join. Mirrors confirm.Tally but kept local
// to avoid the cross-package dependency (see readConfirmTally).
type confirmTally struct {
	Confirms []string
	Refutes  []string
}

// readPromoteAudit parses live/promoted/<id>.gdl and returns the
// @auto-promote audit record (Present=true). Skipped promotions
// (@promote-skipped) and any other content return Present=false.
// Missing file → zero value + nil error. Parse errors propagate.
func readPromoteAudit(root, targetID string) (promoteAudit, error) {
	path := filepath.Join(root, "live", "promoted", targetID+".gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return promoteAudit{}, nil
		}
		return promoteAudit{}, err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return promoteAudit{}, err
	}
	for _, r := range records {
		if r.Type != "auto-promote" {
			continue
		}
		return promoteAudit{
			Present:       true,
			TS:            r.Get("ts"),
			By:            r.Get("by"),
			ObservationID: r.Get("observation"),
		}, nil
	}
	return promoteAudit{}, nil
}

// FilterParams collects the recall flag values for the filter pipeline.
type FilterParams struct {
	Types []string
	// ThoughtTypes (P3/R31) — when non-nil/non-empty, ONLY thought records
	// whose on-disk @thought `type:` is in this set survive the filter.
	// Nil means "no thought-subtype filter" (all thought subtypes pass).
	// Has effect only on rows where Type=="thought"; non-thought rows
	// flow through unchanged.
	ThoughtTypes []string
	// Topics (#180) — when non-nil/non-empty, only records whose on-disk
	// `topics:` field contains AT LEAST ONE of these tokens (ANY-match,
	// set-intersection ≠ ∅) survive the filter. Records with no `topics:`
	// field (or an empty one) are EXCLUDED when Topics is set — there is
	// no implicit "all topics" match for unlabeled records.
	//
	// Nil/empty means "no topic filter" (regression-safe default — when
	// `recall` is called without --topics= the gate is a noop and every
	// record passes it, byte-identical to pre-#180).
	Topics         []string
	Scope          string
	Since          time.Duration
	AsOf           time.Time
	IncludeExpired bool
	CurrentAgent   string    // for --scope=agent filtering
	Now            time.Time // for --since computation (injectable for tests)
}

// Filter applies type/scope/timestamp filters. Records flow through in
// order; only records passing every active filter survive.
//
// Visibility rule: given/ records ALWAYS bypass scope filtering
// (project-wide visibility per D9.3). --scope=agent excludes
// agent-scoped records authored by OTHERS; broader-scoped records
// (deployment, fleet) remain visible at the agent level.
func Filter(records []RecallRecord, p FilterParams) []RecallRecord {
	// Pre-build type set for O(1) lookup.
	var typeSet map[string]bool
	if len(p.Types) > 0 {
		typeSet = make(map[string]bool, len(p.Types))
		for _, t := range p.Types {
			typeSet[t] = true
		}
	}

	var sinceFloor time.Time
	if p.Since > 0 {
		now := p.Now
		if now.IsZero() {
			now = time.Now()
		}
		sinceFloor = now.Add(-p.Since)
	}

	// P3/R31: pre-build thought-subtype set for O(1) lookup. nil/empty
	// → no thought-subtype filter (all subtypes pass).
	var thoughtTypeSet map[string]bool
	if len(p.ThoughtTypes) > 0 {
		thoughtTypeSet = make(map[string]bool, len(p.ThoughtTypes))
		for _, t := range p.ThoughtTypes {
			thoughtTypeSet[t] = true
		}
	}

	// #180: pre-build topic set for O(1) lookup. nil/empty → no topic
	// filter (every record passes the gate; regression-safe default).
	// We deliberately preserve the same "empty CSV = no filter"
	// semantics as ThoughtTypes/Types: the CLI parses `--topics=` (empty
	// value) into a nil slice via splitCSVTrim, and a non-nil but
	// zero-length slice from a programmatic caller is treated
	// identically.
	var topicSet map[string]bool
	if len(p.Topics) > 0 {
		topicSet = make(map[string]bool, len(p.Topics))
		for _, t := range p.Topics {
			topicSet[t] = true
		}
	}

	out := make([]RecallRecord, 0, len(records))
	for _, r := range records {
		// Type filter.
		if typeSet != nil && !typeSet[r.Type] {
			continue
		}
		// P3/R31: thought-subtype filter. Applies ONLY to thought
		// records (non-thought rows pass unchanged so an agent passing
		// --types=thought,observation --thought-types=decision still
		// sees observations alongside decision-thoughts).
		if thoughtTypeSet != nil && r.Type == "thought" {
			if !thoughtTypeSet[r.ThoughtType] {
				continue
			}
		}
		// #180: topic filter — ANY-match. A record passes iff its
		// on-disk topics set intersects p.Topics. Records with no
		// topics: field (r.Topics nil/empty) are UNCONDITIONALLY
		// excluded when the filter is active — there's no implicit
		// "all topics" match for unlabeled records.
		//
		// Applied uniformly across record types: thought + observation
		// are the kinds the write verbs tag with --topics= today;
		// everything else (given/learned-without-topics/reason/summon/
		// confirm/refute/retract) currently has no topics: surface
		// and is correctly excluded by this rule. If a future verb
		// learns to write topics: this gate picks it up for free.
		if topicSet != nil {
			if len(r.Topics) == 0 {
				continue
			}
			any := false
			for _, t := range r.Topics {
				if topicSet[t] {
					any = true
					break
				}
			}
			if !any {
				continue
			}
		}
		// Privacy gate (#147). Mirrors stream.Match's privacy branch: when
		// no explicit --scope filter was set, hide scope:agent records
		// authored by other agents from an identified caller. given/
		// records bypass (project-wide per D9.3). When --scope is set,
		// scopePass below already enforces the same-author rule for
		// same-rank records, so the gate would be redundant — skip it.
		// Anonymous caller (CurrentAgent="") preserves the firehose path
		// inside privacy.IsVisible.
		if p.Scope == "" && r.Type != "given" {
			if !privacy.IsVisible(r, p.CurrentAgent) {
				continue
			}
		}
		// Scope filter (given/ records bypass — D9.3).
		if p.Scope != "" && r.Type != "given" {
			if !scopePass(r, p.Scope, p.CurrentAgent) {
				continue
			}
		}
		// Since filter.
		if !sinceFloor.IsZero() {
			ts, err := time.Parse(time.RFC3339Nano, r.TS)
			if err != nil || ts.Before(sinceFloor) {
				continue
			}
		}
		// AsOf filter.
		if !p.AsOf.IsZero() {
			ts, err := time.Parse(time.RFC3339Nano, r.TS)
			if err != nil || ts.After(p.AsOf) {
				continue
			}
		}
		// IncludeExpired gate: when false, records flagged Retracted=true OR
		// TTL-expired (ts + ttl*seconds < now AND ttl > 0) are hidden from
		// the default view. With --include-expired, both classes surface.
		// (#149: previously the gate handled retracted-only — TTL-expired
		// records were silently unreachable from every read API even
		// though their files were still on disk.)
		if !p.IncludeExpired {
			if r.Retracted {
				continue
			}
			if isTTLExpired(r, p.Now) {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// isTTLExpired reports whether r should be treated as TTL-expired at
// "now". A record is expired iff ttl > 0 AND ts + ttl*seconds < now.
// Records with ttl=0 NEVER expire (D5.1). Unparseable timestamps are
// treated as non-expired — defensive (matches the Since/AsOf branches
// in Filter, which also skip the gate on parse failure rather than
// silently drop the record). If p.Now is the zero value, time.Now() is
// used (matches the Since branch). Exported only to internal callers
// via a shared visibility predicate (#151).
func isTTLExpired(r RecallRecord, now time.Time) bool {
	if r.TTL <= 0 {
		return false
	}
	if r.TS == "" {
		return false
	}
	ts, err := time.Parse(time.RFC3339Nano, r.TS)
	if err != nil {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	return ts.Add(time.Duration(r.TTL) * time.Second).Before(now)
}

// IsExpired is the public visibility predicate for "should this record
// be hidden from the default read view because it has expired in some
// sense" — i.e. retracted OR TTL-past. Callers (thoughts list, recall)
// share this so the two surfaces never diverge again (#151).
//
//   - retracted records are unconditionally hidden in the default view
//   - TTL-expired records are hidden in the default view
//   - both classes are surfaced when includeExpired=true
//
// now is the reference time for the TTL check; zero means time.Now().
func IsExpired(r RecallRecord, now time.Time) bool {
	if r.Retracted {
		return true
	}
	return isTTLExpired(r, now)
}

// scopePass implements the visibility rule: a filter of "agent" means
// "records visible at the agent level" — i.e., the current agent's
// agent-scoped records, plus broader (deployment, fleet) records.
// Records scoped TIGHTER than the filter (e.g., another agent's
// agent-scoped record) are excluded.
func scopePass(r RecallRecord, filterScope, currentAgent string) bool {
	// Records with no scope set (legacy / given) always pass.
	if r.Scope == "" {
		return true
	}
	scopeRank := func(s string) int {
		switch s {
		case "agent":
			return 0
		case "deployment":
			return 1
		case "fleet":
			return 2
		default:
			return -1
		}
	}
	rRank, fRank := scopeRank(r.Scope), scopeRank(filterScope)
	if rRank < 0 || fRank < 0 {
		return false
	}
	if rRank > fRank {
		return true // broader than filter — always visible
	}
	if rRank == fRank {
		// Same scope: only the current agent's own records visible.
		return r.Author == currentAgent
	}
	return false // record scoped tighter than filter
}

// Match applies the query. Empty query → return all. Entity-id form
// (passes thought.ValidateSubject) → exact subject match. Else multi-
// word AND-substring against subject+predicate+object+content
// (case-insensitive).
func Match(records []RecallRecord, query string) []RecallRecord {
	query = strings.TrimSpace(query)
	if query == "" {
		return records
	}

	// Entity-id form: exact subject match.
	if err := thought.ValidateSubject(query); err == nil {
		out := make([]RecallRecord, 0)
		for _, r := range records {
			if r.Subject == query {
				out = append(out, r)
			}
		}
		return out
	}

	// Substring AND across words.
	words := strings.Fields(strings.ToLower(query))
	out := make([]RecallRecord, 0)
	for _, r := range records {
		hay := strings.ToLower(r.Subject + " " + r.Predicate + " " + r.Object + " " + r.Content)
		allFound := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				allFound = false
				break
			}
		}
		if allFound {
			out = append(out, r)
		}
	}
	return out
}

// RenderColumnar prints records as one-per-line TAB-separated columnar
// output (H1c, R25 rewrite). The unified row shape is:
//
//	<reltime>\t<type[:subtype]>\t<author>\t<id>\t<key>\t<scope>[\t<content-snippet>]
//
// where:
//   - <reltime>  — output.RenderRelTime(r.TS, now). Short human form
//     ("12m ago" / "2d ago" / "2026-05-12") replacing the 27-char
//     RFC3339Nano that ate a third of an 80-col terminal on every row.
//   - <type:subtype>:
//   - thought → "thought:<ThoughtType>" (decision/focus/hypothesis/
//     question — R25 reported these collapsed to bare `thought`),
//     falling back to bare `thought` when ThoughtType is empty.
//   - reason  → "reason:<short-decision-id>" when the parent
//     directory is a decision id, else bare `reason` (R25 #134:
//     decision linkage was dropped entirely from the row).
//   - everything else → bare r.Type.
//   - <author>  — r.Author.
//   - <id>      — output.FormatID(idForRow) — short by default, full
//     when RUFIO_FULL_IDS=1. EVERY row gets a non-empty id slot; for
//     `given` records (which have no per-record id) the path basename
//     stands in. R25 #134 surfaced empty id columns on reason rows;
//     this is the symmetric fix.
//   - <key>     — type-specific:
//   - given       → r.Subject (file path)
//   - observation → r.Subject:r.Predicate="r.Object"
//   - thought     → r.Subject
//   - reason      → truncated content
//   - other       → r.Subject
//   - <scope>   — r.Scope (empty for given/reason).
//   - <content-snippet> — appended for thoughts with non-empty content;
//     same content-truncation contract as before.
//
// Sort: ts-DESC across all types (every other read surface is
// ts-DESC; R25 reported recall's per-type/ts-ASC sort as confusing).
// Provenance suffix is preserved (#76) — promoted observations still
// surface origin/confirmed-by/source.
//
// Records flow in input order EXCEPT for the sort; callers that need a
// different order must sort the input themselves.
//
// Wire contract: JSON output is UNCHANGED — humans get the friendly
// form, machines get full RFC3339Nano + full ids. See RenderJSON.
func RenderColumnar(w io.Writer, records []RecallRecord) error {
	// Sort ts-DESC across types. Lexicographic RFC3339Nano IS
	// chronological (writer pins UTC). Rows with unparseable / empty TS
	// drop to the bottom — deterministic via lexicographic fallback.
	sorted := make([]RecallRecord, len(records))
	copy(sorted, records)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].TS > sorted[j].TS })

	now := time.Now()
	for _, r := range sorted {
		reltime := output.RenderRelTime(r.TS, now)
		typeLabel := unifiedTypeLabel(r)
		id := unifiedID(r)
		key := columnarKey(r)
		parts := []string{
			reltime,
			typeLabel,
			r.Author,
			output.FormatID(id),
			key,
			r.Scope,
		}
		// K2 / R28: append the load-bearing free-text payload for thought
		// AND the new cognition-vocabulary kinds (summon/confirm/refute/
		// retract) so cold readers see the searchable payload without
		// having to crack open --json. key/scope already in the initializer.
		switch r.Type {
		case "thought":
			if r.Content != "" {
				parts = append(parts, `"`+truncate(r.Content, 80)+`"`)
			}
		case "summon":
			if r.Intent != "" {
				parts = append(parts, `"`+truncate(r.Intent, 80)+`"`)
			}
		case "confirm":
			if r.Evidence != "" {
				parts = append(parts, `"`+truncate(r.Evidence, 80)+`"`)
			}
		case "refute":
			// reason is load-bearing; evidence is supplementary. Render
			// reason as the primary snippet to mirror lineage's social
			// row format.
			if r.Reason != "" {
				parts = append(parts, `"`+truncate(r.Reason, 80)+`"`)
			} else if r.Evidence != "" {
				parts = append(parts, `"`+truncate(r.Evidence, 80)+`"`)
			}
		case "retract":
			if r.Reason != "" {
				parts = append(parts, `"`+truncate(r.Reason, 80)+`"`)
			}
		}
		// Trim trailing empties for cleaner output.
		for len(parts) > 0 && parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}
		// H2 — state-join badge for thoughts. R24: agents previously ran
		// lineage/confirms/refutes/retracts per id to see "still live".
		// Inlining +N/-M/[RETRACTED]/[PROMOTED] closes that scavenger
		// hunt. Confined to thoughts so observation/reason/given rows
		// stay byte-identical (their counts/promoted state are zero by
		// design — no surface to join).
		if r.Type == "thought" {
			if badge := stateBadge(r); badge != "" {
				parts = append(parts, badge)
			}
		}
		// Provenance suffix (#76) — appended ONLY when the record actually
		// carries provenance (i.e. a crowd-confirmed/promoted observation).
		// Gated on field presence, NOT on type, so a non-promoted
		// observation row stays byte-identical (no suffix when empty).
		if suffix := provenanceSuffix(r); suffix != "" {
			parts = append(parts, suffix)
		}
		if _, err := io.WriteString(w, strings.Join(parts, "\t")+"\n"); err != nil {
			return err
		}
	}
	return nil
}

// unifiedTypeLabel renders the type column with the subtype preserved
// for thoughts (decision/focus/hypothesis/question — R25's "collapsed
// to bare thought" complaint) and decision-linkage for reasons
// (R25 #134). Bare r.Type for the rest.
//
// Falls back to bare `thought` / `reason` when the subtype/linkage is
// absent — never sprouts an empty trailing colon.
func unifiedTypeLabel(r RecallRecord) string {
	switch r.Type {
	case "thought":
		if r.ThoughtType != "" {
			return "thought:" + r.ThoughtType
		}
		return "thought"
	case "reason":
		if did := decisionIDFromPath(r.Path); did != "" {
			return "reason:" + output.FormatID(did)
		}
		return "reason"
	default:
		return r.Type
	}
}

// unifiedID returns the id to render in the unified id column. For
// kinds with a real per-record id, it's r.ID. For `given` records
// (which have no actionable id) we fall back to the basename of the
// path so the column is never empty — H1c mandates every row carries
// an id slot operators can scan and pipe.
func unifiedID(r RecallRecord) string {
	if r.ID != "" {
		return r.ID
	}
	if r.Path != "" {
		return strings.TrimSuffix(filepath.Base(r.Path), filepath.Ext(r.Path))
	}
	return ""
}

// decisionIDFromPath extracts the decision id from a reason file path
// of the form .../live/reasoning/<author>/<decision-id>/<reason-id>.gdl.
// Returns "" for orphan reasons (.../live/reasoning/<author>/<id>.gdl)
// per the reason writer's contract (D7.1: --decision is optional).
//
// We look up two levels: parent dir = decision-id, grandparent = author.
// If the grandparent is `reasoning` (no decision dir layer), this is an
// orphan reason — return "".
func decisionIDFromPath(p string) string {
	if p == "" {
		return ""
	}
	parent := filepath.Base(filepath.Dir(p))
	grand := filepath.Base(filepath.Dir(filepath.Dir(p)))
	if grand == "reasoning" {
		// Orphan layout: .../live/reasoning/<author>/<id>.gdl.
		return ""
	}
	return parent
}

// stateBadge renders the H2 inline state-join token for a thought:
// `+N` (confirm count), `-M` (refute count), `[RETRACTED]` (retract
// marker present), `[PROMOTED]` (auto-promote marker present). Empty
// segments are suppressed so a virgin thought stays unbadged.
//
// Returns "" when there's nothing to say, letting the caller skip
// appending an empty field.
func stateBadge(r RecallRecord) string {
	var segs []string
	if r.ConfirmCount > 0 {
		segs = append(segs, "+"+strconv.Itoa(r.ConfirmCount))
	}
	if r.RefuteCount > 0 {
		segs = append(segs, "-"+strconv.Itoa(r.RefuteCount))
	}
	if r.Retracted {
		segs = append(segs, "[RETRACTED]")
	}
	if r.Promoted {
		segs = append(segs, "[PROMOTED]")
	}
	if len(segs) == 0 {
		return ""
	}
	return strings.Join(segs, " ")
}

func columnarKey(r RecallRecord) string {
	switch r.Type {
	case "given":
		return r.Subject
	case "observation":
		if r.Predicate != "" {
			return r.Subject + ":" + r.Predicate + `="` + r.Object + `"`
		}
		return r.Subject
	case "thought":
		return r.Subject
	case "reason":
		return truncate(r.Content, 80)
	case "summon":
		// Subject (= topic) on summons; intent is rendered separately as
		// the content snippet.
		return r.Subject
	case "confirm", "refute", "retract":
		// These act AGAINST a target thought-id — surface that as the
		// key so a reader sees the linkage at a glance.
		if r.Target != "" {
			return "target=" + r.Target
		}
		return ""
	default:
		return r.Subject
	}
}

// provenanceSuffix renders the #76 provenance trailer for a promoted
// observation: "· origin:<author> · confirmed-by:<id,id,…> · source:<id>".
// Each segment is INDEPENDENTLY emitted only when set, so a partially
// populated record degrades gracefully and a non-promoted record (all
// empty) returns "" — keeping its row byte-identical to pre-#76.
func provenanceSuffix(r RecallRecord) string {
	var segs []string
	if r.Origin != "" {
		segs = append(segs, "origin:"+r.Origin)
	}
	if len(r.ConfirmedBy) > 0 {
		segs = append(segs, "confirmed-by:"+strings.Join(r.ConfirmedBy, ","))
	}
	if r.Source != "" {
		segs = append(segs, "source:"+r.Source)
	}
	if len(segs) == 0 {
		return ""
	}
	return "· " + strings.Join(segs, " · ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// RenderJSON emits JSONL — one JSON object per record, line-delimited.
// Object keys: _type, id, ts, author, subject, predicate, object,
// content, scope, path, retracted, origin, confirmed_by, source. All keys
// always present (consistent shape for consumers); empty optional string
// fields emit as "" and confirmed_by emits as [] (never null) so the #76
// provenance is always range-able. origin/confirmed_by/source are
// populated only for promoted (crowd-confirmed) observations.
//
// One CONDITIONAL key, `type` (#89): the on-disk @thought `type:`
// (decision|hypothesis|observation|…). Emitted with omitempty semantics —
// present ONLY when RecallRecord.ThoughtType is non-empty, so
// non-thoughts (and observation-type thoughts whose type is unset) do not
// sprout a spurious key. Note `type` is distinct from `_type`: `_type` is
// the reserved namespace-kind ("thought"/"observation"/…), `type` is the
// thought's own classification.
//
// `id` is the top-level, machine-clean fix for the dogfood-surfaced gap:
// it is the canonical token confirm/refute/retract/--parent consume
// (previously only embedded inside `path`, forcing agents to string-parse
// the path). Empty for kinds with no verb-consumable id.
//
// Bonus (security audit followup): root is now an explicit parameter
// so the path-relativisation uses filepath.Rel rather than a substring
// search. Callers that don't know the root may pass "" (defensive
// fallback — emits the absolute path as-is, which is the pre-fix
// behaviour for unmatched paths).
func RenderJSON(w io.Writer, root string, records []RecallRecord) error {
	enc := json.NewEncoder(w)
	for _, r := range records {
		// Provenance (#76) — always present (stable consumable shape, same
		// contract as id/scope). confirmed_by is ALWAYS a JSON array (never
		// null): empty [] for non-promoted records so consumers can range
		// it unconditionally. Empty strings for origin/source when absent.
		confirmedBy := r.ConfirmedBy
		if confirmedBy == nil {
			confirmedBy = []string{}
		}
		// Security audit H2: emit root-relative POSIX path instead of
		// the absolute server filesystem path. Pre-fix this leaked the
		// operator's $HOME / substrate-root layout to every
		// authenticated caller.
		obj := map[string]interface{}{
			"_type":        r.Type,
			"id":           r.ID,
			"ts":           r.TS,
			"author":       r.Author,
			"subject":      r.Subject,
			"predicate":    r.Predicate,
			"object":       r.Object,
			"content":      r.Content,
			"scope":        r.Scope,
			"path":         RelativisePath(r.Path, root),
			"retracted":    r.Retracted,
			"origin":       r.Origin,
			"confirmed_by": confirmedBy,
			"source":       r.Source,
		}
		// H2: stable promoted_* keys for thoughts. Present-but-null
		// when absent (matches the #132 contract used by thoughts list
		// and lineage). Non-thoughts also carry the keys for shape
		// stability — they're trivially nil there.
		if r.Promoted {
			obj["promoted_at"] = r.PromotedAt
			obj["promoted_by"] = r.PromotedBy
			obj["promoted_observation"] = r.PromotedObservationID
		} else {
			obj["promoted_at"] = nil
			obj["promoted_by"] = nil
			obj["promoted_observation"] = nil
		}
		// v1.0.4 bug #1 (expanded scope) — multiple on-disk fields were
		// dropped from RenderJSON, breaking the mirror snapshot path
		// (which reconstructs GDL lines from the JSON shape) and the
		// JSONL export round-trip. Each emission below is gated by
		// omitempty so non-carrying record kinds stay byte-identical.
		//
		// topics (#180) — emit when the record carries a non-empty
		// Topics slice on disk. Always rendered as a JSON array of
		// strings (never null) for consumers that range over it.
		if len(r.Topics) > 0 {
			obj["topics"] = r.Topics
		}
		// ttl (D5.1) — ALWAYS present for @thought records (0 is the
		// canonical "never expire" sentinel and BuildThoughtRecord
		// unconditionally writes `ttl:` on disk; the consumer needs
		// to distinguish "0 = never" from "absent = different record
		// kind"). Omitted for non-thought kinds that never carry the
		// field on disk so their JSON shape stays unchanged.
		if r.Type == "thought" {
			obj["ttl"] = r.TTL
		}
		// confidence — emit for @observation records. Range [0, 1] on
		// disk per observation.ParseConfidence. Always present for
		// observations even when 0 (the disk format never elides the
		// field; consumers need to distinguish "0 confidence" from
		// "field absent on non-observation"). Omitted for every other
		// kind.
		if r.Type == "observation" {
			obj["confidence"] = r.Confidence
		}
		// parent — emit when the on-disk `parent:` field is present.
		// Lives on @thought and @reason records (optional on both);
		// elided entirely when empty per the existing on-disk contract,
		// so omitempty here mirrors disk.
		if r.Parent != "" {
			obj["parent"] = r.Parent
		}
		// K2 / R28 — cognition-vocabulary fields. Emitted as omitempty:
		// keys appear ONLY when the record actually carries the value.
		// Keeps existing rows byte-identical for non-summon/confirm/
		// refute/retract types.
		if r.Intent != "" {
			obj["intent"] = r.Intent
		}
		if r.Evidence != "" {
			obj["evidence"] = r.Evidence
		}
		if r.Reason != "" {
			obj["reason"] = r.Reason
		}
		if r.Target != "" {
			obj["target"] = r.Target
		}
		if r.To != "" {
			obj["to"] = r.To
		}
		// #89: conditional `type` key (omitempty semantics) — present
		// only when the on-disk @thought carried a `type:`.
		if r.ThoughtType != "" {
			obj["type"] = r.ThoughtType
		}
		// H1c (R25 #134): surface the decision linkage for reason rows
		// so JSON consumers don't have to string-parse `path`. Only
		// present (omitempty) when the path carries a decision-dir
		// layer — orphan reasons (no --decision) omit the key.
		if r.Type == "reason" {
			// Prefer the explicit on-disk `decision:` field when
			// present (v1.0.4 bug #1 expanded scope — surfaces the
			// canonical value the writer recorded); fall back to the
			// path-derived id for legacy rows that pre-date scanReasoning
			// populating r.Decision.
			if r.Decision != "" {
				obj["decision"] = r.Decision
			} else if did := decisionIDFromPath(r.Path); did != "" {
				obj["decision"] = did
			}
		}
		if err := enc.Encode(obj); err != nil {
			return err
		}
	}
	return nil
}
