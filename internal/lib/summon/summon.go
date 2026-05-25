// Package summon implements the write side of `rufio summon` plus the
// state-machine moves for `rufio accept` and `rufio decline`.
//
// A summon is a one-shot request from "from" (the opener) to "to" (the
// target) to open a private channel. State transitions are
// pending → accepted | declined | expired. The on-disk file at
// live/summons/<state>/<summon-id>.gdl contains the original @summon
// record always; accepted/declined files additionally carry the
// @accept|@decline audit record appended on the move.
//
// Lock domains (D15.6): state transitions go through
// .rufio/locks/summon-<id>.lock so accept and decline are mutually
// exclusive even across processes. The atomic write itself uses
// link(2)+unlink(2) (matching ttlsweep.Move) so the destination is
// guaranteed to be created via a real-syscall existence check —
// Stat+Rename would silently overwrite on Linux when a racing writer
// wins.
package summon

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/fslock"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// DefaultTTL is the v1 summon TTL — 24 hours, expressed in seconds
// (D15.2). Hardcoded; config-driven in v1.1.
const DefaultTTL = 24 * 60 * 60

// State is the on-disk state directory name for a summon.
type State string

const (
	StatePending  State = "pending"
	StateAccepted State = "accepted"
	StateDeclined State = "declined"
	StateExpired  State = "expired"
)

// allStates is the directory-traversal order used by LoadAnyState and
// ReadAll, and also the sort precedence used by ReadAll.
var allStates = []State{StatePending, StateAccepted, StateDeclined, StateExpired}

// topicRegex enforces D15.17's grammar — single segment (free topic) OR
// colon-separated entity form. The brief overrides the plan's wider
// `[a-z0-9]` lead because TestValidateTopic_Malformed_LeadingDigit
// requires leading-digit topics to fail.
var topicRegex = regexp.MustCompile(`^[a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)*$`)

// Summon is the parsed @summon record plus its on-disk state and any
// audit metadata projected onto the row (#140).
//
// Channel is populated from the @accept record's channel field when
// State==StateAccepted; empty for every other state.
// DeclineReason is populated from the @decline record's reason field
// when State==StateDeclined; empty for every other state. The join
// happens inside LoadAnyState / ReadAll so a single read produces the
// full row — callers don't have to crack the audit-trail records
// themselves to wire `summons list` → channel id (#140).
type Summon struct {
	ID            string
	From          string
	To            string
	Topic         string
	Intent        string
	TS            string
	TTL           int
	State         State
	Channel       string
	DeclineReason string
}

// ValidateTopic enforces D15.17: free-topic single segment
// (e.g. `churn-strategy`) OR colon-separated entity form
// (e.g. `customer:5821`). Empty → *InvalidTopicError{Topic:""}.
func ValidateTopic(topic string) error {
	if topic == "" {
		return &rufioerr.InvalidTopicError{}
	}
	if !topicRegex.MatchString(topic) {
		return &rufioerr.InvalidTopicError{Topic: topic}
	}
	return nil
}

// ValidateIntent returns *InvalidContentError{Field:"intent"} when the
// trimmed value is empty.
func ValidateIntent(intent string) error {
	if strings.TrimSpace(intent) == "" {
		return &rufioerr.InvalidContentError{Field: "intent"}
	}
	return nil
}

// GenerateID returns a new summon-id of the form <unix-millis>-<rand6>.
// Mirrors thought.GenerateID — same format AND alphabet so the daemon's
// generic id-shaped parsers handle thoughts and summons uniformly.
func GenerateID() (string, error) {
	return generateIDFromSource(
		func() int64 { return time.Now().UnixMilli() },
		rand.Reader,
	)
}

// generateIDFromSource is the testable variant. Production callers use
// GenerateID. Mirrored verbatim from thought.generateIDFromSource so the
// id alphabets stay in lock-step.
func generateIDFromSource(now func() int64, src io.Reader) (string, error) {
	buf := make([]byte, 6)
	n, err := io.ReadFull(src, buf)
	if err != nil || n != 6 {
		return "", fmt.Errorf("summon: rand source read %d/6 bytes: %w", n, err)
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 6)
	for i, b := range buf {
		out[i] = alphabet[int(b)%36]
	}
	return fmt.Sprintf("%d-%s", now(), out), nil
}

