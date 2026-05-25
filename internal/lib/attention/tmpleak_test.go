package attention

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWriteWithTimeout_RenameFailLeavesNoTmp forces os.Rename(tmp, target)
// to fail deterministically by pre-creating the rename target as a
// NON-EMPTY directory. os.WriteFile(tmp,...) still succeeds (tmp is the
// sibling "<agent>.gdl.tmp" path), then os.Rename fails because a file
// cannot replace a non-empty directory (ENOTEMPTY/EEXIST on darwin/linux).
//
// Contract assertions:
//
//	(a) Write returns a non-nil error (write-side contract unchanged).
//	(b) No "*.tmp" file is stranded under live/attention/ — the deferred
//	    best-effort cleanup must fire on the Rename-fail path.
func TestWriteWithTimeout_RenameFailLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "attention")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-create the rename target "agent-a.gdl" as a non-empty directory.
	target := filepath.Join(dir, "agent-a.gdl")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := BuildRecord("agent-a", "x", "fleet", []string{"customer:1"}, nil, "2026-05-11T12:00:00.000000000Z")
	err := WriteWithTimeout(root, "agent-a", rec, 2*time.Second)
	if err == nil {
		t.Fatal("WriteWithTimeout: want non-nil error on forced Rename failure, got nil")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("stranded .tmp after forced Rename failure: %v", matches)
	}
}
