// Package swarm implements the demo-helper write side of `rufio swarm
// spawn` plus the read side consumed by `rufio demo` (PR #24).
//
// A spawn run scaffolds N agent identities under a shared persona tag
// and records each one as a @spawned record in .rufio/swarm.local.gdl.
// The file is gitignored under the .rufio/ umbrella — swarm state is
// per-project demo metadata, not user content.
//
// On-disk shape (one record per agent, field order locked at D21.6):
//
//	@spawned|persona:<persona>|agent:<persona>-<3-digit-seq>|ts:<ts>
//
// Subsequent spawn invocations APPEND to the same file under
// .rufio/locks/swarm.lock (D21.5) — the lock domain is project-wide
// because the file is shared across personas. Sequence numbers
// auto-increment from max(existing seq for persona)+1 (D21.4); we do
// not gap-fill.
package swarm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/fslock"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// MinCount and MaxCount bound --count per D21.3. The upper bound is a
// pragmatic demo cap — generating 50 identities in a single command is
// already more agents than any v1 scenario exercises.
const (
	MinCount = 1
	MaxCount = 50
)

// personaRegex enforces D21.2 — same grammar as the agent-id leading
// segment so persona-derived ids stay valid against identity.Validate.
// The identity regex itself is wider ([a-z0-9][a-z0-9-]{0,63}) because
// it also accepts opaque ids; personas must be human-typed labels, so
// we require a leading letter.
var personaRegex = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// seqRegex extracts the trailing numeric segment of a spawned agent-id
// (`<persona>-<seq>`). We anchor on the trailing digits rather than
// re-splitting on `-` because personas themselves may contain hyphens
// (e.g., `pull-request-reviewer-001`).
var seqRegex = regexp.MustCompile(`-(\d+)$`)

// Spawned is the parsed @spawned record. State lives on-disk only; the
// in-memory shape is read-only.
type Spawned struct {
	Persona string
	Agent   string
	TS      string
}

// ValidatePersona returns *InvalidPersonaError when persona is empty or
// fails the persona regex. Empty surfaces a distinct message; malformed
// quotes the offending value.
func ValidatePersona(persona string) error {
	if persona == "" {
		return &rufioerr.InvalidPersonaError{}
	}
	if !personaRegex.MatchString(persona) {
		return &rufioerr.InvalidPersonaError{Persona: persona}
	}
	return nil
}

// ValidateCount returns *InvalidCountError when count is outside
// [MinCount, MaxCount]. Per D21.3 the default of 0 is also a usage
// error — the caller is required to supply --count explicitly.
func ValidateCount(count int) error {
	if count < MinCount || count > MaxCount {
		return &rufioerr.InvalidCountError{Count: count}
	}
	return nil
}

// BuildSpawnedRecord returns the @spawned gdl.Record with the locked
// field order (D21.6): persona, agent, ts.
func BuildSpawnedRecord(persona, agent, ts string) gdl.Record {
	return gdl.Record{Type: "spawned", Fields: []gdl.RecordField{
		{Key: "persona", Value: persona},
		{Key: "agent", Value: agent},
		{Key: "ts", Value: ts},
	}}
}

// FormatAgentID returns `<persona>-<3-digit-zero-padded-seq>` per
// D21.4. The width is fixed at 3 digits to keep ids sortable as
// strings; sequences beyond 999 would still sort correctly within the
// same width-bucket but cross-bucket sort breaks. The MaxCount=50 cap
// keeps us comfortably inside one bucket per spawn call, and NextSeq
// can still grow the value across multiple calls (caller's
// responsibility if they want to spawn > 999 of one persona over a
// project's lifetime).
func FormatAgentID(persona string, seq int) string {
	return fmt.Sprintf("%s-%03d", persona, seq)
}

// GenerateBatch produces count agent-ids of the form <persona>-<seq>
// starting from nextSeq (inclusive). The result has length=count and
// preserves spawn order so the caller's stdout/JSONL emission is
// deterministic.
func GenerateBatch(persona string, count, nextSeq int) []string {
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, FormatAgentID(persona, nextSeq+i))
	}
	return out
}

// NextSeq returns max(existing seq for persona) + 1, or MinCount (1)
// when no records of this persona exist. Not gap-filling: if existing
// has support-001 and support-003, NextSeq returns 4. This keeps the
// spawn-order assumption intact for the demo orchestrator (PR #24) —
// later-spawned ids always sort later.
func NextSeq(existing []Spawned, persona string) int {
	maxSeq := 0
	for _, s := range existing {
		if s.Persona != persona {
			continue
		}
		seq := parseSeq(s.Agent)
		if seq > maxSeq {
			maxSeq = seq
		}
	}
	return maxSeq + 1
}

// parseSeq extracts the trailing numeric segment from an agent-id of
// the form `<persona>-<digits>`. Returns 0 when the id doesn't match
// (caller-tolerant — corrupt or hand-edited records degrade gracefully
// rather than poisoning the NextSeq calculation).
func parseSeq(agent string) int {
	m := seqRegex.FindStringSubmatch(agent)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n
}

// localFilePath is the on-disk location of the persisted swarm state.
// The .local. suffix matches the gitignored convention from
// .rufio/identity.local.gdl — swarm state is per-project demo
// metadata, never committed.
func localFilePath(root string) string {
	return filepath.Join(root, ".rufio", "swarm.local.gdl")
}

