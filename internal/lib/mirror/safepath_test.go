package mirror

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSafeRelPath_AcceptsBenignRelativePaths(t *testing.T) {
	cases := []string{
		"live/outbox/alice/1-a.gdl",
		"learned/foo/bar.gdlm",
		"given/some/file.md",
		"a.gdl",
	}
	for _, p := range cases {
		got, err := safeRelPath(p)
		if err != nil {
			t.Errorf("safeRelPath(%q) errored: %v", p, err)
			continue
		}
		want := filepath.FromSlash(p)
		if got != want {
			t.Errorf("safeRelPath(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestSafeRelPath_RejectsTraversal(t *testing.T) {
	cases := []string{
		"../../../etc/cron.d/exploit",
		"../foo",
		"..",
		"live/../../etc/passwd",
		"a/b/../../../../bad",
	}
	for _, p := range cases {
		_, err := safeRelPath(p)
		if err == nil {
			t.Errorf("safeRelPath(%q) accepted a traversal path — must reject", p)
		}
		if err != nil && !strings.Contains(err.Error(), "rejected suspicious path") {
			t.Errorf("safeRelPath(%q) error %q lacks 'rejected suspicious path' prefix", p, err)
		}
	}
}

func TestSafeRelPath_RejectsAbsolute(t *testing.T) {
	// POSIX absolute. Windows absolute (C:\foo) is handled by
	// filepath.IsAbs on Windows — skip cross-platform assertion here
	// and rely on the test running under the host's filepath.
	cases := []string{
		"/etc/passwd",
		"/tmp/x",
		"//foo",
	}
	for _, p := range cases {
		_, err := safeRelPath(p)
		if err == nil {
			t.Errorf("safeRelPath(%q) accepted absolute path — must reject", p)
		}
	}
}

func TestSafeRelPath_RejectsEmptyAndRoot(t *testing.T) {
	for _, p := range []string{"", ".", "./"} {
		_, err := safeRelPath(p)
		if err == nil {
			t.Errorf("safeRelPath(%q) accepted root/empty — must reject", p)
		}
	}
}

func TestSafeRelPath_RejectsControlBytes(t *testing.T) {
	// NUL in path is a classic injection vector; control bytes are
	// universally invalid in filesystem paths but Go silently embeds
	// them. Reject early.
	for _, p := range []string{
		"foo\x00bar",
		"foo\nbar",
		"foo\rbar",
		"\x01evil",
	} {
		_, err := safeRelPath(p)
		if err == nil {
			t.Errorf("safeRelPath(%q) accepted control byte — must reject", p)
		}
	}
}

func TestJoinUnderRoot_HappyPath(t *testing.T) {
	root := t.TempDir()
	dst, err := joinUnderRoot(root, "live/outbox/alice/1.gdl")
	if err != nil {
		t.Fatalf("joinUnderRoot: %v", err)
	}
	// joinUnderRoot canonicalises the root via EvalSymlinks (F4) so
	// the returned dst is rooted at the resolved form (e.g. /tmp →
	// /private/tmp on macOS). Compare against the EvalSymlinks-
	// resolved root to make the assertion symlink-safe across hosts.
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(root): %v", err)
	}
	want := filepath.Join(rootReal, "live", "outbox", "alice", "1.gdl")
	if dst != want {
		t.Errorf("dst=%q want %q", dst, want)
	}
}

func TestJoinUnderRoot_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{
		"../../../etc/passwd",
		"../etc/passwd",
		"a/../../../tmp/x",
	} {
		_, err := joinUnderRoot(root, p)
		if err == nil {
			t.Errorf("joinUnderRoot(%q) should reject traversal", p)
		}
	}
}

// TestJoinUnderRoot_RejectsSymlinkEscape (security audit F4). A local
// attacker (shared dev box, multi-user laptop, CI runner) can pre-stage
// a symlink INSIDE the mirror root that points OUTSIDE it BEFORE the
// operator runs `rufio mirror pull --to <root>`. The lexical Rel check
// in the original joinUnderRoot was pure-string and didn't follow
// symlinks; os.WriteFile then dutifully followed the symlink and wrote
// to /etc/<file> instead of <root>/live/<file>.
//
// Real defense: filepath.EvalSymlinks ancestor check on the resolved
// destination's parent dir. If the resolved parent doesn't sit under
// the resolved root, refuse.
func TestJoinUnderRoot_RejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows; skipping")
	}
	root := t.TempDir()
	outside := t.TempDir()
	// Resolve outside to its real form (TempDir may itself contain a
	// symlink on macOS — /var → /private/var). We want the symlink we
	// install to point at the REAL outside so the assertion is exact.
	outsideReal, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatalf("EvalSymlinks(outside): %v", err)
	}
	// Pre-stage: <root>/live → <outside-real>
	linkPath := filepath.Join(root, "live")
	if err := os.Symlink(outsideReal, linkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	// Now joinUnderRoot(root, "live/outbox/alice/1.gdl") MUST refuse
	// because the resolved parent escapes root.
	_, err = joinUnderRoot(root, "live/outbox/alice/1.gdl")
	if err == nil {
		t.Fatal("joinUnderRoot accepted symlink-escape — F4 floor breached")
	}
	if !strings.Contains(err.Error(), "symlink") && !strings.Contains(err.Error(), "outside root") {
		t.Errorf("error should mention symlink/outside root; got %v", err)
	}
}

// TestJoinUnderRoot_AllowsBenignSymlinkUnderRoot — a symlink that lands
// INSIDE the root is fine. We only refuse symlinks that escape;
// legitimate within-root symlinks must pass.
func TestJoinUnderRoot_AllowsBenignSymlinkUnderRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows; skipping")
	}
	root := t.TempDir()
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(root): %v", err)
	}
	// Create <root>/real-live as a normal dir.
	if err := os.MkdirAll(filepath.Join(rootReal, "real-live", "outbox"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Create <root>/live as a symlink to <root>/real-live (legitimate
	// within-root indirection).
	if err := os.Symlink(filepath.Join(rootReal, "real-live"), filepath.Join(rootReal, "live")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	dst, err := joinUnderRoot(root, "live/outbox/alice.gdl")
	if err != nil {
		t.Errorf("joinUnderRoot rejected benign within-root symlink: %v", err)
	}
	if dst == "" {
		t.Errorf("dst is empty")
	}
}

// TestJoinUnderRoot_FreshRootNoSymlinkResolution — when the
// destination's parent doesn't exist yet (the mirror creates dirs
// lazily under fresh roots), EvalSymlinks returns an error. That's
// NOT an escape — the parent is just not-yet-created. The lexical
// guard upstream is the security floor; this test pins that the
// EvalSymlinks failure on a fresh tree is not mis-interpreted as
// an escape.
func TestJoinUnderRoot_FreshRootNoSymlinkResolution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows; skipping")
	}
	root := t.TempDir()
	// Don't pre-create any dirs — the path "live/outbox/alice.gdl" has
	// no parent on disk.
	dst, err := joinUnderRoot(root, "live/outbox/alice.gdl")
	if err != nil {
		t.Errorf("joinUnderRoot on fresh root should succeed: %v", err)
	}
	if !strings.HasSuffix(dst, filepath.Join("live", "outbox", "alice.gdl")) {
		t.Errorf("dst suffix unexpected: %q", dst)
	}
}
