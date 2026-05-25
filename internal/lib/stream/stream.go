// Package stream is the shared watch+emit machinery for `rufio listen`
// (PR #12 Task 2) and `rufio stream` (PR #12 Task 3). It walks one or
// more subtrees under a project root, parses .gdl / .gdlm files into
// per-record Events, applies type+scope filters, and emits JSONL — one
// JSON object per record — to a writer.
//
// Two entry points: EmitCatchUp for the one-shot replay of existing
// files (used by `listen --catch-up`), and WatchAndEmit for the
// long-running fsnotify watcher loop (the default). Both share the same
// Event/FilterParams shape so a caller can sequence catch-up → watch
// without reformatting.
package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/privacy"
)

// Event is one emitted JSON line. Mirrors recall.RecallRecord field-
// for-field (minus Retracted, which is recall-only) so consumers that
// already parse `rufio recall --json` output keep the same shape. Raw
// carries the original GDL line for debugging / round-trip parity.
//
// v1.0.3 adds optional auto-promote enrichment fields (Version,
// PromotedID, SourceThoughtID, Confirmers, ConfirmCount, RefuteCount,
// Confidence). All omitempty — they only appear on `_type=auto-promote`
// events. Schema LOCKED at version 1; future enrichments bump _version
// rather than renaming fields. Consumers (TUI, third-party watchers)
// gate on _version to know what to read.
type Event struct {
	Type      string `json:"_type"`
	TS        string `json:"ts"`
	Author    string `json:"author,omitempty"`
	Subject   string `json:"subject,omitempty"`
	Predicate string `json:"predicate,omitempty"`
	Object    string `json:"object,omitempty"`
	Content   string `json:"content,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Path      string `json:"path"`
	Raw       string `json:"raw"`

	// Auto-promote enrichment (v1.0.3). Populated only when Type ==
	// "auto-promote". `_version` is 1 at first ship — bump (and add
	// fields) to evolve; never rename.
	Version         int      `json:"_version,omitempty"`
	PromotedID      string   `json:"promoted_id,omitempty"`
	SourceThoughtID string   `json:"source_thought_id,omitempty"`
	Confirmers      []string `json:"confirmers,omitempty"`
	ConfirmCount    int      `json:"confirm_count,omitempty"`
	RefuteCount     int      `json:"refute_count,omitempty"`
	Confidence      float64  `json:"confidence,omitempty"`
}

// GetScope / GetAuthor make Event satisfy privacy.Record so the
// privacy gate in Match below delegates to the shared predicate
// instead of reimplementing the scope:agent rule (#147).
func (e Event) GetScope() string  { return e.Scope }
func (e Event) GetAuthor() string { return e.Author }

// FilterParams is the subset of recall.FilterParams that applies to
// streaming. Since/AsOf don't apply — the stream is "now-forward"; the
// catch-up replay is bounded by what's on disk, not by time.
type FilterParams struct {
	Types        []string
	Scope        string
	CurrentAgent string
}

// Match returns true if ev passes every active filter. Empty FilterParams
// passes every event. The scope rule mirrors recall.scopePass: given
// records bypass scope (project-wide visibility), broader-than-filter
// scopes always pass, same-scope requires Author == CurrentAgent, tighter
// scopes are excluded.
//
// Privacy gate (#139 followup): when CurrentAgent is set AND no explicit
// --scope filter is in play, other agents' scope:agent records are still
// excluded. Without this, the broader catch-up walk added in #139 (which
// reaches live/outbox/<other>/ directly, not just the daemon-routed
// inbox) would leak every agent's private thoughts to every listener.
// The rule is opt-in by CurrentAgent presence so the firehose path
// (anonymous `rufio stream`, admin/test callers) keeps its existing
// semantic — emit everything when no caller identity is supplied.
// `given` records bypass this rule too (project-wide by design).
func Match(ev Event, p FilterParams) bool {
	// Type filter.
	if len(p.Types) > 0 {
		hit := false
		for _, t := range p.Types {
			if ev.Type == t {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	// Scope filter. `given` records bypass (project-wide).
	if p.Scope != "" && ev.Type != "given" {
		if !scopePass(ev, p.Scope, p.CurrentAgent) {
			return false
		}
	}
	// Privacy gate (#147). Delegates to privacy.IsVisible — the same
	// predicate every other read surface (goals list, recall, fleet)
	// now goes through. Only applies when no explicit scope filter was
	// set: when --scope=X is given, scopePass above already enforces
	// the same-author rule for equal-rank records, so layering this
	// on top would be redundant. given/ records bypass (project-wide).
	if p.Scope == "" && ev.Type != "given" {
		if !privacy.IsVisible(ev, p.CurrentAgent) {
			return false
		}
	}
	return true
}

// scopePass duplicates the rank-based scope rule from recall.scopePass.
// Kept local (10 lines) rather than cross-importing recall — avoids a
// cycle and keeps stream self-contained.
func scopePass(ev Event, filterScope, currentAgent string) bool {
	if ev.Scope == "" {
		return true
	}
	rank := func(s string) int {
		switch s {
		case "agent":
			return 0
		case "deployment":
			return 1
		case "fleet":
			return 2
		default:
			return -1
		}
	}
	rRank, fRank := rank(ev.Scope), rank(filterScope)
	if rRank < 0 || fRank < 0 {
		return false
	}
	if rRank > fRank {
		return true // broader than filter
	}
	if rRank == fRank {
		return ev.Author == currentAgent
	}
	return false
}

// FileToEvents parses absPath and returns one Event per record. Returns
// (nil, nil) for files whose extension is neither .gdl nor .gdlm (so a
// .tmp scratch file from another tool can't crash the stream). Errors
// from os.ReadFile / gdl.ParseDocument propagate so callers can decide
// whether to abort or log+continue. relPath is what gets stored in
// Event.Path (POSIX-form, project-root-relative).
func FileToEvents(absPath, relPath string) ([]Event, error) {
	ext := filepath.Ext(absPath)
	if ext != ".gdl" && ext != ".gdlm" {
		return nil, nil
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	records, err := gdl.ParseDocument(string(data))
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(records))
	for _, r := range records {
		ev := Event{
			Type:      r.Type,
			TS:        r.Get("ts"),
			Author:    r.Get("author"),
			Subject:   r.Get("subject"),
			Predicate: r.Get("predicate"),
			Object:    r.Get("object"),
			Content:   r.Get("content"),
			Scope:     r.Get("scope"),
			Path:      relPath,
			Raw:       gdl.RenderLine(r),
		}
		// v1.0.3 auto-promote enrichment. The on-disk record's `origin`
		// field is the original thought author — surface it as
		// Event.Author so privacy.IsVisible can correctly gate
		// scope=agent visibility (the `by:auto-promote` literal is the
		// daemon's identity, not the human-author identity the privacy
		// floor needs). Numeric/list fields are best-effort: a
		// malformed/missing value falls through as the zero value
		// rather than aborting the whole event — the wire is still
		// useful even when the enrichment is incomplete.
		if r.Type == "auto-promote" {
			ev.Author = r.Get("origin")
			ev.Version = parseAutoPromoteInt(r.Get("version"))
			ev.SourceThoughtID = r.Get("thought")
			ev.PromotedID = r.Get("observation")
			if csv := r.Get("confirmers"); csv != "" {
				ev.Confirmers = strings.Split(csv, ",")
			}
			ev.ConfirmCount = parseAutoPromoteInt(r.Get("confirm-count"))
			ev.RefuteCount = parseAutoPromoteInt(r.Get("refute-count"))
			ev.Confidence = parseAutoPromoteFloat(r.Get("confidence"))
		}
		out = append(out, ev)
	}
	return out, nil
}

// parseAutoPromoteInt is the best-effort integer parser for the
// auto-promote enrichment fields. A malformed/missing value falls
// through to zero rather than aborting the event — the rest of the
// stream is still useful when one optional field is corrupted.
func parseAutoPromoteInt(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// parseAutoPromoteFloat mirrors parseAutoPromoteInt for the confidence
// field. Best-effort: malformed → 0.0, NaN suppressed.
func parseAutoPromoteFloat(s string) float64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

// EmitCatchUp walks each dir under root recursively and writes matching
// events to w as JSONL. dirs are POSIX-form paths relative to root.
// Missing dirs are silently skipped (graceful for live/promoted/ which
// doesn't exist until PR #14). Per-file parse failures abort the walk —
// catch-up is a one-shot replay so a malformed file should surface, not
// be silently dropped. Best-effort failure handling is the watcher's
// job, not catch-up's.
func EmitCatchUp(w io.Writer, root string, dirs []string, p FilterParams) error {
	enc := json.NewEncoder(w)
	cache := newMetaCache()
	for _, d := range dirs {
		absDir := filepath.Join(root, d)
		info, err := os.Stat(absDir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			continue
		}
		walkErr := filepath.WalkDir(absDir, func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if de.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relPosix := filepath.ToSlash(rel)
			evs, err := FileToEvents(path, relPosix)
			if err != nil {
				return err
			}
			for _, ev := range evs {
				if !passesAll(root, ev, p, cache) {
					continue
				}
				if err := enc.Encode(ev); err != nil {
					return err
				}
			}
			return nil
		})
		if walkErr != nil {
			return walkErr
		}
	}
	return nil
}

// WatchAndEmit registers an fsnotify watcher on each dir under root and
// emits matching events as files are created. Blocks until ctx is
// cancelled or a fatal watcher-close occurs.
//
// Per-file parse failures are logged to stderr and the loop continues —
// a single malformed file shouldn't kill the stream. On dir-create
// events we add the new subdir to the watcher AND replay any files that
// landed in the race window between MkdirAll and watcher.Add (the
// WK2-ROUTE-1 fix from dev.go, materially relevant on macOS kqueue).
//
// JSON encoding goes through a mutex because nothing in fsnotify
// guarantees single-goroutine event delivery and we may grow concurrent
// emitters later. Cheap insurance.
func WatchAndEmit(ctx context.Context, w io.Writer, root string, dirs []string, p FilterParams) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Register each dir + any existing subdirs. fsnotify has no native
	// recursive watch. Missing dirs are skipped (live/promoted/ may not
	// exist in fresh projects).
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

	var mu sync.Mutex
	emit := func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		if err := json.NewEncoder(w).Encode(ev); err != nil {
			fmt.Fprintf(os.Stderr, "stream encode: %v\n", err)
		}
	}

	// Channel-meta cache shared across the watcher loop. A channel
	// with hundreds of messages doesn't pay one disk read per event.
	cache := newMetaCache()

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
				// File may have been removed between create and stat —
				// nothing to emit. Don't log; this is normal under churn.
				continue
			}
			if info.IsDir() {
				// New dir: add to watcher + replay files that may have
				// landed in the race window (WK2-ROUTE-1).
				if addErr := addRecursive(watcher, ev.Name); addErr != nil {
					fmt.Fprintf(os.Stderr, "stream watch %s: %v\n", ev.Name, addErr)
				}
				replayDir(root, ev.Name, p, emit, cache)
				continue
			}
			// Regular file create: parse + filter + emit.
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
				if passesAll(root, e, p, cache) {
					emit(e)
				}
			}
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "stream watcher error: %v\n", watchErr)
		}
	}
}

// addRecursive walks dir and adds it + every subdirectory to the
// watcher. Mirrors cli.addRecursive in dev.go; duplicated here to keep
// stream self-contained (cli/ can't be imported from internal/lib/).
func addRecursive(watcher *fsnotify.Watcher, dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})
}

// replayDir walks a newly-created dir and emits matching events for any
// files that landed in the race window between the dir-create event and
// the watcher.Add call. Errors logged to stderr; best-effort by design.
// cache is the shared meta cache from the watcher loop — re-using it
// here keeps channel-membership lookups O(1) when many messages land in
// the same channel during a single dir-replay burst.
func replayDir(root, dir string, p FilterParams, emit func(Event), cache *metaCache) {
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
			if passesAll(root, e, p, cache) {
				emit(e)
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream replay walk %s: %v\n", dir, err)
	}
}
