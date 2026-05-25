// Package mirror implements the file-native client-side shadow of a
// remote rufio substrate. Two modes:
//
//   - Snapshot (pull): one-shot fetch of every visible record at a moment
//     in time. Useful for cold-init or periodic forced refresh.
//   - Sync (continuous): long-lived stream from /listen with cursor
//     resume; writes records as they arrive. Default mode.
//
// Both modes preserve the canonical on-disk layout (live/outbox/<agent>/
// <id>.gdl etc.) byte-identically, so the mirror is a drop-in replacement
// for `git clone` of a substrate — the local file tree IS the substrate
// from the client's perspective.
//
// The mirror is read-only on the client side: writes always go through
// the server. This keeps a single canonical store and avoids merge
// conflicts.
package mirror

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/d-mcmillan/rufio/internal/lib/client"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
)

// SnapshotOptions configure a one-shot Pull.
type SnapshotOptions struct {
	// ServerURL is the base URL of the remote rufio server.
	ServerURL string
	// Token is the bearer-token plaintext.
	Token string
	// InsecureTLS skips certificate verification (localhost dev only).
	InsecureTLS bool
	// To is the local destination root where the mirror is written. The
	// canonical substrate dirs (live/outbox/<agent>/...) are created
	// underneath this directory.
	To string
}

// Pull is the snapshot mode entry point. Fetches every visible record
// from the remote server and writes it into opts.To, preserving the
// canonical on-disk layout. Atomic per file (.tmp + rename). Idempotent:
// re-running pull updates changed files and leaves unchanged ones alone.
//
// Privacy: the server filters records BEFORE emitting; the client never
// sees records it isn't entitled to (privacy floor #147 server-side).
func Pull(ctx context.Context, opts SnapshotOptions) (Stats, error) {
	if opts.ServerURL == "" {
		return Stats{}, errors.New("mirror pull: --from is required")
	}
	if opts.Token == "" {
		return Stats{}, errors.New("mirror pull: --token is required")
	}
	if opts.To == "" {
		return Stats{}, errors.New("mirror pull: --to is required")
	}

	// Ensure the --to directory exists before any joinUnderRoot call
	// runs against it. Same fix as Sync — the F4 symlink-defense
	// walked up past a non-existent --to and produced a false-
	// positive escape rejection, dropping every record silently
	// into SkippedNoPath. Mirroring sync.Sync's auto-create here
	// keeps the documented user flow (no preceding mkdir) working.
	if err := os.MkdirAll(opts.To, 0o755); err != nil {
		return Stats{}, fmt.Errorf("mirror pull: cannot create --to directory %q: %w", opts.To, err)
	}

	c, err := client.Dial(ctx, client.Config{
		Endpoint:    opts.ServerURL,
		Token:       opts.Token,
		InsecureTLS: opts.InsecureTLS,
	})
	if err != nil {
		return Stats{}, err
	}
	defer c.Close()

	// Use the existing recall tool — it already walks given/learned/live
	// and emits the records the bearer-token agent is allowed to see.
	// include_expired=true so the mirror is a faithful snapshot including
	// retraction markers; the mirror consumer can re-apply expiry rules
	// locally if it cares about them.
	res, err := c.CallTool(ctx, "recall", map[string]interface{}{
		"include_expired": true,
	})
	if err != nil {
		return Stats{}, err
	}

	rawRecords, ok := res["records"].([]interface{})
	if !ok {
		return Stats{}, fmt.Errorf("mirror pull: recall response has no records array (got %T)", res["records"])
	}

	var st Stats
	for _, r := range rawRecords {
		rec, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		path, content, err := projectRecordToFile(rec)
		if err != nil {
			st.SkippedNoPath++
			continue
		}
		// Defense in depth (security audit H1): the path coming back
		// from projectRecordToFile is assembled from untrusted
		// record fields (id, author, content_path). A crafted record
		// with author="../../etc" or content_path="/etc/cron.d/x" must
		// be rejected before filepath.Join lands us outside the mirror
		// root. joinUnderRoot rejects absolutes, traversal sequences,
		// NUL/control bytes, and post-clean ".."-escape.
		dst, err := joinUnderRoot(opts.To, path)
		if err != nil {
			st.SkippedNoPath++
			continue
		}
		written, err := writeAtomic(dst, content)
		if err != nil {
			return st, err
		}
		if written {
			st.Wrote++
		} else {
			st.Unchanged++
		}
	}
	return st, nil
}

// Stats reports the outcome of a Pull / Sync invocation. Exposed so the
// CLI can print a "wrote N, unchanged M" summary line.
type Stats struct {
	Wrote         int
	Unchanged     int
	SkippedNoPath int
}

