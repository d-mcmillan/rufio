// Package cli — tests for the --quiet / --log gating of the daemon's
// per-event watch log (dev.go handleEvent).
//
// Defect being guarded: `rufio dev --quiet` historically suppressed ONLY
// the startup banner + the "watching ..." line; the per-event watch log
// (`<ts>  add/change/unlink  <path>`) was emitted via output.WriteData
// which deliberately ignores --quiet. When `rufio dev` shared a terminal
// with the full-screen `rufio tui` watch pane, that log punched through
// the Bubble Tea alt-screen and corrupted it.
//
// Contract these tests lock:
//
//	(a) --quiet (opts.Quiet=true)  → ZERO per-event lines on stdout.
//	(b) the daemon STILL functions under --quiet — the event is still
//	    dispatched to the handler (auto-promote still fires). Proven with
//	    the dev_autopromote_test.go pattern: confirms seeded at threshold
//	    but no matching thought → autopromote.Handle returns
//	    *NoSuchThoughtError. Observing that error proves dispatch ran.
//	(c) the --log <file> opt-in captures the per-event line to the file
//	    EVEN under --quiet, and never to the terminal.
//	(d) backward-compatible default: WITHOUT --quiet and WITHOUT --log,
//	    the per-event line still prints to stdout exactly as before.
//
// handleEvent is exercised directly (not the full runDev loop) because
// runDev blocks on SIGINT/SIGTERM — the same unit-not-blocking-loop
// convention the sibling dev_*_test.go files use (they call
// defaultEventHandler directly). handleEvent is precisely where the
// gating decision lives.
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsnotify/fsnotify"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. output.WriteData writes to os.Stdout, so this
// captures the per-event watch log exactly as a terminal would see it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

// confirmsCreateEvent builds the fsnotify Create event the daemon would
// observe when an agent appends the first @confirm to
// live/confirms/<targetID>.gdl. A regular file path (not a dir) so
// handleEvent skips the dir-watch/replay branch and flows straight to the
// per-event-log emit + handler dispatch.
func confirmsCreateEvent(root, targetID string) fsnotify.Event {
	return fsnotify.Event{
		Name: filepath.Join(root, "live", "confirms", targetID+".gdl"),
		Op:   fsnotify.Create,
	}
}

// TestHandleEvent_Quiet_NoPerEventLogOnStdout asserts (a): with
// opts.Quiet, handleEvent writes NOTHING to stdout for a substrate write.
func TestHandleEvent_Quiet_NoPerEventLogOnStdout(t *testing.T) {
	root := t.TempDir()
	ev := confirmsCreateEvent(root, "1727000000-quiet1")
	opts := output.RenderOpts{Quiet: true}

	out := captureStdout(t, func() {
		handleEvent(ev, root, NoopHandler, opts, nil, nil)
	})

	if out != "" {
		t.Errorf("--quiet must suppress the per-event watch log on stdout; got %q", out)
	}
}

// TestHandleEvent_Quiet_DaemonStillDispatches asserts (b): under
// --quiet the event is STILL dispatched to the handler — daemon
// functional behaviour is unchanged. Confirms are seeded at threshold
// with no matching thought, so the real defaultEventHandler routes the
// live/confirms/ add to autopromote.Handle → ExecutePromote →
// *NoSuchThoughtError. Observing that specific error proves dispatch ran
// despite --quiet silencing the log. (NoopHandler can't prove dispatch;
// the real dispatch table can.)
func TestHandleEvent_Quiet_DaemonStillDispatches(t *testing.T) {
	root := t.TempDir()
	targetID := "1727000000-quiet2"
	seedConfirmsAtThreshold(t, root, targetID)

	var dispatchErr error
	handler := func(e FileEvent) error {
		dispatchErr = defaultEventHandler(root)(e)
		return dispatchErr
	}
	ev := confirmsCreateEvent(root, targetID)
	opts := output.RenderOpts{Quiet: true}

	out := captureStdout(t, func() {
		handleEvent(ev, root, handler, opts, nil, nil)
	})

	if out != "" {
		t.Errorf("--quiet must suppress the per-event log on stdout; got %q", out)
	}
	if dispatchErr == nil {
		t.Fatal("expected dispatch to reach autopromote.Handle under --quiet (NoSuchThoughtError), got nil — daemon behaviour changed")
	}
	var nstErr *rufioerr.NoSuchThoughtError
	if !errors.As(dispatchErr, &nstErr) {
		t.Errorf("expected *NoSuchThoughtError (dispatch ran under --quiet), got %T: %v", dispatchErr, dispatchErr)
	}
}

