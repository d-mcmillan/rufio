package summon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWritePending_RenameFailLeavesNoTmp forces os.Rename(tmp, target) to
// fail by pre-creating live/summons/pending/<id>.gdl as a non-empty
// directory. WriteFile of "<id>.gdl.tmp" still succeeds; the Rename then
// fails. The deferred cleanup must remove the stranded tmp.
//
// Scope note: ONLY the rename-based WritePending path is exercised here.
// The other summon transition path (moveTo, link(2) create-no-overwrite)
// already does explicit best-effort os.Remove(tmp) on both branches and is
// deliberately left byte-unchanged.
func TestWritePending_RenameFailLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "summons", string(StatePending))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "s-1.gdl")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := BuildSummonRecord("s-1", "agent-a", "agent-b", "topic", "intent", "ts", 0)
	err := WritePending(root, "s-1", rec)
	if err == nil {
		t.Fatal("WritePending: want non-nil error on forced Rename failure, got nil")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("stranded .tmp after forced Rename failure: %v", matches)
	}
}
