package swarm

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAtomicWrite_RenameFailLeavesNoTmp forces os.Rename(tmp, target) to
// fail by pre-creating the rename target as a non-empty directory.
// WriteFile of "<target>.tmp" still succeeds; the Rename then fails. The
// deferred cleanup must remove the stranded tmp.
func TestAtomicWrite_RenameFailLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".rufio")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := localFilePath(root) // <root>/.rufio/swarm.local.gdl
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := atomicWrite(target, []byte("@swarm|persona:reviewer|count:1\n"))
	if err == nil {
		t.Fatal("atomicWrite: want non-nil error on forced Rename failure, got nil")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("stranded .tmp after forced Rename failure: %v", matches)
	}
}

// TestAtomicWrite_SuccessNoTmp pins the success path: the added defer must
// be a byte-identical no-op (file written with trailing newline, no tmp).
func TestAtomicWrite_SuccessNoTmp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".rufio")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := localFilePath(root)
	if err := atomicWrite(target, []byte("@swarm|persona:reviewer|count:1")); err != nil {
		t.Fatalf("atomicWrite: unexpected error %v", err)
	}
	bs, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(bs) != "@swarm|persona:reviewer|count:1\n" {
		t.Errorf("content=%q, want trailing-newline-normalised", string(bs))
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("leftover .tmp on success path: %v", matches)
	}
}
