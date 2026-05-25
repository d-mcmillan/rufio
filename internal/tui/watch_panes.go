// watch_panes.go — channels/goals/inbox watcher extensions + walk.
//
// PR #23 (D23.9) adds three new watcher domains on top of the PR #22
// substrate:
//
//   - live/channels/active/<ch>/meta.gdl     → ChannelMsg
//   - live/channels/active/<ch>/messages/    → ChannelMessageMsg
//   - live/goals/{active,completed,abandoned}/<id>.gdl → GoalMsg
//   - live/inbox/<me>/<source>-overlap-<n>.gdl       → InboxMsg
//
// Initial-walk + per-event hooks live here so watch.go stays focused
// on the fleet (attention/outbox) substrate.
package tui

import (
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
)

// ChannelMsg is delivered when a live/channels/active/<id>/meta.gdl
// file is created or modified. The Channel struct mirrors the parsed
// audit log (members, closed-state) so the renderer can use it as-is.
type ChannelMsg struct {
	Channel channels.Channel
}

// ChannelMessage is the projected view of a single @say record from
// a live/channels/active/<id>/messages/<msg-id>.gdl file.
type ChannelMessage struct {
	ID      string
	By      string
	Content string
	TS      string
}

// ChannelMessageMsg is delivered when a message file appears under
// live/channels/active/<id>/messages/. The Message field is the
// projection ready to append to the per-channel buffer.
type ChannelMessageMsg struct {
	ChannelID string
	Message   ChannelMessage
}

// GoalMsg is delivered when a goal file appears or changes under any
// of the three state directories. State is derived from the directory.
type GoalMsg struct {
	Goal goal.Goal
}

// InboxOverlap is the projected view of a single @goal-overlap record
// from a live/inbox/<me>/<src>-overlap-<n>.gdl file.
type InboxOverlap struct {
	To           string
	From         string
	Entity       string
	SourceGoalID string
	TargetGoalID string
	TS           string
}

// appendUniqueOverlap appends overlap to existing iff no entry with the
// same source-goal+target-goal+entity triple is already present. Allows
// the watcher to re-fire over already-seen inbox files without
// duplicating the rendered notification list.
func appendUniqueOverlap(existing []InboxOverlap, overlap InboxOverlap) []InboxOverlap {
	for _, e := range existing {
		if e.SourceGoalID == overlap.SourceGoalID &&
			e.TargetGoalID == overlap.TargetGoalID &&
			e.Entity == overlap.Entity {
			return existing
		}
	}
	return append(existing, overlap)
}

// InboxMsg is delivered when an inbox overlap file is observed.
type InboxMsg struct {
	Overlap InboxOverlap
}

// PaneWatchPaths is the set of additional directories the panes
// watcher subscribes to. Exposed for testing.
func PaneWatchPaths(root, me string) []string {
	paths := []string{
		filepath.Join(root, "live", "channels", "active"),
		filepath.Join(root, "live", "goals", "active"),
		filepath.Join(root, "live", "goals", "completed"),
		filepath.Join(root, "live", "goals", "abandoned"),
	}
	if me != "" {
		paths = append(paths, filepath.Join(root, "live", "inbox", me))
	}
	return paths
}

// addPaneWatches registers the channels/goals/inbox directories on w.
// Missing dirs are mkdir'd defensively so the TUI works on an in-
// progress workspace. Per-channel subdirs are added on demand by
// consumePaneEvent when a new active/<ch>/ Create event arrives.
func addPaneWatches(root, me string, w *fsnotify.Watcher) error {
	for _, d := range PaneWatchPaths(root, me) {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		if err := w.Add(d); err != nil {
			return err
		}
	}
	// Add every existing active/<ch>/ AND active/<ch>/messages/ dir so
	// meta + message events flow.
	activeDir := filepath.Join(root, "live", "channels", "active")
	entries, _ := os.ReadDir(activeDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(activeDir, e.Name())
		_ = w.Add(sub)
		messagesDir := filepath.Join(sub, "messages")
		if err := os.MkdirAll(messagesDir, 0o755); err == nil {
			_ = w.Add(messagesDir)
		}
	}
	return nil
}

