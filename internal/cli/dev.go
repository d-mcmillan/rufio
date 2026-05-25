package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/banner"
	"github.com/d-mcmillan/rufio/internal/lib/autopromote"
	"github.com/d-mcmillan/rufio/internal/lib/devhealth"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/retract"
	"github.com/d-mcmillan/rufio/internal/lib/routing"
	"github.com/d-mcmillan/rufio/internal/lib/summon"
	"github.com/d-mcmillan/rufio/internal/lib/ttlsweep"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// engineDispatch is one row in the daemon's engine dispatch table.
// Adding a new daemon engine = appending one entry to the table.
type engineDispatch struct {
	Kind       string // FileEvent.Kind ("add", "change", "unlink")
	PathPrefix string // event path must start with this
	PathSuffix string // event path must end with this (e.g., ".gdl")
	Handler    func(root, eventPath string) error
}

// FileEvent is what the daemon emits for each filesystem change. The kind
// strings ("add", "change", "unlink") match the TS chokidar vocabulary so
// downstream tooling stays consistent across the port.
type FileEvent struct {
	Kind string `json:"kind"`
	Path string `json:"path"` // POSIX-form, project-root-relative
	TS   string `json:"ts"`
}

// EventHandler is the hookable interface week 2+ will plug routing into.
// Week 1's daemon ships NoopHandler.
type EventHandler func(event FileEvent) error

// NoopHandler is the default — does nothing. Retained for test fixtures
// and future opt-out scenarios.
func NoopHandler(event FileEvent) error { return nil }

// defaultEventHandler returns the production engine dispatch table.
// Adding a new engine (PR #13 AutoPromoteHandler, PR #14 TTLSweeper,
// PR #19 GoalOverlapHandler) = appending one entry to the table.
// Resolves WK2-RETRACT-6.
func defaultEventHandler(root string) EventHandler {
	table := []engineDispatch{
		{
			Kind: "add", PathPrefix: "live/retracted/", PathSuffix: ".gdl",
			Handler: func(root, path string) error {
				targetID := strings.TrimSuffix(filepath.Base(path), ".gdl")
				return retract.PropagateRetract(root, targetID)
			},
		},
		{
			Kind: "add", PathPrefix: "live/outbox/", PathSuffix: ".gdl",
			Handler: func(root, path string) error {
				return routing.RouteThought(root, filepath.Join(root, path))
			},
		},
		{
			Kind: "add", PathPrefix: "live/summons/pending/", PathSuffix: ".gdl",
			Handler: func(root, path string) error {
				return routing.RouteSummon(root, filepath.Join(root, path))
			},
		},
		// Channel @say messages live at
		// live/channels/active/<ch-id>/messages/<msg-id>.gdl. The PathPrefix
		// match also fires on the channel's meta.gdl writes (open, leave,
		// close) — skip those via the basename check so we only route
		// actual message files.
		{
			Kind: "add", PathPrefix: "live/channels/active/", PathSuffix: ".gdl",
			Handler: func(root, path string) error {
				if filepath.Base(path) == "meta.gdl" {
					return nil
				}
				return routing.RouteChannelMessage(root, filepath.Join(root, path))
			},
		},
		// Active goals trigger entity-overlap detection: scan all other
		// agents' active goals for shared entity ids and deliver
		// @goal-overlap notifications to both inboxes. Terminal-state
		// goals (completed/abandoned) are intentionally NOT wired — only
		// live goals participate in overlap detection.
		{
			Kind: "add", PathPrefix: "live/goals/active/", PathSuffix: ".gdl",
			Handler: func(root, path string) error {
				return routing.RouteGoalOverlap(root, filepath.Join(root, path))
			},
		},
		// Confirms are APPENDED to live/confirms/<id>.gdl, so fsnotify emits
		// Create for the 1st confirm and Write for the 2nd+. Both must fire
		// the autopromote engine so the threshold check runs after every
		// confirm landing. autopromote.Handle is idempotent (the
		// live/promoted/<id>.gdl marker short-circuits future evaluations).
		{
			Kind: "add", PathPrefix: "live/confirms/", PathSuffix: ".gdl",
			Handler: func(root, path string) error {
				targetID := strings.TrimSuffix(filepath.Base(path), ".gdl")
				return autopromote.Handle(root, targetID)
			},
		},
		{
			Kind: "change", PathPrefix: "live/confirms/", PathSuffix: ".gdl",
			Handler: func(root, path string) error {
				targetID := strings.TrimSuffix(filepath.Base(path), ".gdl")
				return autopromote.Handle(root, targetID)
			},
		},
	}
	return func(event FileEvent) error {
		for _, d := range table {
			if event.Kind == d.Kind &&
				strings.HasPrefix(event.Path, d.PathPrefix) &&
				strings.HasSuffix(event.Path, d.PathSuffix) {
				return d.Handler(root, event.Path)
			}
		}
		return nil
	}
}

