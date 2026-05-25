package retract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWrite_RenameFailLeavesNoTmp forces os.Rename(tmp, target) to fail by
// pre-creating live/retracted/<targetID>.gdl as a non-empty directory.
// WriteFile of "<targetID>.gdl.tmp" still succeeds; the Rename then fails.
// The deferred cleanup must remove the stranded tmp.
func TestWrite_RenameFailLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "retracted")
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

	rec := BuildRecord("thought-1", "obsolete", "agent-a", "ts")
	err := Write(root, "thought-1", rec)
	if err == nil {
		t.Fatal("Write: want non-nil error on forced Rename failure, got nil")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("stranded .tmp after forced Rename failure: %v", matches)
	}
}

// TestAppendRetractIfMissing_SuccessNoTmp is the success-path regression
// guard for the read-modify-write helper. The Rename-fail strand cannot be
// deterministically injected in-process here: the rename target is the SAME
// path that is ReadFile'd first, so the target-as-non-empty-dir technique
// (the only injection that keeps the directory writable so the deferred
// Remove can demonstrably clean up) would break the prior ReadFile. This
// test instead pins that the success path leaves NO *.tmp behind, proving
// the added defer is a harmless no-op on success (no regression). The
// Rename-fail-path correctness of the identical defer is proven by the
// pure-write helper tests (retract.Write, attention, thought, ...).
func TestAppendRetractIfMissing_SuccessNoTmp(t *testing.T) {
	root := t.TempDir()
	inboxDir := filepath.Join(root, "live", "inbox", "agent-a")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inboxPath := filepath.Join(inboxDir, "t-1.gdl")
	if err := os.WriteFile(inboxPath, []byte("@thought|id:t-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	retractLine := "@retract|target:t-1|by:a|ts:t"
	if err := appendRetractIfMissing(inboxPath, retractLine, "t-1"); err != nil {
		t.Fatalf("appendRetractIfMissing: unexpected error %v", err)
	}
	bs, err := os.ReadFile(inboxPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bs), retractLine) {
		t.Errorf("inbox file missing appended retract line: %q", string(bs))
	}
	matches, _ := filepath.Glob(filepath.Join(inboxDir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("leftover .tmp on success path: %v", matches)
	}
}
