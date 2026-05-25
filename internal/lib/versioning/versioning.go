// Package versioning is Rufio's content-addressed version engine. Mirrors
// src/lib/versioning.ts.
//
// IMPORTANT: AppendRef assigns the version INSIDE the per-path lock, not
// from a value supplied by the caller. This is the week-1 Phase 4 review
// I1 fix baked in from day 1: two concurrent pushers cannot collide on the
// same version number because the read-and-compute-version step happens
// while the lock is held. The TestAppendRef_SerialisesConcurrentCallers
// test directly proves the contract.
package versioning

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/fslock"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
)

// Stage is the lifecycle stage of a ref: draft → staged → live.
type Stage string

const (
	StageDraft  Stage = "draft"
	StageStaged Stage = "staged"
	StageLive   Stage = "live"
)

// IsValidStage reports whether s is a known stage value.
func IsValidStage(s string) bool {
	return s == string(StageDraft) || s == string(StageStaged) || s == string(StageLive)
}

// RefRecord is one @ref entry: a content-path version.
type RefRecord struct {
	Path           string `json:"path"`
	Version        int    `json:"version"`
	SHA256         string `json:"sha256"`
	Stage          Stage  `json:"stage"`
	Timestamp      string `json:"ts"`
	Author         string `json:"author"`
	RolledBackFrom *int   `json:"rolledBackFrom,omitempty"`
	ApprovedBy     string `json:"approvedBy,omitempty"`
	PromotedFrom   string `json:"promotedFrom,omitempty"`
}

// RefIntent is everything required to write a new @ref EXCEPT the version.
// AppendRef assigns the version inside the lock so concurrent callers
// can't collide.
type RefIntent struct {
	Path           string
	SHA256         string
	Stage          Stage
	Timestamp      string
	Author         string
	RolledBackFrom *int
	ApprovedBy     string
	PromotedFrom   string
}