// watchSubdirs is the canonical list of directories the daemon watches.
// internal/ and .rufio/ are NOT watched — exclusion-by-construction.
//
// live/channels/closed is registered for defense-in-depth (pre-create
// so fsnotify won't silently skip the path); no engine consumes events
// from there. Channel message events fire under live/channels/active/.
var watchSubdirs = []string{"given", "learned", "live/outbox", "live/retracted", "live/confirms", "live/promoted", "live/expired", "live/summons/pending", "live/channels/active", "live/channels/closed", "live/goals/active"}

// NewDevCmd returns the `rufio dev` Cobra command. The version is threaded
// through so the compact banner shows the right value.
func NewDevCmd(version string) *cobra.Command {
	var quietFlag, noColorFlag, forceFlag, statusFlag bool
	var logFile string
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Foreground substrate daemon (chokidar/fsnotify watcher)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err != nil {
				HandleError("dev", err)
				return nil
			}
			// --status is the inspection path: parse the heartbeat and
			// print a one-line liveness report, then exit. Never starts a
			// daemon and never blocks. #154.
			if statusFlag {
				root, rerr := paths.FindProjectRoot(cwd)
				if rerr != nil {
					HandleError("dev", rerr)
					return nil
				}
				if err := runDevStatus(root, opts, time.Now); err != nil {
					HandleError("dev", err)
				}
				return nil
			}
			err = runDev(cwd, opts, version, logFile, forceFlag, defaultEventHandler)
			if err != nil {
				HandleError("dev", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "silence banner, watching line, and the per-event watch log")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	cmd.Flags().StringVar(&logFile, "log", "", "append the per-event watch log to this file (never the terminal; honoured even under --quiet)")
	// NOTE: avoid backticks in the description — pflag promotes
	// backtick-quoted content to the flag's value-placeholder, so
	// `rufio dev` here would render as `--force rufio dev` in --help
	// (issue #123). Plain single quotes are the safe choice.
	cmd.Flags().BoolVar(&forceFlag, "force", false, "bypass the daemon singleton guard and start anyway (escape hatch for a known-stale lock; normally 'rufio dev' refuses to start a 2nd daemon on the same project)")
	// #154 daemon supervision: --status prints a one-line liveness report
	// (pid, uptime, last-heartbeat age) from .rufio/dev.heartbeat and
	// exits. Inspection-only; never starts a daemon.
	cmd.Flags().BoolVar(&statusFlag, "status", false, "print daemon liveness (pid/uptime/heartbeat age) and exit; reads .rufio/dev.heartbeat")
	// NOTE: --json deliberately NOT accepted on dev for week 1 — there's no
	// structured event-stream format yet. Accepting --json as a no-op flag
	// would be a half-built feature per CLAUDE.md. Will be wired when the
	// daemon grows a JSONL event-stream mode in a later phase.
	return cmd
}

// runDev sets up the watcher, registers each watch dir, dispatches events
// via the handler, and blocks until SIGINT/SIGTERM. Returns 0 on clean
// shutdown.
//
// The handlerFactory parameter receives the project root once resolved
// and returns the EventHandler. This lets handlers close over the root
// (e.g. defaultEventHandler dispatching to retract.PropagateRetract).
//
// logFile is the optional --log opt-in: when non-empty, the per-event
// watch log is appended there (created if absent). This is the
// footgun-free observability path — the file is written regardless of
// --quiet but NEVER goes to the terminal, so it cannot corrupt a
// full-screen `rufio tui` sharing the shell.
//
// force is the --force escape hatch: when true the daemon singleton
// guard (#133) is skipped entirely, so the daemon starts (and the
// existing writeDevPid overwrites the pidfile) even if a live same-host
// daemon already owns the project. The documented break-glass for a
// known-stale/edge lock.
func runDev(cwd string, opts output.RenderOpts, version, logFile string, force bool, handlerFactory func(root string) EventHandler) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	// #154 daemon supervision: top-level panic recover so an unhandled
	// crash never disappears silently. The recovered panic is persisted
	// to .rufio/dev.crash.log AND re-emitted to stderr — the log path
	// survives even if the caller redirected stderr somewhere lost (the
	// R14 vet failure mode).
	defer recoverDevPanic(root)
	handler := handlerFactory(root)

	// Optional --log sink for the per-event watch log. Append mode so a
	// restarted daemon doesn't truncate an operator's tail -f. This is
	// purely an observability channel — open failure is fatal (the
	// operator explicitly asked for it; silently dropping it would be a
	// half-built feature) but the daemon's functional behaviour does not
	// depend on it.
	var logSink io.Writer
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		logSink = f
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Ensure all watched subdirs exist before registering watchers — fsnotify
	// silently skips non-existent paths, so a missing subdir would create a
	// gap in the event stream (e.g., live/retracted/ on fresh projects;
	// would-be-Critical bug fixed in PR #8).
	for _, sub := range watchSubdirs {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return err
		}
	}

	// fsnotify doesn't recurse — register each watched subdir + their
	// existing children. New children get added on Create events below.
	for _, sub := range watchSubdirs {
		subRoot := filepath.Join(root, sub)
		if err := addRecursive(watcher, subRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	// Catch-up scan: replay any retracts that landed while the daemon was
	// down. Idempotent per design §2.I + D8.7.
	retractedDir := filepath.Join(root, "live", "retracted")
	if entries, err := os.ReadDir(retractedDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
				continue
			}
			targetID := strings.TrimSuffix(e.Name(), ".gdl")
			if err := retract.PropagateRetract(root, targetID); err != nil {
				fmt.Fprintf(os.Stderr, "catch-up retract propagation %s: %v\n", targetID, err)
			}
		}
	}

	// Catch-up scan for outbox: replay any thoughts that landed while the
	// daemon was down. Idempotent per design §2.I (RouteThought skips
	// inbox files that already exist). Order: retract catch-up first
	// (acts on existing inboxes), then routing (creates new inboxes) —
	// they don't interfere.
	outboxRoot := filepath.Join(root, "live", "outbox")
	if entries, err := os.ReadDir(outboxRoot); err == nil {
		for _, agentDir := range entries {
			if !agentDir.IsDir() {
				continue
			}
			agentPath := filepath.Join(outboxRoot, agentDir.Name())
			thoughts, err := os.ReadDir(agentPath)
			if err != nil {
				continue
			}
			for _, th := range thoughts {
				if th.IsDir() || !strings.HasSuffix(th.Name(), ".gdl") {
					continue
				}
				thoughtPath := filepath.Join(agentPath, th.Name())
				if err := routing.RouteThought(root, thoughtPath); err != nil {
					fmt.Fprintf(os.Stderr, "catch-up routing %s: %v\n", thoughtPath, err)
				}
			}
		}
	}

	// Catch-up scan for summons: replay any pending summons that landed
	// while the daemon was down. Idempotent per design §2.I (RouteSummon
	// skips already-delivered inbox files).
	summonsPendingRoot := filepath.Join(root, "live", "summons", "pending")
	if entries, err := os.ReadDir(summonsPendingRoot); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
				continue
			}
			summonPath := filepath.Join(summonsPendingRoot, e.Name())
			if err := routing.RouteSummon(root, summonPath); err != nil {
				fmt.Fprintf(os.Stderr, "catch-up summon routing %s: %v\n", e.Name(), err)
			}
		}
	}

	// Catch-up scan for confirms: replay any auto-promote decisions that
	// landed while the daemon was down. Idempotent per design §2.I
	// (autopromote.Handle skips already-promoted thoughts via the
	// live/promoted/<id>.gdl marker check).
	confirmsRoot := filepath.Join(root, "live", "confirms")
	if entries, err := os.ReadDir(confirmsRoot); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
				continue
			}
			targetID := strings.TrimSuffix(e.Name(), ".gdl")
			if err := autopromote.Handle(root, targetID); err != nil {
				fmt.Fprintf(os.Stderr, "catch-up autopromote %s: %v\n", targetID, err)
			}
		}
	}

	// Catch-up TTL sweep: process any thoughts that expired while the
	// daemon was down. Idempotent per design §2.I (FindExpired skips
	// records already moved to live/expired/).
	if n, err := ttlsweep.Sweep(root, time.Now, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "catch-up ttl sweep: %v\n", err)
	} else if n > 0 && !opts.Quiet {
		fmt.Fprintf(os.Stderr, "catch-up ttl sweep: moved %d expired records\n", n)
	}

	// Catch-up summon TTL sweep: move expired pending summons to
	// live/summons/expired/. Idempotent per D16.10 (the move IS the
	// audit; missing pending file → skip).
	if n, err := summon.SweepExpired(root, time.Now, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "catch-up summon sweep: %v\n", err)
	} else if n > 0 && !opts.Quiet {
		fmt.Fprintf(os.Stderr, "catch-up summon sweep: moved %d expired summons\n", n)
	}

	// Catch-up scan for channel messages: replay any @say files that
	// landed while the daemon was down. Idempotent per design §2.I
	// (RouteChannelMessage delivers via deliverToInbox which skips
	// already-delivered inbox files).
	channelsActiveRoot := filepath.Join(root, "live", "channels", "active")
	if entries, err := os.ReadDir(channelsActiveRoot); err == nil {
		for _, ch := range entries {
			if !ch.IsDir() {
				continue
			}
			messagesDir := filepath.Join(channelsActiveRoot, ch.Name(), "messages")
			msgs, err := os.ReadDir(messagesDir)
			if err != nil {
				continue
			}
			for _, msg := range msgs {
				if msg.IsDir() || !strings.HasSuffix(msg.Name(), ".gdl") {
					continue
				}
				msgPath := filepath.Join(messagesDir, msg.Name())
				if err := routing.RouteChannelMessage(root, msgPath); err != nil {
					fmt.Fprintf(os.Stderr, "catch-up channel-message %s: %v\n", msg.Name(), err)
				}
			}
		}
	}

	// Catch-up scan for active goals: replay any goal-overlap detection
	// that landed while the daemon was down. Idempotent per design §2.I
	// (deliverOverlapFile skips existing inbox files).
	goalsActiveRoot := filepath.Join(root, "live", "goals", "active")
	if entries, err := os.ReadDir(goalsActiveRoot); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".gdl") {
				continue
			}
			goalPath := filepath.Join(goalsActiveRoot, e.Name())
			if err := routing.RouteGoalOverlap(root, goalPath); err != nil {
				fmt.Fprintf(os.Stderr, "catch-up goal-overlap %s: %v\n", e.Name(), err)
			}
		}
	}

	if !opts.Quiet {
		banner.PrintCompact(banner.Options{Version: version})
	}
	output.WriteOut("  watching "+root, opts)

	// Write the daemon pid file under .rufio/locks/dev.pid. The TUI's
	// online indicator and `rufio demo`'s orchestrator both block on
	// its existence (design line 228 — daemon singleton). Format is
	// `<hostname>:<pid>:<start-ts-unix>`; multi-line is fine for the
	// readers (they only check existence). Removed on clean shutdown.
	pidFile := filepath.Join(root, ".rufio", "locks", "dev.pid")

	// Singleton guard (#133): if a live same-host daemon already owns
	// this project, refuse to start — a 2nd `rufio dev` would
	// duplicate/corrupt event processing (surfaced LIVE when an agent
	// spawned its own daemon on top of the running one). Checked BEFORE
	// writeDevPid so we never clobber the live daemon's pidfile, and
	// before the blocking watch loop. --force bypasses this. A
	// stale/dead/foreign/missing pidfile is NOT a conflict — the
	// legitimate "previous daemon died, restart it" path still proceeds
	// (writeDevPid then overwrites the stale file exactly as today).
	if !force {
		if conflictPID, conflict := devLockConflict(pidFile); conflict {
			return fmt.Errorf("a daemon is already running for this project (pid %d); stop it first (kill %d) or pass --force", conflictPID, conflictPID)
		}
	}

	if err := writeDevPid(pidFile); err != nil {
		return err
	}
	defer os.Remove(pidFile)

	// Channel for the signal-driven shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// Periodic TTL sweep: drives the TTLSweeper engine on a fixed cadence
	// (D14.1). Independent of fsnotify events because expiry is a
	// wall-clock condition, not a filesystem event.
	ttlTicker := time.NewTicker(ttlsweep.TickInterval)
	defer ttlTicker.Stop()

	// #154 daemon supervision: the heartbeat ticker writes
	// .rufio/dev.heartbeat every devhealth.TickInterval. Readers (rufio
	// dev --status, rufio fleet) parse that file to surface daemon
	// liveness. Initial write happens immediately so a freshly-started
	// daemon is visible without waiting a full interval.
	startedAt := time.Now()
	if err := devhealth.WriteHeartbeat(root, os.Getpid(), startedAt, startedAt); err != nil {
		// Observability path; never gate startup on it.
		fmt.Fprintf(os.Stderr, "heartbeat init: %v\n", err)
	}
	heartbeatTicker := time.NewTicker(devhealth.TickInterval)
	defer heartbeatTicker.Stop()
	// Best-effort cleanup of the heartbeat file on clean shutdown so a
	// stopped daemon doesn't leave a stale "I'm running" signal.
	defer os.Remove(devhealth.HeartbeatPath(root))

	for {
		select {
		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			handleEvent(ev, root, handler, opts, logSink, watcher)
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			output.WriteErr("watcher error: " + watchErr.Error())
		case <-ttlTicker.C:
			if _, err := ttlsweep.Sweep(root, time.Now, os.Stderr); err != nil {
				fmt.Fprintf(os.Stderr, "ttl sweep: %v\n", err)
			}
			if _, err := summon.SweepExpired(root, time.Now, os.Stderr); err != nil {
				fmt.Fprintf(os.Stderr, "summon sweep: %v\n", err)
			}
		case <-heartbeatTicker.C:
			if err := devhealth.WriteHeartbeat(root, os.Getpid(), startedAt, time.Now()); err != nil {
				fmt.Fprintf(os.Stderr, "heartbeat: %v\n", err)
			}
		case <-sigCh:
			return nil
		}
	}
}

