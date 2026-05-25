package mirror

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// safeRelPath canonicalises a POSIX-style relative path supplied by an
// untrusted remote (the /listen SSE event's `path` field, or the
// recall-tool record fields the snapshot path reassembles) and returns
// it ready for filepath.Join under the mirror root.
//
// Threat model: the trusted-collaborator daemon is the server-side floor
// of trust, but a defense-in-depth layer here protects against:
//   - operator typo on --server= URL landing on an attacker's host
//   - DNS hijacking / typosquatting of the server's domain
//   - compromised peer with token write access serving crafted records
//
// Returns an error for:
//   - empty input
//   - absolute paths (POSIX `/foo` or Windows `C:\foo`)
//   - paths whose cleaned form is `..` or starts with `../`
//   - paths containing NUL or control bytes
//
// On success returns the cleaned OS-native path safe to filepath.Join.
func safeRelPath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("rejected suspicious path: empty")
	}
	// Defense against NUL/control-byte injection before any normalisation.
	for _, ch := range p {
		if ch == 0 || ch < 0x20 {
			return "", fmt.Errorf("rejected suspicious path: contains control byte (%q)", p)
		}
	}
	osPath := filepath.FromSlash(p)
	if filepath.IsAbs(osPath) {
		return "", fmt.Errorf("rejected suspicious path: absolute (%q)", p)
	}
	cleaned := filepath.Clean(osPath)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("rejected suspicious path: escapes mirror root (%q)", p)
	}
	// filepath.Clean of "" returns ".", which would land us at the mirror
	// root — treat as invalid (the snapshot path needs a file, not the
	// dir).
	if cleaned == "." {
		return "", fmt.Errorf("rejected suspicious path: refers to root (%q)", p)
	}
	return cleaned, nil
}

// validateCleanedTopLevel enforces the v1.0.5 N1 security floor: a
// wire path's CLEANED top-level directory MUST be one of the
// canonical substrate roots {live, learned, given}. The raw
// HasPrefix check that this replaces was vulnerable to a hostile
// `live/../.rufio/.mirror-cursor` payload — raw form passes
// HasPrefix("live/"), but filepath.Clean reduces to
// `.rufio/.mirror-cursor`, which writes inside --to root yet
// bypasses the canonical-dir intent.
//
// This function is the defence: clean first via safeRelPath (which
// also rejects absolutes / NUL / ".." escape) and then check the
// cleaned form's first path segment against the allow-list. Any
// other top-level dir surfaces a clear error with the actual cleaned
// form in the message so debugging is straightforward.
func validateCleanedTopLevel(wirePath string) error {
	cleaned, err := safeRelPath(wirePath)
	if err != nil {
		return err
	}
	// Split on OS separator so Windows joins the same way the Unix
	// path does. cleaned can never be empty (safeRelPath rejects ".").
	parts := strings.SplitN(cleaned, string(filepath.Separator), 2)
	topLevel := parts[0]
	allowed := map[string]bool{"live": true, "learned": true, "given": true}
	if !allowed[topLevel] {
		return fmt.Errorf("rejected suspicious path: cleaned top-level dir %q not in {live, learned, given} (raw=%q, cleaned=%q)", topLevel, wirePath, cleaned)
	}
	return nil
}

