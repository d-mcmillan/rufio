// Package cli — `rufio demo` orchestrator.
//
// `rufio demo [--reset]` is the v1.0 ship gate: a scripted Beat-2
// scenario that wires up two agent identities (claude-code + cursor),
// spawns the daemon and a listen process, narrates 4 cognitive
// primitive calls, and finally launches the TUI in the foreground.
// When the TUI exits (or on SIGINT) the orchestrator terminates the
// spawned children with a SIGTERM → 5s grace → SIGKILL ladder so no
// zombie daemons linger after the showcase.
//
// Subprocess management is direct os/exec; the orchestrator is the
// single point of cleanup. The daemon and listen processes are
// detached from the TUI's stdio (their output goes to /dev/null) so
// the TUI controls the terminal cleanly. Narration steps (attend +
// think) are short-lived subprocess execs with overridden
// RUFIO_AGENT_ID; we wait for each to exit before continuing so the
// effects land in order.
//
// Per design line 271 + v1-spec lines 372-386. Locked decisions in
// docs/plans/2026-05-14-pr24-rufio-demo.md.
package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/swarm"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// Demo agent identities are locked by D24.2. The persona arg passed to
// swarm.Append is "demo" — it satisfies the persona regex but is never
// surfaced in the agent ids themselves (those are explicit literals).
const (
	demoPersonaTag    = "demo"
	demoAgentClaude   = "claude-code"
	demoAgentCursor   = "cursor"
	demoBeat2Subject  = "customer:5821"
	demoBeat2Content  = "Showing churn signals"
	demoBeat2Scope    = "fleet"
	demoBeat2TTL      = "300"
	demoBeat2Intent   = "watching"
	demoDaemonTimeout = 5 * time.Second
	demoCleanupGrace  = 5 * time.Second
	demoNarrationGap1 = 2 * time.Second
	demoNarrationGap2 = 1 * time.Second
)

// demoSubdirsToReset is the on-disk surface area --reset nukes before
// the demo seeds state. Listed explicitly (rather than a recursive
// live/ wipe) so any out-of-list subtree the operator added by hand
// survives — only the demo's own working set is touched. Per D24.3.
var demoSubdirsToReset = []string{
	"live/inbox",
	"live/outbox",
	"live/confirms",
	"live/promoted",
	"live/retracted",
	"live/expired",
	"live/summons",
	"live/channels",
	"live/goals",
	"live/attention",
	"live/reasoning",
}

// envSkipTUI is a TEST-ONLY env override. When set, runDemo skips the
// foreground TUI launch and returns nil after narration so the
// integration test can exercise the spawn + narration + cleanup path
// without needing a TTY. Documented inline per D24.13; NO public flag.
const envSkipTUI = "RUFIO_DEMO_SKIP_TUI"

// NewDemoCmd returns the `rufio demo` Cobra command. Single flag:
// --reset (DESTRUCTIVE) per D24.1.
func NewDemoCmd() *cobra.Command {
	var resetFlag bool
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Launch the scripted Beat-2 showcase + Bubble Tea TUI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err == nil {
				err = runDemo(cwd, resetFlag)
			}
			if err != nil {
				HandleError("demo", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&resetFlag, "reset", false, "nuke live/ subtrees before starting (DESTRUCTIVE)")
	return cmd
}

// runDemo is the pure orchestration body. Order:
//  1. Resolve project root (NotInProjectError if outside).
//  2. Pre-flight: reset live/ if --reset, else assert inbox empty.
//  3. Seed claude-code + cursor via swarm.Append (direct lib call).
//  4. Spawn daemon, wait for .rufio/locks/dev.pid (≤5s).
//  5. Spawn listen --as=cursor.
//  6. Narration: attend (cursor) → 2s → think (claude-code) → 1s.
//  7. Launch TUI in the foreground (or return early if SKIP_TUI set).
//
// Cleanup of spawned children happens via a deferred closure that
// reads the children slice by reference — see the closure body. SIGINT
// in a goroutine triggers the same cleanup path and exits with 130
// (POSIX 128 + SIGINT).
//
// The cleanup defer captures a pointer to the children slice so
// appends in this function are visible to the deferred call. (The
// classic Go gotcha: `defer cleanup(children, ...)` evaluates the
// slice value at the defer-call site, which is empty.)
func runDemo(cwd string, reset bool) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}

	if err := checkOrResetLiveState(root, reset); err != nil {
		return err
	}

	selfPath, err := os.Executable()
	if err != nil || selfPath == "" {
		selfPath = os.Args[0]
	}

	if err := seedDemoIdentities(root); err != nil {
		return err
	}

	// Children slice + mutex (the SIGINT goroutine reads it; runDemo
	// writes it). The deferred cleanup uses the closure form so
	// late-appended children are still visible.
	var (
		children    []*exec.Cmd
		childrenMu  sync.Mutex
		cleanupOnce sync.Once
	)
	doCleanup := func() {
		cleanupOnce.Do(func() {
			childrenMu.Lock()
			snapshot := append([]*exec.Cmd(nil), children...)
			childrenMu.Unlock()
			cleanupChildren(snapshot, demoCleanupGrace)
		})
	}
	defer doCleanup()

	// SIGINT/SIGTERM goroutine. On signal: cleanup, then exit with the
	// conventional 128 + signal number. We exit explicitly (not
	// re-raise) so the goroutine doesn't race the main return path.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	sigDone := make(chan struct{})
	go func() {
		select {
		case sig := <-sigCh:
			doCleanup()
			code := 130 // 128 + SIGINT default
			if sig == syscall.SIGTERM {
				code = 143 // 128 + SIGTERM
			}
			os.Exit(code)
		case <-sigDone:
			return
		}
	}()
	defer close(sigDone)

	fmt.Fprintln(os.Stderr, "→ scaffolding two agents (claude-code, cursor)...")
	fmt.Fprintln(os.Stderr, "→ starting daemon...")
	daemon, err := spawnDaemon(selfPath, root)
	if err != nil {
		return err
	}
	childrenMu.Lock()
	children = append(children, daemon)
	childrenMu.Unlock()

	if err := waitForDaemonPid(root, demoDaemonTimeout); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "→ cursor listening...")
	listen, err := spawnListen(selfPath, root, demoAgentCursor)
	if err != nil {
		return err
	}
	childrenMu.Lock()
	children = append(children, listen)
	childrenMu.Unlock()

	if err := narrate(selfPath, root); err != nil {
		return err
	}

	// Test seam (D24.13): integration test sets this to skip the TUI
	// launch and exercise spawn + narration + deferred cleanup only.
	if os.Getenv(envSkipTUI) != "" {
		return nil
	}

	fmt.Fprintln(os.Stderr, "→ launching TUI (press q to exit)...")
	return launchTui(selfPath, root)
}