// runHeartbeatTicker writes .rufio/dev.heartbeat on `interval` until
// `stop` closes. Extracted for testability: the production daemon runs
// the equivalent select-arm above (so the heartbeat shares the runDev
// goroutine and dies WITH the daemon), but tests need a standalone
// helper they can drive at sub-second intervals to verify the cadence
// without depending on the full watch loop.
func runHeartbeatTicker(root string, pid int, startedAt time.Time, interval time.Duration, stop <-chan struct{}) {
	// Initial write so the first tick is visible immediately.
	_ = devhealth.WriteHeartbeat(root, pid, startedAt, time.Now())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = devhealth.WriteHeartbeat(root, pid, startedAt, time.Now())
		case <-stop:
			return
		}
	}
}

// runDevStatus is `rufio dev --status`: parse the heartbeat and print a
// single-line liveness report on stdout. Never starts a daemon. Errors
// are filesystem-level only; a missing/malformed heartbeat surfaces as
// "not running" rather than an error.
func runDevStatus(root string, opts output.RenderOpts, now func() time.Time) error {
	st := devhealth.Status(root, now())
	var line string
	switch st.State {
	case devhealth.StateNotRunning:
		line = "daemon: not running (no heartbeat)"
	case devhealth.StateStale:
		line = fmt.Sprintf(
			"daemon: STALE - last heartbeat %s ago; routing may be delayed (pid %d, uptime %s)",
			formatAge(st.LastTickAge), st.PID, formatAge(st.Uptime),
		)
	default:
		line = fmt.Sprintf(
			"daemon: ok (pid %d, uptime %s, heartbeat %s ago)",
			st.PID, formatAge(st.Uptime), formatAge(st.LastTickAge),
		)
	}
	output.WriteData(line, opts)
	return nil
}