// BuildSummonRecord returns the @summon gdl.Record. Field order locked
// at id, from, to, topic, intent, ts, ttl (D15.2).
func BuildSummonRecord(id, from, to, topic, intent, ts string, ttl int) gdl.Record {
	return gdl.Record{Type: "summon", Fields: []gdl.RecordField{
		{Key: "id", Value: id},
		{Key: "from", Value: from},
		{Key: "to", Value: to},
		{Key: "topic", Value: topic},
		{Key: "intent", Value: intent},
		{Key: "ts", Value: ts},
		{Key: "ttl", Value: strconv.Itoa(ttl)},
	}}
}

// BuildAcceptRecord returns the @accept gdl.Record per D15.4. Field order
// locked at id, by, channel, ts.
func BuildAcceptRecord(summonID, by, channelID, ts string) gdl.Record {
	return gdl.Record{Type: "accept", Fields: []gdl.RecordField{
		{Key: "id", Value: summonID},
		{Key: "by", Value: by},
		{Key: "channel", Value: channelID},
		{Key: "ts", Value: ts},
	}}
}

// BuildDeclineRecord returns the @decline gdl.Record per D15.5. Field
// order locked at id, by, reason, ts.
func BuildDeclineRecord(summonID, by, reason, ts string) gdl.Record {
	return gdl.Record{Type: "decline", Fields: []gdl.RecordField{
		{Key: "id", Value: summonID},
		{Key: "by", Value: by},
		{Key: "reason", Value: reason},
		{Key: "ts", Value: ts},
	}}
}

// WritePending atomically writes a single-record file to
// live/summons/pending/<summon-id>.gdl via .tmp + os.Rename. Creates
// parent dir. No lock (D15.3) — fresh summon-id per call.
func WritePending(root, id string, record gdl.Record) error {
	dir := filepath.Join(root, "live", "summons", string(StatePending))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, id+".gdl")
	tmp := target + ".tmp"
	// Best-effort cleanup so a failed WriteFile/Rename never strands
	// <id>.gdl.tmp under live/summons/pending/ (#141). Success path:
	// Rename already moved tmp, so this Remove is a harmless no-op.
	// Scope: this is ONLY the rename-based WritePending path; the
	// link(2)-based moveTo path keeps its own explicit tmp cleanup.
	defer func() { _ = os.Remove(tmp) }()
	contents := gdl.RenderLine(record) + "\n"
	if err := os.WriteFile(tmp, []byte(contents), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// shortIDRegex matches a 6-char [a-z0-9] suffix — the form `summons
// list` renders in text mode (output.FormatID → output.ShortID). R29a
// accepts the suffix on the write side (accept/decline) so the IDs the
// agent SEES are the IDs they can PASTE.
var shortIDRegex = regexp.MustCompile(`^[a-z0-9]{6}$`)

// LoadAnyState scans live/summons/{pending,accepted,declined,expired}/
// for <summon-id>.gdl. Returns the loaded Summon with State derived from
// the source directory. Returns *NoSuchSummonError when no matching file
// exists in any of the four state directories.
//
// The first @summon record in the file populates the struct; any
// @accept and @decline records in the file project onto Channel and
// DeclineReason respectively (#140).
//
// R29a: a 6-char [a-z0-9]{6} value is interpreted as a suffix match and
// resolved across all four state directories. Multiple matches surface
// *AmbiguousIDError listing canonical ids — accept/decline can't pick a
// random summon. Full canonical ids take the legacy fast path (one
// stat per state dir).
func LoadAnyState(root, summonID string) (Summon, error) {
	if shortIDRegex.MatchString(summonID) {
		canonical, err := resolveSuffix(root, summonID)
		if err != nil {
			return Summon{}, err
		}
		summonID = canonical
	}
	for _, state := range allStates {
		path := filepath.Join(root, "live", "summons", string(state), summonID+".gdl")
		bs, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return Summon{}, err
		}
		records, err := gdl.ParseDocument(string(bs))
		if err != nil {
			return Summon{}, fmt.Errorf("summon: parse %s: %w", path, err)
		}
		s, ok := summonFromRecords(records, state)
		if !ok {
			// File exists but no @summon record — treat as missing.
			return Summon{}, &rufioerr.NoSuchSummonError{ID: summonID}
		}
		return s, nil
	}
	return Summon{}, &rufioerr.NoSuchSummonError{ID: summonID}
}

