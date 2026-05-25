package reason

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWrite_RenameFailLeavesNoTmp forces os.Rename(tmp, target) to fail by
// pre-creating the reason target file path as a non-empty directory.
// WriteFile of "<id>.gdl.tmp" still succeeds; the Rename then fails. The
// deferred cleanup must remove the stranded tmp.
func TestWrite_RenameFailLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	target := Path(root, "agent-a", "dec-1", "1-a")
	dir := filepath.Dir(target)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := BuildRecord(ReasonInput{
		ID: "1-a", Author: "agent-a", Content: "because", TS: "ts",
	})
	err := Write(root, "agent-a", "dec-1", "1-a", rec)
	if err == nil {
		t.Fatal("Write: want non-nil error on forced Rename failure, got nil")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("stranded .tmp after forced Rename failure: %v", matches)
	}
}
