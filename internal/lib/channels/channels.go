// Package channels implements the channel substrate: meta.gdl creation
// (PR #15), the audit-derived state machine + say/leave/close write paths
// (PR #16), and the read-side projection (Channel + LoadMeta).
//
// Channel id format is `ch-<unix-millis>-<rand6>` (D15.1): same
// uniqueness/sort discipline as thought/summon ids, with the `ch-`
// prefix to disambiguate channel-ids in lineage walks and routing logs.
// Message id format is `<unix-millis>-<rand6>` (D16.1) — same shape
// without the `ch-` prefix since context disambiguates.
package channels

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/fslock"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// GenerateID returns a new channel-id of the form
// `ch-<unix-millis>-<rand6>`. The `ch-` prefix distinguishes channel-ids
// from thought-ids and summon-ids per D15.1.
func GenerateID() (string, error) {
	return generateIDFromSource(
		func() int64 { return time.Now().UnixMilli() },
		rand.Reader,
		"ch-",
	)
}

// GenerateMessageID returns a new message-id of the form
// `<unix-millis>-<rand6>`. Per D16.1, same alphabet/format as channel ids
// minus the `ch-` prefix because context already disambiguates message
// ids from channel ids (they only ever appear together in say records).
func GenerateMessageID() (string, error) {
	return generateIDFromSource(
		func() int64 { return time.Now().UnixMilli() },
		rand.Reader,
		"",
	)
}

// generateIDFromSource is the testable variant. Production callers use
// GenerateID / GenerateMessageID. The alphabet matches
// thought.generateIDFromSource so id shapes stay consistent across
// Rufio's id-emitting packages.
func generateIDFromSource(now func() int64, src io.Reader, prefix string) (string, error) {
	buf := make([]byte, 6)
	n, err := io.ReadFull(src, buf)
	if err != nil || n != 6 {
		return "", fmt.Errorf("channels: rand source read %d/6 bytes: %w", n, err)
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 6)
	for i, b := range buf {
		out[i] = alphabet[int(b)%36]
	}
	return fmt.Sprintf("%s%d-%s", prefix, now(), out), nil
}

// Channel is the parsed @channel record plus audit-derived state assembled
// by walking subsequent @channel-leave / @channel-close records in
// meta.gdl. Membership is NOT stored on the record — it's derived per
// D16.3 from opener/target minus Left, gated on Closed.
type Channel struct {
	ID        string
	Opener    string
	Target    string
	Topic     string
	Intent    string
	CreatedAt string
	// Left maps an agent id to the ts of their @channel-leave record.
	// nil when no one has left. Membership lookups should treat nil and
	// empty maps identically.
	Left map[string]string
	// Closed is true when a @channel-close record is present in meta
	// OR (defense, per spec note) when meta lives under closed/ even
	// without an explicit close record.
	Closed   bool
	ClosedBy string // empty if not closed
	ClosedAt string // empty if not closed
}

// BuildMetaRecord returns the @channel gdl.Record per D15.7. Field
// order locked at id, opener, target, topic, intent, created-at.
func BuildMetaRecord(chID, opener, target, topic, intent, createdAt string) gdl.Record {
	return gdl.Record{Type: "channel", Fields: []gdl.RecordField{
		{Key: "id", Value: chID},
		{Key: "opener", Value: opener},
		{Key: "target", Value: target},
		{Key: "topic", Value: topic},
		{Key: "intent", Value: intent},
		{Key: "created-at", Value: createdAt},
	}}
}

// BuildSayRecord returns the @channel-message gdl.Record per D16.2.
// Field order locked at id, channel, by, content, ts.
//
// The on-disk record's Type is "channel-message" to align with
// recall.AllTypes (the canonical taxonomy used by stream.Match in
// `rufio listen`). Issue #107: the writer previously produced
// Type="say" which was NOT in AllTypes — every channel-message event
// was silently dropped by listen's default type filter, making
// channels appear write-only to recipients. The CLI verb name (`rufio
// say`) is unchanged; only the on-disk record's Type token shifts.
func BuildSayRecord(msgID, chID, by, content, ts string) gdl.Record {
	return gdl.Record{Type: "channel-message", Fields: []gdl.RecordField{
		{Key: "id", Value: msgID},
		{Key: "channel", Value: chID},
		{Key: "by", Value: by},
		{Key: "content", Value: content},
		{Key: "ts", Value: ts},
	}}
}

