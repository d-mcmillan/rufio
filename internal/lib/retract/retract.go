// Package retract holds the helpers for `rufio retract` (write side)
// and the daemon's RetractPropagator engine (PR #8 Task 6).
//
// Each retract is uniquely named by the target thought-id, so the write
// has no lock domain (D8.4) — concurrent retracts of different thoughts
// can't collide, and concurrent retracts of the same thought are
// idempotent (last writer wins; records are functionally equivalent).
package retract

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// Record is the rendered shape of an @retract file surfaced to read
// callers (thoughts list, lineage, reason/confirm/refute advisories).
// Fields mirror the on-disk record (target/reason/by/ts). Present is
// false when no retract exists for the target — keeps callers ifless.
type Record struct {
	Present bool
	Target  string
	Reason  string
	By      string
	TS      string
}

// ReadByTarget loads live/retracted/<targetID>.gdl and returns the
// first @retract record found, with Present=true. Missing file →
// Record{Present:false}, nil error (the common "not retracted" case
// is not exceptional). Malformed files or unparseable contents return
// a wrapped parse error. A file that exists but contains no @retract
// record returns Record{Present:false}, nil — degrades to "not
// retracted" for the read surface.
//
// Used by `rufio thoughts list`, `rufio lineage`, and the advisory
// stderr warnings on `reason --decision`, `confirm`, and `refute`
// against a retracted target. The write path (Write) stays untouched.
func ReadByTarget(root, targetID string) (Record, error) {
	path := filepath.Join(root, "live", "retracted", targetID+".gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Record{}, nil
		}
		return Record{}, err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return Record{}, fmt.Errorf("retract: parse %s: %w", path, err)
	}
	for _, r := range records {
		if r.Type != "retract" {
			continue
		}
		return Record{
			Present: true,
			Target:  r.Get("target"),
			Reason:  r.Get("reason"),
			By:      r.Get("by"),
			TS:      r.Get("ts"),
		}, nil
	}
	return Record{}, nil
}

// Lookup locates the target record under live/outbox/*/<id>.gdl OR
// learned/.../<id>.gdlm and returns the author. Returns
// *NoSuchThoughtError if neither lookup root contains the file.
//
// #150: observations (learned/) are now retractable. The learned/
// walk is depth-unconstrained because observation.SubjectPath maps
// subjects with arbitrary colon-segment counts to nested directories
// (e.g. customer:5821 → learned/customer/5821, agent:foo:bar →
// learned/agent/foo/bar). Author is parsed from the @observation
// record's author: field rather than the directory name (which is
// the subject, not the author).
func Lookup(root, targetID string) (string, error) {
	// Outbox first (preserves the existing common-case fast path).
	pattern := filepath.Join(root, "live", "outbox", "*", targetID+".gdl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	if len(matches) > 0 {
		// matches[0] = <root>/live/outbox/<author>/<targetID>.gdl
		return filepath.Base(filepath.Dir(matches[0])), nil
	}

	// Fallback: walk learned/ for <targetID>.gdlm. observation.SubjectPath
	// nests directories per colon-segment, so the file may be at any depth.
	_, learnedAuthor, _, found, err := findLearnedRecord(root, targetID)
	if err != nil {
		return "", err
	}
	if found {
		return learnedAuthor, nil
	}
	return "", &rufioerr.NoSuchThoughtError{ID: targetID}
}