// checkOrResetLiveState enforces D24.3. When reset=true, every demo
// subdir is deleted (and the parent re-created as an empty dir so the
// daemon's catch-up scan + watchSubdirs registration are happy on next
// start). When reset=false, we require live/inbox/ to be empty —
// otherwise *DemoStateError. We single out inbox because it is the
// most likely sign of an active project (routed thoughts pile up
// there as soon as the daemon runs); a stricter check would surface
// false positives on developer machines that ran isolated commands.
func checkOrResetLiveState(root string, reset bool) error {
	if reset {
		for _, sub := range demoSubdirsToReset {
			abs := filepath.Join(root, sub)
			if err := os.RemoveAll(abs); err != nil {
				return err
			}
			if err := os.MkdirAll(abs, 0o755); err != nil {
				return err
			}
		}
		return nil
	}

	inboxDir := filepath.Join(root, "live", "inbox")
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // fresh project — inbox dir absent is fine.
		}
		return err
	}
	for _, e := range entries {
		// Per-agent inbox dirs may exist (init.go doesn't create
		// them) but be empty. Treat a dir as non-empty only if it
		// contains at least one entry.
		if !e.IsDir() {
			return &rufioerr.DemoStateError{Reason: "live/inbox is non-empty; pass --reset to wipe state before the demo"}
		}
		sub, err := os.ReadDir(filepath.Join(inboxDir, e.Name()))
		if err != nil {
			return err
		}
		if len(sub) > 0 {
			return &rufioerr.DemoStateError{Reason: "live/inbox is non-empty; pass --reset to wipe state before the demo"}
		}
	}
	return nil
}

// seedDemoIdentities scaffolds the two demo agents via the swarm
// library (direct call, no subprocess — per D24.2). swarm.Append is
// idempotent on duplicate ids, so re-runs are safe: the second call
// returns both agents in `skipped`, which we ignore here (it's a demo
// helper, not a user-facing surface).
func seedDemoIdentities(root string) error {
	ts := versioning.NowISO()
	_, _, err := swarm.Append(root, demoPersonaTag,
		[]string{demoAgentClaude, demoAgentCursor}, ts)
	return err
}