// BuildLeaveRecord returns the @channel-leave gdl.Record per D16.4.
// Field order locked at by, ts.
func BuildLeaveRecord(by, ts string) gdl.Record {
	return gdl.Record{Type: "channel-leave", Fields: []gdl.RecordField{
		{Key: "by", Value: by},
		{Key: "ts", Value: ts},
	}}
}

// BuildCloseRecord returns the @channel-close gdl.Record per D16.5.
// Field order locked at by, ts.
func BuildCloseRecord(by, ts string) gdl.Record {
	return gdl.Record{Type: "channel-close", Fields: []gdl.RecordField{
		{Key: "by", Value: by},
		{Key: "ts", Value: ts},
	}}
}

// WriteMeta writes live/channels/active/<ch-id>/meta.gdl atomically via
// .tmp + os.Rename. Creates the parent <ch-id> directory. No lock —
// each accept mints a fresh ch-id, so no concurrent writer can target
// the same path.
func WriteMeta(root, chID string, record gdl.Record) error {
	dir := filepath.Join(root, "live", "channels", "active", chID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, "meta.gdl")
	tmp := target + ".tmp"
	// Best-effort cleanup so a failed WriteFile/Rename never strands
	// meta.gdl.tmp under live/channels/active/ (#141). Success path:
	// Rename already moved tmp, so this Remove is a harmless no-op.
	defer func() { _ = os.Remove(tmp) }()
	contents := gdl.RenderLine(record) + "\n"
	if err := os.WriteFile(tmp, []byte(contents), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// activeMetaPath returns the path to the active meta.gdl for ch-id.
func activeMetaPath(root, chID string) string {
	return filepath.Join(root, "live", "channels", "active", chID, "meta.gdl")
}

// closedMetaPath returns the path to the closed meta.gdl for ch-id.
func closedMetaPath(root, chID string) string {
	return filepath.Join(root, "live", "channels", "closed", chID, "meta.gdl")
}

// channelLockDir returns the lockdir path for per-channel mutations
// (leave/close). Per design §4.D line 395 — one lock per ch-id.
func channelLockDir(root, chID string) string {
	return filepath.Join(root, ".rufio", "locks", "channel-"+chID+".lock")
}

// LoadMeta reads live/channels/active/<ch-id>/meta.gdl FIRST. If not
// found, falls through to live/channels/closed/<ch-id>/meta.gdl. If
// neither exists, returns *NoSuchChannelError.
//
// The first record must be @channel — it populates ID/Opener/Target/
// Topic/Intent/CreatedAt. Subsequent @channel-leave records populate
// the Left map. Any @channel-close record sets Closed=true plus
// ClosedBy/ClosedAt.
//
// Per the plan, when meta lives under closed/ but has no explicit
// @channel-close record (corruption), Closed is still reported true
// based on the directory location — the audit trail is the file's path
// AND its contents, so disagreement leans pessimistic.
func LoadMeta(root, chID string) (Channel, error) {
	path := activeMetaPath(root, chID)
	closedByDir := false
	bs, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return Channel{}, err
		}
		// Fall through to closed/.
		path = closedMetaPath(root, chID)
		bs, err = os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return Channel{}, &rufioerr.NoSuchChannelError{ID: chID}
			}
			return Channel{}, err
		}
		closedByDir = true
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return Channel{}, fmt.Errorf("channels: parse %s: %w", path, err)
	}
	if len(records) == 0 || records[0].Type != "channel" {
		// File exists but no @channel head record — surface as
		// NoSuchChannelError. (Corruption shows up the same as
		// missing-file to keep the caller's error switch simple.)
		return Channel{}, &rufioerr.NoSuchChannelError{ID: chID}
	}
	head := records[0]
	ch := Channel{
		ID:        head.Get("id"),
		Opener:    head.Get("opener"),
		Target:    head.Get("target"),
		Topic:     head.Get("topic"),
		Intent:    head.Get("intent"),
		CreatedAt: head.Get("created-at"),
	}
	for _, r := range records[1:] {
		switch r.Type {
		case "channel-leave":
			if ch.Left == nil {
				ch.Left = make(map[string]string)
			}
			ch.Left[r.Get("by")] = r.Get("ts")
		case "channel-close":
			ch.Closed = true
			ch.ClosedBy = r.Get("by")
			ch.ClosedAt = r.Get("ts")
		}
	}
	if closedByDir {
		ch.Closed = true
	}
	return ch, nil
}

