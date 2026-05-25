// Package goal implements the write + state-machine helpers for
// rufio goal, rufio goals list, rufio goal complete, rufio goal abandon.
//
// A goal is a one-author-owned coordination primitive. State transitions
// are one-way per D17.13: active → {completed | abandoned}. The on-disk
// file at live/goals/<state>/<goal-id>.gdl contains the original @goal
// record always; completed/abandoned files additionally carry the
// @goal-complete | @goal-abandon audit record appended on the move.
//
// Lock domains (D17.7): state transitions go through
// .rufio/locks/goal-<id>.lock so concurrent complete/abandon are mutually
// exclusive even across processes. The atomic create uses link(2)+unlink(2)
// (matching summon.MoveToAccepted from PR #15) so the destination is
// guaranteed to be created via a real-syscall existence check — Stat+Rename
// would silently overwrite on Linux when a racing writer wins.
package goal

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/fslock"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
)

// shortIDRegex matches a bare 6-char [a-z0-9] suffix — the form
// `goals list` renders in text mode (output.FormatID → output.ShortID).
// R29a accepts the suffix on the write side (goal complete / abandon)
// so the IDs the agent SEES are the IDs they can PASTE.
var shortIDRegex = regexp.MustCompile(`^[a-z0-9]{6}$`)

// State is the on-disk state directory name for a goal.
type State string

const (
	StateActive    State = "active"
	StateCompleted State = "completed"
	StateAbandoned State = "abandoned"
)

// allStates is the directory-traversal order used by LoadAnyState and
// ReadAll, and also the sort precedence used by ReadAll (D17.11:
// active < completed < abandoned).
var allStates = []State{StateActive, StateCompleted, StateAbandoned}

// Goal is the parsed @goal record + audit-derived state.
type Goal struct {
	ID        string
	Author    string
	Statement string
	By        string // free-text in v1 (D17.5); may be empty
	Parent    string // optional; may be empty
	Scope     string
	TS        string
	State     State
	// Populated only when State != Active:
	CompletedBy string
	CompletedAt string
	Outcome     string
	AbandonedBy string
	AbandonedAt string
	Reason      string
}

// GetScope satisfies privacy.Record. Returns the goal's declared scope
// (agent|deployment|fleet); empty for legacy records that pre-date the
// scope field.
func (g Goal) GetScope() string { return g.Scope }

// GetAuthor satisfies privacy.Record. Returns the agent who wrote the
// @goal record (the `author:` field on disk).
func (g Goal) GetAuthor() string { return g.Author }

// ValidateStatement returns *InvalidStatementError when TrimSpace(s) is
// empty. Per D17.3.
func ValidateStatement(s string) error {
	if strings.TrimSpace(s) == "" {
		return &rufioerr.InvalidStatementError{}
	}
	return nil
}

// GenerateID returns a new goal-id of the form <unix-millis>-<rand6>.
// Mirrors thought.GenerateID + summon.GenerateID verbatim — same format
// AND alphabet so generic id-shaped parsers handle goals uniformly.
func GenerateID() (string, error) {
	return generateIDFromSource(
		func() int64 { return time.Now().UnixMilli() },
		rand.Reader,
	)
}

// generateIDFromSource is the testable variant. Production callers use
// GenerateID. Mirrored verbatim from summon.generateIDFromSource so the
// id alphabets stay in lock-step.
func generateIDFromSource(now func() int64, src io.Reader) (string, error) {
	buf := make([]byte, 6)
	n, err := io.ReadFull(src, buf)
	if err != nil || n != 6 {
		return "", fmt.Errorf("goal: rand source read %d/6 bytes: %w", n, err)
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 6)
	for i, b := range buf {
		out[i] = alphabet[int(b)%36]
	}
	return fmt.Sprintf("%d-%s", now(), out), nil
}

// BuildGoalRecord returns the @goal gdl.Record. Field order locked at
// id, author, statement, by?, parent?, scope, ts (D17.2). `by` and
// `parent` are omitted entirely when empty (sibling pattern — matches
// thought.BuildThoughtRecord's handling of optional parent).
func BuildGoalRecord(id, author, statement, by, parent, scope, ts string) gdl.Record {
	fields := []gdl.RecordField{
		{Key: "id", Value: id},
		{Key: "author", Value: author},
		{Key: "statement", Value: statement},
	}
	if by != "" {
		fields = append(fields, gdl.RecordField{Key: "by", Value: by})
	}
	if parent != "" {
		fields = append(fields, gdl.RecordField{Key: "parent", Value: parent})
	}
	fields = append(fields,
		gdl.RecordField{Key: "scope", Value: scope},
		gdl.RecordField{Key: "ts", Value: ts},
	)
	return gdl.Record{Type: "goal", Fields: fields}
}

