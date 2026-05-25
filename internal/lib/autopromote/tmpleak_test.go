package autopromote

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// TestWritePromotedMarker_RenameFailLeavesNoTmp forces os.Rename(tmp,
// target) to fail by pre-creating live/promoted/<targetID>.gdl as a
// non-empty directory. WriteFile of "<targetID>.gdl.tmp" still succeeds;
// the Rename then fails. The deferred cleanup must remove the tmp.
func TestWritePromotedMarker_RenameFailLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "promoted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "thought-1.gdl")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := gdl.Record{Type: "promoted", Fields: []gdl.RecordField{
		{Key: "target", Value: "thought-1"},
		{Key: "ts", Value: "ts"},
	}}
	err := writePromotedMarker(root, "thought-1", rec)
	if err == nil {
		t.Fatal("writePromotedMarker: want non-nil error on forced Rename failure, got nil")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("stranded .tmp after forced Rename failure: %v", matches)
	}
}
