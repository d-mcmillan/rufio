// Package attention holds the validation + record-build + write helpers
// for `rufio attend` (write side) and `rufio attention` (read side, PR #21).
//
// The write-side contract from design §2.B:
//
//	ResolveIdentity → BuildRecord → AcquireLock → WriteAtomic(.tmp + rename) → EmitConfirmation
//
// Decision D4.9: Greppable key order is agent, intent, entities, topics?, ts.
// The topics key is absent (not empty) when no topics were supplied.
package attention

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/fslock"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

var (
	entityIDRegex = regexp.MustCompile(`^[a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)+$`)
	// topicRegex permits a-z0-9 plus _ . - after the leading alnum.
	// '.' was added 2026-05-23 (#176) so version-string topics like
	// "v1.1" are accepted — they're a universal CLI shape that
	// cold-vet agents reach for on instinct.
	topicRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
)

// ValidateIntent returns *InvalidContentError{Field:"intent"} when the
// trimmed value is empty.
func ValidateIntent(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return &rufioerr.InvalidContentError{Field: "intent"}
	}
	return nil
}

// ValidateEntities returns *InvalidEntitiesError if entities is nil/empty
// or any token fails the entity-id regex. Tokens are checked verbatim —
// callers should pre-trim and split.
func ValidateEntities(entities []string) error {
	if len(entities) == 0 {
		return &rufioerr.InvalidEntitiesError{}
	}
	for _, tok := range entities {
		if !entityIDRegex.MatchString(tok) {
			return &rufioerr.InvalidEntitiesError{Token: tok}
		}
	}
	return nil
}

// ValidateTopics returns nil for nil/empty slices (topics are optional)
// and *InvalidTopicsError for any malformed token.
func ValidateTopics(topics []string) error {
	for _, tok := range topics {
		if !topicRegex.MatchString(tok) {
			return &rufioerr.InvalidTopicsError{Token: tok}
		}
	}
	return nil
}

// BuildRecord returns the @attention gdl.Record for live/attention/<agent>.gdl.
// Field order: agent, intent, scope, entities, topics?, ts. The scope field
// was added in #125 to give the privacy filter (#147) the same lever it has
// on every other write surface — without it, the only way to hide an
// attention from non-self callers was to redact fields, leaving presence +
// intent string visible (R8 vet 2026-05-20 leak). Placement mirrors the
// thought-record order (id, author, type, subject, content, scope, …) so
// every record carrying scope puts it in the same relative slot.
//
// Empty scope renders as an empty value rather than an omitted key —
// callers must default upstream (the CLI defaults to "fleet" so a writer
// who omitted --scope still gets a meaningful on-disk value). The topics
// key is still omitted entirely when empty (D4.9 — unchanged).
func BuildRecord(agent, intent, scope string, entities, topics []string, ts string) gdl.Record {
	fields := []gdl.RecordField{
		{Key: "agent", Value: agent},
		{Key: "intent", Value: intent},
		{Key: "scope", Value: scope},
		{Key: "entities", Value: strings.Join(entities, ",")},
	}
	if len(topics) > 0 {
		fields = append(fields, gdl.RecordField{Key: "topics", Value: strings.Join(topics, ",")})
	}
	fields = append(fields, gdl.RecordField{Key: "ts", Value: ts})
	return gdl.Record{Type: "attention", Fields: fields}
}

const defaultLockTimeout = 5 * time.Second

// Write writes record to live/attention/<agent>.gdl using the canonical
// write-side contract: AcquireLock → WriteTmp → Rename. Wholesale overwrite
// per design lock L2.3 — observers see prior or new, never a partial.
//
// Returns *fslock.LockBusyError (RufioError, exit 1) if the per-agent lock
// cannot be acquired within the default 5s timeout.
func Write(root, agent string, record gdl.Record) error {
	return WriteWithTimeout(root, agent, record, defaultLockTimeout)
}

// Attention is the parsed read-side projection of a single
// live/attention/<agent>.gdl file. Used by `rufio fleet` and `rufio
// attention <agent>` (the inspection commands, PR #20). The write-side
// canonical shape lives in BuildRecord above; routing has its own
// trimmed Attention type tuned for the matcher.
//
// Fields:
//   - Agent:    agent id (matches the file basename minus ".gdl")
//   - Intent:   free-text declaration
//   - Entities: parsed CSV (empty slice when the field is absent — the
//     write path requires at least one entity, but the inspector treats
//     a malformed historical file defensively)
//   - Topics:   parsed CSV (nil when the field is omitted entirely — the
//     write path elides the topics key when none were supplied)
//   - TS:       RFC3339Nano timestamp written by BuildRecord
type Attention struct {
	Agent  string
	Intent string
	Scope  string // #125: on-disk @attention scope field; empty for
	// legacy records that pre-date the field (treat permissively — the
	// fleet privacy filter only hides rows when Scope=="agent").
	Entities []string
	Topics   []string
	TS       string
}

