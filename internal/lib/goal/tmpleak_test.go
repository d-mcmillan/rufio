package goal

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteActive_RenameFailLeavesNoTmp forces os.Rename(tmp, target) to
// fail by pre-creating live/goals/active/<id>.gdl as a non-empty
// directory. WriteFile of "<id>.gdl.tmp" still succeeds; the Rename then
// fails. The deferred cleanup must remove the stranded tmp.
func TestWriteActive_RenameFailLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "goals", string(StateActive))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "g-1.gdl")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := BuildGoalRecord("g-1", "agent-a", "ship it", "", "", "agent", "ts")
	err := WriteActive(root, "g-1", rec)
	if err == nil {
		t.Fatal("WriteActive: want non-nil error on forced Rename failure, got nil")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("stranded .tmp after forced Rename failure: %v", matches)
	}
}