// recoverDevPanic is the deferred panic-recover the daemon installs at
// the top of runDev. The recovered value is captured + persisted to
// .rufio/dev.crash.log (so the trace survives a redirect-lost stderr)
// AND re-emitted to stderr (so an attentive operator sees it live).
// After logging the panic is RE-RAISED so the process still exits non-
// zero — the supervision goal is "no silent death", not "swallow all
// crashes".
func recoverDevPanic(root string) {
	r := recover()
	if r == nil {
		return
	}
	msg := fmt.Sprintf("panic: %v", r)
	stack := devhealth.CaptureStack()
	if err := devhealth.WriteCrashLog(root, msg, stack); err != nil {
		fmt.Fprintf(os.Stderr, "crash log: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "%s\n%s\n", msg, stack)
}

// formatAge renders a duration with whole-second precision for the
// status/header lines. Sub-second precision is useless to operators and
// noisy in output ("4.001s ago" vs "4s ago").
func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Truncate(time.Second).String()
}

func handleEvent(ev fsnotify.Event, root string, handler EventHandler, opts output.RenderOpts, logSink io.Writer, watcher *fsnotify.Watcher) {
	kind := ""
	switch {
	case ev.Op&fsnotify.Create != 0:
		kind = "add"
		// Newly-created subdirs need to be added to the watcher so we
		// observe their children.
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			_ = addRecursive(watcher, ev.Name)
			// WK2-ROUTE-1 fix: replay any files that landed in the new dir
			// between MkdirAll and watcher.Add (the fsnotify race window).
			// On macOS (kqueue) this race is materially more likely than on
			// Linux (inotify). Without this replay, the first thought from a
			// brand-new agent can be silently dropped.
			replayDirContents(handler, root, ev.Name)
		}
	case ev.Op&fsnotify.Write != 0:
		kind = "change"
	case ev.Op&fsnotify.Remove != 0 || ev.Op&fsnotify.Rename != 0:
		kind = "unlink"
	default:
		return // ignored op (Chmod, etc.)
	}

	rel, err := filepath.Rel(root, ev.Name)
	if err != nil {
		rel = ev.Name
	}
	event := FileEvent{
		Kind: kind,
		Path: filepath.ToSlash(rel),
		TS:   versioning.NowISO(),
	}
	// The human-readable per-event watch log. This is OBSERVABILITY, not
	// load-bearing data: the daemon's functional work (dispatch via
	// handler(), auto-promote, routing, TTL) happens regardless of whether
	// any of this line is written. --quiet now means *quiet* — it silences
	// this log too, not just the banner + "watching ..." line. Rationale:
	// when `rufio dev` shares a terminal with the full-screen `rufio tui`
	// watch pane, an un-suppressed per-event line punches through Bubble
	// Tea's alt-screen and corrupts it the moment an agent writes. Watch-
	// event observability stays reachable WITHOUT that footgun via the
	// explicit --log <file> opt-in (logSink), which is written even under
	// --quiet but only ever to the file, never the terminal.
	logLine := fmt.Sprintf("%s  %s  %s", event.TS, event.Kind, event.Path)
	if !opts.Quiet {
		output.WriteData(logLine, opts)
	}
	if logSink != nil {
		fmt.Fprintln(logSink, logLine)
	}
	if err := handler(event); err != nil {
		output.WriteErr("event handler error: " + err.Error())
	}
}

