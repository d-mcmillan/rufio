package reason

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

func TestValidateDecision_AbsentIsOK(t *testing.T) {
	if err := ValidateDecision(""); err != nil {
		t.Errorf("unexpected %v", err)
	}
}

func TestValidateDecision_RejectsMalformed(t *testing.T) {
	cases := []string{"abc", "123", "123-abc", "1727000000-ABC123", "1727000000-abc12$"}
	for _, d := range cases {
		err := ValidateDecision(d)
		var got *rufioerr.InvalidDecisionError
		if !errors.As(err, &got) || got.ID != d {
			t.Errorf("ValidateDecision(%q): want *InvalidDecisionError{ID:%q}, got %T %v", d, d, err, err)
		}
	}
}

func TestValidateDecision_AcceptsCanonical(t *testing.T) {
	for _, d := range []string{"1727000000-a1b2c3", "1-abcdef"} {
		if err := ValidateDecision(d); err != nil {
			t.Errorf("ValidateDecision(%q): unexpected %v", d, err)
		}
	}
}

// writeOutboxThought seeds live/outbox/<author>/<id>.gdl with a single
// @thought record of the given type, mirroring the on-disk shape `think`
// produces. Used to exercise ValidateDecisionTarget's resolve+type check.
func writeOutboxThought(t *testing.T, root, author, id, typ string) {
	t.Helper()
	dir := filepath.Join(root, "live", "outbox", author)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir outbox: %v", err)
	}
	rec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: id},
		{Key: "author", Value: author},
		{Key: "type", Value: typ},
		{Key: "subject", Value: "customer:1"},
		{Key: "content", Value: "x"},
		{Key: "scope", Value: "fleet"},
		{Key: "ts", Value: "ts"},
	}}
	contents := gdl.RenderLine(rec) + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".gdl"), []byte(contents), 0o644); err != nil {
		t.Fatalf("write thought: %v", err)
	}
}

func TestValidateDecisionTarget_EmptyIsNoop(t *testing.T) {
	// Omitted --decision must not trigger a resolve (free reasoning step).
	if err := ValidateDecisionTarget(t.TempDir(), ""); err != nil {
		t.Errorf("unexpected %v", err)
	}
}

func TestValidateDecisionTarget_ResolvesDecisionThought(t *testing.T) {
	root := t.TempDir()
	writeOutboxThought(t, root, "agent-a", "1727000000-dec123", "decision")
	if err := ValidateDecisionTarget(root, "1727000000-dec123"); err != nil {
		t.Errorf("want nil for a real type:decision thought, got %v", err)
	}
}

func TestValidateDecisionTarget_RejectsHypothesisThought(t *testing.T) {
	root := t.TempDir()
	writeOutboxThought(t, root, "agent-a", "1727000000-hyp123", "hypothesis")
	err := ValidateDecisionTarget(root, "1727000000-hyp123")
	var got *rufioerr.NotADecisionError
	if !errors.As(err, &got) {
		t.Fatalf("want *NotADecisionError, got %T %v", err, err)
	}
	if got.ID != "1727000000-hyp123" || got.Type != "hypothesis" {
		t.Errorf("NotADecisionError{ID:%q,Type:%q}, want {1727000000-hyp123,hypothesis}", got.ID, got.Type)
	}
}

func TestValidateDecisionTarget_RejectsMissingThought(t *testing.T) {
	root := t.TempDir()
	err := ValidateDecisionTarget(root, "9999999999-zzzzzz")
	var got *rufioerr.NoSuchThoughtError
	if !errors.As(err, &got) || got.ID != "9999999999-zzzzzz" {
		t.Errorf("want *NoSuchThoughtError{ID:9999999999-zzzzzz}, got %T %v", err, err)
	}
}

func TestPath_NoDecision_OmitsSubdir(t *testing.T) {
	got := Path("/tmp/root", "agent-a", "", "1-aaaaaa")
	want := filepath.Join("/tmp/root", "live", "reasoning", "agent-a", "1-aaaaaa.gdl")
	if got != want {
		t.Errorf("Path:\n got %q\nwant %q", got, want)
	}
}