// GetScope satisfies privacy.Record so an Attention can flow through
// privacy.IsVisible alongside thoughts/observations/goals. Returns the
// on-disk `scope:` field; legacy records without the field return ""
// (which IsVisible treats as visible — see privacy.go for the rule).
func (a Attention) GetScope() string { return a.Scope }

// GetAuthor satisfies privacy.Record. An @attention is authored by
// exactly one agent (the file basename + the `agent:` field), so the
// author key for privacy gating IS the agent id.
func (a Attention) GetAuthor() string { return a.Agent }

// LoadOne reads live/attention/<agent>.gdl and parses the first
// @attention record. Used by `rufio attention <agent>`.
//
// Returns *NoAttentionError{Agent} when the file does not exist —
// per D20.2 + D20.5, this is the canonical "this agent has no
// attention record" failure (exit 1, not a generic open-failure).
//
// Other read or parse errors propagate as-is (the file exists but is
// unreadable / malformed → caller's responsibility, exit 1 via the
// fallthrough in HandleError).
func LoadOne(root, agent string) (Attention, error) {
	path := filepath.Join(root, "live", "attention", agent+".gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Attention{}, &rufioerr.NoAttentionError{Agent: agent}
		}
		return Attention{}, err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return Attention{}, err
	}
	for _, r := range records {
		if r.Type != "attention" {
			continue
		}
		return projectAttention(r), nil
	}
	// File present but no @attention record. Treat as "no record".
	return Attention{}, &rufioerr.NoAttentionError{Agent: agent}
}

// ReadAll walks live/attention/*.gdl, parses each, and returns one
// Attention per file. Sorted by agent name ascending (deterministic
// output for `rufio fleet`).
//
// Missing live/attention/ directory → empty slice, nil error. Files
// without an @attention record are silently skipped (matches the
// routing.ReadAttentions behaviour). Read or parse errors propagate.
func ReadAll(root string) ([]Attention, error) {
	dir := filepath.Join(root, "live", "attention")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Attention{}, nil
		}
		return nil, err
	}
	out := make([]Attention, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
			continue
		}
		bs, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		records, err := gdl.ParseDocument(string(bs))
		if err != nil {
			return nil, err
		}
		for _, r := range records {
			if r.Type != "attention" {
				continue
			}
			out = append(out, projectAttention(r))
			break
		}
	}
	sortByAgent(out)
	return out, nil
}

// projectAttention converts a parsed @attention gdl.Record into the
// inspection-facing Attention struct. CSV fields are split on comma
// (empty string → nil slice, matching the encode-side convention in
// BuildRecord that omits the topics key entirely when topics is nil).
func projectAttention(r gdl.Record) Attention {
	a := Attention{
		Agent:  r.Get("agent"),
		Intent: r.Get("intent"),
		Scope:  r.Get("scope"),
		TS:     r.Get("ts"),
	}
	if v := r.Get("entities"); v != "" {
		a.Entities = strings.Split(v, ",")
	}
	if v := r.Get("topics"); v != "" {
		a.Topics = strings.Split(v, ",")
	}
	return a
}

// sortByAgent sorts atts in-place by Agent ascending. Pulled out so
// every caller of ReadAll gets the same deterministic ordering. Uses
// a simple insertion-sort-style swap loop to avoid a sort import; for
// the typical fleet (<100 agents) this is fine.
func sortByAgent(atts []Attention) {
	// Use sort.Slice-equivalent with a local closure; tiny dependency.
	// Inline insertion sort keeps the import surface minimal.
	for i := 1; i < len(atts); i++ {
		for j := i; j > 0 && atts[j-1].Agent > atts[j].Agent; j-- {
			atts[j-1], atts[j] = atts[j], atts[j-1]
		}
	}
}

// WriteWithTimeout is the timeout-parameterised variant of Write. Tests
// use it with a short timeout to assert lock-contention behaviour without
// waiting 5s in CI.
func WriteWithTimeout(root, agent string, record gdl.Record, timeout time.Duration) error {
	lockDir := filepath.Join(root, ".rufio", "locks", "attention-"+agent+".lock")
	dir := filepath.Join(root, "live", "attention")
	target := filepath.Join(dir, agent+".gdl")
	_, err := fslock.WithLock(lockDir, timeout, func() (struct{}, error) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return struct{}{}, err
		}
		tmp := target + ".tmp"
		// Best-effort cleanup so a failed WriteFile/Rename (or any early
		// return between) never strands <name>.tmp under the substrate
		// tree (#141). On the success path Rename has already removed
		// tmp, so this Remove is a harmless no-op (ErrNotExist ignored).
		defer func() { _ = os.Remove(tmp) }()
		contents := gdl.RenderLine(record) + "\n"
		if err := os.WriteFile(tmp, []byte(contents), 0o644); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, os.Rename(tmp, target)
	})
	return err
}
