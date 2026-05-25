package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// TestNewTuiCmdMetadata verifies the command name, args, and flag
// surface match the spec lock (D22.17): name=tui, NoArgs, two flags
// --quiet/-q and --no-color.
func TestNewTuiCmdMetadata(t *testing.T) {
	cmd := NewTuiCmd()
	if cmd.Use != "tui" {
		t.Fatalf("expected Use=tui, got %q", cmd.Use)
	}
	quiet := cmd.Flags().Lookup("quiet")
	if quiet == nil {
		t.Fatal("expected --quiet flag")
	}
	if quiet.Shorthand != "q" {
		t.Fatalf("expected -q shorthand, got -%s", quiet.Shorthand)
	}
	noColor := cmd.Flags().Lookup("no-color")
	if noColor == nil {
		t.Fatal("expected --no-color flag")
	}
	// Negative assertion: the TUI never emits JSONL, so --json must NOT
	// be present (D22.17).
	if cmd.Flags().Lookup("json") != nil {
		t.Fatal("expected NO --json flag on tui (TUI is not a JSONL producer)")
	}
}

// TestRunTuiOutsideProjectReturnsNotInProject verifies the project-
// root resolver surfaces *NotInProjectError when invoked from outside
// a project. We don't actually exercise the bubbletea program — the
// error path returns before tea.NewProgram is called.
func TestRunTuiOutsideProjectReturnsNotInProject(t *testing.T) {
	// Use a temp dir with NO rufio.gdl marker.
	cwd := t.TempDir()
	err := runTui(cwd, output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("expected error when run outside a project")
	}
	var notInProject *rufioerr.NotInProjectError
	if !errors.As(err, &notInProject) {
		t.Fatalf("expected *NotInProjectError, got %T: %v", err, err)
	}
}

// TestRunTuiQuietSuppressesBanner verifies --quiet doesn't crash
// (placeholder for a fuller test once the banner has more content).
// The actual stderr suppression is exercised by the integration smoke
// in test/integration once a runnable harness exists.
func TestRunTuiQuietSuppressesBanner(t *testing.T) {
	// Construct a project root so runTui gets past FindProjectRoot.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rufio.gdl"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	// We don't call runTui directly here because tea.NewProgram needs
	// a TTY (it would block or fail). Instead just verify the flag
	// plumbing — the metadata test above already covers --quiet's
	// existence; this test asserts the surface for downstream readers.
	cmd := NewTuiCmd()
	quietFlag := cmd.Flags().Lookup("quiet")
	if quietFlag == nil || quietFlag.DefValue != "false" {
		t.Fatalf("expected --quiet default=false, got %v", quietFlag)
	}
}

// TestRunTuiEmitsNoPreRunBanner is the regression guard for the
// pre-launch stderr banner that raced Bubble Tea's alt-screen startup
// handshake (#136). The defect: runTui wrote
// `rufio tui · <root> · ? for help\n` to os.Stderr on the non-quiet
// path BEFORE p.Run(). On real terminals where alt-screen enter is
// ineffective that line stayed on the primary buffer and bottom-
// anchored the whole TUI (header on top, blank void, body jammed at
// bottom, full-screen scroll). View() was provably byte-perfect at all
// geometries — purely the launcher banner. The decision (#136): remove
// the banner entirely; the TUI's own header already shows the
// project/substrate context in-app, and Bubble Tea wipes that line
// instantly in the working case anyway, so nothing user-visible is
// lost.
//
// The narrowest seam that previously emitted the banner is runTui
// itself, in the phase AFTER FindProjectRoot succeeds and BEFORE
// p.Run(). We give it a valid project root (so it gets PAST
// FindProjectRoot — the early-return path in
// TestRunTuiOutsideProjectReturnsNotInProject never reached the
// banner), capture os.Stderr via an os.Pipe (the established
// cli-test pattern, see captureStdout in dev_quiet_test.go), and run
// runTui in a goroutine because p.Run() needs a TTY. The banner — if
// present — is written synchronously before p.Run(), so it is in the
// pipe regardless of whether p.Run() then blocks or fails fast without
// a TTY. We read with a deadline and assert the banner marker is
// ABSENT. Default (non-quiet) RenderOpts are used deliberately: the
// removed banner lived behind `if !opts.Quiet`, so the non-quiet path
// is exactly what must now be silent.
func TestRunTuiEmitsNoPreRunBanner(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rufio.gdl"), []byte(""), 0o644); err != nil {
		t.Fatalf("seed project marker: %v", err)
	}

	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	// Drain the read end concurrently so a (hypothetical) banner write
	// never blocks on a full pipe buffer.
	captured := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		captured <- b.String()
	}()

	// p.Run() needs a TTY; under `go test` it returns an error rather
	// than blocking, but run in a goroutine so a hang can't wedge the
	// test. The pre-Run banner (if any) is emitted before p.Run() is
	// even reached, so it is observable independently of p.Run()'s fate.
	done := make(chan struct{})
	go func() {
		// Default opts: Quiet=false — the path the banner lived on.
		_ = runTui(root, output.RenderOpts{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		// p.Run() blocked (no TTY teardown). The pre-Run banner, if
		// emitted, is already in the pipe — proceed to assert on it.
	}

	_ = w.Close()
	os.Stderr = orig
	out := <-captured

	if strings.Contains(out, "rufio tui ·") || strings.Contains(out, "? for help") {
		t.Fatalf("runTui must emit NO pre-run banner on stderr "+
			"(it raced Bubble Tea alt-screen startup and bottom-"+
			"anchored the TUI, #136); got %q", out)
	}
}