// hasUnsafeComponent reports whether s contains anything that should
// never appear inside a single filesystem path component (id, author,
// directory name). Used by projectRecordToFile to reject crafted
// record fields BEFORE filepath.Join composes them, so traversal can't
// sneak in through the lateral fields the snapshot path reassembles.
//
// Rejects: empty, "..", any path separator (POSIX `/` or OS-native),
// any NUL or control byte, the standalone "." sentinel.
func hasUnsafeComponent(s string) bool {
	if s == "" {
		return true
	}
	if s == "." || s == ".." {
		return true
	}
	if strings.ContainsAny(s, `/\`+"\x00") {
		return true
	}
	for _, ch := range s {
		if ch < 0x20 {
			return true
		}
	}
	return false
}

// joinUnderRoot resolves rel against root and returns the absolute path,
// asserting (belt-and-suspenders) that the result is genuinely under
// root. Even with safeRelPath upstream, this defends against a future
// safeRelPath bug AND against symlinks the attacker might pre-stage
// inside the mirror dir.
//
// Security audit F4: the original pure-lexical Rel check was a lie —
// it did NOT defend against symlinks. A local attacker on a shared
// dev box / multi-user laptop / CI runner could pre-stage
// <root>/live → /etc as a symlink BEFORE the operator ran `rufio
// mirror pull --to <root>`. The lexical check passed, os.WriteFile
// followed the symlink, and the GDL records landed under /etc. F4
// adds a real EvalSymlinks ancestor check on the destination's parent.
//
// Strategy:
//  1. Resolve root via EvalSymlinks (handles macOS /var → /private/var
//     normalisation so the prefix check is exact).
//  2. Compose dst lexically.
//  3. Lexical Rel check against the resolved root (existing layer).
//  4. EvalSymlinks the deepest existing ancestor of dst — typically
//     the parent dir, walking up if it doesn't exist yet. Compare to
//     the resolved root with a separator-aware HasPrefix.
//
// When NO ancestor exists yet (fresh mirror dir, first write), the
// lexical guard above is the security floor — there are no symlinks
// to follow because the path doesn't exist. The mirror creates dirs
// fresh via os.MkdirAll which itself refuses to traverse through a
// symlink that escapes (it succeeds at creating a new dir or fails
// with EEXIST, neither of which produces a written file outside root).
func joinUnderRoot(root, rel string) (string, error) {
	cleanRel, err := safeRelPath(rel)
	if err != nil {
		return "", err
	}
	// Resolve root via EvalSymlinks so the prefix check is against the
	// canonical form (macOS / multi-user setups often have /tmp →
	// /private/tmp etc.). If the root itself doesn't exist, fall back
	// to a cleaned absolute form — the lexical guard still applies and
	// MkdirAll will create the tree fresh, no symlinks involved.
	cleanRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		abs, absErr := filepath.Abs(root)
		if absErr != nil {
			return "", absErr
		}
		cleanRoot = filepath.Clean(abs)
	}
	dst := filepath.Join(cleanRoot, cleanRel)

	// Lexical guard (kept from existing layer): a relative path that
	// post-Clean starts with ".." escapes the root regardless of
	// symlinks.
	relCheck, err := filepath.Rel(cleanRoot, dst)
	if err != nil {
		return "", fmt.Errorf("rejected suspicious path: rel failed (%q): %w", rel, err)
	}
	if relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("rejected suspicious path: resolves outside root (%q)", rel)
	}

	// Symlink-aware ancestor check: walk up from dst's parent until we
	// find an existing dir, EvalSymlinks it, and assert it sits under
	// the resolved root. This is the layer that catches pre-staged
	// symlinks the lexical Rel check above misses.
	//
	// Walk-termination invariant (fresh-tree bugfix): the walk MUST
	// stop at cleanRoot itself. Pre-fix, if cleanRoot didn't exist
	// either (e.g. `rufio mirror sync --to=/tmp/fresh` where /tmp
	// exists but /tmp/fresh doesn't), the walk continued past the
	// configured root and found /tmp — whose macOS canonical form
	// /private/tmp does NOT sit under /tmp/fresh — and rejected every
	// write as a symlink escape. Result: silent zero-event syncs.
	// Halting at cleanRoot means: if the walk has reached the
	// configured root without escaping, the lexical guard above is
	// the floor and MkdirAll will create dirs fresh.
	rootPrefix := cleanRoot + string(filepath.Separator)
	ancestor := filepath.Dir(dst)
	for {
		// Stop walking once we hit (or would walk above) cleanRoot.
		// At/above cleanRoot, the lexical guard already proved the
		// dst is rooted under cleanRoot — there's no symlink we
		// could possibly catch by walking further up.
		if ancestor == cleanRoot || !strings.HasPrefix(ancestor+string(filepath.Separator), rootPrefix) {
			break
		}
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			if resolved != cleanRoot && !strings.HasPrefix(resolved+string(filepath.Separator), rootPrefix) {
				return "", fmt.Errorf("rejected suspicious path: resolves outside root via symlink (%q → %q)", rel, resolved)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			// Non-existence is the common "fresh tree" signal —
			// keep walking up. Any other stat error (permission
			// denied, EIO) means we can't prove safety; refuse.
			return "", fmt.Errorf("rejected suspicious path: cannot resolve ancestor of %q: %w", rel, err)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			// Hit the filesystem root without finding an existing
			// dir — the entire dst chain is unstamped. The lexical
			// guard above already proved safety; MkdirAll will
			// create everything fresh.
			break
		}
		ancestor = parent
	}
	return dst, nil
}