// consumePaneEvent maps a single fsnotify event to a tea.Msg for the
// pane-watch domain. Returns (msg, true) when an event is relevant,
// (nil, false) otherwise. Side effect: when a new active/<ch>/ subdir
// is created, registers a watch for it AND its messages/ subdir.
func consumePaneEvent(root, me string, w *fsnotify.Watcher, ev fsnotify.Event) (tea.Msg, bool) {
	if ev.Op&(fsnotify.Create|fsnotify.Write) == 0 {
		return nil, false
	}
	if strings.HasSuffix(ev.Name, ".tmp") {
		return nil, false
	}
	rel, err := filepath.Rel(root, ev.Name)
	if err != nil {
		return nil, false
	}
	rel = filepath.ToSlash(rel)

	// New channel dir → subscribe so subsequent meta/messages writes flow.
	if strings.HasPrefix(rel, "live/channels/active/") && ev.Op&fsnotify.Create != 0 {
		if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
			_ = w.Add(ev.Name)
			messagesDir := filepath.Join(ev.Name, "messages")
			if err := os.MkdirAll(messagesDir, 0o755); err == nil {
				_ = w.Add(messagesDir)
			}
			return nil, false
		}
	}

	switch {
	case strings.HasPrefix(rel, "live/channels/active/") && strings.HasSuffix(rel, "/meta.gdl"):
		chID := channelIDFromMetaPath(rel)
		if chID == "" {
			return nil, false
		}
		ch, err := channels.LoadMeta(root, chID)
		if err != nil {
			return nil, false
		}
		// Channel-membership floor (mirrors the listen-surface fix in
		// internal/lib/stream/channel_privacy.go): a non-member's TUI
		// must not render channel metadata for channels they're not in.
		// Empty `me` (anonymous TUI / tests) preserves firehose semantics
		// to match the existing convention.
		if me != "" && !ch.IsEverMember(me) {
			return nil, false
		}
		return ChannelMsg{Channel: ch}, true
	case strings.HasPrefix(rel, "live/channels/active/") &&
		strings.Contains(rel, "/messages/") && strings.HasSuffix(rel, ".gdl"):
		chID, msg, ok := loadChannelMessage(ev.Name, rel)
		if !ok {
			return nil, false
		}
		// Channel-membership floor — load the channel meta to check
		// whether the current operator is entitled to see this message.
		// Drop conservatively on any meta-load failure (matches the
		// stream predicate's failure-mode posture).
		if me != "" {
			ch, err := channels.LoadMeta(root, chID)
			if err != nil || !ch.IsEverMember(me) {
				return nil, false
			}
		}
		return ChannelMessageMsg{ChannelID: chID, Message: msg}, true
	case strings.HasPrefix(rel, "live/goals/") && strings.HasSuffix(rel, ".gdl"):
		g, ok := loadGoalFromPath(root, rel)
		if !ok {
			return nil, false
		}
		return GoalMsg{Goal: g}, true
	case me != "" && strings.HasPrefix(rel, "live/inbox/"+me+"/") && strings.HasSuffix(rel, ".gdl"):
		overlap, ok := loadInboxOverlap(ev.Name)
		if !ok {
			return nil, false
		}
		return InboxMsg{Overlap: overlap}, true
	}
	return nil, false
}

// channelIDFromMetaPath extracts the channel-id from a POSIX-form
// path "live/channels/active/<ch-id>/meta.gdl".
func channelIDFromMetaPath(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) < 5 {
		return ""
	}
	return parts[3]
}

// channelIDFromMessagePath extracts the channel-id from a POSIX-form
// path "live/channels/active/<ch-id>/messages/<msg-id>.gdl".
func channelIDFromMessagePath(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) < 6 {
		return ""
	}
	return parts[3]
}

// loadChannelMessage reads + projects a single message file. Returns
// (chID, msg, true) on success.
func loadChannelMessage(absPath, relPath string) (string, ChannelMessage, bool) {
	chID := channelIDFromMessagePath(relPath)
	if chID == "" {
		return "", ChannelMessage{}, false
	}
	bs, err := os.ReadFile(absPath)
	if err != nil {
		return "", ChannelMessage{}, false
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return "", ChannelMessage{}, false
	}
	for _, r := range records {
		// Issue #107: the canonical on-disk Type is "channel-message"
		// (aligned with recall.AllTypes). Legacy "say" support dropped
		// 2026-05-23 per the TODO's hard deadline; channels TTL = 24h
		// and v1.0.1 shipped 2026-05-19, so all stale "say" records have
		// long since aged out.
		if r.Type != "channel-message" {
			continue
		}
		return chID, ChannelMessage{
			ID:      r.Get("id"),
			By:      r.Get("by"),
			Content: r.Get("content"),
			TS:      r.Get("ts"),
		}, true
	}
	return "", ChannelMessage{}, false
}

