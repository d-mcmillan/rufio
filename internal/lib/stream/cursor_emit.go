package stream

// EmitCatchUpFrom + WatchAndEmitFrom — cursor-aware variants of
// EmitCatchUp / WatchAndEmit that close the SDK reconnect gap (#155).
//
// Why a new pair instead of changing the existing two? The existing
// signatures are stable across multiple call sites + their own test
// surface, and the new cursor + cadence parameters would force every
// caller through a non-zero struct argument. Two thin variants keep
// the legacy code path verbatim (proven correct) and add the cursor
// machinery in additive code only.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// EmitOpts collects the cursor + cadence knobs for WatchAndEmitFrom.
// All fields have safe zero-value defaults so callers that only want
// "watch from now, no cursor emission" can pass EmitOpts{}.
type EmitOpts struct {
	// FromCursor is the opaque resume token (CursorOf / Poll.next_cursor /
	// CursorRecord.Value). Empty means "no resume — live tail from now".
	// If ReplayBeforeWatch is also true, a non-empty FromCursor bounds the
	// replay; an empty FromCursor with ReplayBeforeWatch replays everything
	// (the `rufio listen --catch-up` semantic).
	FromCursor string

	// ReplayBeforeWatch toggles the catch-up pass. When true, a bounded
	// strictly-after-cursor walk runs to completion (and emits a
	// CursorRecord) BEFORE the live watcher engages. This is how `rufio
	// listen --catch-up` and `rufio listen --from=...` enter the live
	// stream without dropping records that landed during the catch-up.
	ReplayBeforeWatch bool

	// CursorEveryNEvents — emit a {"_type":"cursor",...} JSONL line every
	// N events. Zero or negative disables the count-based path.
	CursorEveryNEvents int

	// CursorEveryD — emit a CursorRecord at most this often by wall clock,
	// even if event count is below CursorEveryNEvents. Zero or negative
	// disables the time-based path. Both knobs may be on at once: the
	// first to fire wins, then both reset.
	CursorEveryD time.Duration
}

// EmitCatchUpFrom is the bounded "strictly after fromCursor" replay used
// by `rufio listen --from=<cursor>` and the catch-up half of any
// fromCursor-equipped WatchAndEmitFrom. It walks the configured dirs
// once, parses + filters every file (same source EmitCatchUp uses),
// sorts the matched events into chronological (canonicalTS,path) order,
// and writes each one JSONL to w. The returned cursor points at the
// last emitted event so a subsequent watcher can pick up from there;
// an empty input cursor with no events returns "".
//
// Errors are deliberately strict (catch-up is a one-shot replay), with
// one exception: a missing dir is skipped cleanly — graceful for fresh
// projects where live/promoted/ etc. don't exist yet.
func EmitCatchUpFrom(w io.Writer, root string, dirs []string, p FilterParams, fromCursor string) (string, error) {
	curTS, curPath, err := decodeCursor(fromCursor)
	if err != nil {
		return "", err
	}

	type keyed struct {
		ev   Event
		csTS string
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
			return "", statErr
		}
		if !info.IsDir() {
			continue
		}
		walkErr := filepath.WalkDir(absDir, func(path string, de fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if de.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			relPosix := filepath.ToSlash(rel)
			fe, ferr := FileToEvents(path, relPosix)
			if ferr != nil {
				return ferr
			}
			for _, e := range fe {
				if !passesAll(root, e, p, cache) {
					continue
				}
				csTS := versioning.CanonicalTS(e.TS)
				// Skip records that would corrupt the cursor format —
				// same defence-in-depth Poll applies.
				if hasNUL(csTS) || hasNUL(e.Path) {
					continue
				}
				if fromCursor != "" && !less(curTS, curPath, csTS, e.Path) {
					continue // at-or-before the cursor → already delivered
				}
				evs = append(evs, keyed{ev: e, csTS: csTS})
			}
			return nil
		})
		if walkErr != nil {
			return "", walkErr
		}
	}

	sort.Slice(evs, func(i, j int) bool {
		return less(evs[i].csTS, evs[i].ev.Path, evs[j].csTS, evs[j].ev.Path)
	})

	enc := json.NewEncoder(w)
	last := fromCursor
	for _, k := range evs {
		if err := enc.Encode(k.ev); err != nil {
			return last, err
		}
		last = encodeCursor(k.csTS, k.ev.Path)
	}
	return last, nil
}