// SHA256Of returns the hex-encoded sha256 digest of buf.
func SHA256Of(buf []byte) string {
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// NowISO returns the current UTC time in RFC3339 with nanosecond precision.
func NowISO() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// canonicalTSLayout is RFC3339 with a FIXED 9-digit fractional second.
// time.RFC3339Nano (what NowISO emits) trims trailing-zero fraction digits,
// so its width is variable and a lexical compare of two such strings is NOT
// chronological (e.g. "…01.1Z" sorts AFTER "…01.15Z"). Re-formatting through
// this fixed-width layout makes lexical order == chronological order, which
// is what (ts,path) cursor monotonicity depends on.
const canonicalTSLayout = "2006-01-02T15:04:05.000000000Z07:00"

// CanonicalTS re-formats an RFC3339Nano timestamp into the fixed-width
// 9-digit-fraction form whose lexical order IS chronological order. On parse
// failure it returns raw UNCHANGED — a deterministic fallback matching the
// documented "fall back to lexicographic compare so output is still
// deterministic" pattern at internal/cli/thoughts.go. Callers that need a
// total order over possibly-hostile ts fields (e.g. the listen cursor) use
// this for the SORT/CURSOR key only; the wire value keeps its original ts.
func CanonicalTS(raw string) string {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	return t.Format(canonicalTSLayout)
}

// ReadRefs reads all @ref records for a content path. Returns []RefRecord
// in file order (oldest first). Returns nil if the refs file doesn't exist.
func ReadRefs(root, contentPath string) ([]RefRecord, error) {
	file := paths.RefsPath(root, contentPath)
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	records, err := gdl.ParseDocument(string(data))
	if err != nil {
		return nil, err
	}
	out := make([]RefRecord, 0, len(records))
	for _, r := range records {
		if r.Type != "ref" {
			continue
		}
		ref, err := toRefRecord(r)
		if err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, nil
}

func toRefRecord(r gdl.Record) (RefRecord, error) {
	versionStr := r.Get("version")
	version, err := strconv.Atoi(versionStr)
	if err != nil {
		return RefRecord{}, fmt.Errorf("malformed @ref record: invalid version (%s)", versionStr)
	}
	stage := r.Get("stage")
	if !IsValidStage(stage) {
		return RefRecord{}, fmt.Errorf("malformed @ref record: invalid stage (%s)", stage)
	}
	path := r.Get("path")
	sha := r.Get("sha256")
	ts := r.Get("ts")
	if path == "" || sha == "" || ts == "" {
		return RefRecord{}, errors.New("malformed @ref record: missing required field")
	}
	author := r.Get("author")
	if author == "" {
		author = "unknown"
	}
	out := RefRecord{
		Path:      path,
		Version:   version,
		SHA256:    sha,
		Stage:     Stage(stage),
		Timestamp: ts,
		Author:    author,
	}
	if rbf := r.Get("rolled_back_from"); rbf != "" {
		if v, err := strconv.Atoi(rbf); err == nil {
			out.RolledBackFrom = &v
		}
	}
	if ab := r.Get("approved-by"); ab != "" {
		out.ApprovedBy = ab
	}
	if pf := r.Get("promoted-from"); pf != "" {
		out.PromotedFrom = pf
	}
	return out, nil
}

// LatestRef returns the highest-version ref. Returns *EmptyRefsError when
// refs is empty (typed so the dispatcher can map to exit code 1).
func LatestRef(refs []RefRecord) (RefRecord, error) {
	if len(refs) == 0 {
		return RefRecord{}, &rufioerr.EmptyRefsError{}
	}
	max := refs[0]
	for _, r := range refs[1:] {
		if r.Version > max.Version {
			max = r
		}
	}
	return max, nil
}

// LatestRefByStage returns the highest-version ref matching stage, or nil
// if no matching ref exists. Distinct from LatestRef so callers can
// distinguish "no refs at all" from "no refs in this stage."
func LatestRefByStage(refs []RefRecord, stage Stage) *RefRecord {
	var matches []RefRecord
	for _, r := range refs {
		if r.Stage == stage {
			matches = append(matches, r)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	max := matches[0]
	for _, r := range matches[1:] {
		if r.Version > max.Version {
			max = r
		}
	}
	return &max
}

// RefByVersion returns the ref with the given version, or nil if not found.
func RefByVersion(refs []RefRecord, version int) *RefRecord {
	for _, r := range refs {
		if r.Version == version {
			return &r
		}
	}
	return nil
}

// ReadBlob reads the content-addressed blob at sha256.
func ReadBlob(root, sha string) ([]byte, error) {
	p := paths.BlobPath(root, sha)
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("blob %s missing at %s", sha, p)
	}
	return data, err
}

// VersionSelectorKind enumerates the two ways to select a ref.
type VersionSelectorKind int

const (
	SelectorVersion VersionSelectorKind = iota
	SelectorStage
)

// VersionSelector is the tagged-union equivalent for a ref selector.
// Use SelectorVersion for `path@v3`; SelectorStage for `path@draft|staged|live`.
type VersionSelector struct {
	Kind    VersionSelectorKind
	Version int
	Stage   Stage
}

// LookupRefOrThrow finds a ref matching the selector, returning
// *NoSuchVersionError if none matches.
func LookupRefOrThrow(refs []RefRecord, sel VersionSelector, contentPath string) (RefRecord, error) {
	switch sel.Kind {
	case SelectorVersion:
		ref := RefByVersion(refs, sel.Version)
		if ref == nil {
			return RefRecord{}, &rufioerr.NoSuchVersionError{
				Path: contentPath, Version: fmt.Sprintf("v%d", sel.Version),
			}
		}
		return *ref, nil
	case SelectorStage:
		ref := LatestRefByStage(refs, sel.Stage)
		if ref == nil {
			return RefRecord{}, &rufioerr.NoSuchVersionError{
				Path: contentPath, Version: fmt.Sprintf("stage=%s", sel.Stage),
			}
		}
		return *ref, nil
	}
	return RefRecord{}, fmt.Errorf("unknown selector kind: %d", sel.Kind)
}

var versionTagRE = regexp.MustCompile(`^v(\d+)$`)

// ParsePathSelector parses `path@vN` or `path@stage` or plain path.
// Unrecognised tails after the last `@` fall through to "treat the whole
// input as a plain path" — `@` is a valid filename character (e.g.
// `posts/@username.md`, scoped npm-style paths). This matches the week-1
// Phase 2 fix.
func ParsePathSelector(input string) (path string, sel *VersionSelector) {
	at := -1
	for i := len(input) - 1; i >= 0; i-- {
		if input[i] == '@' {
			at = i
			break
		}
	}
	if at < 0 {
		return input, nil
	}
	tag := input[at+1:]
	if m := versionTagRE.FindStringSubmatch(tag); m != nil {
		v, _ := strconv.Atoi(m[1])
		return input[:at], &VersionSelector{Kind: SelectorVersion, Version: v}
	}
	if IsValidStage(tag) {
		return input[:at], &VersionSelector{Kind: SelectorStage, Stage: Stage(tag)}
	}
	// Unrecognised tail → treat whole input as a plain path. Typos like
	// `path@v999xyz` will surface as "no refs" downstream.
	return input, nil
}

// WriteBlob writes content as a content-addressed blob. Returns the
// sha256. Idempotent: if the blob already exists, the existing file is
// preserved (since same content → same sha → same path).
func WriteBlob(root string, content []byte) (string, error) {
	sha := SHA256Of(content)
	p := paths.BlobPath(root, sha)
	if _, err := os.Stat(p); err == nil {
		return sha, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, content, 0o644); err != nil {
		return "", err
	}
	return sha, nil
}

// NextVersion returns the next monotonic version number. Returns 1 for
// empty refs, otherwise max(existing) + 1.
func NextVersion(refs []RefRecord) int {
	if len(refs) == 0 {
		return 1
	}
	max := refs[0].Version
	for _, r := range refs[1:] {
		if r.Version > max {
			max = r.Version
		}
	}
	return max + 1
}

// AppendRef appends a new @ref record under a per-path mkdir lock. The
// version is computed INSIDE the lock by re-reading the file and calling
// NextVersion. This prevents the TOCTOU bug where two concurrent callers
// could both compute version=N+1 outside the lock and write duplicate
// records.
//
// Returns the fully-formed RefRecord (with its assigned version).
func AppendRef(root string, intent RefIntent) (RefRecord, error) {
	file := paths.RefsPath(root, intent.Path)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return RefRecord{}, err
	}
	lockDir := file + ".lock"
	return fslock.WithLock(lockDir, 0, func() (RefRecord, error) {
		// Read INSIDE the lock — TOCTOU-safe version assignment.
		var existing string
		if data, err := os.ReadFile(file); err == nil {
			existing = string(data)
		} else if !errors.Is(err, os.ErrNotExist) {
			return RefRecord{}, err
		}
		records, err := gdl.ParseDocument(existing)
		if err != nil {
			return RefRecord{}, err
		}
		var refs []RefRecord
		for _, r := range records {
			if r.Type != "ref" {
				continue
			}
			rec, err := toRefRecord(r)
			if err != nil {
				return RefRecord{}, err
			}
			refs = append(refs, rec)
		}
		version := NextVersion(refs)
		ref := RefRecord{
			Path:           intent.Path,
			Version:        version,
			SHA256:         intent.SHA256,
			Stage:          intent.Stage,
			Timestamp:      intent.Timestamp,
			Author:         intent.Author,
			RolledBackFrom: intent.RolledBackFrom,
			ApprovedBy:     intent.ApprovedBy,
			PromotedFrom:   intent.PromotedFrom,
		}
		fields := []gdl.RecordField{
			{Key: "path", Value: ref.Path},
			{Key: "version", Value: strconv.Itoa(ref.Version)},
			{Key: "sha256", Value: ref.SHA256},
			{Key: "stage", Value: string(ref.Stage)},
			{Key: "ts", Value: ref.Timestamp},
			{Key: "author", Value: ref.Author},
		}
		if ref.RolledBackFrom != nil {
			fields = append(fields, gdl.RecordField{
				Key: "rolled_back_from", Value: strconv.Itoa(*ref.RolledBackFrom),
			})
		}
		if ref.ApprovedBy != "" {
			fields = append(fields, gdl.RecordField{Key: "approved-by", Value: ref.ApprovedBy})
		}
		if ref.PromotedFrom != "" {
			fields = append(fields, gdl.RecordField{Key: "promoted-from", Value: ref.PromotedFrom})
		}
		_, result := gdl.AppendRecord(existing, gdl.Record{Type: "ref", Fields: fields})
		if err := os.WriteFile(file, []byte(result), 0o644); err != nil {
			return RefRecord{}, err
		}
		return ref, nil
	})
}

// EnsureRufioDir creates the .rufio/ scaffolding (idempotent).
func EnsureRufioDir(root string) error {
	base := paths.RufioDir(root)
	for _, sub := range []string{"history", "refs", "snapshots", "locks"} {
		if err := os.MkdirAll(filepath.Join(base, sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}