// projectRecordToFile reconstructs the on-disk path and raw GDL line
// for a single recall-returned record. The recall JSONL shape carries
// _type plus the record's fields; we re-render via gdl.RenderLine to
// produce byte-identical output to what the server has on disk.
//
// Returns ("", "", error) when the record's _type does not map onto a
// canonical filesystem layout (e.g. a synthetic record that the mirror
// can safely skip).
func projectRecordToFile(rec map[string]interface{}) (string, string, error) {
	typ, _ := rec["_type"].(string)
	if typ == "" {
		// Recall sometimes uses "type" key instead.
		typ, _ = rec["type"].(string)
	}
	if typ == "" {
		return "", "", errors.New("no type")
	}

	// Gate 4 follow-up: prefer the wire `path` field (root-relative
	// POSIX, set by recall.RenderJSON via H2). The server is
	// authoritative about its on-disk layout — using the server's
	// path means we mirror learned/<subject-segments>/<id>.gdlm
	// correctly without reproducing observation.SubjectPath's
	// subject-splitting logic here. Falls back to per-type
	// reconstruction when the path isn't supplied (defensive — should
	// never happen with the current recall scanner, but kept so a
	// future tool that supplies records without path still works).
	//
	// Security: the wire path flows through joinUnderRoot's safeRelPath
	// in the caller, which rejects absolutes / NUL / traversal /
	// control bytes (security audit H1). The H1 hasUnsafeComponent
	// guard on id/author below stays for the reconstruction fallback.
	if wirePath, ok := rec["path"].(string); ok && wirePath != "" {
		// Security audit N1 (v1.0.5): the top-level prefix gate MUST
		// operate on the CLEANED form, not the raw wire path. Pre-fix,
		// a hostile server emitting `path:"live/../.rufio/.mirror-cursor"`
		// passed the raw HasPrefix("live/") check, but filepath.Clean
		// resolved it to `.rufio/.mirror-cursor` — which then wrote
		// inside --to root but bypassed the "canonical substrate dirs
		// only" intent (could clobber the mirror cursor file the
		// caller hasn't read yet, replaying a stale snapshot).
		//
		// Fix: validate the cleaned form's top-level dir, not the
		// raw form. safeRelPath rejects ".." escape AND clean output
		// matching exactly one of the canonical substrate dirs.
		if err := validateCleanedTopLevel(wirePath); err != nil {
			return "", "", err
		}
		content, err := renderRecord(typ, rec)
		if err != nil {
			return "", "", err
		}
		return wirePath, content, nil
	}

	id, _ := rec["id"].(string)
	author, _ := rec["author"].(string)

	// Defense in depth (security audit H1): id and author come from an
	// untrusted recall record. Reject any value containing a path
	// separator, ".." segment, or NUL byte BEFORE filepath.Join
	// composes them into a destination. This is a tight gate — both
	// fields are bounded to the [a-z0-9-] charset upstream
	// (thought.GenerateID / identity.Validate) so a benign value
	// always slips through; a crafted record fails fast.
	if hasUnsafeComponent(id) || hasUnsafeComponent(author) {
		return "", "", fmt.Errorf("rejected suspicious path component (id=%q author=%q)", id, author)
	}

	// Reconstruction fallback (used when the wire record lacks a
	// `path` field). Path layout matches what each writer in
	// internal/lib produces.
	var path string
	switch typ {
	case "thought":
		if id == "" || author == "" {
			return "", "", errors.New("thought missing id/author")
		}
		path = filepath.Join("live", "outbox", author, id+".gdl")
	case "observation":
		if id == "" || author == "" {
			return "", "", errors.New("observation missing id/author")
		}
		path = filepath.Join("live", "outbox", author, id+".gdl")
	case "reason":
		if id == "" {
			return "", "", errors.New("reason missing id")
		}
		path = filepath.Join("live", "reasoning", id+".gdl")
	case "given":
		// given records are content-addressed; recall provides the
		// content_path so we can reconstruct the on-disk slot. If
		// content_path isn't present, skip — the mirror only mirrors
		// what the server gives it.
		cp, _ := rec["content_path"].(string)
		if cp == "" {
			return "", "", errors.New("given missing content_path")
		}
		path = filepath.Join("given", cp)
	case "learned":
		cp, _ := rec["content_path"].(string)
		if cp == "" {
			return "", "", errors.New("learned missing content_path")
		}
		path = filepath.Join("learned", cp)
	default:
		return "", "", fmt.Errorf("unsupported record type for mirror: %s", typ)
	}

	content, err := renderRecord(typ, rec)
	if err != nil {
		return "", "", err
	}
	return path, content, nil
}