// WatchAndEmitFrom is the cursor + cadence variant of WatchAndEmit. It
// behaves like the legacy WatchAndEmit when opts is the zero value;
// every flag is additive. The sequence is:
//
//  1. If opts.ReplayBeforeWatch OR opts.FromCursor != "", run
//     EmitCatchUpFrom for the configured dirs + cursor. This drains the
//     "after-cursor" backlog before live tail starts so the consumer
//     sees no gap.
//  2. After the catch-up pass, emit a CursorRecord pointing at the last
//     replayed event (if any) so the consumer can checkpoint
//     immediately.
//  3. Engage fsnotify on each dir. As live events arrive, emit them and
//     advance the in-memory "last cursor". The periodic CursorRecord is
//     emitted on the SAME stdout stream every CursorEveryNEvents or
//     CursorEveryD, whichever fires first. The cursor-emission goroutine
//     shares the existing emit mutex so cursor lines never interleave
//     with event lines mid-record.
//
// Returns when ctx is cancelled or the watcher closes.
func WatchAndEmitFrom(ctx context.Context, w io.Writer, root string, dirs []string, p FilterParams, opts EmitOpts) error {
	// State carried across the catch-up + live loops.
	var (
		mu          sync.Mutex
		lastCursor  = opts.FromCursor // last emitted-OR-input cursor
		eventsSince int               // emitted since last CursorRecord
	)

	// emit serialises every JSON write on a single mutex. fsnotify makes
	// no single-goroutine guarantee and we may grow concurrent emitters
	// later; the cursor-tick goroutine also takes this mutex.
	enc := json.NewEncoder(w)
	emitEvent := func(ev Event, csTS string) {
		mu.Lock()
		defer mu.Unlock()
		if err := enc.Encode(ev); err != nil {
			fmt.Fprintf(os.Stderr, "stream encode: %v\n", err)
			return
		}
		lastCursor = encodeCursor(csTS, ev.Path)
		eventsSince++
		if opts.CursorEveryNEvents > 0 && eventsSince >= opts.CursorEveryNEvents {
			emitCursorRecordLocked(enc, lastCursor, csTS)
			eventsSince = 0
		}
	}

	// Catch-up half — only if explicitly requested or a FromCursor was
	// supplied. Without the request, live tail starts immediately (legacy
	// behaviour).
	if opts.ReplayBeforeWatch || opts.FromCursor != "" {
		// Inline the catch-up walk so we can route every emit through the
		// SAME emit/cursor accounting — calling EmitCatchUpFrom would
		// bypass the count/cadence machinery.
		curTS, curPath, err := decodeCursor(opts.FromCursor)
		if err != nil {
			return err
		}
		type keyed struct {
			ev   Event
			csTS string
		}
		var evs []keyed
		catchUpCache := newMetaCache()
		for _, d := range dirs {
			absDir := filepath.Join(root, d)
			info, statErr := os.Stat(absDir)
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			if statErr != nil {
				return statErr
			}
			if !info.IsDir() {
				continue
			}
			walkErr := filepath.WalkDir(absDir, func(path string, de fs.DirEntry, werr error) error {
				if werr != nil {
					return werr
				}
				if de.IsDir() {
					return nil
				}
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}
				relPosix := filepath.ToSlash(rel)
				fe, ferr := FileToEvents(path, relPosix)
				if ferr != nil {
					// Catch-up leniency: a single malformed file mustn't
					// stop a long-lived SDK reconnect — log + continue.
					fmt.Fprintf(os.Stderr, "stream catch-up parse %s: %v\n", path, ferr)
					return nil
				}
				for _, e := range fe {
					if !passesAll(root, e, p, catchUpCache) {
						continue
					}
					csTS := versioning.CanonicalTS(e.TS)
					if hasNUL(csTS) || hasNUL(e.Path) {
						continue
					}
					if opts.FromCursor != "" && !less(curTS, curPath, csTS, e.Path) {
						continue
					}
					evs = append(evs, keyed{ev: e, csTS: csTS})
				}
				return nil
			})
			if walkErr != nil {
				return walkErr
			}
		}
		sort.Slice(evs, func(i, j int) bool {
			return less(evs[i].csTS, evs[i].ev.Path, evs[j].csTS, evs[j].ev.Path)
		})
		for _, k := range evs {
			emitEvent(k.ev, k.csTS)
		}
		// L1 (R26 short-pipeline gap): emit a final cursor record at
		// the END of the catch-up replay, BEFORE the live watch engages.
		// Without this, low-event substrates wait up to 30s (the periodic
		// CursorEveryD floor) before any cursor appears — breaking the
		// `listen --catch-up | head -N | jq` shape SDK reconnect uses.
		// The emitted cursor is `lastCursor` (which emitEvent already
		// advanced to point at the last replayed event), or the input
		// FromCursor when zero events replayed. Same format as the
		// periodic tick so consumers can route on a single shape.
		mu.Lock()
		// Derive the canonical-TS hint from lastCursor if non-empty, or
		// pass through empty when no events replayed and FromCursor was
		// itself empty — the consumer can still pass back the resume
		// token even when it's the from-epoch sentinel.
		hintTS, _, _ := decodeCursor(lastCursor)
		emitCursorRecordLocked(enc, lastCursor, hintTS)
		eventsSince = 0
		mu.Unlock()
	}

	// Periodic cursor ticker — runs only if CursorEveryD > 0. The ticker
	// goroutine takes the same mutex so cursor lines never interleave
	// with event lines.
	tickerCtx, cancelTicker := context.WithCancel(ctx)
	defer cancelTicker()
	if opts.CursorEveryD > 0 {
		go func() {
			t := time.NewTicker(opts.CursorEveryD)
			defer t.Stop()
			for {
				select {
				case <-tickerCtx.Done():
					return
				case <-t.C:
					mu.Lock()
					if lastCursor != "" {
						// Best-effort: derive the canonical TS for the
						// hint field by decoding the cursor.
						hintTS, _, _ := decodeCursor(lastCursor)
						emitCursorRecordLocked(enc, lastCursor, hintTS)
						eventsSince = 0
					}
					mu.Unlock()
				}
			}
		}()
	}

	// Live watch — register each dir + subdirs (fsnotify has no native
	// recursive watch). Missing dirs are skipped.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	for _, d := range dirs {
		absDir := filepath.Join(root, d)
		if _, statErr := os.Stat(absDir); errors.Is(statErr, os.ErrNotExist) {
			continue
		} else if statErr != nil {
			return statErr
		}
		if err := addRecursive(watcher, absDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	// Channel-meta cache shared across the live watcher loop. One
	// disk read per channel, regardless of how many messages stream
	// in. Created fresh here (not shared with the catch-up cache)
	// because the catch-up walk completed before this loop starts —
	// the live cache picks up any newly-opened channels.
	liveCache := newMetaCache()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if ev.Op&fsnotify.Create == 0 {
				continue
			}
			info, statErr := os.Stat(ev.Name)
			if statErr != nil {
				continue
			}
			if info.IsDir() {
				if addErr := addRecursive(watcher, ev.Name); addErr != nil {
					fmt.Fprintf(os.Stderr, "stream watch %s: %v\n", ev.Name, addErr)
				}
				// Mirror WatchAndEmit's WK2-ROUTE-1 race-window replay,
				// routed through the same emitEvent so the cursor stays
				// monotonic.
				replayDirInto(root, ev.Name, p, emitEvent, liveCache)
				continue
			}
			rel, relErr := filepath.Rel(root, ev.Name)
			if relErr != nil {
				fmt.Fprintf(os.Stderr, "stream rel %s: %v\n", ev.Name, relErr)
				continue
			}
			relPosix := filepath.ToSlash(rel)
			evs, parseErr := FileToEvents(ev.Name, relPosix)
			if parseErr != nil {
				fmt.Fprintf(os.Stderr, "stream parse %s: %v\n", ev.Name, parseErr)
				continue
			}
			for _, e := range evs {
				if !passesAll(root, e, p, liveCache) {
					continue
				}
				csTS := versioning.CanonicalTS(e.TS)
				if hasNUL(csTS) || hasNUL(e.Path) {
					continue
				}
				emitEvent(e, csTS)
			}
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "stream watcher error: %v\n", watchErr)
		}
	}
}

