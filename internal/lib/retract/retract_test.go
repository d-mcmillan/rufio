package retract

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// --- Lookup ---

func TestLookup_NoMatch_ReturnsNoSuchThoughtError(t *testing.T) {
	root := t.TempDir()
	_, err := Lookup(root, "1727000000-missing")
	var got *rufioerr.NoSuchThoughtError
	if !errors.As(err, &got) {
		t.Fatalf("want *NoSuchThoughtError, got %T %v", err, err)
	}
}

func TestLookup_FoundInOutbox_ReturnsAuthor(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "outbox", "agent-a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "1727000000-thought1.gdl")
	if err := os.WriteFile(target, []byte("@thought|id:1727000000-thought1|...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	author, err := Lookup(root, "1727000000-thought1")
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if author != "agent-a" {
		t.Errorf("author=%q, want agent-a", author)
	}
}

func TestLookup_MultipleAgentDirs_FindsCorrectOne(t *testing.T) {
	root := t.TempDir()
	for _, a := range []string{"agent-a", "agent-b", "agent-c"} {
		dir := filepath.Join(root, "live", "outbox", a)
		os.MkdirAll(dir, 0o755)
	}
	target := filepath.Join(root, "live", "outbox", "agent-b", "1727000000-thought1.gdl")
	os.WriteFile(target, []byte("@thought|..."), 0o644)

	author, err := Lookup(root, "1727000000-thought1")
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if author != "agent-b" {
		t.Errorf("author=%q, want agent-b", author)
	}
}

// --- BuildRecord ---

func TestBuildRecord_RendersWithFieldOrder(t *testing.T) {
	rec := BuildRecord("1727000000-target", "outdated info", "agent-a", "2026-05-12T12:00:00Z")
	if rec.Type != "retract" {
		t.Fatalf("Type=%q, want retract", rec.Type)
	}
	want := []string{"target", "reason", "by", "ts"}
	gotKeys := make([]string, 0, len(rec.Fields))
	for _, f := range rec.Fields {
		gotKeys = append(gotKeys, f.Key)
	}
	if len(gotKeys) != 4 {
		t.Fatalf("got %d fields, want 4", len(gotKeys))
	}
	for i, w := range want {
		if gotKeys[i] != w {
			t.Errorf("field[%d]=%q, want %q (got=%v)", i, gotKeys[i], w, gotKeys)
		}
	}
	if rec.Get("target") != "1727000000-target" {
		t.Error("target mismatch")
	}
	if rec.Get("reason") != "outdated info" {
		t.Error("reason mismatch")
	}
	if rec.Get("by") != "agent-a" {
		t.Error("by mismatch")
	}
	if rec.Get("ts") != "2026-05-12T12:00:00Z" {
		t.Error("ts mismatch")
	}
}

// --- Write ---

func TestWrite_CreatesRetractedFile(t *testing.T) {
	root := t.TempDir()
	rec := BuildRecord("1727000000-target", "reason here", "agent-a", "ts")
	if err := Write(root, "1727000000-target", rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	target := filepath.Join(root, "live", "retracted", "1727000000-target.gdl")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("file not at expected path: %v", err)
	}
}

func TestWrite_RoundTripsThroughParser(t *testing.T) {
	root := t.TempDir()
	rec := BuildRecord("1727000000-t", "r", "agent-a", "ts")
	if err := Write(root, "1727000000-t", rec); err != nil {
		t.Fatal(err)
	}
	bs, _ := os.ReadFile(filepath.Join(root, "live", "retracted", "1727000000-t.gdl"))
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v\n%q", err, bs)
	}
	if len(records) != 1 || records[0].Type != "retract" {
		t.Fatalf("got %d records, type[0]=%q", len(records), records[0].Type)
	}
}

func TestWrite_NoTempFileLeftBehind(t *testing.T) {
	root := t.TempDir()
	rec := BuildRecord("1-t", "r", "a", "ts")
	if err := Write(root, "1-t", rec); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "retracted", "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("leftover .tmp: %v", matches)
	}
}

// G/#R28: --reason on `rufio retract` accepted multi-line free-text,
// poisoning live/retracted/<id>.gdl.
func TestRetract_MultilineReason_DoesNotPoisonSubstrate(t *testing.T) {
	root := t.TempDir()
	multiline := "primary reason\ndetailed context\n- bullet"
	rec := BuildRecord("1-t", multiline, "agent-a", "ts")
	if err := Write(root, "1-t", rec); err != nil {
		t.Fatal(err)
	}
	bs, err := os.ReadFile(filepath.Join(root, "live", "retracted", "1-t.gdl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// One trailing newline only; nothing embedded.
	if want := 1; bytesCount(bs, '\n') != want {
		t.Errorf("retract file has %d newlines, want %d (poisoned): %q", bytesCount(bs, '\n'), want, string(bs))
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument errored after multi-line retract: %v\nfile: %q", err, string(bs))
	}
	if len(records) != 1 || records[0].Get("reason") != multiline {
		t.Errorf("retract reason round-trip mismatch:\n records=%+v\nwant reason=%q", records, multiline)
	}
}

func bytesCount(b []byte, c byte) int {
	n := 0
	for _, x := range b {
		if x == c {
			n++
		}
	}
	return n
}
