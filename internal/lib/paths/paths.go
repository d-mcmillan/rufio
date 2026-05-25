// Package paths handles project-root discovery, content-path validation,
// and the symlink-safe path resolution (the week-1 Phase 2 M2 fix that
// catches symlinked subdirs pointing outside the project root).
//
// Mirrors src/lib/paths.ts. POSIX-form output for ref keys (forward
// slashes regardless of OS).
package paths

import (
	"os"
	"path/filepath"
	"strings"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

const (
	configFile = "rufio.gdl"
	rufioDir   = ".rufio"
)

var forbiddenPrefixes = []string{".rufio", "internal", ".git"}

// FindProjectRoot walks up from startCwd until it finds a directory
// containing rufio.gdl. Returns *NotInProjectError if no project found.
func FindProjectRoot(startCwd string) (string, error) {
	cur, err := filepath.Abs(startCwd)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(cur, configFile)); err == nil {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", &rufioerr.NotInProjectError{Cwd: startCwd}
		}
		cur = parent
	}
}

// TryFindProjectRoot returns the project root or an empty string if not
// found. Never errors — useful for "is this a project?" checks.
func TryFindProjectRoot(startCwd string) string {
	root, err := FindProjectRoot(startCwd)
	if err != nil {
		return ""
	}
	return root
}

// RufioDir returns <root>/.rufio.
func RufioDir(root string) string {
	return filepath.Join(root, rufioDir)
}

// realpathSafe is the recursive best-effort symlink resolver. It evaluates
// symlinks for the deepest existing prefix of p and appends the
// non-existing tail unchanged. Critical for catching symlink escapes on
// paths that don't exist yet (e.g., the first push to a new file).
//
// Without this, a symlink at <root>/link → /tmp/elsewhere would let
// `push link/x.md` write outside the root.
func realpathSafe(p string) string {
	if _, err := os.Stat(p); err == nil {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realpathSafe(parent), filepath.Base(p))
}

// ResolveContentPath validates a user-supplied content path against the
// project root. Returns the path normalised and POSIX-form-relative to the
// realpath of root.
//
// Throws:
//   - *PathOutsideRootError if the path escapes the root (including via
//     symlink resolution)
//   - *IneligiblePathError if the path is the root itself or targets a
//     forbidden tree (.rufio/, internal/, .git/)
//
// Symlinks pointing inside the project canonicalise to the realpath, so
// the ref-key reflects the actual location and survives if the symlink
// is later removed.
func ResolveContentPath(root, userPath string) (string, error) {
	realRoot := realpathSafe(root)
	var absolute string
	if filepath.IsAbs(userPath) {
		absolute = userPath
	} else {
		absolute = filepath.Join(realRoot, userPath)
	}
	realAbsolute := realpathSafe(absolute)

	rel, err := filepath.Rel(realRoot, realAbsolute)
	if err != nil {
		return "", &rufioerr.PathOutsideRootError{Path: userPath}
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", &rufioerr.PathOutsideRootError{Path: userPath}
	}
	if rel == "" || rel == "." {
		return "", &rufioerr.IneligiblePathError{
			Path:   userPath,
			Reason: "cannot version the project root",
		}
	}

	segments := strings.Split(rel, string(filepath.Separator))
	if len(segments) > 0 {
		top := segments[0]
		for _, forbidden := range forbiddenPrefixes {
			if top == forbidden {
				return "", &rufioerr.IneligiblePathError{
					Path:   userPath,
					Reason: forbidden + "/ is reserved",
				}
			}
		}
	}

	// POSIX-form for ref keys (forward slashes regardless of OS).
	return filepath.ToSlash(filepath.Clean(rel)), nil
}

// BlobPath returns the on-disk path for a content-addressed blob:
// <root>/.rufio/history/<2chars>/<rest-of-sha256>.
func BlobPath(root, sha256 string) string {
	if len(sha256) < 3 {
		// Defensive — should never happen with a valid sha. Keep the
		// behaviour deterministic rather than panicking.
		return filepath.Join(RufioDir(root), "history", sha256)
	}
	return filepath.Join(RufioDir(root), "history", sha256[:2], sha256[2:])
}

// RefsPath returns the on-disk path for a ref file:
// <root>/.rufio/refs/<contentPath>.gdl. The contentPath is in POSIX form;
// we convert to OS form for filesystem operations.
func RefsPath(root, contentPath string) string {
	osPath := filepath.FromSlash(contentPath)
	return filepath.Join(RufioDir(root), "refs", osPath+".gdl")
}
