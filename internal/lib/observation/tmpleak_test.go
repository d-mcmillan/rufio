package observation

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWrite_RenameFailLeavesNoTmp forces os.Rename(tmp, target) to fail by
// pre-creating the learned/<subject>/<id>.gdlm rename target as a
// non-empty directory. WriteFile of "<id>.gdlm.tmp" still succeeds; the
// Rename then fails. The deferred cleanup must remove the stranded tmp.
func TestWrite_RenameFailLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	target := SubjectPath(root, "x:1", "1-a")
	dir := filepath.Dir(target)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := BuildObservationRecord(ObservationInput{
		ID: "1-a", Author: "a", Subject: "x:1", Predicate: "is",
		Object: "y", Scope: "agent", Confidence: 1.0, TS: "ts",
	})
	err := Write(root, "x:1", "1-a", rec)
	if err == nil {
		t.Fatal("Write: want non-nil error on forced Rename failure, got nil")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("stranded .tmp after forced Rename failure: %v", matches)
	}
}