// renderRecord reconstructs the GDL line for a single record. The recall
// JSON shape has the same fields as the on-disk GDL — we just need to
// re-emit them in the canonical order. ToFields preserves field order
// from the original record where possible.
func renderRecord(typ string, rec map[string]interface{}) (string, error) {
	// Field order MUST mirror the canonical on-disk layout produced by
	// each lib's Build*Record so reconstructed lines stay byte-stable
	// across the snapshot path (mirror pull) and downstream JSONL
	// pipelines. Sources of truth:
	//   thought     → internal/lib/thought.BuildThoughtRecord
	//   observation → internal/lib/observation.BuildRecord
	//   reason      → internal/lib/reason.BuildRecord
	orders := map[string][]string{
		"thought":     {"id", "author", "type", "subject", "content", "scope", "topics", "ts", "ttl", "parent"},
		"observation": {"id", "author", "subject", "predicate", "object", "scope", "topics", "confidence", "ts"},
		"reason":      {"id", "author", "content", "scope", "subject", "topics", "parent", "decision", "ts"},
		"given":       {"content_path", "sha256", "stage", "ts", "author"},
		"learned":     {"content_path", "sha256", "stage", "ts", "author"},
	}
	order, ok := orders[typ]
	if !ok {
		return "", fmt.Errorf("renderRecord: no field order for type %s", typ)
	}
	var fields []gdl.RecordField
	used := map[string]bool{}
	for _, k := range order {
		v, ok := rec[k]
		if !ok {
			continue
		}
		s := toString(v)
		if s == "" {
			continue
		}
		fields = append(fields, gdl.RecordField{Key: k, Value: s})
		used[k] = true
	}
	// Skip metadata keys we DO NOT want round-tripping back to disk.
	skip := map[string]bool{
		"_type": true, "_version": true, "type": true,
		"retracted": true, "expired": true,
	}
	// (Other unknown keys are dropped — the mirror is a snapshot of
	// canonical fields, not of recall's view envelope.)
	_ = skip

	return gdl.RenderLine(gdl.Record{Type: typ, Fields: fields}) + "\n", nil
}

func toString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []interface{}:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			parts = append(parts, toString(x))
		}
		return strings.Join(parts, ",")
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	return ""
}

// writeAtomic writes content to absPath via .tmp + rename. Returns
// (wrote=true) when the file changed; (wrote=false) when the existing
// file already had the same content (idempotent re-pull). Atomic on
// POSIX.
//
// Security audit M4 (v1.0.5): the .tmp filename MUST be unique per
// call so concurrent writers don't clobber each other's tmp file.
// Pre-fix, two goroutines writing the same target both opened
// `<target>.tmp` for write, and the second's content overwrote the
// first's before either renamed — atomicity violated.
//
// os.CreateTemp produces a process-unique tmp name with a random
// suffix (e.g. `<target>.123456.tmp`) so each writer gets its own
// fd. The rename is still atomic; concurrent writers see the LAST
// rename winning, which is the correct content-equality semantic.
func writeAtomic(absPath, content string) (bool, error) {
	if existing, err := os.ReadFile(absPath); err == nil {
		if string(existing) == content {
			return false, nil // unchanged
		}
	}
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	// Use CreateTemp for a process-unique tmp filename. The pattern
	// `<base>.*.tmp` puts the random suffix in the middle so the
	// `.tmp` extension survives, making the file easy to clean up
	// by tooling that filters on .tmp.
	base := filepath.Base(absPath)
	tmp, err := os.CreateTemp(dir, base+".*.tmp")
	if err != nil {
		return false, err
	}
	tmpPath := tmp.Name()
	if _, werr := tmp.Write([]byte(content)); werr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return false, werr
	}
	if cerr := tmp.Close(); cerr != nil {
		_ = os.Remove(tmpPath)
		return false, cerr
	}
	if rerr := os.Rename(tmpPath, absPath); rerr != nil {
		_ = os.Remove(tmpPath)
		return false, rerr
	}
	return true, nil
}

// PrivacyRecordView wraps a recall record so it satisfies privacy.Record.
// Defensive: the server is the floor, but we keep the predicate available
// for any future client-side cross-check.
type PrivacyRecordView struct {
	Scope  string
	Author string
}

func (p PrivacyRecordView) GetScope() string  { return p.Scope }
func (p PrivacyRecordView) GetAuthor() string { return p.Author }

// Ensure privacy.Record is satisfiable from the local view (compile-time
// guard; the predicate is exercised server-side, so this is belt-and-
// suspenders).
var _ privacy.Record = PrivacyRecordView{}
