package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

// mkProjectDir returns a symlink-resolved temp dir containing a rufio.gdl
// (macOS t.TempDir() lives under /var → /private/var; FindProjectRoot
// returns the realpath, so tests must compare against the resolved path).
func mkProjectDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(real, "rufio.gdl"), []byte("@rufio|version:1\n"), 0o644); err != nil {
		t.Fatalf("write rufio.gdl: %v", err)
	}
	return real
}

func TestResolve_ExplicitRootAndAgent(t *testing.T) {
	dir := mkProjectDir(t)
	got, err := resolve(dir, "agent-a")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Root != dir {
		t.Errorf("Root = %q, want %q", got.Root, dir)
	}
	if got.Agent != "agent-a" {
		t.Errorf("Agent = %q, want %q", got.Agent, "agent-a")
	}
}

func TestResolve_InvalidAgentFlag(t *testing.T) {
	dir := mkProjectDir(t)
	_, err := resolve(dir, "BAD ID")
	if err == nil {
		t.Fatalf("expected error for invalid agent id, got nil")
	}
	var ide *rufioerr.InvalidIdentityError
	if !errors.As(err, &ide) {
		t.Fatalf("error = %T (%v), want *rufioerr.InvalidIdentityError", err, err)
	}
}

func TestResolve_NotInProject(t *testing.T) {
	dir := t.TempDir() // intentionally NO rufio.gdl
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	_, err = resolve(real, "agent-a")
	if err == nil {
		t.Fatalf("expected error for non-project dir, got nil")
	}
	var nip *rufioerr.NotInProjectError
	if !errors.As(err, &nip) {
		t.Fatalf("error = %T (%v), want *rufioerr.NotInProjectError", err, err)
	}
}