// goalIDFromPath extracts the goal-id from a POSIX-form path
// "live/goals/<state>/<id>.gdl".
func goalIDFromPath(rel string) (string, goal.State, bool) {
	parts := strings.Split(rel, "/")
	if len(parts) < 4 {
		return "", "", false
	}
	state := goal.State(parts[2])
	id := strings.TrimSuffix(parts[3], ".gdl")
	return id, state, true
}

// loadGoalFromPath reads + projects a single goal file. Uses
// goal.LoadAnyState so the audit overlay (complete/abandon records)
// is populated.
func loadGoalFromPath(root, rel string) (goal.Goal, bool) {
	id, _, ok := goalIDFromPath(rel)
	if !ok {
		return goal.Goal{}, false
	}
	g, err := goal.LoadAnyState(root, id)
	if err != nil {
		return goal.Goal{}, false
	}
	return g, true
}

// loadInboxOverlap reads + projects a single inbox overlap file. The
// file contains exactly one @goal-overlap record per D18.4.
func loadInboxOverlap(absPath string) (InboxOverlap, bool) {
	bs, err := os.ReadFile(absPath)
	if err != nil {
		return InboxOverlap{}, false
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return InboxOverlap{}, false
	}
	for _, r := range records {
		if r.Type != "goal-overlap" {
			continue
		}
		return InboxOverlap{
			To:           r.Get("to"),
			From:         r.Get("from"),
			Entity:       r.Get("entity"),
			SourceGoalID: r.Get("source-goal"),
			TargetGoalID: r.Get("target-goal"),
			TS:           r.Get("ts"),
		}, true
	}
	return InboxOverlap{}, false
}

// InitialWalkPanes scans the channels/goals/inbox state and returns a
// slice of tea.Msgs ready for the Model.fold pipeline. Missing dirs
// are skipped silently — fresh projects have nothing to walk.
func InitialWalkPanes(root, me string) []tea.Msg {
	var msgs []tea.Msg

	// Channels (active only — closed channels are out of scope per D23.10).
	// Channel-membership floor: a non-member must NOT see channels they
	// don't belong to (mirrors the listen-surface fix in
	// internal/lib/stream/channel_privacy.go). Empty `me` (anonymous TUI
	// / tests) preserves firehose semantics to match the existing
	// convention used elsewhere.
	activeChannels := filepath.Join(root, "live", "channels", "active")
	if entries, err := os.ReadDir(activeChannels); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			chID := e.Name()
			ch, err := channels.LoadMeta(root, chID)
			if err != nil {
				continue
			}
			if me != "" && !ch.IsEverMember(me) {
				continue
			}
			msgs = append(msgs, ChannelMsg{Channel: ch})
			messagesDir := filepath.Join(activeChannels, chID, "messages")
			msgFiles, err := os.ReadDir(messagesDir)
			if err != nil {
				continue
			}
			for _, mf := range msgFiles {
				if mf.IsDir() || !strings.HasSuffix(mf.Name(), ".gdl") {
					continue
				}
				absPath := filepath.Join(messagesDir, mf.Name())
				rel := filepath.ToSlash(filepath.Join("live", "channels", "active", chID, "messages", mf.Name()))
				if id, msg, ok := loadChannelMessage(absPath, rel); ok {
					msgs = append(msgs, ChannelMessageMsg{ChannelID: id, Message: msg})
				}
			}
		}
	}

	// Goals (all states).
	if goals, err := goal.ReadAll(root); err == nil {
		for _, g := range goals {
			msgs = append(msgs, GoalMsg{Goal: g})
		}
	}

	// Inbox overlaps for the current agent.
	if me != "" {
		inboxDir := filepath.Join(root, "live", "inbox", me)
		if entries, err := os.ReadDir(inboxDir); err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
					continue
				}
				absPath := filepath.Join(inboxDir, e.Name())
				if overlap, ok := loadInboxOverlap(absPath); ok {
					msgs = append(msgs, InboxMsg{Overlap: overlap})
				}
			}
		}
	}

	return msgs
}
