// Package observation holds the validation, record-build, path-mapping,
// and write helpers for `rufio observe` (write side) and the read-side
// consumers `recall` (PR #9) and friends.
//
// Write-side contract from design §2.B:
//
//	ResolveIdentity → BuildRecord → WriteAtomic(.tmp + rename) → EmitConfirmation
//
// No lock domain: each observation has a unique <unix-millis>-<rand6>
// filename (D6.5). Subject path mapping (D6.4) replaces `:` with `/` so
// each entity gets its own directory.
package observation

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

var predicateRegex = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// ValidatePredicate returns *InvalidPredicateError on empty or malformed
// input.
func ValidatePredicate(p string) error {
	if p == "" {
		return &rufioerr.InvalidPredicateError{}
	}
	if !predicateRegex.MatchString(p) {
		return &rufioerr.InvalidPredicateError{Predicate: p}
	}
	return nil
}

// ValidateObject returns *InvalidContentError{Field:"object"} when the
// trimmed value is empty.
func ValidateObject(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return &rufioerr.InvalidContentError{Field: "object"}
	}
	return nil
}

// ParseConfidence converts the --confidence flag value to a float in
// [0,1]. Empty input returns (1.0, nil) per D6.1.
func ParseConfidence(raw string) (float64, error) {
	if raw == "" {
		return 1.0, nil
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, &rufioerr.InvalidConfidenceError{Raw: raw}
	}
	if n < 0 || n > 1 {
		return 0, &rufioerr.InvalidConfidenceError{Raw: raw}
	}
	return n, nil
}

// SubjectPath maps a subject (e.g., "customer:5821") to the file path
// where its observation record lives:
//
//	root/learned/<segment1>/<segment2>/.../<id>.gdlm
//
// Per D6.4 the colon-separated segments of the subject become directory
// levels; the id (typically <unix-millis>-<rand6>) becomes the filename
// with .gdlm extension. Callers are expected to have validated subject
// via thought.ValidateSubject first.
func SubjectPath(root, subject, id string) string {
	segments := strings.Split(subject, ":")
	parts := append([]string{root, "learned"}, segments...)
	parts = append(parts, id+".gdlm")
	return filepath.Join(parts...)
}

// ObservationInput is the value type that BuildObservationRecord projects
// to a gdl.Record. All fields are caller-supplied; validation happens
// upstream.
type ObservationInput struct {
	ID         string
	Author     string
	Subject    string
	Predicate  string
	Object     string
	Scope      string
	Topics     []string // optional; omitted when nil/empty
	Confidence float64  // always rendered (default 1.0 per D6.1)
	TS         string

	// Provenance (#76). OPTIONAL — set ONLY by the auto-promote engine
	// when it durably persists a crowd-confirmed observation. Every field
	// is omit-empty: a normal hand-authored `rufio observe` supplies none
	// of them, so its rendered record is byte-identical to pre-#76. They
	// are appended AFTER ts so the locked id..ts field order (design §2.B
	// line 128) is preserved for all existing records.
	//
	//   - Origin      = the originating thought's author (who first
	//                    asserted the hypothesis). author stays
	//                    "auto-promote" (D13.7): the daemon IS the writer
	//                    of the crowd-confirmed fact; origin is ADDITIVE.
	//   - ConfirmedBy = the distinct confirmer ids. Pass the value from
	//                    confirm.Tally.Confirms, which confirm.ReadAll
	//                    has ALREADY deduped + sorted — do NOT re-sort.
	//                    Serialized comma-joined, same as Topics.
	//   - Source      = the source thought-id (the confirm/refute target).
	Origin      string
	ConfirmedBy []string
	Source      string
}

// BuildObservationRecord returns the @observation gdl.Record. Field order
// is locked at id, author, subject, predicate, object, scope, topics?,
// confidence, ts (per design §2.B line 128).
//
// Inputs assumed pre-validated. Confidence is rendered via strconv.FormatFloat
// with -1 precision (shortest unique representation): 1.0→"1", 0.5→"0.5".
func BuildObservationRecord(in ObservationInput) gdl.Record {
	fields := []gdl.RecordField{
		{Key: "id", Value: in.ID},
		{Key: "author", Value: in.Author},
		{Key: "subject", Value: in.Subject},
		{Key: "predicate", Value: in.Predicate},
		{Key: "object", Value: in.Object},
		{Key: "scope", Value: in.Scope},
	}
	if len(in.Topics) > 0 {
		fields = append(fields, gdl.RecordField{Key: "topics", Value: strings.Join(in.Topics, ",")})
	}
	fields = append(fields, gdl.RecordField{Key: "confidence", Value: strconv.FormatFloat(in.Confidence, 'f', -1, 64)})
	fields = append(fields, gdl.RecordField{Key: "ts", Value: in.TS})
	// Provenance (#76) — appended AFTER ts so the locked id..ts order is
	// untouched for every existing record. Each is INDEPENDENTLY
	// omit-empty: a non-promoted observe sets none → the record is
	// byte-identical to pre-#76 (no empty origin:/confirmed-by:/source:).
	if in.Origin != "" {
		fields = append(fields, gdl.RecordField{Key: "origin", Value: in.Origin})
	}
	if len(in.ConfirmedBy) > 0 {
		fields = append(fields, gdl.RecordField{Key: "confirmed-by", Value: strings.Join(in.ConfirmedBy, ",")})
	}
	if in.Source != "" {
		fields = append(fields, gdl.RecordField{Key: "source", Value: in.Source})
	}
	return gdl.Record{Type: "observation", Fields: fields}
}

// Write atomically writes record to learned/<subject-path>/<id>.gdlm via
// a single .tmp + os.Rename. No lock (D6.5) — unique <id> per call.
//
// Creates all parent directories under learned/ as needed.
func Write(root, subject, id string, record gdl.Record) error {
	target := SubjectPath(root, subject, id)
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := target + ".tmp"
	// Best-effort cleanup so a failed WriteFile/Rename never strands
	// <id>.gdlm.tmp under learned/ (#141). Success path: Rename already
	// moved tmp, so this Remove is a harmless no-op.
	defer func() { _ = os.Remove(tmp) }()
	contents := gdl.RenderLine(record) + "\n"
	if err := os.WriteFile(tmp, []byte(contents), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