// BuildCompleteRecord returns the @goal-complete gdl.Record per D17.9.
// Field order locked at id, by, outcome, ts.
func BuildCompleteRecord(goalID, by, outcome, ts string) gdl.Record {
	return gdl.Record{Type: "goal-complete", Fields: []gdl.RecordField{
		{Key: "id", Value: goalID},
		{Key: "by", Value: by},
		{Key: "outcome", Value: outcome},
		{Key: "ts", Value: ts},
	}}
}

// BuildAbandonRecord returns the @goal-abandon gdl.Record per D17.10.
// Field order locked at id, by, reason, ts.
func BuildAbandonRecord(goalID, by, reason, ts string) gdl.Record {
	return gdl.Record{Type: "goal-abandon", Fields: []gdl.RecordField{
		{Key: "id", Value: goalID},
		{Key: "by", Value: by},
		{Key: "reason", Value: reason},
		{Key: "ts", Value: ts},
	}}
}

// WriteActive atomically writes a single-record file to
// live/goals/active/<goal-id>.gdl via .tmp + os.Rename. Creates parent
// dir. No lock (D17.6) — fresh goal-id per call.
func WriteActive(root, id string, record gdl.Record) error {
	dir := filepath.Join(root, "live", "goals", string(StateActive))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, id+".gdl")
	tmp := target + ".tmp"
	// Best-effort cleanup so a failed WriteFile/Rename never strands
	// <id>.gdl.tmp under live/goals/active/ (#141). Success path:
	// Rename already moved tmp, so this Remove is a harmless no-op.
	defer func() { _ = os.Remove(tmp) }()
	contents := gdl.RenderLine(record) + "\n"
	if err := os.WriteFile(tmp, []byte(contents), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// LoadAnyState scans live/goals/{active,completed,abandoned}/<id>.gdl,
// returns the loaded Goal with State derived from the source directory.
// Returns *NoSuchGoalError on miss.
//
// The first @goal record in the file populates the struct; @goal-complete
// and @goal-abandon records (if present) populate the audit-derived
// fields so callers can render the full life-cycle in `goals list`.
//
// R29a: a 6-char [a-z0-9]{6} value is interpreted as a suffix match
// across all three state directories. Multiple matches surface
// *AmbiguousIDError listing canonical ids — complete/abandon can't pick
// a random goal. Full canonical ids take the legacy fast path.
//
// FIREHOSE form — privacy floor (#147) NOT applied; suffix candidates
// across all authors are considered. Read-side callers (TUI, MCP
// goals_list overlay) that already render every visible goal continue
// to use this. CLI write verbs (goal complete / abandon / --parent)
// MUST use LoadAnyStateAs with the resolved agent so the suffix lookup
// can't leak existence of other-author scope:agent records.
func LoadAnyState(root, goalID string) (Goal, error) {
	return LoadAnyStateAs(root, goalID, "")
}

// LoadAnyStateAs is the agent-aware variant. currentAgent="" preserves
// the firehose behaviour (admin/test paths); a non-empty value gates
// the suffix-resolution candidate set with the same privacy.IsVisible
// predicate the listing surfaces use, so non-author scope:agent records
// can't be probed via suffix collisions (R30 / #147).
//
// The post-resolve LOAD itself is NOT gated — the author-only authz
// check downstream (GoalAuthError) covers the act-on-it surface. Only
// the *enumeration* of candidates during disambiguation is filtered.
func LoadAnyStateAs(root, goalID, currentAgent string) (Goal, error) {
	if shortIDRegex.MatchString(goalID) {
		canonical, err := resolveSuffix(root, goalID, currentAgent)
		if err != nil {
			return Goal{}, err
		}
		goalID = canonical
	}
	for _, state := range allStates {
		path := filepath.Join(root, "live", "goals", string(state), goalID+".gdl")
		bs, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return Goal{}, err
		}
		records, err := gdl.ParseDocument(string(bs))
		if err != nil {
			return Goal{}, fmt.Errorf("goal: parse %s: %w", path, err)
		}
		var goalRec *gdl.Record
		for i, r := range records {
			if r.Type == "goal" {
				goalRec = &records[i]
				break
			}
		}
		if goalRec == nil {
			// File exists but no @goal record — treat as missing.
			return Goal{}, &rufioerr.NoSuchGoalError{ID: goalID}
		}
		g := goalFromRecord(*goalRec, state)
		// Audit overlay — second/third record carries the transition data.
		for _, r := range records {
			switch r.Type {
			case "goal-complete":
				g.CompletedBy = r.Get("by")
				g.CompletedAt = r.Get("ts")
				g.Outcome = r.Get("outcome")
			case "goal-abandon":
				g.AbandonedBy = r.Get("by")
				g.AbandonedAt = r.Get("ts")
				g.Reason = r.Get("reason")
			}
		}
		return g, nil
	}
	return Goal{}, &rufioerr.NoSuchGoalError{ID: goalID}
}