// resolveSuffix maps a 6-char suffix to the canonical summon-id. Walks
// each state dir for *-<suffix>.gdl filenames. Ambiguity surfaces as
// *AmbiguousIDError; zero matches as *NoSuchSummonError. Privacy isn't
// applied here — summons are addressed point-to-point (from/to) and the
// authz check downstream (SummonAuthError) catches a non-target trying
// to act on a resolved id.
func resolveSuffix(root, suffix string) (string, error) {
	var ids []string
	seen := make(map[string]bool)
	for _, state := range allStates {
		pattern := filepath.Join(root, "live", "summons", string(state), "*-"+suffix+".gdl")
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
			ids = append(ids, id)
		}
	}
	switch len(ids) {
	case 0:
		return "", &rufioerr.NoSuchSummonError{ID: suffix}
	case 1:
		return ids[0], nil
	default:
		sort.Strings(ids)
		rows := make([]rufioerr.AmbiguousCandidate, 0, len(ids))
		for _, id := range ids {
			rows = append(rows, rufioerr.AmbiguousCandidate{ID: id, Type: "summon"})
		}
		return "", &rufioerr.AmbiguousIDError{Short: suffix, Candidates: rows}
	}
}

// summonFromRecords scans the full record list for the file and returns
// a Summon. The first @summon record populates the header; any
// trailing @accept / @decline records project their join fields onto
// the row (#140). Returns (zero, false) if no @summon record is found.
//
// TTL parse failures fall back to zero — the record's other fields are
// still useful for display.
func summonFromRecords(records []gdl.Record, state State) (Summon, bool) {
	var s Summon
	var foundHead bool
	for _, r := range records {
		switch r.Type {
		case "summon":
			if foundHead {
				continue // ignore stray second @summon records
			}
			ttl, _ := strconv.Atoi(r.Get("ttl"))
			s = Summon{
				ID:     r.Get("id"),
				From:   r.Get("from"),
				To:     r.Get("to"),
				Topic:  r.Get("topic"),
				Intent: r.Get("intent"),
				TS:     r.Get("ts"),
				TTL:    ttl,
				State:  state,
			}
			foundHead = true
		case "accept":
			s.Channel = r.Get("channel")
		case "decline":
			s.DeclineReason = r.Get("reason")
		}
	}
	return s, foundHead
}

// MoveToAccepted moves live/summons/pending/<id>.gdl to
// live/summons/accepted/<id>.gdl AND appends the @accept record to the
// destination file. Atomic under .rufio/locks/summon-<id>.lock (D15.6).
//
// Returns *NoSuchSummonError when the pending file is gone (race with a
// concurrent accept/decline, or already-handled).
//
// Implementation uses link(2)+unlink(2) for the destination create —
// link(2) fails with EEXIST if the destination already exists, which
// promotes the race to *NoSuchSummonError instead of silently
// overwriting a prior accept/decline. See ttlsweep.Move for the same
// pattern.
func MoveToAccepted(root, summonID, by, channelID, ts string) error {
	acceptRec := BuildAcceptRecord(summonID, by, channelID, ts)
	return moveTo(root, summonID, StateAccepted, acceptRec)
}

// MoveToDeclined moves live/summons/pending/<id>.gdl to
// live/summons/declined/<id>.gdl AND appends the @decline record.
// Atomicity guarantees match MoveToAccepted.
func MoveToDeclined(root, summonID, by, reason, ts string) error {
	declineRec := BuildDeclineRecord(summonID, by, reason, ts)
	return moveTo(root, summonID, StateDeclined, declineRec)
}

