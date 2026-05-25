package channels

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WriteMeta and WriteMessage are pure-write helpers (no pre-write Stat
// skip): the Rename-fail strand is deterministically injectable via the
// target-as-non-empty-directory technique. AppendLeave / AppendClose are
// read-modify-write (they ReadFile meta.gdl first), so the same technique
// would break the prior read and the Rename-fail strand is NOT cleanly
// injectable in-process; those are covered by a success-path no-tmp
// regression test (the identical one-line defer's Rename-fail correctness
// is proven by the pure-write tests in this and sibling packages).

func TestWriteMeta_RenameFailLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "channels", "active", "ch-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "meta.gdl")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := BuildMetaRecord("ch-1", "alice", "bob", "topic", "intent", "ts")
	err := WriteMeta(root, "ch-1", rec)
	if err == nil {
		t.Fatal("WriteMeta: want non-nil error on forced Rename failure, got nil")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("stranded .tmp after forced Rename failure: %v", matches)
	}
}

func TestWriteMessage_RenameFailLeavesNoTmp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "channels", "active", "ch-1", "messages")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "m-1.gdl")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := BuildSayRecord("m-1", "ch-1", "alice", "hello", "ts")
	err := WriteMessage(root, "ch-1", "m-1", rec)
	if err == nil {
		t.Fatal("WriteMessage: want non-nil error on forced Rename failure, got nil")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("stranded .tmp after forced Rename failure: %v", matches)
	}
}

func TestAppendLeave_SuccessNoTmp(t *testing.T) {
	root := t.TempDir()
	chDir := filepath.Join(root, "live", "channels", "active", "ch-1")
	if err := os.MkdirAll(chDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(chDir, "meta.gdl")
	if err := os.WriteFile(meta, []byte("@channel|id:ch-1|opener:alice\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendLeave(root, "ch-1", "bob", "2026-05-12T13:00:00Z"); err != nil {
		t.Fatalf("AppendLeave: unexpected error %v", err)
	}
	bs, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bs), "@channel-leave") {
		t.Errorf("meta missing appended leave record: %q", string(bs))
	}
	matches, _ := filepath.Glob(filepath.Join(chDir, "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("leftover .tmp on AppendLeave success path: %v", matches)
	}
}

func TestAppendClose_SuccessNoTmp(t *testing.T) {
	root := t.TempDir()
	chDir := filepath.Join(root, "live", "channels", "active", "ch-1")
	if err := os.MkdirAll(chDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "live", "channels", "closed"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(chDir, "meta.gdl")
	if err := os.WriteFile(meta, []byte("@channel|id:ch-1|opener:alice\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendClose(root, "ch-1", "alice", "2026-05-12T14:00:00Z"); err != nil {
		t.Fatalf("AppendClose: unexpected error %v", err)
	}
	// active/<ch-id> moved to closed/<ch-id>; assert no *.tmp anywhere
	// under the channels subtree.
	var leftover []string
	_ = filepath.Walk(filepath.Join(root, "live", "channels"), func(p string, _ os.FileInfo, _ error) error {
		if strings.HasSuffix(p, ".tmp") {
			leftover = append(leftover, p)
		}
		return nil
	})
	if len(leftover) != 0 {
		t.Errorf("leftover .tmp on AppendClose success path: %v", leftover)
	}
}