// lockPath is the project-wide swarm lock. Singular per D21 — the
// file is shared across personas so per-persona locks would still
// need to serialise the read-merge-write of the underlying file.
func lockPath(root string) string {
	return filepath.Join(root, ".rufio", "locks", "swarm.lock")
}

// ReadAll reads .rufio/swarm.local.gdl and returns the @spawned
// records in file order. A missing file is not an error (fresh
// projects never invoked `swarm spawn`) — returns empty + nil.
// Non-@spawned records are tolerated but skipped, leaving the file
// hospitable to future record types under the same umbrella.
func ReadAll(root string) ([]Spawned, error) {
	bs, err := os.ReadFile(localFilePath(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return nil, fmt.Errorf("swarm: parse %s: %w", localFilePath(root), err)
	}
	out := make([]Spawned, 0, len(records))
	for _, r := range records {
		if r.Type != "spawned" {
			continue
		}
		out = append(out, Spawned{
			Persona: r.Get("persona"),
			Agent:   r.Get("agent"),
			TS:      r.Get("ts"),
		})
	}
	return out, nil
}

// Append writes one @spawned record per newAgent to
// .rufio/swarm.local.gdl, atomically (via .tmp + os.Rename) under
// .rufio/locks/swarm.lock. The lock spans the read-merge-write so
// concurrent spawn invocations across processes never lose records
// or collide on agent-ids.
//
// Idempotency (D21.7): any agent-id already present in the existing
// file is skipped — returned in the `skipped` slice without erroring.
// The caller can warn on stderr. In normal use NextSeq picks fresh
// ids so this path is defensive only (hand-edited file, or a future
// caller that bypasses NextSeq). The `added` slice preserves input
// order so the caller's render stays deterministic.
//
// The function CREATES the .rufio/ dir and the .rufio/locks/ parent
// dir as needed. Permissions match the rest of the substrate
// (0o755 for dirs, 0o644 for the data file).
func Append(root, persona string, newAgents []string, ts string) (added []string, skipped []string, err error) {
	if err := os.MkdirAll(filepath.Dir(localFilePath(root)), 0o755); err != nil {
		return nil, nil, err
	}
	type result struct {
		added   []string
		skipped []string
	}
	res, err := fslock.WithLock(lockPath(root), 0, func() (result, error) {
		// Read existing inside the lock so the duplicate-id check is
		// authoritative against a concurrently-extended file.
		existing, err := readExistingLocked(root)
		if err != nil {
			return result{}, err
		}
		have := make(map[string]struct{}, len(existing))
		for _, s := range existing {
			have[s.Agent] = struct{}{}
		}

		var addedIDs, skippedIDs []string
		records := make([]gdl.Record, 0, len(newAgents))
		for _, agent := range newAgents {
			if _, dup := have[agent]; dup {
				skippedIDs = append(skippedIDs, agent)
				continue
			}
			have[agent] = struct{}{}
			addedIDs = append(addedIDs, agent)
			records = append(records, BuildSpawnedRecord(persona, agent, ts))
		}

		if len(records) == 0 {
			// All inputs were duplicates — nothing to write. Return the
			// skip list so the caller can warn; the file is untouched.
			return result{added: addedIDs, skipped: skippedIDs}, nil
		}

		// Compose the new file body in-memory, then atomic-rename. We
		// re-read the raw bytes (rather than re-rendering the parsed
		// records) so any hand-added comments or non-@spawned records
		// in the file survive the round-trip verbatim.
		rawBytes, err := readRawLocked(root)
		if err != nil {
			return result{}, err
		}
		body := string(rawBytes)
		for _, rec := range records {
			_, body = gdl.AppendRecord(body, rec)
		}

		if err := atomicWrite(localFilePath(root), []byte(body)); err != nil {
			return result{}, err
		}
		return result{added: addedIDs, skipped: skippedIDs}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return res.added, res.skipped, nil
}

// readExistingLocked is ReadAll without re-acquiring the lock. Callers
// must already hold .rufio/locks/swarm.lock.
func readExistingLocked(root string) ([]Spawned, error) {
	bs, err := readRawLocked(root)
	if err != nil {
		return nil, err
	}
	if len(bs) == 0 {
		return nil, nil
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return nil, fmt.Errorf("swarm: parse %s: %w", localFilePath(root), err)
	}
	out := make([]Spawned, 0, len(records))
	for _, r := range records {
		if r.Type != "spawned" {
			continue
		}
		out = append(out, Spawned{
			Persona: r.Get("persona"),
			Agent:   r.Get("agent"),
			TS:      r.Get("ts"),
		})
	}
	return out, nil
}

// readRawLocked returns the raw file bytes (or empty on ENOENT). Like
// readExistingLocked, the caller must already hold the swarm lock.
func readRawLocked(root string) ([]byte, error) {
	bs, err := os.ReadFile(localFilePath(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return bs, nil
}

// atomicWrite writes data to target via target+".tmp" + os.Rename so
// a partial write can never expose a half-parsed file (mirrors the
// identity.WriteLocalFile discipline). Ensures the body ends with a
// single trailing newline.
func atomicWrite(target string, data []byte) error {
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		data = append(data, '\n')
	}
	tmp := target + ".tmp"
	// Best-effort cleanup so a failed WriteFile/Rename never strands
	// swarm.local.gdl.tmp under .rufio/ (#141). Success path: Rename
	// already moved tmp, so this Remove is a harmless no-op. The
	// trailing-newline normalisation above is unchanged.
	defer func() { _ = os.Remove(tmp) }()
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