// devLockConflict is the daemon singleton guard (#133). It inspects an
// EXISTING .rufio/locks/dev.pid and reports whether a *live same-host*
// daemon already owns this project — i.e. whether starting another
// `rufio dev` here would duplicate/corrupt event processing.
//
// It is a pure, blocking-loop-free decision function (unit-testable in
// isolation, the convention the sibling dev_*_test.go files follow). It
// reads the pidfile written by writeDevPid in the fixed format
// `<hostname>:<pid>:<start-ts-unix>` (it does NOT mutate the file or the
// format). Every failure mode FAILS SAFE — proceed (no conflict), never
// crash the daemon on a bad/legacy/foreign lock file:
//
//   - missing / unreadable / empty pidfile → no conflict (first run, or
//     scaffolded-but-no-daemon).
//   - malformed / legacy / partial content → no conflict (defensive).
//   - hostname != this host → no conflict (a pid from another machine on
//     a shared/networked FS is meaningless locally).
//   - same host, pid not a running process (ESRCH) → no conflict (the
//     previous daemon died; the existing writeDevPid then overwrites the
//     stale file exactly as it does today — the legitimate restart path).
//   - same host, pid alive (signal 0 → nil or EPERM) → CONFLICT.
//
// pid <= 0 and pid == our own pid are treated as no-conflict defensively
// (a daemon must never refuse to start because the lock names itself or
// is nonsensical).
func devLockConflict(pidFile string) (conflictPID int, conflict bool) {
	return devLockConflictForPID(pidFile, os.Getpid())
}