// moveTo is the shared transition helper for accept/decline. Holds the
// per-summon lock for the entire read-original + write-new + remove-old
// sequence so two callers can't both win.
//
// On-disk steps inside the lock:
//  1. Read live/summons/pending/<id>.gdl. Missing → *NoSuchSummonError.
//  2. Write live/summons/<dest>/<id>.gdl.tmp with original + audit record.
//  3. os.Link(tmp, dest). Fails with EEXIST → *NoSuchSummonError.
//  4. os.Remove(tmp).
//  5. os.Remove(pending/<id>.gdl).
//
// On crash between steps 3 and 5: dest exists, pending exists, dest
// authoritative — the next caller hits step 1 (read pending) then step 3
// (link fails EEXIST) and surfaces *NoSuchSummonError. Step 5 itself is
// idempotent on re-run (the file is gone from a prior successful
// transition). The strategy is "dest-presence wins" which matches
// ttlsweep's audit-trail discipline.
func moveTo(root, summonID string, dest State, auditRecord gdl.Record) error {
	pendingPath := filepath.Join(root, "live", "summons", string(StatePending), summonID+".gdl")
	destDir := filepath.Join(root, "live", "summons", string(dest))
	destPath := filepath.Join(destDir, summonID+".gdl")
	lockDir := filepath.Join(root, ".rufio", "locks", "summon-"+summonID+".lock")

	_, err := fslock.WithLock(lockDir, 0, func() (struct{}, error) {
		var zero struct{}

		// 1. Read pending.
		origBytes, err := os.ReadFile(pendingPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return zero, &rufioerr.NoSuchSummonError{ID: summonID}
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
		// transition already wrote the dest — surface as NoSuchSummonError.
		if err := os.Link(tmp, destPath); err != nil {
			// Best-effort cleanup of the orphan tmp; the real error is the
			// EEXIST. Subsequent re-runs replace the tmp.
			_ = os.Remove(tmp)
			if errors.Is(err, fs.ErrExist) {
				return zero, &rufioerr.NoSuchSummonError{ID: summonID}
			}
			return zero, err
		}

		// 5. Best-effort cleanup of the link source.
		if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return zero, err
		}

		// 6. Remove the pending file. Re-run safety: ErrNotExist is benign.
		if err := os.Remove(pendingPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return zero, err
		}
		return zero, nil
	})
	return err
}

// SweepExpired scans live/summons/pending/*.gdl, parses each @summon
// record, and atomically moves files whose now > ts + (ttl seconds) to
// live/summons/expired/<summon-id>.gdl. The move location itself IS the
// audit trail per D16.10 — the original @summon record is preserved
// verbatim and no @expire record is appended.
//
// Atomicity matches MoveToAccepted/MoveToDeclined: os.Link(src, dst) +
// os.Remove(src) under the per-summon .rufio/locks/summon-<id>.lock so
// a concurrent accept/decline cannot race us into a double-move.
// Inside the lock we re-Stat the pending source — if it's gone, the
// accept/decline path won the race and we skip-and-continue rather than
// surfacing an error (D16.10).
//
// Per-file failures (parse error, ts/ttl parse error, link failure)
// are logged to logW and the sweep continues. The aggregate error is
// nil unless the initial directory scan itself fails. Idempotent:
// re-running after all expirable summons have been processed returns
// (0, nil). A missing live/summons/pending dir is not an error —
// fresh projects haven't seen a summon yet — and returns (0, nil).
//
// now is injected per ttlsweep.Sweep's pattern so tests can pin a
// fixed time. logW nil → io.Discard.
//
// Per D16.11 the daemon calls SweepExpired on its 10s ticker and on
// startup catch-up; the dev.go wiring lives in a separate task.
func SweepExpired(root string, now func() time.Time, logW io.Writer) (int, error) {
	if logW == nil {
		logW = io.Discard
	}
	pendingDir := filepath.Join(root, "live", "summons", string(StatePending))
	entries, err := os.ReadDir(pendingDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}

	t := now()
	moved := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
			continue
		}
		summonID := strings.TrimSuffix(e.Name(), ".gdl")
		src := filepath.Join(pendingDir, e.Name())

		ok, err := sweepOne(root, src, summonID, t, logW)
		if err != nil {
			// Lock acquisition failure is the only path that surfaces
			// here. Log and continue rather than aborting the whole sweep.
			fmt.Fprintf(logW, "summon: sweep lock %s: %v\n", summonID, err)
			continue
		}
		if ok {
			moved++
		}
	}
	return moved, nil
}