// spawnDaemon execs `<self> dev --quiet` with stdio piped to
// /dev/null. The dev command writes .rufio/locks/dev.pid as soon as
// the watcher is up; the caller then waits for that file before
// continuing.
func spawnDaemon(selfPath, root string) (*exec.Cmd, error) {
	cmd := exec.Command(selfPath, "dev", "--quiet")
	cmd.Dir = root
	if err := redirectToDevNull(cmd); err != nil {
		return nil, err
	}
	// Run the daemon in its own process group so we can deliver a
	// signal to the whole tree if it ever spawns helpers (defensive —
	// today it does not).
	cmd.SysProcAttr = newProcessGroupAttr()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// spawnListen execs `<self> listen --as=<agent>` with stdio piped to
// /dev/null. The process is long-lived; cleanup tears it down.
func spawnListen(selfPath, root, agent string) (*exec.Cmd, error) {
	cmd := exec.Command(selfPath, "listen", "--as="+agent)
	cmd.Dir = root
	if err := redirectToDevNull(cmd); err != nil {
		return nil, err
	}
	cmd.SysProcAttr = newProcessGroupAttr()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// waitForDaemonPid blocks (up to timeout) for .rufio/locks/dev.pid to
// appear. Polls at 50ms — fast enough that the user doesn't notice a
// delay, slow enough that we don't melt the CPU.
func waitForDaemonPid(root string, timeout time.Duration) error {
	pidFile := filepath.Join(root, ".rufio", "locks", "dev.pid")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidFile); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return &rufioerr.DemoStateError{
		Reason: fmt.Sprintf("daemon pid file %s did not appear within %s", pidFile, timeout),
	}
}

// narrate runs the Beat-2 script: attend (cursor) → 2s → think
// (claude-code) → 1s. Each step shells out to the same binary with
// RUFIO_AGENT_ID set, so the spawned subprocesses identify as the
// scripted agents. We wait for each step to exit before moving on so
// the effects land in order (otherwise the TUI may launch before the
// thought lands and the routing is invisible).
//
// Per D24.6 + v1-spec lines 372-384.
func narrate(selfPath, root string) error {
	fmt.Fprintln(os.Stderr, "→ cursor attending to "+demoBeat2Subject+"...")
	if err := runWithAgent(selfPath, root, demoAgentCursor, []string{
		"attend",
		"--intent=" + demoBeat2Intent,
		"--entities=" + demoBeat2Subject,
	}); err != nil {
		return fmt.Errorf("narrate attend: %w", err)
	}

	time.Sleep(demoNarrationGap1)

	fmt.Fprintln(os.Stderr, "→ claude-code thinking...")
	if err := runWithAgent(selfPath, root, demoAgentClaude, []string{
		"think",
		"--type=hypothesis",
		"--subject=" + demoBeat2Subject,
		"--content=" + demoBeat2Content,
		"--scope=" + demoBeat2Scope,
		"--ttl=" + demoBeat2TTL,
	}); err != nil {
		return fmt.Errorf("narrate think: %w", err)
	}

	time.Sleep(demoNarrationGap2)
	return nil
}

// runWithAgent execs `<self> <args...>` with RUFIO_AGENT_ID=<agent>,
// inheriting the rest of the environment. Output is piped to the
// orchestrator's stderr so the operator sees any narration failures
// before the TUI launches.
func runWithAgent(selfPath, root, agent string, args []string) error {
	cmd := exec.Command(selfPath, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "RUFIO_AGENT_ID="+agent)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// launchTui execs `<self> tui` with the orchestrator's stdio passed
// through. The TUI takes over the terminal until the user quits; on
// return we fall through to the cleanup defer.
func launchTui(selfPath, root string) error {
	cmd := exec.Command(selfPath, "tui")
	cmd.Dir = root
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// cleanupChildren delivers SIGTERM to each child, waits up to
// grace for them to exit, then SIGKILLs the holdouts. Safe to call
// with a nil or empty slice; safe to call concurrently (each *exec.Cmd
// is only operated on once because the caller holds the slice).
//
// On platforms with process groups we signal the group so any
// hypothetical grandchildren are caught too.
func cleanupChildren(children []*exec.Cmd, grace time.Duration) {
	if len(children) == 0 {
		return
	}
	// Step 1: SIGTERM in reverse order (listen first so it doesn't try
	// to read from the daemon as it shuts down).
	for i := len(children) - 1; i >= 0; i-- {
		c := children[i]
		if c == nil || c.Process == nil {
			continue
		}
		_ = signalProcessGroup(c, syscall.SIGTERM)
	}

	// Step 2: wait up to grace for everyone to exit. Each child gets
	// its own goroutine so a wedged child can't starve the others.
	done := make(chan struct{}, len(children))
	for _, c := range children {
		c := c
		if c == nil || c.Process == nil {
			done <- struct{}{}
			continue
		}
		go func() {
			_ = c.Wait()
			done <- struct{}{}
		}()
	}
	deadline := time.After(grace)
	for range children {
		select {
		case <-done:
		case <-deadline:
			// Step 3: SIGKILL holdouts. Note we still need to drain
			// the remaining done sends after we kill, but the goroutines
			// will publish them as the processes die — we just stop
			// waiting on them here.
			for _, c := range children {
				if c == nil || c.Process == nil {
					continue
				}
				_ = signalProcessGroup(c, syscall.SIGKILL)
			}
			return
		}
	}
}

// redirectToDevNull rewires the command's stdio to /dev/null so its
// output cannot leak into the orchestrator's terminal (especially
// once the TUI takes the screen). Three separate opens because each
// closer is independent and the os/exec contract owns each FD.
func redirectToDevNull(cmd *exec.Cmd) error {
	in, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		in.Close()
		return err
	}
	errF, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		in.Close()
		out.Close()
		return err
	}
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = errF
	return nil
}
