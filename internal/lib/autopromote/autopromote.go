// Package autopromote is the daemon engine for confirm-based promotion of
// hypothesis-thoughts to durable observations.
//
// When confirms accumulate on a thought-id past the threshold (≥3 distinct
// `by:` authors AND confidence ≥ 0.85), the engine writes an @observation
// to learned/<subject-path>/<new-id>.gdlm and an @auto-promote audit marker
// to live/promoted/<thought-id>.gdl. If the thought has already been
// retracted (live/retracted/<id>.gdl exists), the engine writes a
// @promote-skipped marker instead. Promotion is one-way and idempotent:
// a present live/promoted/<id>.gdl short-circuits future evaluations.
//
// This is the decision engine only — fsnotify dispatch lives in dev.go
// (PR #13 Task 7). Per design §2.I engine #2 + design line 218.
package autopromote

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/observation"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// AutoPromoteEventVersion is the LOCKED schema version of the
// @auto-promote audit record / stream event payload. Future
// enrichments MUST bump this value rather than rename/repurpose
// existing fields — consumers (TUI, listen, third-party watchers)
// gate on the version to know which fields to read. Per the v1.0.3
// plan's non-negotiable: schema is LOCKED at first ship.
const AutoPromoteEventVersion = 1

// Decision is the engine's evaluation outcome for a single thought-id.
type Decision int

const (
	// DecisionNoop means the thought is below threshold OR already-promoted.
	DecisionNoop Decision = iota
	// DecisionPromote means the thought has crossed the threshold and the
	// engine should write an @observation + @auto-promote marker.
	DecisionPromote
	// DecisionSkipRetracted means the thought has a live/retracted/<id>.gdl
	// marker and the engine should write a @promote-skipped audit instead.
	DecisionSkipRetracted
)

// Thresholds (D13.6). Hardcoded in v1; config-driven in v1.1 per
// WK4-FOLLOWUP (rufio.gdl @autopromote-config record).
const (
	// MinDistinctConfirmers is the minimum number of unique `by:` authors
	// required before a thought is eligible for promotion.
	MinDistinctConfirmers = 3
	// MinConfidence is the minimum value of confirms / (confirms + refutes)
	// required for promotion. Strictly ≥, matching design line 178.
	MinConfidence = 0.85
)

// promoteAuthor is the literal author string written into @auto-promote /
// @promote-skipped / @observation records the daemon emits. Per D13.7 the
// daemon itself is the author of crowd-confirmed observations.
const promoteAuthor = "auto-promote"

// Evaluate reads confirms + retracted state + already-promoted state for
// the given thought-id and returns the decision plus the current Tally.
//
// Order of checks (matches D13.10 / D13.9):
//  1. already-promoted (live/promoted/<id>.gdl exists) → DecisionNoop
//  2. retracted (live/retracted/<id>.gdl exists) → DecisionSkipRetracted
//  3. below threshold → DecisionNoop
//  4. else → DecisionPromote
//
// Evaluate does NOT verify the @thought exists in live/outbox/; that lookup
// happens in ExecutePromote, which needs the record itself. A missing
// thought with a present confirms file is a real edge case (concurrent
// retract-then-cleanup), and the safest behaviour is to let the engine
// observe whatever state is on disk and decide.
func Evaluate(root, targetID string) (Decision, confirm.Tally, error) {
	tally, err := confirm.ReadAll(root, targetID)
	if err != nil {
		return DecisionNoop, confirm.Tally{}, err
	}

	// 1. Idempotency guard — promotion (or skip) is one-way per D13.10.
	if alreadyPromoted(root, targetID) {
		return DecisionNoop, tally, nil
	}

	// 2. Retraction guard — D13.9.
	if isRetracted(root, targetID) {
		return DecisionSkipRetracted, tally, nil
	}

	// 3. Threshold check — D13.6.
	if len(tally.Confirms) < MinDistinctConfirmers {
		return DecisionNoop, tally, nil
	}
	if tally.Confidence() < MinConfidence {
		return DecisionNoop, tally, nil
	}

	return DecisionPromote, tally, nil
}