func TestPath_WithDecision_NestsUnderDecisionDir(t *testing.T) {
	got := Path("/tmp/root", "agent-a", "1727000000-dec123", "1-aaaaaa")
	want := filepath.Join("/tmp/root", "live", "reasoning", "agent-a", "1727000000-dec123", "1-aaaaaa.gdl")
	if got != want {
		t.Errorf("Path:\n got %q\nwant %q", got, want)
	}
}

func TestBuildRecord_RendersWithFieldOrder(t *testing.T) {
	rec := BuildRecord(ReasonInput{
		ID: "1-aaaaaa", Author: "agent-a", Content: "step one",
		Scope:  "fleet",
		Topics: []string{"t1"}, Parent: "1727000000-par123",
		Decision: "1727000000-dec123", TS: "ts",
	})
	if rec.Type != "reason" {
		t.Fatalf("Type=%q, want reason", rec.Type)
	}
	// #125: scope is now a required field, in the slot after content
	// (mirrors the @thought id/author/type/subject/content/scope order).
	want := []string{"id", "author", "content", "scope", "topics", "parent", "decision", "ts"}
	got := keysOf(rec)
	if !equalStrings(got, want) {
		t.Errorf("field order=%v, want %v", got, want)
	}
	if rec.Get("scope") != "fleet" {
		t.Errorf("scope=%q, want fleet", rec.Get("scope"))
	}
}

func TestBuildRecord_OmitsTopicsParentDecisionWhenEmpty(t *testing.T) {
	rec := BuildRecord(ReasonInput{
		ID: "1-a", Author: "a", Content: "step", Scope: "fleet", TS: "ts",
	})
	for _, k := range []string{"topics", "parent", "decision"} {
		for _, f := range rec.Fields {
			if f.Key == k {
				t.Errorf("expected no %q field, got %+v", k, f)
			}
		}
	}
	// id, author, content, scope, ts are the always-present fields after
	// #125 — scope was added to make the privacy floor reach @reason.
	for _, k := range []string{"id", "author", "content", "scope", "ts"} {
		if rec.Get(k) == "" {
			t.Errorf("required field %q missing/empty", k)
		}
	}
}

func TestWrite_NoDecision_AtTopLevel(t *testing.T) {
	root := t.TempDir()
	rec := BuildRecord(ReasonInput{
		ID: "1-aaaaaa", Author: "a", Content: "step", TS: "ts",
	})
	if err := Write(root, "a", "", "1-aaaaaa", rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	target := filepath.Join(root, "live", "reasoning", "a", "1-aaaaaa.gdl")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("file not at expected path: %v", err)
	}
}

func TestWrite_WithDecision_NestsUnderDecisionDir(t *testing.T) {
	root := t.TempDir()
	rec := BuildRecord(ReasonInput{
		ID: "1-aaaaaa", Author: "a", Content: "step", Decision: "1727000000-dec123", TS: "ts",
	})
	if err := Write(root, "a", "1727000000-dec123", "1-aaaaaa", rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	target := filepath.Join(root, "live", "reasoning", "a", "1727000000-dec123", "1-aaaaaa.gdl")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("file not at expected nested path: %v", err)
	}
}

func TestWrite_RoundTripsThroughParser(t *testing.T) {
	root := t.TempDir()
	rec := BuildRecord(ReasonInput{
		ID: "1-a", Author: "agent-a", Content: "step one",
		Topics: []string{"x"}, Parent: "1727000000-par123",
		Decision: "1727000000-dec123", TS: "ts",
	})
	if err := Write(root, "agent-a", "1727000000-dec123", "1-a", rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	target := Path(root, "agent-a", "1727000000-dec123", "1-a")
	bs, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v\nfile: %q", err, string(bs))
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].Type != "reason" {
		t.Errorf("Type=%q", records[0].Type)
	}
	if records[0].Get("decision") != "1727000000-dec123" {
		t.Errorf("decision=%q", records[0].Get("decision"))
	}
}

func TestWrite_NoTempFileLeftBehind(t *testing.T) {
	root := t.TempDir()
	rec := BuildRecord(ReasonInput{ID: "1-a", Author: "a", Content: "x", TS: "t"})
	if err := Write(root, "a", "", "1-a", rec); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "a", "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("leftover .tmp: %v", matches)
	}
}

// helpers
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func keysOf(r gdl.Record) []string {
	out := make([]string, 0, len(r.Fields))
	for _, f := range r.Fields {
		out = append(out, f.Key)
	}
	return out
}

var _ = strings.TrimSpace
