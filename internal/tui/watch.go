// watch.go — fsnotify watcher, initial walk, and daemon-online polling.
//
// The watcher subscribes to live/attention/ + live/outbox/ (the two
// directories the fleet tab needs). Subsequent PRs may add more
// watchers for channels/goals/lineage.
//
// Event translation flows fsnotify.Event → tea.Msg via a goroutine that
// drains the fsnotify channel and pushes parsed messages onto an
// internal Go channel. The model consumes that channel via a tea.Cmd
// that blocks on one receive per call and then re-issues itself.
package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"

	"github.com/d-mcmillan/rufio/internal/lib/attention"
	"github.com/d-mcmillan/rufio/internal/lib/devhealth"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// AttentionMsg is delivered when a live/attention/<agent>.gdl file is
// created or modified. The fields mirror attention.Attention so the
// Model.Update handler can fold the message into the agent cache
// without re-parsing.
type AttentionMsg struct {
	Agent    string
	Intent   string
	Entities []string
	Topics   []string
	TS       string
}

// ThoughtMsg is delivered when a live/outbox/<agent>/<id>.gdl file is
// created or modified. The Summary is the parsed projection — content
// is included so the fleet pane can show preview lines without an
// extra disk read.
type ThoughtMsg struct {
	Agent   string
	Summary ThoughtSummary
}

// ConfirmMsg is delivered when a live/confirms/<thought-id>.gdl file is
// created or modified (a @confirm/@refute was appended). It carries ONLY
// the thought-id; the consumer re-reads the tally via confirm.ReadAll
// (the file is append-only and ReadAll dedups+sorts, so re-reading is
// the correct, drift-free fold). PR-G1: added so the v8 substrate chat's
// decision-row quorum dots update live as confirms accumulate (the chat
// is read-only this slice; the operator never writes here).
type ConfirmMsg struct {
	ThoughtID string
}

// DaemonOnlineMsg carries the result of the periodic daemon-online
// check. Online is true when .rufio/dev.heartbeat reports a fresh
// last_tick (within devhealth.StaleThreshold); see DaemonOnline.
type DaemonOnlineMsg struct {
	Online bool
}

// WatcherErrMsg surfaces an error from the fsnotify watcher's error
// channel. The Model logs but does not fail on these — fsnotify
// occasionally emits transient errors (e.g., file removed mid-rename)
// and crashing the TUI on every one would be unfriendly.
type WatcherErrMsg struct {
	Err error
}

// WatcherClosedMsg fires once when the watcher's goroutine exits
// (during shutdown). The Model uses it to ensure the goroutine has
// stopped before returning from Update on tea.QuitMsg.
type WatcherClosedMsg struct{}

// ThoughtSummary is the projected read-only view of a single
// live/outbox/<agent>/<id>.gdl record. Mirrors a subset of the parsed
// @thought fields the TUI needs for pane rendering.
type ThoughtSummary struct {
	ID      string
	Author  string
	Type    string
	Subject string
	Content string
	TS      string
}

// WatchPaths is the set of directories the fleet-tab watcher
// subscribes to. Exposed so tests can assert the exact list.
//
// PR-G1: live/confirms/ is added so the v8 substrate chat's decision-row
// quorum dots update live as @confirm records accumulate (the chat is
// read-only this slice). This is the minimal retained-reader extension
// the G1 spec sanctions — the rest of watch.go is unchanged.
func WatchPaths(root string) []string {
	return []string{
		filepath.Join(root, "live", "attention"),
		filepath.Join(root, "live", "outbox"),
		filepath.Join(root, "live", "confirms"),
	}
}

// NewWatcher creates an fsnotify watcher subscribed to live/attention/
// and live/outbox/ (plus each agent's outbox subdir already on disk —
// fsnotify does not recurse) AND the pane-watch dirs added in PR #23
// (channels/goals/inbox). Missing directories are created so the
// caller doesn't need to coordinate with `rufio init`.
//
// Returns a watcher handle, the tea.Cmd that yields events as
// AttentionMsg/ThoughtMsg/ChannelMsg/GoalMsg/InboxMsg/WatcherErrMsg,
// and a stop function that shuts down the watcher cleanly.
//
// The caller MUST defer the stop function (Model.Update invokes it on
// tea.QuitMsg). Failing to do so leaks the goroutine + watcher.
func NewWatcher(root string) (*fsnotify.Watcher, tea.Cmd, func(), error) {
	return NewWatcherFor(root, "")
}