// ExecutePromote loads the @thought record from live/outbox/*/<id>.gdl,
// builds an @observation, writes it to learned/<subject-path>/<new-id>.gdlm
// (via observation.Write), and writes an @auto-promote audit record to
// live/promoted/<id>.gdl. Idempotent: re-running on an already-promoted
// thought is a no-op (the live/promoted/ marker existence check guards it).
//
// Observation field mapping (D13.7):
//
//	id        = thought.GenerateID()  (new id for the observation)
//	author    = "auto-promote"
//	subject   = thought.subject       (carried forward)
//	predicate = "asserted"            (literal — "crowd-confirmed")
//	object    = thought.content
//	scope     = thought.scope
//	topics    = thought.topics
//	confidence = Tally.Confidence()   (computed from current confirms/refutes)
//	ts        = versioning.NowISO()
func ExecutePromote(root, targetID string) error {
	// Idempotency: if a promote/skip marker already exists, do nothing.
	if alreadyPromoted(root, targetID) {
		return nil
	}

	thoughtRec, err := findThought(root, targetID)
	if err != nil {
		return err
	}

	tally, err := confirm.ReadAll(root, targetID)
	if err != nil {
		return err
	}

	obsID, err := thought.GenerateID()
	if err != nil {
		return err
	}

	subject := thoughtRec.Get("subject")
	in := observation.ObservationInput{
		ID:         obsID,
		Author:     promoteAuthor,
		Subject:    subject,
		Predicate:  "asserted",
		Object:     thoughtRec.Get("content"),
		Scope:      thoughtRec.Get("scope"),
		Topics:     splitTopics(thoughtRec.Get("topics")),
		Confidence: tally.Confidence(),
		TS:         versioning.NowISO(),
		// Provenance (#76): carry quorum's auditable corroboration INTO
		// the durable learned/ record so `recall` can surface WHO
		// originated the thought and WHO corroborated it. author stays
		// promoteAuthor (D13.7 — the daemon is the writer of the
		// crowd-confirmed fact); provenance is ADDITIVE.
		//
		//   Origin      = originating thought author (thoughtRec author)
		//   ConfirmedBy = tally.Confirms — confirm.ReadAll has ALREADY
		//                 deduped + sorted these; reuse verbatim, do NOT
		//                 reinvent the dedupe/sort.
		//   Source      = the source thought-id (= targetID, the exact
		//                 token confirm/refute consume).
		Origin:      thoughtRec.Get("author"),
		ConfirmedBy: tally.Confirms,
		Source:      targetID,
	}
	obsRec := observation.BuildObservationRecord(in)
	if err := observation.Write(root, subject, obsID, obsRec); err != nil {
		return err
	}

	// v1.0.3: emit the enriched @auto-promote audit record. Carries the
	// full quorum dynamics (confirmers, counts, confidence, scope) into
	// the on-disk record so the listen stream surfaces them without a
	// separate read. Origin (= original thought author) is required so
	// privacy.IsVisible can correctly gate scope=agent visibility on the
	// emitted stream event (the `by:auto-promote` literal is the
	// daemon's identity per D13.7 — not the human-author identity the
	// privacy floor needs).
	auditRec := buildAutoPromoteEnriched(autoPromoteInputs{
		ThoughtID:     targetID,
		ObservationID: obsID,
		Origin:        thoughtRec.Get("author"),
		Subject:       subject,
		Scope:         thoughtRec.Get("scope"),
		Confirmers:    tally.Confirms,
		ConfirmCount:  len(tally.Confirms),
		RefuteCount:   len(tally.Refutes),
		Confidence:    tally.Confidence(),
		TS:            versioning.NowISO(),
	})
	return writePromotedMarker(root, targetID, auditRec)
}

// ExecuteSkip writes an @promote-skipped audit record to
// live/promoted/<targetID>.gdl with the given reason (e.g., "retracted").
// Idempotent: returns nil silently if the file already exists (D13.10 —
// the skip outcome IS the final promotion outcome for that thought).
//
// Record shape: @promote-skipped|target:<id>|reason:<reason>|by:auto-promote|ts:<iso>
func ExecuteSkip(root, targetID, reason string) error {
	if alreadyPromoted(root, targetID) {
		return nil
	}
	rec := buildPromoteSkipped(targetID, reason, versioning.NowISO())
	return writePromotedMarker(root, targetID, rec)
}

// Handle is the top-level engine entry called per fsnotify event on
// live/confirms/<id>.gdl. Evaluates + executes idempotently. This is what
// dev.go's dispatch table will call (Task 7).
//
// NoSuchThoughtError is propagated to the caller; the daemon's dispatch
// layer will log + skip rather than crash the loop.
func Handle(root, targetID string) error {
	decision, _, err := Evaluate(root, targetID)
	if err != nil {
		return err
	}
	switch decision {
	case DecisionPromote:
		return ExecutePromote(root, targetID)
	case DecisionSkipRetracted:
		return ExecuteSkip(root, targetID, "retracted")
	case DecisionNoop:
		return nil
	default:
		return nil
	}
}

// --- helpers (unexported) ----------------------------------------------------

// alreadyPromoted reports whether live/promoted/<id>.gdl already exists.
// This is the one-way guard per D13.10: a present marker (auto-promote OR
// promote-skipped) is the final state.
func alreadyPromoted(root, targetID string) bool {
	path := filepath.Join(root, "live", "promoted", targetID+".gdl")
	_, err := os.Stat(path)
	return err == nil
}

// isRetracted reports whether live/retracted/<id>.gdl exists.
func isRetracted(root, targetID string) bool {
	path := filepath.Join(root, "live", "retracted", targetID+".gdl")
	_, err := os.Stat(path)
	return err == nil
}