// LookupTarget is the richer variant Lookup callers use when the
// privacy gate (#147) needs the target's scope too. Returns the
// author AND the scope: field parsed from the on-disk record.
// Existing Lookup callers stay untouched — confirm/refute upgrade to
// LookupTarget because the authz check (privacy.CanWriteAgainst)
// inspects target.GetScope().
//
// #150: walks learned/<...>/<id>.gdlm in addition to
// live/outbox/<author>/<id>.gdl. The author returned for an observation
// is the @observation record's author: field; for a thought it remains
// the parent directory name (the existing behavior).
//
// Returns *NoSuchThoughtError when neither root contains the id. Parse
// errors propagate. An on-disk record without scope: returns the author
// with scope="" — privacy then treats it as non-private, the safe
// pre-#147 default.
func LookupTarget(root, targetID string) (author, scope string, err error) {
	pattern := filepath.Join(root, "live", "outbox", "*", targetID+".gdl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", "", err
	}
	if len(matches) > 0 {
		path := matches[0]
		author = filepath.Base(filepath.Dir(path))

		bs, err := os.ReadFile(path)
		if err != nil {
			return author, "", err
		}
		records, err := gdl.ParseDocument(string(bs))
		if err != nil {
			return author, "", fmt.Errorf("retract: parse %s: %w", path, err)
		}
		for _, r := range records {
			if r.Type != "thought" {
				continue
			}
			return author, r.Get("scope"), nil
		}
		return author, "", nil
	}

	// Fallback: learned/<...>/<targetID>.gdlm.
	_, lAuthor, lScope, found, err := findLearnedRecord(root, targetID)
	if err != nil {
		return "", "", err
	}
	if found {
		return lAuthor, lScope, nil
	}
	return "", "", &rufioerr.NoSuchThoughtError{ID: targetID}
}

// findLearnedRecord walks <root>/learned/ recursively for a file named
// <targetID>.gdlm. On hit it parses the first @observation record and
// returns (path, author, scope, true, nil). On miss returns
// (_, _, _, false, nil). Filesystem errors propagate.
//
// The depth-unbounded walk is necessary because observation.SubjectPath
// nests one directory per colon-segment of the subject; a subject like
// "x:y:z" lives at learned/x/y/z/<id>.gdlm. Worst case is the full
// learned tree, but learned/ is bounded in practice (one .gdlm per
// observation).
func findLearnedRecord(root, targetID string) (path, author, scope string, found bool, err error) {
	learnedDir := filepath.Join(root, "learned")
	if _, statErr := os.Stat(learnedDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return "", "", "", false, nil
		}
		return "", "", "", false, statErr
	}
	want := targetID + ".gdlm"
	var hit string
	walkErr := filepath.WalkDir(learnedDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == want {
			hit = p
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return "", "", "", false, walkErr
	}
	if hit == "" {
		return "", "", "", false, nil
	}
	bs, err := os.ReadFile(hit)
	if err != nil {
		return hit, "", "", true, err
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return hit, "", "", true, fmt.Errorf("retract: parse %s: %w", hit, err)
	}
	for _, r := range records {
		if r.Type != "observation" {
			continue
		}
		return hit, r.Get("author"), r.Get("scope"), true, nil
	}
	// File exists but no @observation record — return the path with
	// empty author/scope; callers treat empty as the safe default.
	return hit, "", "", true, nil
}

// BuildRecord returns the @retract gdl.Record. Field order locked at
// target, reason, by, ts (per design §2.B line 131, D8.11).
func BuildRecord(target, reason, by, ts string) gdl.Record {
	return gdl.Record{Type: "retract", Fields: []gdl.RecordField{
		{Key: "target", Value: target},
		{Key: "reason", Value: reason},
		{Key: "by", Value: by},
		{Key: "ts", Value: ts},
	}}
}

// Write atomically writes record to live/retracted/<targetID>.gdl.
// .tmp + os.Rename; no lock (D8.4 — unique target id per file).
func Write(root, targetID string, record gdl.Record) error {
	dir := filepath.Join(root, "live", "retracted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, targetID+".gdl")
	tmp := target + ".tmp"
	// Best-effort cleanup so a failed WriteFile/Rename never strands
	// <targetID>.gdl.tmp under live/retracted/ (#141). Success path:
	// Rename already moved tmp, so this Remove is a harmless no-op.
	defer func() { _ = os.Remove(tmp) }()
	contents := gdl.RenderLine(record) + "\n"
	if err := os.WriteFile(tmp, []byte(contents), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