// devLockConflictForPID is devLockConflict with the "our own pid"
// injected so the self-pid defensive branch is deterministically
// testable. Production always calls devLockConflict (ownPID =
// os.Getpid()).
func devLockConflictForPID(pidFile string, ownPID int) (conflictPID int, conflict bool) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, false // missing / unreadable → first run; proceed
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		return 0, false // empty / whitespace-only → defensive; proceed
	}
	parts := strings.Split(line, ":")
	if len(parts) < 2 {
		return 0, false // not `<host>:<pid>:...` → legacy/garbage; proceed
	}
	recordedHost := parts[0]
	pid, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || pid <= 0 {
		return 0, false // non-numeric / non-positive pid → defensive; proceed
	}
	if pid == ownPID {
		return 0, false // names our own process → never self-conflict
	}
	host, err := os.Hostname()
	if err != nil {
		host = "unknown" // mirror writeDevPid's fallback
	}
	if recordedHost != host {
		return 0, false // foreign host → remote pid; meaningless locally
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, false // can't reference it → treat as stale; proceed
	}
	// signal 0 probes liveness without delivering a signal: nil ⇒ alive;
	// EPERM ⇒ alive but owned by another user (still a running daemon);
	// ESRCH / anything else ⇒ no such process ⇒ stale.
	switch sigErr := proc.Signal(syscall.Signal(0)); {
	case sigErr == nil:
		return pid, true
	case errors.Is(sigErr, syscall.EPERM):
		return pid, true
	default:
		return 0, false
	}
}

