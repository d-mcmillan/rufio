package serve

import (
	"os"
	"path/filepath"
)

// mkdirAllImpl shelters the test files from importing os directly while
// still providing a recursive mkdir. Kept tiny so the privacy/listen tests
// stay focused on what they assert (not on FS plumbing).
func mkdirAllImpl(p string) error {
	return os.MkdirAll(filepath.FromSlash(p), 0o755)
}

func writeFileImpl(p, content string) error {
	if err := os.MkdirAll(filepath.Dir(filepath.FromSlash(p)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.FromSlash(p), []byte(content), 0o644)
}
