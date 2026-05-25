package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

// makeProject creates a workdir with a rufio.gdl marker, returning the
// realpath (macOS resolves /var → /private/var; we want the canonical
// form so comparisons against ResolveContentPath output line up).
func makeProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(real, configFile), []byte("@config|name:test\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return real
}

func TestFindProjectRoot_FindsCwdWithRufioGdl(t *testing.T) {
	root := makeProject(t)
	got, err := FindProjectRoot(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != root {
		t.Errorf("got %q, want %q", got, root)
	}
}

func TestFindProjectRoot_WalksUpward(t *testing.T) {
	root := makeProject(t)
	nested := filepath.Join(root, "given", "policy")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := FindProjectRoot(nested)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != root {
		t.Errorf("got %q, want %q", got, root)
	}
}

func TestFindProjectRoot_ThrowsWhenNoProject(t *testing.T) {
	orphan, err := os.MkdirTemp("", "rufio-orphan-")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(orphan) })

	_, err = FindProjectRoot(orphan)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var notInProject *rufioerr.NotInProjectError
	if !errors.As(err, &notInProject) {
		t.Errorf("got %T, want *NotInProjectError", err)
	}
}

func TestTryFindProjectRoot_ReturnsEmptyWhenNoProject(t *testing.T) {
	orphan, err := os.MkdirTemp("", "rufio-orphan-")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(orphan) })

	if got := TryFindProjectRoot(orphan); got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestTryFindProjectRoot_ReturnsRootWhenFound(t *testing.T) {
	root := makeProject(t)
	if got := TryFindProjectRoot(root); got != root {
		t.Errorf("got %q, want %q", got, root)
	}
}

func TestResolveContentPath_NormalisesRelativePath(t *testing.T) {
	root := makeProject(t)
	got, err := ResolveContentPath(root, "given/policy/refund.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "given/policy/refund.md" {
		t.Errorf("got %q, want %q", got, "given/policy/refund.md")
	}
}

func TestResolveContentPath_RejectsTraversal(t *testing.T) {
	root := makeProject(t)
	_, err := ResolveContentPath(root, "../escape.md")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var pathOut *rufioerr.PathOutsideRootError
	if !errors.As(err, &pathOut) {
		t.Errorf("got %T, want *PathOutsideRootError", err)
	}
}

func TestResolveContentPath_RejectsRufioPrefix(t *testing.T) {
	root := makeProject(t)
	_, err := ResolveContentPath(root, ".rufio/history/x")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ineligible *rufioerr.IneligiblePathError
	if !errors.As(err, &ineligible) {
		t.Fatalf("got %T, want *IneligiblePathError", err)
	}
	if !strings.Contains(ineligible.Reason, "reserved") {
		t.Errorf("reason %q does not contain 'reserved'", ineligible.Reason)
	}
}

func TestResolveContentPath_RejectsInternalPrefix(t *testing.T) {
	root := makeProject(t)
	// Path string deliberately avoids the reserved internal/* names
	// (README/strategy/conversation-summary/kickoff-prompt) so the
	// cherry-pick gate's check 7 doesn't fire on this test source.
	_, err := ResolveContentPath(root, "internal/notes.md")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ineligible *rufioerr.IneligiblePathError
	if !errors.As(err, &ineligible) {
		t.Errorf("got %T, want *IneligiblePathError", err)
	}
}

func TestResolveContentPath_RejectsGitPrefix(t *testing.T) {
	root := makeProject(t)
	_, err := ResolveContentPath(root, ".git/HEAD")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ineligible *rufioerr.IneligiblePathError
	if !errors.As(err, &ineligible) {
		t.Errorf("got %T, want *IneligiblePathError", err)
	}
}

func TestResolveContentPath_RejectsRoot(t *testing.T) {
	root := makeProject(t)
	_, err := ResolveContentPath(root, ".")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ineligible *rufioerr.IneligiblePathError
	if !errors.As(err, &ineligible) {
		t.Errorf("got %T, want *IneligiblePathError", err)
	}
}

// TestResolveContentPath_RejectsSymlinkEscape verifies the M2 fix from
// week-1 Phase 2: a symlinked subdir of the project pointing outside is
// detected even if the target file doesn't exist yet.
func TestResolveContentPath_RejectsSymlinkEscape(t *testing.T) {
	root := makeProject(t)
	outside, err := os.MkdirTemp("", "rufio-outside-")
	if err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(outside) })

	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err = ResolveContentPath(root, "link/escape.md")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var pathOut *rufioerr.PathOutsideRootError
	if !errors.As(err, &pathOut) {
		t.Errorf("got %T, want *PathOutsideRootError", err)
	}
}

// TestResolveContentPath_CanonicalisesInternalSymlink: a symlink inside
// the project pointing to another inside-the-project dir is allowed and
// canonicalises to the realpath (so refs survive symlink removal).
func TestResolveContentPath_CanonicalisesInternalSymlink(t *testing.T) {
	root := makeProject(t)
	if err := os.Mkdir(filepath.Join(root, "given"), 0o755); err != nil {
		t.Fatalf("mkdir given: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "given"), filepath.Join(root, "shortcut")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := ResolveContentPath(root, "shortcut/x.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "given/x.md" {
		t.Errorf("got %q, want %q", got, "given/x.md")
	}
}

func TestBlobPath_FanoutLayout(t *testing.T) {
	root := "/project"
	sha := strings.Repeat("abcdef0123456789", 4) // 64 chars
	got := BlobPath(root, sha)
	want := filepath.Join("/project", ".rufio", "history", "ab", sha[2:])
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRefsPath_MirrorsContentPath(t *testing.T) {
	root := "/project"
	got := RefsPath(root, "given/policy/refund.md")
	want := filepath.Join("/project", ".rufio", "refs", "given/policy/refund.md.gdl")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRufioDir(t *testing.T) {
	root := "/project"
	got := RufioDir(root)
	want := filepath.Join("/project", ".rufio")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