// findThought scans live/outbox/*/<targetID>.gdl, reads the first matching
// file, parses it, and returns the @thought record. Returns
// *NoSuchThoughtError if no agent's outbox contains the file (mirroring
// retract.Lookup) or a parse error if the file is malformed.
func findThought(root, targetID string) (gdl.Record, error) {
	pattern := filepath.Join(root, "live", "outbox", "*", targetID+".gdl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return gdl.Record{}, err
	}
	if len(matches) == 0 {
		return gdl.Record{}, &rufioerr.NoSuchThoughtError{ID: targetID}
	}
	bs, err := os.ReadFile(matches[0])
	if err != nil {
		return gdl.Record{}, err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return gdl.Record{}, err
	}
	for _, r := range records {
		if r.Type == "thought" {
			return r, nil
		}
	}
	return gdl.Record{}, &rufioerr.NoSuchThoughtError{ID: targetID}
}

// splitTopics converts the comma-joined topics value back to a slice.
// Returns nil when raw is empty so BuildObservationRecord omits the field.
func splitTopics(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

// autoPromoteInputs collects the fields buildAutoPromoteEnriched
// renders into the v1.0.3 @auto-promote audit record. Tuple-struct
// (no methods) so caller-site argument order can't drift silently.
type autoPromoteInputs struct {
	ThoughtID     string
	ObservationID string
	Origin        string   // original thought author — drives privacy.IsVisible on the stream
	Subject       string   // carried from the thought; lets watchers route by subject
	Scope         string   // carried from the thought; gates scope=agent visibility on listen
	Confirmers    []string // sorted, deduped (confirm.Tally already guarantees both)
	ConfirmCount  int
	RefuteCount   int
	Confidence    float64
	TS            string
}

// buildAutoPromoteEnriched returns the v1.0.3 @auto-promote audit
// record. Schema LOCKED at version 1 — future enrichments bump
// AutoPromoteEventVersion and add fields, never rename existing ones.
//
//	@auto-promote|version:1|thought:<id>|observation:<obs-id>
//	             |origin:<original-author>|subject:<ns:local>|scope:<scope>
//	             |confirmers:<csv>|confirm-count:<n>|refute-count:<n>
//	             |confidence:<float>|by:auto-promote|ts:<iso>
//
// Field order: legacy fields (thought, observation) first so a v1.0.2
// consumer doing positional parses still sees what it expects; new
// fields trail. version lives near the head so the wire is self-
// describing.
//
// `by:auto-promote` is the daemon's identity (D13.7 — the daemon
// authors crowd-confirmed observations). `origin:<author>` is the
// human-author identity the privacy floor needs to gate scope=agent
// visibility correctly when the record surfaces as a stream event.
func buildAutoPromoteEnriched(in autoPromoteInputs) gdl.Record {
	fields := []gdl.RecordField{
		{Key: "version", Value: strconv.Itoa(AutoPromoteEventVersion)},
		{Key: "thought", Value: in.ThoughtID},
		{Key: "observation", Value: in.ObservationID},
		{Key: "origin", Value: in.Origin},
	}
	if in.Subject != "" {
		fields = append(fields, gdl.RecordField{Key: "subject", Value: in.Subject})
	}
	if in.Scope != "" {
		fields = append(fields, gdl.RecordField{Key: "scope", Value: in.Scope})
	}
	fields = append(fields,
		gdl.RecordField{Key: "confirmers", Value: strings.Join(in.Confirmers, ",")},
		gdl.RecordField{Key: "confirm-count", Value: strconv.Itoa(in.ConfirmCount)},
		gdl.RecordField{Key: "refute-count", Value: strconv.Itoa(in.RefuteCount)},
		gdl.RecordField{Key: "confidence", Value: strconv.FormatFloat(in.Confidence, 'g', -1, 64)},
		gdl.RecordField{Key: "by", Value: promoteAuthor},
		gdl.RecordField{Key: "ts", Value: in.TS},
	)
	return gdl.Record{Type: "auto-promote", Fields: fields}
}

// buildPromoteSkipped returns the @promote-skipped audit record (D13.9).
//
//	@promote-skipped|target:<id>|reason:<reason>|by:auto-promote|ts:<iso>
func buildPromoteSkipped(targetID, reason, ts string) gdl.Record {
	return gdl.Record{Type: "promote-skipped", Fields: []gdl.RecordField{
		{Key: "target", Value: targetID},
		{Key: "reason", Value: reason},
		{Key: "by", Value: promoteAuthor},
		{Key: "ts", Value: ts},
	}}
}

// writePromotedMarker atomically writes record to live/promoted/<id>.gdl
// via .tmp + os.Rename. One record per file (D13.8) — never appends.
func writePromotedMarker(root, targetID string, record gdl.Record) error {
	dir := filepath.Join(root, "live", "promoted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, targetID+".gdl")
	tmp := target + ".tmp"
	// Best-effort cleanup so a failed WriteFile/Rename never strands
	// <id>.gdl.tmp under live/promoted/ (#141). Success path: Rename
	// already moved tmp, so this Remove is a harmless no-op.
	defer func() { _ = os.Remove(tmp) }()
	contents := gdl.RenderLine(record) + "\n"
	if err := os.WriteFile(tmp, []byte(contents), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