// NewWatcherFor is the identity-aware variant: subscribes to
// live/inbox/<me>/ in addition to the public dirs. Callers that already
// know the active agent (e.g. Model.Init) should use this. Pass an
// empty `me` to skip the inbox subscription (no-identity case).
func NewWatcherFor(root, me string) (*fsnotify.Watcher, tea.Cmd, func(), error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, nil, err
	}

	// Ensure target dirs exist. `rufio init` creates them but the TUI
	// may be invoked on an in-progress workspace before init finishes
	// (defence in depth per the plan's D22.7 commentary).
	attentionDir := filepath.Join(root, "live", "attention")
	outboxDir := filepath.Join(root, "live", "outbox")
	// confirmsDir (PR-G1): the v8 substrate chat's decision-row quorum
	// must update live as @confirm records accumulate. confirm.Append
	// writes flat live/confirms/<id>.gdl files (no per-id subdirs —
	// confirm.go:69-73), so a single non-recursive watch on the dir
	// catches every tally change.
	confirmsDir := filepath.Join(root, "live", "confirms")
	for _, d := range []string{attentionDir, outboxDir, confirmsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			_ = w.Close()
			return nil, nil, nil, err
		}
	}

	if err := w.Add(attentionDir); err != nil {
		_ = w.Close()
		return nil, nil, nil, err
	}
	if err := w.Add(outboxDir); err != nil {
		_ = w.Close()
		return nil, nil, nil, err
	}
	if err := w.Add(confirmsDir); err != nil {
		_ = w.Close()
		return nil, nil, nil, err
	}
	// Each existing agent outbox subdir needs its own watch (fsnotify
	// doesn't recurse). Subdirs created later are picked up via the
	// outbox-dir watcher's Create events — see consumeEvent.
	if entries, err := os.ReadDir(outboxDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sub := filepath.Join(outboxDir, e.Name())
			if err := w.Add(sub); err != nil {
				_ = w.Close()
				return nil, nil, nil, err
			}
		}
	}

	// Register the PR #23 pane watch domains (channels/goals/inbox).
	// Failure here is fatal — without these the pane tabs are stuck on
	// the cached initial walk.
	if err := addPaneWatches(root, me, w); err != nil {
		_ = w.Close()
		return nil, nil, nil, err
	}

	// Buffered so a short burst of file events doesn't block the
	// fsnotify goroutine while bubbletea is still rendering. 64 is
	// arbitrary but generous given typical fleet sizes (<10 agents).
	out := make(chan tea.Msg, 64)
	done := make(chan struct{})

	go runWatcher(root, me, w, out, done)

	stop := func() {
		_ = w.Close() // closes w.Events, which makes runWatcher exit.
		<-done        // wait for goroutine teardown.
	}

	return w, watcherCmd(out), stop, nil
}

// watcherCmd returns the tea.Cmd shape bubbletea expects: a func that
// reads ONE message and returns it. tea.Program re-issues the cmd
// after every Update, so this naturally drains the channel one
// message per render cycle.
func watcherCmd(out chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-out
		if !ok {
			return WatcherClosedMsg{}
		}
		return msg
	}
}

// runWatcher is the fsnotify-side goroutine: it reads w.Events/Errors
// and translates each event into a tea.Msg on the out channel. Exits
// when w.Events closes (Close was called).
//
// Each event flows through BOTH consumeEvent (fleet substrate) and
// consumePaneEvent (channels/goals/inbox). Only one of the two will
// actually emit for a given path; routing is by prefix.
func runWatcher(root, me string, w *fsnotify.Watcher, out chan<- tea.Msg, done chan<- struct{}) {
	defer func() {
		close(done)
		close(out)
	}()
	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if msg, emit := consumeEvent(root, w, ev); emit {
				out <- msg
			}
			if msg, emit := consumePaneEvent(root, me, w, ev); emit {
				out <- msg
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			if err != nil {
				out <- WatcherErrMsg{Err: err}
			}
		}
	}
}

