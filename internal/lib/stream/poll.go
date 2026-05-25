package stream

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// The Poll cursor is base64(canonicalTS + "\x00" + path) of the LAST returned
// event. It is opaque to clients, monotonic over the (canonicalTS,path)
// total order, and notification-ready: a future push transport reuses the
// identical stream.Event schema and resumes from the same key. No new
// substrate state is introduced — the cursor is derived purely from event
// fields already on disk.
//
// The ts component is versioning.CanonicalTS(Event.TS), NOT the raw ts:
// NowISO emits RFC3339Nano (trailing-zero fraction digits trimmed →
// variable width), so a lexical compare of raw ts is NOT chronological and
// would silently skip same-second events across a page boundary. The
// canonical fixed-9-digit-fraction form's lexical order IS chronological.
// Only the SORT/CURSOR key is canonicalised; the returned Event.TS keeps
// its original raw value (wire fidelity).

func encodeCursor(ts, path string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(ts + "\x00" + path))
}

// decodeCursor splits a cursor back into (canonicalTS, path). An empty
// cursor is the "from the beginning" sentinel. Any structurally-invalid
// cursor returns a plain fmt.Errorf("invalid cursor") — deliberately NOT a
// new rufioerr type (per the no-new-error-type invariant); toolErr maps the
// untyped error to the [rufio:1] class for MCP clients.
//
// The split is on the LAST NUL, not the first: encodeCursor only ever joins
// a canonical ts (which contains no NUL) and a path (the trailing
// component; a NUL cannot appear in a real path). Using LastIndexByte means
// a hostile event that somehow smuggled a NUL into the ts can never shift
// the path boundary — defence in depth alongside the per-event NUL skip in
// Poll.
func decodeCursor(c string) (ts, path string, err error) {
	if c == "" {
		return "", "", nil
	}
	b, e := base64.RawURLEncoding.DecodeString(c)
	if e != nil {
		return "", "", fmt.Errorf("invalid cursor")
	}
	s := string(b)
	i := strings.LastIndexByte(s, 0)
	if i < 0 {
		return "", "", fmt.Errorf("invalid cursor")
	}
	return s[:i], s[i+1:], nil
}

// less reports whether (ts,path) a strictly precedes b in the total order.
// CALLERS MUST PASS CANONICAL ts (versioning.CanonicalTS): the raw NowISO
// RFC3339Nano form is variable-width and NOT lexically chronological. path
// is the repo-root-relative POSIX path (unique per file), so
// (canonicalTS,path) is a stable, chronological total order with no ties.
func less(aTS, aPath, bTS, bPath string) bool {
	if aTS != bTS {
		return aTS < bTS
	}
	return aPath < bPath
}

// Poll is a stateless read of dirs (relative to root) over the same source
// FileToEvents/Match use for `rufio listen`. It returns up to max events
// strictly AFTER cursor in chronological (canonicalTS,path) order, plus an
// opaque next cursor (unchanged from the input cursor when there are no new
// events, so an idempotent re-poll yields zero events and the same cursor).
//
// `max` bounds the RESPONSE, not the work: Poll walks and parses the WHOLE
// inbox into memory each call, then sorts and slices to max. This is a
// bounded RESPONSE, not streaming-grade pagination — acceptable for v1.1
// (inboxes are TTL-swept; per-agent inboxes stay small). There is no
// watching and no blocking: a single WalkDir per dir + an in-memory sort +
// a slice. It is therefore safe to invoke from an MCP tool handler under
// context.Background() — it cannot block unboundedly (its only I/O is
// reading the files already present on disk at call time).
//
// Divergence from EmitCatchUp's error policy (intentional): EmitCatchUp
// aborts the whole replay on a per-file parse error because catch-up is a
// one-shot replay where a malformed file should surface loudly. Poll instead
// SKIPS an unparseable file (mirroring WatchAndEmit's best-effort leniency):
// a repeatedly-invoked poll must not be permanently poisoned by one bad file
// dropped into the inbox. Missing dirs are skipped cleanly, exactly as
// EmitCatchUp/WatchAndEmit do (live/promoted may not exist yet).
func Poll(root string, dirs []string, p FilterParams, cursor string, max int) ([]Event, string, error) {
	curTS, curPath, err := decodeCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	if max <= 0 {
		max = 100
	}

	// keyed pairs each Event with its canonical (ts,path) sort/cursor key,
	// computed once per event so CanonicalTS isn't re-parsed during sorting.
	type keyed struct {
		ev   Event
		csTS string // versioning.CanonicalTS(ev.TS) — the chronological key
	}
	var evs []keyed
	cache := newMetaCache()
	for _, d := range dirs {
		absDir := filepath.Join(root, d)
		info, statErr := os.Stat(absDir)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return nil, "", statErr
		}
		if !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(absDir, func(path string, de fs.DirEntry, werr error) error {
			if werr != nil || de.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return nil
			}
			relPosix := filepath.ToSlash(rel)
			fe, ferr := FileToEvents(path, relPosix)
			if ferr != nil {
				return nil // skip unparseable — see the divergence note above
			}
			for _, e := range fe {
				if !passesAll(root, e, p, cache) {
					continue
				}
				csTS := versioning.CanonicalTS(e.TS)
				// A NUL in the canonical ts or path would corrupt the
				// NUL-delimited cursor. Event.TS is an unvalidated GDL
				// field and the parse-leniency design expects hostile
				// inbox files, so treat such an event as unparseable
				// (skip) — consistent with the per-file leniency above.
				if strings.IndexByte(csTS, 0) >= 0 || strings.IndexByte(e.Path, 0) >= 0 {
					continue
				}
				if cursor != "" && !less(curTS, curPath, csTS, e.Path) {
					continue // at or before the cursor — already delivered
				}
				evs = append(evs, keyed{ev: e, csTS: csTS})
			}
			return nil
		})
	}

	sort.Slice(evs, func(i, j int) bool {
		return less(evs[i].csTS, evs[i].ev.Path, evs[j].csTS, evs[j].ev.Path)
	})

	next := cursor
	if len(evs) > max {
		evs = evs[:max]
	}
	out := make([]Event, len(evs))
	for i := range evs {
		out[i] = evs[i].ev // wire event keeps its original raw ts
	}
	if n := len(evs); n > 0 {
		// Cursor carries the CANONICAL ts so encode/decode/compare are
		// self-consistent across polls.
		next = encodeCursor(evs[n-1].csTS, evs[n-1].ev.Path)
	}
	return out, next, nil
}