// ResolveSuffixAs is the public wrapper around resolveSuffix used by
// the CLI --parent flag handler. It exists ONLY because goal --parent
// has to resolve a suffix BEFORE thought.ValidateParent runs (which
// rejects non-canonical shapes with exit code 2) and BEFORE the parent
// is loaded for the cross-author warning. All other consumers should
// keep using LoadAnyState / LoadAnyStateAs which fold suffix resolution
// into the load itself. See runGoalWrite for the call site.
func ResolveSuffixAs(root, suffix, currentAgent string) (string, error) {
	return resolveSuffix(root, suffix, currentAgent)
}

// resolveSuffix maps a 6-char suffix to the canonical goal-id. Walks
// each state dir for *-<suffix>.gdl filenames. Ambiguity surfaces as
// *AmbiguousIDError; zero matches as *NoSuchGoalError.
//
// currentAgent gates the candidate set via the privacy floor (#147):
// when non-empty, other-author scope:agent records are dropped BEFORE
// the uniqueness check, so a non-author can't probe existence by
// hitting an ambiguous-vs-clean-resolve boundary. Empty preserves
// firehose semantics for admin/test paths — matches retract.Resolve.
//
// Each candidate is parsed once to recover author + scope + statement,
// so the ambiguous-disambiguation render carries real disambiguation
// context (not just bare canonical ids).
func resolveSuffix(root, suffix, currentAgent string) (string, error) {
	type cand struct {
		ID        string
		Author    string
		Statement string
		Scope     string
	}
	var cands []cand
	seen := make(map[string]bool)
	for _, state := range allStates {
		pattern := filepath.Join(root, "live", "goals", string(state), "*-"+suffix+".gdl")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return "", err
		}
		for _, p := range matches {
			id := strings.TrimSuffix(filepath.Base(p), ".gdl")
			if seen[id] {
				continue
			}
			seen[id] = true
			c := cand{ID: id}
			// Best-effort parse — failures keep the candidate (filename
			// alone proves existence) with empty author/scope. Empty
			// scope means privacy filter treats it as visible (only
			// scope:agent gates non-authors).
			if bs, err := os.ReadFile(p); err == nil {
				if records, err := gdl.ParseDocument(string(bs)); err == nil {
					for _, r := range records {
						if r.Type == "goal" {
							c.Author = r.Get("author")
							c.Statement = r.Get("statement")
							c.Scope = r.Get("scope")
							break
						}
					}
				}
			}
			cands = append(cands, c)
		}
	}
	// Privacy floor: drop other-author scope:agent BEFORE the uniqueness
	// check (currentAgent="" is firehose — admin/test path).
	if currentAgent != "" {
		filtered := cands[:0]
		for _, c := range cands {
			rec := privacyShim{author: c.Author, scope: c.Scope}
			if !privacy.IsVisible(rec, currentAgent) {
				continue
			}
			filtered = append(filtered, c)
		}
		cands = filtered
	}
	switch len(cands) {
	case 0:
		return "", &rufioerr.NoSuchGoalError{ID: suffix}
	case 1:
		return cands[0].ID, nil
	default:
		sort.Slice(cands, func(i, j int) bool { return cands[i].ID < cands[j].ID })
		rows := make([]rufioerr.AmbiguousCandidate, 0, len(cands))
		for _, c := range cands {
			rows = append(rows, rufioerr.AmbiguousCandidate{
				ID:      c.ID,
				Author:  c.Author,
				Type:    "goal",
				Subject: c.Statement,
			})
		}
		return "", &rufioerr.AmbiguousIDError{Short: suffix, Candidates: rows}
	}
}

// privacyShim is a tiny adapter so a candidate row can flow through
// privacy.IsVisible without exporting the candidate struct itself.
type privacyShim struct{ author, scope string }

func (p privacyShim) GetAuthor() string { return p.author }
func (p privacyShim) GetScope() string  { return p.scope }