// consumeEvent maps a single fsnotify event into a tea.Msg. Returns
// (msg, true) when an event should be delivered, (nil, false) when it
// should be ignored (chmod-only events, .tmp files, unrelated paths).
//
// Side effect: when a new agent outbox subdir is created under
// live/outbox/, the watcher subscribes to it so subsequent thought
// writes are observed.
func consumeEvent(root string, w *fsnotify.Watcher, ev fsnotify.Event) (tea.Msg, bool) {
	// Filter on create/write; chmod and remove are uninteresting for
	// the fleet view.
	if ev.Op&(fsnotify.Create|fsnotify.Write) == 0 {
		return nil, false
	}
	// Skip .tmp files (the atomic-rename source — the real event
	// arrives when the rename target gets a Create).
	if strings.HasSuffix(ev.Name, ".tmp") {
		return nil, false
	}

	rel, err := filepath.Rel(root, ev.Name)
	if err != nil {
		return nil, false
	}
	rel = filepath.ToSlash(rel)

	// New agent outbox subdir: subscribe so we observe its thought writes.
	if strings.HasPrefix(rel, "live/outbox/") && ev.Op&fsnotify.Create != 0 {
		if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
			_ = w.Add(ev.Name)
			return nil, false
		}
	}

	switch {
	case strings.HasPrefix(rel, "live/attention/") && strings.HasSuffix(rel, ".gdl"):
		att, err := loadAttention(root, attentionAgentFromPath(rel))
		if err != nil {
			return WatcherErrMsg{Err: err}, true
		}
		return AttentionMsg{
			Agent:    att.Agent,
			Intent:   att.Intent,
			Entities: att.Entities,
			Topics:   att.Topics,
			TS:       att.TS,
		}, true
	case strings.HasPrefix(rel, "live/outbox/") && strings.HasSuffix(rel, ".gdl"):
		agent, summary, ok := loadThought(ev.Name, rel)
		if !ok {
			return nil, false
		}
		return ThoughtMsg{Agent: agent, Summary: summary}, true
	case strings.HasPrefix(rel, "live/confirms/") && strings.HasSuffix(rel, ".gdl"):
		// PR-G1: a @confirm/@refute landed for this thought-id. Emit the
		// id only; the consumer re-tallies via confirm.ReadAll (the file
		// is append-only + ReadAll dedups, so re-reading is drift-free).
		id := strings.TrimSuffix(filepath.Base(rel), ".gdl")
		if id == "" {
			return nil, false
		}
		return ConfirmMsg{ThoughtID: id}, true
	}
	return nil, false
}

// attentionAgentFromPath extracts the agent id from a POSIX-form
// relative path "live/attention/<agent>.gdl".
func attentionAgentFromPath(rel string) string {
	idx := strings.LastIndex(rel, "/")
	base := rel
	if idx >= 0 {
		base = rel[idx+1:]
	}
	return strings.TrimSuffix(base, ".gdl")
}

// loadAttention re-reads + parses a single attention file. Wraps
// attention.LoadOne so consumeEvent doesn't carry stale fields if the
// file was overwritten between the fsnotify event and the read.
func loadAttention(root, agent string) (attention.Attention, error) {
	return attention.LoadOne(root, agent)
}

// loadThought reads + parses a single live/outbox/<agent>/<id>.gdl
// file and projects it to a ThoughtSummary. Returns (agent, summary,
// true) on success. Returns (_, _, false) when the file is unreadable
// or contains no @thought record — neither is an error worth surfacing
// because the rename-source .tmp pattern guarantees we'll see the
// final file shortly.
func loadThought(absPath, relPath string) (string, ThoughtSummary, bool) {
	// rel form: live/outbox/<agent>/<id>.gdl
	parts := strings.Split(relPath, "/")
	if len(parts) < 4 {
		return "", ThoughtSummary{}, false
	}
	agent := parts[2]

	bs, err := os.ReadFile(absPath)
	if err != nil {
		return "", ThoughtSummary{}, false
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		return "", ThoughtSummary{}, false
	}
	for _, r := range records {
		if r.Type != "thought" {
			continue
		}
		return agent, ThoughtSummary{
			ID:      r.Get("id"),
			Author:  r.Get("author"),
			Type:    r.Get("type"),
			Subject: r.Get("subject"),
			Content: r.Get("content"),
			TS:      r.Get("ts"),
		}, true
	}
	return "", ThoughtSummary{}, false
}

// MaxRecentThoughtsPerAgent caps the in-memory recent-thought cache so
// long-running TUI sessions don't unbounded-grow. Per design L2.15 +
// plan D22.5.
const MaxRecentThoughtsPerAgent = 100

