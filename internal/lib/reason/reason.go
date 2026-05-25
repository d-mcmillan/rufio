// Package reason holds the helpers for `rufio reason` (write side) and
// the read-side consumer `lineage` (PR #20).
//
// Write path is conditional on --decision:
//   - omitted → live/reasoning/<me>/<id>.gdl
//   - set     → live/reasoning/<me>/<decision-id>/<id>.gdl
//
// No lock domain (D7.4): unique <unix-millis>-<rand6> id per call.
package reason

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

var decisionRegex = regexp.MustCompile(`^[0-9]+-[a-z0-9]{6}$`)

// ValidateDecision returns nil for empty (omitted flag) and
// *InvalidDecisionError when the value fails the thought-id regex.
// Distinct error type from InvalidParentError so user-facing messages
// name the correct flag (D7.3).
func ValidateDecision(d string) error {
	if d == "" {
		return nil
	}
	if !decisionRegex.MatchString(d) {
		return &rufioerr.InvalidDecisionError{ID: d}
	}
	return nil
}

// ValidateDecisionTarget is the existence + type half of --decision
// validation, distinct from ValidateDecision (pure shape). For an empty
// id (omitted flag) it is a no-op — a free reasoning step never resolves
// a decision. For a non-empty id it linear-scans live/outbox/*/<id>.gdl,
// parses the @thought, and enforces the same decision-only contract that
// lineage's read side enforces (lineage.go:112): the thought must EXIST
// and be type:decision. Without this, `reason --decision=<hypothesis-id>`
// silently writes a reason chain that lineage then permanently refuses to
// render (GH #77).
//
// Reuses the resolver shape established by autopromote.findThought
// (filepath.Glob across agent outboxes) and the typed errors lineage
// already returns for the same conditions:
//
//   - *NoSuchThoughtError when no agent's outbox holds the id (exit 1).
//   - *NotADecisionError when the @thought's type is not "decision"
//     (exit 1) — same error type/wording lineage emits, so the two
//     commands' decision-only contract is reported identically.
//
// Callers must run ValidateDecision (shape) first; a malformed id is a
// usage error (exit 2) and is reported by that distinct earlier step.
func ValidateDecisionTarget(root, id string) error {
	if id == "" {
		return nil
	}
	pattern := filepath.Join(root, "live", "outbox", "*", id+".gdl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return &rufioerr.NoSuchThoughtError{ID: id}
	}
	bs, err := os.ReadFile(matches[0])
	if err != nil {
		return err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.Type == "thought" {
			if r.Get("type") != "decision" {
				return &rufioerr.NotADecisionError{ID: id, Type: r.Get("type")}
			}
			return nil
		}
	}
	return &rufioerr.NoSuchThoughtError{ID: id}
}

// Path returns the file path for a reason record. decisionID is optional:
// when empty, the file lives at live/reasoning/<agent>/<id>.gdl (D7.1);
// when set, at live/reasoning/<agent>/<decision-id>/<id>.gdl.
func Path(root, agent, decisionID, id string) string {
	parts := []string{root, "live", "reasoning", agent}
	if decisionID != "" {
		parts = append(parts, decisionID)
	}
	parts = append(parts, id+".gdl")
	return filepath.Join(parts...)
}

// ReasonInput is the value type for BuildRecord.
type ReasonInput struct {
	ID      string
	Author  string
	Content string
	Scope   string // #125: visibility scope (agent|deployment|fleet);
	// empty for legacy records that pre-date the field — privacy filters
	// treat empty as visible (the conservative default).
	// Subject (P2/R31) — canonical "what this reasoning is about"
	// (entity-id form per thought.ValidateSubject, e.g. customer:5821).
	// Mirrors think/observe's --subject. Optional today (empty allowed)
	// so legacy reason rows that pre-date the field still parse cleanly.
	// --topics remains a sibling record-label slot (plural CSV).
	Subject  string
	Topics   []string
	Parent   string
	Decision string
	TS       string
}

// BuildRecord returns the @reason gdl.Record. Field order: id, author,
// content, scope, topics?, parent?, decision?, ts. The scope field was
// added in #125 to mirror the thought record's id/author/.../scope slot
// and give every read surface the same privacy lever; previously @reason
// records carried no scope at all, so `rufio reason --scope=...` was
// rejected with "unknown flag" and an agent who reasoned privately had
// to retract+rewrite to hide it. Empty scope renders as an empty value
// (the CLI defaults to "fleet" upstream so no actual record lands empty
// in v1.1+; legacy records read back with Scope="" stay visible).
func BuildRecord(in ReasonInput) gdl.Record {
	fields := []gdl.RecordField{
		{Key: "id", Value: in.ID},
		{Key: "author", Value: in.Author},
		{Key: "content", Value: in.Content},
		{Key: "scope", Value: in.Scope},
	}
	// P2/R31: subject is OPTIONAL on reason — omitted entirely when
	// empty so legacy reason rows (pre-#125 polish pass) stay
	// byte-identical. Field order mirrors think's @thought layout (id,
	// author, ..., subject, ...).
	if in.Subject != "" {
		fields = append(fields, gdl.RecordField{Key: "subject", Value: in.Subject})
	}
	if len(in.Topics) > 0 {
		fields = append(fields, gdl.RecordField{Key: "topics", Value: strings.Join(in.Topics, ",")})
	}
	if in.Parent != "" {
		fields = append(fields, gdl.RecordField{Key: "parent", Value: in.Parent})
	}
	if in.Decision != "" {
		fields = append(fields, gdl.RecordField{Key: "decision", Value: in.Decision})
	}
	fields = append(fields, gdl.RecordField{Key: "ts", Value: in.TS})
	return gdl.Record{Type: "reason", Fields: fields}
}

// Write atomically writes record to the reason path. Creates parent
// directories as needed. .tmp + os.Rename — no lock (D7.4).
func Write(root, agent, decisionID, id string, record gdl.Record) error {
	target := Path(root, agent, decisionID, id)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp := target + ".tmp"
	// Best-effort cleanup so a failed WriteFile/Rename never strands
	// <id>.gdl.tmp under live/reasoning/ (#141). Success path: Rename
	// already moved tmp, so this Remove is a harmless no-op.
	defer func() { _ = os.Remove(tmp) }()
	contents := gdl.RenderLine(record) + "\n"
	if err := os.WriteFile(tmp, []byte(contents), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
