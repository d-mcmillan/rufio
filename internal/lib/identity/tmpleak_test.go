package identity

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteLocalFile_RenameFailLeavesNoTmp forces os.Rename(tmp, target)
// to fail by pre-creating .rufio/identity.local.gdl as a non-empty
// directory. WriteFile of ".rufio/identity.local.gdl.tmp" still succeeds;
// the Rename then fails. The deferred cleanup must remove the tmp.
func TestWriteLocalFile_RenameFailLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	rufioDir := filepath.Join(root, ".rufio")
	if err := os.MkdirAll(rufioDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := localFilePath(root)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := WriteLocalFile(root, "agent-a")
	if err == nil {
		t.Fatal("WriteLocalFile: want non-nil error on forced Rename failure, got nil")
	}
	matches, _ := filepath.Glob(filepath.Join(rufioDir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("stranded .tmp after forced Rename failure: %v", matches)
	}
}