// goalFromRecord projects an @goal gdl.Record + the on-disk state dir
// into a Goal value. Optional fields (by, parent) read as "" when absent
// (gdl.Record.Get returns "" for missing keys).
func goalFromRecord(r gdl.Record, state State) Goal {
	return Goal{
		ID:        r.Get("id"),
		Author:    r.Get("author"),
		Statement: r.Get("statement"),
		By:        r.Get("by"),
		Parent:    r.Get("parent"),
		Scope:     r.Get("scope"),
		TS:        r.Get("ts"),
		State:     state,
	}
}

// MoveToCompleted moves live/goals/active/<id>.gdl to
// live/goals/completed/<id>.gdl AND appends the @goal-complete record
// to the destination file. Atomic under .rufio/locks/goal-<id>.lock
// (D17.7). Returns *NoSuchGoalError when the active file is gone (race
// with a concurrent complete/abandon, or already-handled — including
// the case where the goal has been moved to abandoned).
//
// Implementation uses link(2)+unlink(2) for the destination create —
// link(2) fails with EEXIST if the destination already exists, which
// promotes the race to *NoSuchGoalError instead of silently overwriting
// a prior transition. See summon.MoveToAccepted for the same pattern.
func MoveToCompleted(root, goalID, by, outcome, ts string) error {
	completeRec := BuildCompleteRecord(goalID, by, outcome, ts)
	return moveTo(root, goalID, StateCompleted, completeRec)
}

// MoveToAbandoned moves live/goals/active/<id>.gdl to
// live/goals/abandoned/<id>.gdl AND appends the @goal-abandon record.
// Atomicity guarantees match MoveToCompleted.
func MoveToAbandoned(root, goalID, by, reason, ts string) error {
	abandonRec := BuildAbandonRecord(goalID, by, reason, ts)
	return moveTo(root, goalID, StateAbandoned, abandonRec)
}

// moveTo is the shared transition helper for complete/abandon. Holds
// the per-goal lock for the entire read-active + write-new + remove-old
// sequence so two callers can't both win.
//
// On-disk steps inside the lock:
//  1. Read live/goals/active/<id>.gdl. Missing → *NoSuchGoalError.
//  2. Compose dest contents: original @goal + audit record.
//  3. Write live/goals/<dest>/<id>.gdl.tmp.
//  4. os.Link(tmp, dest). EEXIST → *NoSuchGoalError (race lost).
//  5. os.Remove(tmp).
//  6. os.Remove(active/<id>.gdl).
//
// On crash between steps 4 and 6: dest exists, active exists, dest
// authoritative — the next caller hits step 1 (read active) then step 4
// (link fails EEXIST) and surfaces *NoSuchGoalError.
func moveTo(root, goalID string, dest State, auditRecord gdl.Record) error {
	activePath := filepath.Join(root, "live", "goals", string(StateActive), goalID+".gdl")
	destDir := filepath.Join(root, "live", "goals", string(dest))
	destPath := filepath.Join(destDir, goalID+".gdl")
	lockDir := filepath.Join(root, ".rufio", "locks", "goal-"+goalID+".lock")

	_, err := fslock.WithLock(lockDir, 0, func() (struct{}, error) {
		var zero struct{}

		// 1. Read active.
		origBytes, err := os.ReadFile(activePath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return zero, &rufioerr.NoSuchGoalError{ID: goalID}
			}
			return zero, err
		}

		// 2. Compose dest contents: original (preserved verbatim) + audit
		// record on its own line. AppendRecord handles trailing-newline
		// normalisation so dest always ends with exactly one newline.
		_, contents := gdl.AppendRecord(string(origBytes), auditRecord)

		// 3. Write tmp.
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return zero, err
		}
		tmp := destPath + ".tmp"
		if err := os.WriteFile(tmp, []byte(contents), 0o644); err != nil {
			return zero, err
		}

		// 4. Atomic create-no-overwrite via link(2). EEXIST means a prior
		// transition already wrote the dest — surface as NoSuchGoalError.
		if err := os.Link(tmp, destPath); err != nil {
			_ = os.Remove(tmp)
			if errors.Is(err, fs.ErrExist) {
				return zero, &rufioerr.NoSuchGoalError{ID: goalID}
			}
			return zero, err
		}

		// 5. Best-effort cleanup of the link source.
		if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return zero, err
		}

		// 6. Remove the active file. Re-run safety: ErrNotExist is benign.
		if err := os.Remove(activePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return zero, err
		}
		return zero, nil
	})
	return err
}