// emitCursorRecordLocked writes one {"_type":"cursor",...} line to enc.
// Callers must hold the emit mutex. Errors are logged to stderr and
// otherwise swallowed — a cursor-emit failure must not break the live
// event stream.
func emitCursorRecordLocked(enc *json.Encoder, value, ts string) {
	rec := CursorRecord{Type: "cursor", Value: value, TS: ts}
	if err := enc.Encode(rec); err != nil {
		fmt.Fprintf(os.Stderr, "stream cursor emit: %v\n", err)
	}
}

// hasNUL is a one-line helper kept out of the inline branch to keep the
// catch-up loop readable. Returns true iff s contains a literal NUL.
func hasNUL(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

// replayDirInto walks a newly-created dir and emits matching events
// through emit. Mirrors stream.go's replayDir but routes via the
// cursor-aware emit closure so the replayed events advance the cursor.
// cache is the live watcher's shared channel-meta cache — re-using it
// here means a burst of messages in the same channel doesn't pay
// channels.LoadMeta once per file.
func replayDirInto(root, dir string, p FilterParams, emit func(Event, string), cache *metaCache) {
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relPosix := filepath.ToSlash(rel)
		evs, parseErr := FileToEvents(path, relPosix)
		if parseErr != nil {
			fmt.Fprintf(os.Stderr, "stream replay parse %s: %v\n", path, parseErr)
			return nil
		}
		for _, e := range evs {
			if !passesAll(root, e, p, cache) {
				continue
			}
			csTS := versioning.CanonicalTS(e.TS)
			if hasNUL(csTS) || hasNUL(e.Path) {
				continue
			}
			emit(e, csTS)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream replay walk %s: %v\n", dir, err)
	}
}