// sweepOne evaluates and (if expired) moves a single pending summon
// under the per-summon lock. Returns (true, nil) when the file was
// moved, (false, nil) for any non-fatal skip (not yet expired, ttl=0,
// malformed, already moved by a racing accept/decline), and
// (false, err) only when the lock helper itself fails.
func sweepOne(root, src, summonID string, now time.Time, logW io.Writer) (bool, error) {
	lockDir := filepath.Join(root, ".rufio", "locks", "summon-"+summonID+".lock")
	return fslock.WithLock(lockDir, 0, func() (bool, error) {
		// Re-stat under the lock — accept/decline may have removed the
		// pending file between ReadDir and now. Treat as "won by another
		// path" and skip silently (no log: races are expected, D16.10).
		bs, err := os.ReadFile(src)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return false, nil
			}
			fmt.Fprintf(logW, "summon: sweep read %s: %v\n", summonID, err)
			return false, nil
		}

		records, err := gdl.ParseDocument(string(bs))
		if err != nil {
			fmt.Fprintf(logW, "summon: sweep parse %s: %v\n", summonID, err)
			return false, nil
		}
		var rec gdl.Record
		var foundSummon bool
		for _, r := range records {
			if r.Type == "summon" {
				rec = r
				foundSummon = true
				break
			}
		}
		if !foundSummon {
			fmt.Fprintf(logW, "summon: sweep skip %s — no @summon record\n", summonID)
			return false, nil
		}

		tsRaw := rec.Get("ts")
		ts, err := time.Parse(time.RFC3339Nano, tsRaw)
		if err != nil {
			fmt.Fprintf(logW, "summon: sweep skip %s — malformed ts %q: %v\n", summonID, tsRaw, err)
			return false, nil
		}
		ttlRaw := rec.Get("ttl")
		ttlSec, err := strconv.Atoi(ttlRaw)
		if err != nil {
			fmt.Fprintf(logW, "summon: sweep skip %s — malformed ttl %q: %v\n", summonID, ttlRaw, err)
			return false, nil
		}
		if ttlSec == 0 {
			// Defensive: ttl=0 means "never expires" (parallel to D5.1
			// for thoughts). Current summons always carry ttl=86400.
			return false, nil
		}
		expiry := ts.Add(time.Duration(ttlSec) * time.Second)
		if !now.After(expiry) {
			return false, nil
		}

		// Move pending → expired. Original @summon record is preserved
		// verbatim — no @expire record is appended (D16.10: move is
		// the audit). Same link(2)+unlink(2) discipline as moveTo so a
		// crash between Link and Remove leaves the dest authoritative.
		destDir := filepath.Join(root, "live", "summons", string(StateExpired))
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			fmt.Fprintf(logW, "summon: sweep mkdir %s: %v\n", destDir, err)
			return false, nil
		}
		dst := filepath.Join(destDir, summonID+".gdl")
		if err := os.Link(src, dst); err != nil {
			if errors.Is(err, fs.ErrExist) {
				// Pre-existing expired file (rare crash-recovery case);
				// remove the pending source so the next tick doesn't
				// re-evaluate the same file forever.
				fmt.Fprintf(logW, "summon: sweep skip %s — already in expired/\n", summonID)
				if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
					fmt.Fprintf(logW, "summon: sweep remove %s: %v\n", summonID, err)
				}
				return false, nil
			}
			fmt.Fprintf(logW, "summon: sweep link %s: %v\n", summonID, err)
			return false, nil
		}
		if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(logW, "summon: sweep remove %s: %v\n", summonID, err)
			// The link succeeded so the dest is authoritative; the
			// stale pending will be cleaned on next tick (link will
			// then EEXIST → branch above removes it).
			return true, nil
		}
		return true, nil
	})
}

// ReadAll scans all four state dirs and returns []Summon with State
// populated. Sort order: state precedence (pending < accepted < declined
// < expired); within each state, ts descending (newest first) per D15.8.
//
// Missing state directories are NOT an error — fresh projects have none.
// Per-file parse errors are propagated so list commands surface
// corruption rather than silently dropping records.
func ReadAll(root string) ([]Summon, error) {
	var out []Summon
	for _, state := range allStates {
		dir := filepath.Join(root, "live", "summons", string(state))
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		var bucket []Summon
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
				return nil, fmt.Errorf("summon: parse %s: %w", path, err)
			}
			if s, ok := summonFromRecords(records, state); ok {
				bucket = append(bucket, s)
			}
		}
		// Sort this state's bucket by ts descending. Stable sort so
		// ties retain readdir order for determinism.
		sort.SliceStable(bucket, func(i, j int) bool {
			return bucket[i].TS > bucket[j].TS
		})
		out = append(out, bucket...)
	}
	return out, nil
}