// IsCurrentMember returns true iff agent is opener OR target, AND has
// not left, AND the channel is not closed. Per D16.3.
func (c Channel) IsCurrentMember(agent string) bool {
	if c.Closed {
		return false
	}
	if agent != c.Opener && agent != c.Target {
		return false
	}
	if _, left := c.Left[agent]; left {
		return false
	}
	return true
}

// CurrentMembers returns the currently-active members of the channel
// (opener + target minus Left). Returns an empty slice when closed.
// Order is deterministic: opener first, then target.
func (c Channel) CurrentMembers() []string {
	if c.Closed {
		return []string{}
	}
	out := make([]string, 0, 2)
	if _, left := c.Left[c.Opener]; !left {
		out = append(out, c.Opener)
	}
	if _, left := c.Left[c.Target]; !left {
		out = append(out, c.Target)
	}
	return out
}

// Other returns the OTHER current member relative to agent, or "" if
// agent isn't a current member or there is no other current member.
// Used by the router to identify the recipient of a @say record.
func (c Channel) Other(agent string) string {
	if !c.IsCurrentMember(agent) {
		return ""
	}
	for _, m := range c.CurrentMembers() {
		if m != agent {
			return m
		}
	}
	return ""
}

// WriteMessage writes live/channels/active/<ch-id>/messages/<msg-id>.gdl
// atomically via .tmp + os.Rename. Creates messages/ on first call
// (D16.15). No lock per D16.7 — fresh msg-id per call avoids contention.
// Caller is expected to have verified membership via LoadMeta +
// IsCurrentMember.
func WriteMessage(root, chID, msgID string, record gdl.Record) error {
	dir := filepath.Join(root, "live", "channels", "active", chID, "messages")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	target := filepath.Join(dir, msgID+".gdl")
	tmp := target + ".tmp"
	// Best-effort cleanup so a failed WriteFile/Rename never strands
	// <msg-id>.gdl.tmp under live/channels/active/<ch>/messages/ (#141).
	// Success path: Rename already moved tmp, so this is a no-op.
	defer func() { _ = os.Remove(tmp) }()
	contents := gdl.RenderLine(record) + "\n"
	if err := os.WriteFile(tmp, []byte(contents), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// AppendLeave atomically appends a @channel-leave record to
// live/channels/active/<ch-id>/meta.gdl under channel-<ch-id>.lock
// (D16.4). Idempotent: if the audit already shows a leave by `by`,
// returns nil without rewriting.
//
// Returns *NoSuchChannelError if active/<ch-id>/meta.gdl is missing
// (channel never existed or already closed — both surface the same way,
// since closed channels can't accept further state changes).
func AppendLeave(root, chID, by, ts string) error {
	_, err := fslock.WithLock(channelLockDir(root, chID), 0, func() (struct{}, error) {
		var zero struct{}
		path := activeMetaPath(root, chID)
		bs, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return zero, &rufioerr.NoSuchChannelError{ID: chID}
			}
			return zero, err
		}
		records, err := gdl.ParseDocument(string(bs))
		if err != nil {
			return zero, fmt.Errorf("channels: parse %s: %w", path, err)
		}
		for _, r := range records {
			if r.Type == "channel-leave" && r.Get("by") == by {
				// Idempotent: already left.
				return zero, nil
			}
		}
		leaveRec := BuildLeaveRecord(by, ts)
		appended := string(bs)
		if len(appended) > 0 && appended[len(appended)-1] != '\n' {
			appended += "\n"
		}
		appended += gdl.RenderLine(leaveRec) + "\n"
		tmp := path + ".tmp"
		// Best-effort cleanup so a failed WriteFile/Rename never
		// strands meta.gdl.tmp (#141). Success path: Rename already
		// moved tmp, so this Remove is a harmless no-op. The
		// idempotency guard and append ordering above are unchanged.
		defer func() { _ = os.Remove(tmp) }()
		if err := os.WriteFile(tmp, []byte(appended), 0o644); err != nil {
			return zero, err
		}
		if err := os.Rename(tmp, path); err != nil {
			return zero, err
		}
		return zero, nil
	})
	return err
}