// TestHandleEvent_Quiet_LogFileStillCaptures asserts (c): the --log
// opt-in (logSink) captures the per-event line to the file EVEN under
// --quiet, while stdout stays clean (no terminal-corruption footgun).
func TestHandleEvent_Quiet_LogFileStillCaptures(t *testing.T) {
	root := t.TempDir()
	targetID := "1727000000-quiet3"
	logPath := filepath.Join(t.TempDir(), "watch.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open log sink: %v", err)
	}
	defer f.Close()

	ev := confirmsCreateEvent(root, targetID)
	opts := output.RenderOpts{Quiet: true}

	out := captureStdout(t, func() {
		handleEvent(ev, root, NoopHandler, opts, f, nil)
	})

	if out != "" {
		t.Errorf("--log must never write to the terminal; stdout got %q", out)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	got := string(data)
	wantPath := "live/confirms/" + targetID + ".gdl"
	if !strings.Contains(got, wantPath) {
		t.Errorf("--log file must capture the per-event line under --quiet; got %q (want substring %q)", got, wantPath)
	}
	if !strings.Contains(got, "  add  ") {
		t.Errorf("--log line should carry the event kind; got %q", got)
	}
}

// TestHandleEvent_NoQuiet_LogStillPrints asserts (d): the
// backward-compatible default — WITHOUT --quiet and WITHOUT --log, the
// per-event line still prints to stdout exactly as before this change.
func TestHandleEvent_NoQuiet_LogStillPrints(t *testing.T) {
	root := t.TempDir()
	targetID := "1727000000-quiet4"
	ev := confirmsCreateEvent(root, targetID)
	opts := output.RenderOpts{} // Quiet:false — default

	out := captureStdout(t, func() {
		handleEvent(ev, root, NoopHandler, opts, nil, nil)
	})

	wantPath := "live/confirms/" + targetID + ".gdl"
	if !strings.Contains(out, wantPath) {
		t.Errorf("without --quiet the per-event log must still print (backward-compatible); got %q (want substring %q)", out, wantPath)
	}
	if !strings.Contains(out, "  add  ") {
		t.Errorf("default per-event log should carry the event kind; got %q", out)
	}
}

// TestNewDevCmd_FlagSurface locks the dev command's flag surface after
// this change: --quiet/-q help reflects the new (silences the watch log
// too) behaviour, and the new --log opt-in exists with no shorthand and a
// string default of "".
func TestNewDevCmd_FlagSurface(t *testing.T) {
	cmd := NewDevCmd("test")

	quiet := cmd.Flags().Lookup("quiet")
	if quiet == nil {
		t.Fatal("expected --quiet flag")
	}
	if quiet.Shorthand != "q" {
		t.Fatalf("expected -q shorthand, got -%s", quiet.Shorthand)
	}
	if !strings.Contains(quiet.Usage, "watch log") {
		t.Errorf("--quiet help must state it silences the watch log now; got %q", quiet.Usage)
	}

	logFlag := cmd.Flags().Lookup("log")
	if logFlag == nil {
		t.Fatal("expected new --log flag (footgun-free observability opt-in)")
	}
	if logFlag.DefValue != "" {
		t.Errorf("expected --log default empty, got %q", logFlag.DefValue)
	}
	if logFlag.Shorthand != "" {
		t.Errorf("--log should have no shorthand (avoid colliding with global -v/--version), got -%s", logFlag.Shorthand)
	}
	if !strings.Contains(logFlag.Usage, "file") {
		t.Errorf("--log help should describe the file sink; got %q", logFlag.Usage)
	}
}
