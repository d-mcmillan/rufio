package retract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

func TestPropagateRetract_NoInboxes_NoOp(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "retracted")
	os.MkdirAll(dir, 0o755)
	rec := BuildRecord("1727000000-t", "outdated", "agent-a", "ts")
	os.WriteFile(filepath.Join(dir, "1727000000-t.gdl"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)

	if err := PropagateRetract(root, "1727000000-t"); err != nil {
		t.Fatalf("PropagateRetract: %v", err)
	}
	// Expect: no error; nothing else happened (live/inbox/ doesn't exist).
}

func TestPropagateRetract_AppendsToMatchingInbox(t *testing.T) {
	root := t.TempDir()
	inboxDir := filepath.Join(root, "live", "inbox", "agent-b")
	os.MkdirAll(inboxDir, 0o755)
	inboxFile := filepath.Join(inboxDir, "1727000000-t.gdl")
	os.WriteFile(inboxFile, []byte("@thought|id:1727000000-t|author:agent-a|content:x|ts:ts\n"), 0o644)

	retractDir := filepath.Join(root, "live", "retracted")
	os.MkdirAll(retractDir, 0o755)
	rec := BuildRecord("1727000000-t", "outdated", "agent-a", "2026-05-12T12:00:00Z")
	os.WriteFile(filepath.Join(retractDir, "1727000000-t.gdl"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)

	if err := PropagateRetract(root, "1727000000-t"); err != nil {
		t.Fatalf("PropagateRetract: %v", err)
	}

	bs, _ := os.ReadFile(inboxFile)
	if !strings.Contains(string(bs), "@thought|") {
		t.Errorf("inbox lost original @thought line:\n%s", bs)
	}
	if !strings.Contains(string(bs), "@retract|target:1727000000-t") {
		t.Errorf("inbox missing appended @retract line:\n%s", bs)
	}
}

func TestPropagateRetract_Idempotent(t *testing.T) {
	root := t.TempDir()
	inboxDir := filepath.Join(root, "live", "inbox", "agent-b")
	os.MkdirAll(inboxDir, 0o755)
	os.WriteFile(filepath.Join(inboxDir, "1727000000-t.gdl"), []byte("@thought|id:1727000000-t|content:x|ts:ts\n"), 0o644)
	retractDir := filepath.Join(root, "live", "retracted")
	os.MkdirAll(retractDir, 0o755)
	rec := BuildRecord("1727000000-t", "r", "a", "ts")
	os.WriteFile(filepath.Join(retractDir, "1727000000-t.gdl"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)

	if err := PropagateRetract(root, "1727000000-t"); err != nil {
		t.Fatal(err)
	}
	if err := PropagateRetract(root, "1727000000-t"); err != nil {
		t.Fatal(err)
	}

	bs, _ := os.ReadFile(filepath.Join(inboxDir, "1727000000-t.gdl"))
	count := strings.Count(string(bs), "@retract|")
	if count != 1 {
		t.Errorf("expected exactly 1 @retract line, got %d:\n%s", count, bs)
	}
}

func TestPropagateRetract_MultipleInboxes_AllAppended(t *testing.T) {
	root := t.TempDir()
	for _, a := range []string{"agent-b", "agent-c", "agent-d"} {
		inboxDir := filepath.Join(root, "live", "inbox", a)
		os.MkdirAll(inboxDir, 0o755)
		os.WriteFile(filepath.Join(inboxDir, "1727000000-t.gdl"), []byte("@thought|id:1727000000-t|content:x|ts:ts\n"), 0o644)
	}
	retractDir := filepath.Join(root, "live", "retracted")
	os.MkdirAll(retractDir, 0o755)
	rec := BuildRecord("1727000000-t", "r", "a", "ts")
	os.WriteFile(filepath.Join(retractDir, "1727000000-t.gdl"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)

	if err := PropagateRetract(root, "1727000000-t"); err != nil {
		t.Fatal(err)
	}

	for _, a := range []string{"agent-b", "agent-c", "agent-d"} {
		bs, _ := os.ReadFile(filepath.Join(root, "live", "inbox", a, "1727000000-t.gdl"))
		if !strings.Contains(string(bs), "@retract|") {
			t.Errorf("agent %s inbox missing @retract: %s", a, bs)
		}
	}
}