// InitialWalk synchronously enumerates the existing attention + outbox
// state and returns a slice of tea.Msgs the Model can fold into its
// cache during Init. The slice is deterministically ordered:
// AttentionMsg per agent (sorted by agent id ascending), then
// ThoughtMsg per (agent, ts-descending) up to MaxRecentThoughtsPerAgent.
//
// A missing live/attention/ or live/outbox/ directory is not an error
// — the slice is just shorter.
func InitialWalk(root string) []tea.Msg {
	var msgs []tea.Msg

	atts, err := attention.ReadAll(root)
	if err == nil {
		for _, a := range atts {
			msgs = append(msgs, AttentionMsg{
				Agent:    a.Agent,
				Intent:   a.Intent,
				Entities: a.Entities,
				Topics:   a.Topics,
				TS:       a.TS,
			})
		}
	}

	outboxRoot := filepath.Join(root, "live", "outbox")
	// A missing outbox is not an error — skip the thought walk but STILL
	// fall through to the confirms walk below (PR-G1: a project may have
	// confirms before/without an outbox dir on a partial workspace).
	entries, err := os.ReadDir(outboxRoot)
	if err != nil {
		entries = nil
	}

	type stamped struct {
		summary ThoughtSummary
		stamp   int64
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		agent := e.Name()
		sub := filepath.Join(outboxRoot, agent)
		files, err := os.ReadDir(sub)
		if err != nil {
			continue
		}
		batch := make([]stamped, 0, len(files))
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".gdl") {
				continue
			}
			absPath := filepath.Join(sub, f.Name())
			rel := filepath.ToSlash(filepath.Join("live", "outbox", agent, f.Name()))
			_, summary, ok := loadThought(absPath, rel)
			if !ok {
				continue
			}
			batch = append(batch, stamped{summary: summary, stamp: stampFromID(summary.ID)})
		}
		// Sort ts-descending via the unix-millis prefix of the thought id
		// (more stable than ts-string comparison, and the canonical write
		// path guarantees the prefix per D5.10).
		sort.SliceStable(batch, func(i, j int) bool {
			return batch[i].stamp > batch[j].stamp
		})
		if len(batch) > MaxRecentThoughtsPerAgent {
			batch = batch[:MaxRecentThoughtsPerAgent]
		}
		for _, b := range batch {
			msgs = append(msgs, ThoughtMsg{Agent: agent, Summary: b.summary})
		}
	}

	// PR-G1: enumerate existing confirms so a cold start (history on
	// disk, daemon offline) renders decision-row quorum immediately,
	// before any live ConfirmMsg streams. One ConfirmMsg per
	// live/confirms/<id>.gdl (sorted for deterministic fold order); the
	// consumer re-tallies each via confirm.ReadAll.
	confirmsDir := filepath.Join(root, "live", "confirms")
	if cf, err := os.ReadDir(confirmsDir); err == nil {
		ids := make([]string, 0, len(cf))
		for _, f := range cf {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".gdl") {
				continue
			}
			ids = append(ids, strings.TrimSuffix(f.Name(), ".gdl"))
		}
		sort.Strings(ids)
		for _, id := range ids {
			msgs = append(msgs, ConfirmMsg{ThoughtID: id})
		}
	}

	return msgs
}

// stampFromID extracts the unix-millis prefix from a thought id of the
// form "<unix-millis>-<rand6>". Returns 0 on malformed input so a
// historic file with a non-standard name sorts last.
func stampFromID(id string) int64 {
	idx := strings.Index(id, "-")
	if idx <= 0 {
		return 0
	}
	n, err := strconv.ParseInt(id[:idx], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// DaemonOnline returns true iff the daemon's heartbeat at
// .rufio/dev.heartbeat is fresh (last_tick within devhealth.StaleThreshold).
//
// HISTORICAL (pre-v1.0.6.3): this used to stat .rufio/locks/dev.pid and
// return true whenever the file existed. That was wrong: when the
// daemon died ungracefully (SIGKILL, crash, OOM, pkill), the PID file
// was left on disk → DaemonOnline returned true forever → the TUI kept
// rendering `live` / `syncing` indicators against a dead daemon. Fixed
// in v1.0.6.3 to use the existing #154 heartbeat staleness check (the
// same signal `rufio dev --status` reports), which fails closed:
// missing/stale/malformed heartbeat ⇒ offline.
func DaemonOnline(root string) bool {
	hb, ok, _ := devhealth.ReadHeartbeat(root)
	if !ok {
		return false
	}
	return time.Since(hb.LastTick) < devhealth.StaleThreshold
}

// PollDaemonOnlineInterval is the cadence at which the TUI re-checks
// the daemon's lock-file. Plan D22.13 fixes this at 2 seconds.
const PollDaemonOnlineInterval = 2 * time.Second

// PollDaemonOnline returns a tea.Cmd that fires once after
// PollDaemonOnlineInterval, emitting a DaemonOnlineMsg with the
// current state. The Model re-issues the cmd in Update so the polling
// runs continuously without spawning a long-lived goroutine.
func PollDaemonOnline(root string) tea.Cmd {
	return tea.Tick(PollDaemonOnlineInterval, func(time.Time) tea.Msg {
		return DaemonOnlineMsg{Online: DaemonOnline(root)}
	})
}
