// Package ttlsweep is the daemon engine that moves expired @thought
// records out of live/outbox/ and live/inbox/ into live/expired/, preserving
// an audit trail per D14.
//
// Decision (pure): a record is expired when now > ts + ttl AND ttl > 0
// (D5.1 — ttl=0 means never expire). Each sweep tick scans
// live/outbox/*/*.gdl and live/inbox/*/*.gdl, parses the @thought record,
// and returns the candidates as ExpiredFile values. The Move helper then
// atomically renames each source into live/expired/<agent>/<id>.gdl.
//
// Concurrency model (D14.6): the engine runs on a single goroutine in
// the daemon. Inside one sweep tick, two source files (outbox + inbox
// copies of the same id) can both land in the same destination — Move
// returns *AlreadyExpiredError without overwriting; the caller (Sweep)
// logs and continues so the audit trail keeps the first-moved copy.
//
// Clock injection (D14.11): now is a function so tests pin a fixed time
// without monkey-patching time.Now.
package ttlsweep

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// TickInterval is the periodic sweep cadence the daemon runs at (D14.1).
const TickInterval = 10 * time.Second

// ExpiredFile is one expired record ready to move.
type ExpiredFile struct {
	// SourcePath is the absolute path to the file holding the @thought
	// record. One of:
	//   <root>/live/outbox/<agent>/<id>.gdl
	//   <root>/live/inbox/<agent>/<id>.gdl
	SourcePath string
	// Agent is the owning agent (the parent directory name of SourcePath).
	Agent string
	// ID is the thought id (filename minus .gdl).
	ID string
}

// FindExpired scans live/outbox/*/*.gdl + live/inbox/*/*.gdl, parses each
// @thought record, and returns ExpiredFile entries where now > ts + ttl
// AND ttl > 0. Files without an @thought record (e.g., future
// channel-message files) and records with ttl == 0 (D5.1) are skipped.
//
// Per-file parse errors are logged to stderr and the file is skipped so
// one malformed record cannot abort the tick (D14 best-effort).
func FindExpired(root string, now func() time.Time) ([]ExpiredFile, error) {
	t := now()
	var out []ExpiredFile
	for _, sub := range []string{"outbox", "inbox"} {
		pattern := filepath.Join(root, "live", sub, "*", "*.gdl")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, p := range matches {
			rec, ok := readThought(p)
			if !ok {
				continue
			}
			ts, ttl := ParseThoughtTTL(rec)
			if !IsExpired(ts, ttl, t) {
				continue
			}
			out = append(out, ExpiredFile{
				SourcePath: p,
				Agent:      filepath.Base(filepath.Dir(p)),
				ID:         trimExt(filepath.Base(p)),
			})
		}
	}
	return out, nil
}

// ParseThoughtTTL returns (ts, ttl) from a @thought record. Returns
// (zero, 0) if the record has no ts field, no ttl field, or ttl == "0".
// Returns (zero, 0) on parse errors so callers can short-circuit cleanly.
func ParseThoughtTTL(record gdl.Record) (time.Time, time.Duration) {
	tsRaw := record.Get("ts")
	ttlRaw := record.Get("ttl")
	if tsRaw == "" || ttlRaw == "" {
		return time.Time{}, 0
	}
	ttlSec, err := strconv.Atoi(ttlRaw)
	if err != nil {
		return time.Time{}, 0
	}
	if ttlSec == 0 {
		// D5.1: ttl=0 is "never expire". Surface (zero, 0) so IsExpired
		// trivially returns false and callers don't depend on the ts.
		return time.Time{}, 0
	}
	ts, err := time.Parse(time.RFC3339Nano, tsRaw)
	if err != nil {
		return time.Time{}, 0
	}
	return ts, time.Duration(ttlSec) * time.Second
}

// IsExpired returns true when now > ts + ttl AND ttl > 0. ttl <= 0 is
// always treated as "never expires" (D5.1 + defensive).
func IsExpired(ts time.Time, ttl time.Duration, now time.Time) bool {
	if ttl <= 0 {
		return false
	}
	return now.After(ts.Add(ttl))
}

// Move moves src to live/expired/<agent>/<id>.gdl. The parent directory
// is created if needed. If the destination already exists (D14.6), Move
// returns *AlreadyExpiredError without overwriting; the source file is
// left in place so no data is lost. Callers log and skip.
//
// Implementation: atomic via link(2) + unlink(2). link(2) is required
// by POSIX to fail with EEXIST if the destination exists, so the
// AlreadyExpiredError guard is a real syscall guarantee rather than a
// Stat hint vulnerable to TOCTOU races (a Stat-then-Rename pair would
// silently overwrite on Linux when a concurrent writer wins the race).
// This requires the rufio project root to live on a single filesystem
// (no cross-fs hardlink); that is already a project invariant — every
// other writer in the substrate uses .tmp + os.Rename for the same
// reason (see observation.Write, thought writers, etc.).
func Move(root string, file ExpiredFile) error {
	dstDir := filepath.Join(root, "live", "expired", file.Agent)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dstDir, file.ID+".gdl")
	if err := os.Link(file.SourcePath, dst); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return &rufioerr.AlreadyExpiredError{Agent: file.Agent, ID: file.ID}
		}
		return err
	}
	return os.Remove(file.SourcePath)
}

// Sweep is the top-level engine entry: FindExpired + Move for each entry.
// Returns the count of files actually moved.
//
// Per-file errors are written to logW (typically os.Stderr from the
// daemon; tests pass a bytes.Buffer to assert log content). If logW is
// nil, log output is discarded. The sweep does not abort on a single
// Move failure. The aggregate error is nil unless the initial scan
// itself fails (Glob/IO).
func Sweep(root string, now func() time.Time, logW io.Writer) (int, error) {
	if logW == nil {
		logW = io.Discard
	}
	expired, err := FindExpired(root, now)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, f := range expired {
		if err := Move(root, f); err != nil {
			fmt.Fprintf(logW, "ttlsweep: move %s/%s: %v\n", f.Agent, f.ID, err)
			continue
		}
		moved++
	}
	return moved, nil
}

// --- helpers (unexported) --------------------------------------------------

// readThought reads file p, parses it as a GDL document, and returns the
// first @thought record. Returns (zero, false) when:
//   - the file can't be read (transient — the file may have been moved
//     out from under us in a concurrent sweep cycle)
//   - the document has a parse error
//   - the document has no @thought record (e.g., future
//     channel-message-only files in live/outbox/)
//
// Read/parse errors are logged to stderr so operators can investigate
// without the sweep aborting.
func readThought(p string) (gdl.Record, bool) {
	bs, err := os.ReadFile(p)
	if err != nil {
		// File may have been removed between Glob and Read. Don't log
		// "not exist" — it's the expected race; log everything else.
		if !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "ttlsweep: read %s: %v\n", p, err)
		}
		return gdl.Record{}, false
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ttlsweep: parse %s: %v\n", p, err)
		return gdl.Record{}, false
	}
	for _, r := range records {
		if r.Type == "thought" {
			return r, true
		}
	}
	return gdl.Record{}, false
}

// trimExt drops the .gdl suffix from a basename. Defined locally rather
// than reaching for strings.TrimSuffix to keep the call site inline.
func trimExt(name string) string {
	const ext = ".gdl"
	if len(name) > len(ext) && name[len(name)-len(ext):] == ext {
		return name[:len(name)-len(ext)]
	}
	return name
}