// AppendClose appends a @channel-close record to
// active/<ch-id>/meta.gdl AND renames active/<ch-id>/ →
// closed/<ch-id>/, all under channel-<ch-id>.lock (D16.5).
//
// Idempotent: if the channel is already in closed/ (or active/<ch-id>/
// is gone), returns nil. Returns *NoSuchChannelError when no trace of
// the channel exists in either active/ or closed/.
//
// Caller is expected to have verified opener authorisation via
// LoadMeta + comparison against Channel.Opener; this function does
// NOT recheck authorisation under the lock (it would require re-parsing
// meta only to throw the same error a microsecond later).
func AppendClose(root, chID, by, ts string) error {
	_, err := fslock.WithLock(channelLockDir(root, chID), 0, func() (struct{}, error) {
		var zero struct{}
		activeDir := filepath.Join(root, "live", "channels", "active", chID)
		closedDir := filepath.Join(root, "live", "channels", "closed", chID)
		activeMeta := activeMetaPath(root, chID)
		closedMeta := closedMetaPath(root, chID)
		_, errStat := os.Stat(activeMeta)
		if errStat != nil {
			if !errors.Is(errStat, fs.ErrNotExist) {
				return zero, errStat
			}
			// Active meta missing — check closed for idempotency.
			if _, err := os.Stat(closedMeta); err == nil {
				return zero, nil // already closed
			} else if !errors.Is(err, fs.ErrNotExist) {
				return zero, err
			}
			return zero, &rufioerr.NoSuchChannelError{ID: chID}
		}

		// 1. Append the @channel-close record to the active meta.gdl.
		bs, err := os.ReadFile(activeMeta)
		if err != nil {
			return zero, err
		}
		closeRec := BuildCloseRecord(by, ts)
		appended := string(bs)
		if len(appended) > 0 && appended[len(appended)-1] != '\n' {
			appended += "\n"
		}
		appended += gdl.RenderLine(closeRec) + "\n"
		tmp := activeMeta + ".tmp"
		// Best-effort cleanup so a failed WriteFile/Rename of the
		// @channel-close append never strands meta.gdl.tmp (#141).
		// Success path: Rename already moved tmp, so this Remove is a
		// harmless no-op. The subsequent active/->closed/ directory
		// rename (step 2) and its ordering are unchanged.
		defer func() { _ = os.Remove(tmp) }()
		if err := os.WriteFile(tmp, []byte(appended), 0o644); err != nil {
			return zero, err
		}
		if err := os.Rename(tmp, activeMeta); err != nil {
			return zero, err
		}

		// 2. Rename active/<ch-id>/ → closed/<ch-id>/.
		//
		// `closed/` is created by init.go, but be defensive: ensure
		// the parent exists. Do NOT create closed/<ch-id>/ itself —
		// rename's target must not exist on the same filesystem.
		if err := os.MkdirAll(filepath.Dir(closedDir), 0o755); err != nil {
			return zero, err
		}
		if err := os.Rename(activeDir, closedDir); err != nil {
			// If the target already exists, treat as "already closed
			// by a concurrent caller" — idempotent. We already
			// appended @channel-close above; the closed/ copy
			// authoritatively wins per D16.6. The orphaned active/
			// would be a corruption but we can't undo the rename
			// failure cleanly under the lock. In practice this only
			// fires under torn-state recovery.
			if errors.Is(err, fs.ErrExist) || isENotEmpty(err) {
				return zero, nil
			}
			return zero, err
		}
		return zero, nil
	})
	return err
}

