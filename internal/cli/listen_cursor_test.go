// Package cli — #155 cursor-flag tests for `rufio listen` + `rufio stream`.
//
// These tests cover the CLI surface: the `--from=<cursor>` flag is parsed
// and forwarded, mutual exclusion with --catch-up is enforced, and both
// verbs document the cursor contract in their --help text.
//
// Streaming behavioural tests (watch-and-emit periodic cursors, catch-up
// from cursor) live in internal/lib/stream/cursor_test.go where they can
// exercise the lib directly. The CLI tests below are the wiring layer.
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// seedListenProject creates a tmp rufio project (rufio.gdl marker) and
// returns the realpath. RUFIO_AGENT_ID is set on the test env so the
// identity.Resolve inside runListen succeeds without filesystem state.
func seedListenProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(real, "rufio.gdl"), []byte("@config|name:test\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("RUFIO_AGENT_ID", "agent-a")
	return real
}

// TestListenCmd_FlagFromExists — `--from` MUST be a registered flag on
// `rufio listen`.
func TestListenCmd_FlagFromExists(t *testing.T) {
	cmd := NewListenCmd()
	f := cmd.Flags().Lookup("from")
	if f == nil {
		t.Fatal("rufio listen is missing --from flag (#155)")
	}
}

// TestStreamCmd_FlagFromExists — `--from` MUST be a registered flag on
// `rufio stream`. Both CLI verbs must be symmetric.
func TestStreamCmd_FlagFromExists(t *testing.T) {
	cmd := NewStreamCmd()
	f := cmd.Flags().Lookup("from")
	if f == nil {
		t.Fatal("rufio stream is missing --from flag (#155)")
	}
}

// TestListenHelp_DocumentsCursorContract — both --help blocks must
// describe the opaque-cursor pass-back contract; otherwise SDK
// integrators have no in-binary surface to learn it from.
func TestListenHelp_DocumentsCursorContract(t *testing.T) {
	cmd := NewListenCmd()
	out := renderHelp(t, cmd)
	if !strings.Contains(out, "--from") {
		t.Errorf("listen --help must mention --from flag: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "opaque") {
		t.Errorf("listen --help must describe cursor as opaque: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "cursor") {
		t.Errorf("listen --help must describe the cursor contract: %q", out)
	}
}

// TestStreamHelp_DocumentsCursorContract — same contract for stream.
func TestStreamHelp_DocumentsCursorContract(t *testing.T) {
	cmd := NewStreamCmd()
	out := renderHelp(t, cmd)
	if !strings.Contains(out, "--from") {
		t.Errorf("stream --help must mention --from flag: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "opaque") {
		t.Errorf("stream --help must describe cursor as opaque: %q", out)
	}
}

// TestListen_FromAndCatchUp_MutuallyExclusive — passing BOTH --from and
// --catch-up to runListen surfaces a clear error. (Empty --from + --catch-up
// is fine — `--from=""` is the epoch sentinel that IS catch-up.)
func TestListen_FromAndCatchUp_MutuallyExclusive(t *testing.T) {
	root := seedListenProject(t)
	err := runListen(root, "" /*as*/, "" /*types*/, "" /*scope*/, true /*catchUp*/, "some-cursor", output.RenderOpts{})
	if err == nil {
		t.Fatal("expected error when --from and --catch-up are both set")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "mutually exclusive") &&
		!strings.Contains(strings.ToLower(err.Error()), "cannot use") {
		t.Errorf("error %q should explain the conflict between --from and --catch-up", err.Error())
	}
}

// TestStream_FromAndCatchUp_NoCatchUpOnStream — stream has no --catch-up
// today; --from must still parse cleanly. A malformed cursor on stream
// must surface "invalid cursor" before any FS work begins.
func TestStream_InvalidCursorErrors(t *testing.T) {
	root := seedListenProject(t)
	err := runStream(root, "" /*types*/, "" /*scope*/, "!!!not-base64!!!" /*from*/, true /*fromFlagSet*/, output.RenderOpts{})
	if err == nil {
		t.Fatal("expected error for malformed --from cursor on stream")
	}
	if !strings.Contains(err.Error(), "invalid cursor") {
		t.Errorf("error %q must mention 'invalid cursor'", err.Error())
	}
}