// writeDevPid records the running daemon's identity at .rufio/locks/dev.pid
// in the design-line-228 format `<hostname>:<pid>:<start-ts-unix>`. The
// parent directory is created defensively (init.go scaffolds it, but the
// daemon may be run from a partially-initialised project too).
//
// Readers (the TUI's daemon-online check; `rufio demo`'s spawn deadline)
// treat the file as an existence flag — they do not parse it. The format
// is fixed so a future singleton-reclaim path can inspect it.
func writeDevPid(pidFile string) error {
	if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
		return err
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	contents := fmt.Sprintf("%s:%d:%d", hostname, os.Getpid(), time.Now().Unix())
	return os.WriteFile(pidFile, []byte(contents+"\n"), 0o644)
}

// addRecursive walks dir and adds it + every subdirectory to the watcher.
// fsnotify doesn't natively support recursive watches.
func addRecursive(watcher *fsnotify.Watcher, dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return watcher.Add(path)
		}
		return nil
	})
}

// replayDirContents walks dir and synthesizes FileEvent{Kind:"add"} events
// for each regular file underneath, dispatching through the handler. This
// catches the fsnotify race where a directory is created alongside its first
// file: the dir CREATE event fires, the watcher is added, but the file CREATE
// event may have already fired before the watcher caught up. WK2-ROUTE-1 fix.
//
// Errors during walk are logged to stderr but don't abort (best-effort,
// mirrors the routing engine's error policy in catch-up scan above).
func replayDirContents(handler EventHandler, root, dir string) {
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		// Build the project-root-relative POSIX path the handler expects.
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		ev := FileEvent{
			Kind: "add",
			Path: filepath.ToSlash(rel),
			TS:   versioning.NowISO(),
		}
		if err := handler(ev); err != nil {
			fmt.Fprintf(os.Stderr, "replay handler error %s: %v\n", p, err)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "replay walk error %s: %v\n", dir, err)
	}
}