// isENotEmpty matches the ENOTEMPTY errno returned by os.Rename when
// renaming over a non-empty directory. Both Linux and macOS return
// ENOTEMPTY in that case. Wrapped here to avoid the call site importing
// syscall.
func isENotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY)
}

// IsEverMember returns true iff agent is or ever was a participant of
// the channel — i.e. opener or target, regardless of Left status or
// Closed state. Per #142 this is the "past member" gate used by the
// read-only `rufio channel show` path: a member who has left should
// still be able to read the audit trail. Looser than IsCurrentMember,
// which is the write-side gate (excludes Left and Closed).
func (c Channel) IsEverMember(agent string) bool {
	return agent == c.Opener || agent == c.Target
}

// Message is the parsed @channel-message record off a per-channel
// messages/<id>.gdl file (D16.2). Surfaced via ReadMessages for the
// `rufio channel show` read API (#142).
type Message struct {
	ID      string
	Channel string
	By      string
	Content string
	TS      string
}

// messagesDirActive / messagesDirClosed locate the messages subdir for
// active and closed channels respectively. Closed channels keep their
// messages alongside meta.gdl under closed/<ch-id>/messages/.
func messagesDirActive(root, chID string) string {
	return filepath.Join(root, "live", "channels", "active", chID, "messages")
}

func messagesDirClosed(root, chID string) string {
	return filepath.Join(root, "live", "channels", "closed", chID, "messages")
}

// ReadMessages reads every @channel-message record under the channel's
// messages/ subdir (active first, then closed). Returns the messages
// sorted by ts ascending (chronological order — oldest first, since
// that's what a human reader expects from a "show me the history" view,
// per #142).
//
// A missing messages/ dir is not an error — fresh channels with no
// messages yet return ([], nil). Per-file parse failures propagate
// because the file's continued presence implies a write committed and
// the operator deserves to see corruption rather than silently lose
// rows.
func ReadMessages(root, chID string) ([]Message, error) {
	var out []Message
	for _, dir := range []string{messagesDirActive(root, chID), messagesDirClosed(root, chID)} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".gdl" {
				continue
			}
			path := filepath.Join(dir, e.Name())
			bs, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			records, err := gdl.ParseDocument(string(bs))
			if err != nil {
				return nil, fmt.Errorf("channels: parse %s: %w", path, err)
			}
			for _, r := range records {
				if r.Type != "channel-message" {
					continue
				}
				out = append(out, Message{
					ID:      r.Get("id"),
					Channel: r.Get("channel"),
					By:      r.Get("by"),
					Content: r.Get("content"),
					TS:      r.Get("ts"),
				})
				break // one @channel-message per file
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].TS < out[j].TS
	})
	return out, nil
}

// ReadAll enumerates every channel (active + closed) on disk by
// listing live/channels/active/* and live/channels/closed/* dirs and
// calling LoadMeta on each ch-id. Sorted ID-ascending for determinism
// (ch-<unix-millis>-<rand6> sorts roughly creation-time-ascending).
//
// Missing live/channels/ dirs are NOT an error — fresh projects have
// none — and contribute zero channels. Per-channel LoadMeta failures
// propagate so an operator sees torn-state corruption.
//
// Used by `rufio channels list` (#142). The caller is expected to
// filter by membership/state via the returned slice.
func ReadAll(root string) ([]Channel, error) {
	var out []Channel
	for _, sub := range []string{"active", "closed"} {
		dir := filepath.Join(root, "live", "channels", sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			chID := e.Name()
			meta, err := LoadMeta(root, chID)
			if err != nil {
				// Skip "no @channel record" corruption (LoadMeta surfaces
				// it as NoSuchChannelError) so a single bad dir doesn't
				// poison the whole listing — operators can still see the
				// healthy rows.
				var notFound *rufioerr.NoSuchChannelError
				if errors.As(err, &notFound) {
					continue
				}
				return nil, err
			}
			out = append(out, meta)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}
