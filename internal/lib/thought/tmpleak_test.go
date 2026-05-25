package thought

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// TestWrite_RenameFailLeavesNoTmp forces os.Rename(tmp, target) to fail by
// pre-creating live/outbox/<agent>/<id>.gdl as a non-empty directory. The
// WriteFile of "<id>.gdl.tmp" still succeeds; the Rename then fails. The
// deferred best-effort cleanup must remove the stranded tmp.
func TestWrite_RenameFailLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "outbox", "a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "1-a.gdl")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := BuildThoughtRecord(ThoughtInput{
		ID: "1-a", Author: "a", Type: "focus", Subject: "x:1",
		Content: "x", Scope: "agent", TS: "t", TTL: 0,
	})
	err := Write(root, "a", "1-a", []gdl.Record{rec})
	if err == nil {
		t.Fatal("Write: want non-nil error on forced Rename failure, got nil")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("stranded .tmp after forced Rename failure: %v", matches)
	}
}