// ReadAll scans all three state dirs and returns []Goal with State
// populated + audit fields filled in for non-active goals. Sort order:
// state precedence (active < completed < abandoned) per D17.11; within
// each state, ts descending (newest first).
//
// Missing state directories are NOT an error — fresh projects have none.
// Per-file parse errors are propagated so list commands surface
// corruption rather than silently dropping records.
func ReadAll(root string) ([]Goal, error) {
	var out []Goal
	for _, state := range allStates {
		dir := filepath.Join(root, "live", "goals", string(state))
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		var bucket []Goal
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			bs, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			records, err := gdl.ParseDocument(string(bs))
			if err != nil {
				return nil, fmt.Errorf("goal: parse %s: %w", path, err)
			}
			var goalRec *gdl.Record
			for i, r := range records {
				if r.Type == "goal" {
					goalRec = &records[i]
					break
				}
			}
			if goalRec == nil {
				continue
			}
			g := goalFromRecord(*goalRec, state)
			for _, r := range records {
				switch r.Type {
				case "goal-complete":
					g.CompletedBy = r.Get("by")
					g.CompletedAt = r.Get("ts")
					g.Outcome = r.Get("outcome")
				case "goal-abandon":
					g.AbandonedBy = r.Get("by")
					g.AbandonedAt = r.Get("ts")
					g.Reason = r.Get("reason")
				}
			}
			bucket = append(bucket, g)
		}
		// Sort this state's bucket by ts descending. Stable sort so ties
		// retain readdir order for determinism.
		sort.SliceStable(bucket, func(i, j int) bool {
			return bucket[i].TS > bucket[j].TS
		})
		out = append(out, bucket...)
	}
	return out, nil
}

// ActiveChildren returns the ids of active goals whose `parent:` field
// equals parentID. Returned in directory-read order (no sort) — callers
// that need stability should sort. Missing live/goals/active/ is NOT an
// error (fresh projects have none) and surfaces as a nil slice.
//
// Used by the #130 hierarchy-integrity gate in `goal complete` / `goal
// abandon`: a parent cannot transition out of active while any of its
// declared children are still active.
//
// O(N) over live/goals/active/. Scoped to the active dir because that's
// the only state where a child can still be a coordination obstacle —
// completed/abandoned children are settled and shouldn't block their
// parent.
func ActiveChildren(root, parentID string) ([]string, error) {
	dir := filepath.Join(root, "live", "goals", string(StateActive))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var children []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		bs, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		records, err := gdl.ParseDocument(string(bs))
		if err != nil {
			return nil, fmt.Errorf("goal: parse %s: %w", path, err)
		}
		for _, r := range records {
			if r.Type != "goal" {
				continue
			}
			if r.Get("parent") == parentID {
				children = append(children, r.Get("id"))
			}
			break
		}
	}
	return children, nil
}

// RenderJSON emits JSONL — one JSON object per goal, line-delimited.
//
// Locked keys (ALWAYS present): _type ("goal"), _version ("1"), id,
// author, statement, scope, ts, state. Audit-derived keys
// (by, parent, completed_by, completed_at, outcome, abandoned_by,
// abandoned_at, reason) are emitted ONLY when their source field is
// non-empty, so the JSON shape stays predictable for active goals (no
// half-set keys).
//
// This is the single source of truth for the goals JSON shape, consumed
// by `rufio goals list --json` (internal/cli/goals.go) and the MCP
// goals_list tool (internal/mcp/tools_goals.go). Output is byte-identical
// across both: each line is json.Marshal(payload) + "\n", and Go sorts
// map keys deterministically, so the framing matches the prior
// output.WriteJSONL path exactly.
func RenderJSON(w io.Writer, rows []Goal) error {
	enc := json.NewEncoder(w)
	for _, g := range rows {
		payload := map[string]interface{}{
			"_type":     "goal",
			"_version":  "1",
			"id":        g.ID,
			"author":    g.Author,
			"statement": g.Statement,
			"scope":     g.Scope,
			"ts":        g.TS,
			"state":     string(g.State),
		}
		if g.By != "" {
			payload["by"] = g.By
		}
		if g.Parent != "" {
			payload["parent"] = g.Parent
		}
		if g.CompletedBy != "" {
			payload["completed_by"] = g.CompletedBy
		}
		if g.CompletedAt != "" {
			payload["completed_at"] = g.CompletedAt
		}
		if g.Outcome != "" {
			payload["outcome"] = g.Outcome
		}
		if g.AbandonedBy != "" {
			payload["abandoned_by"] = g.AbandonedBy
		}
		if g.AbandonedAt != "" {
			payload["abandoned_at"] = g.AbandonedAt
		}
		if g.Reason != "" {
			payload["reason"] = g.Reason
		}
		if err := enc.Encode(payload); err != nil {
			return err
		}
	}
	return nil
}
