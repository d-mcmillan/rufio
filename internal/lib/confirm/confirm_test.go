package confirm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

func TestBuildConfirm_OmitsEvidenceWhenEmpty(t *testing.T) {
	rec := BuildConfirm("1-target", "agent-a", "", "ts")
	if rec.Type != "confirm" {
		t.Errorf("Type=%q", rec.Type)
	}
	for _, f := range rec.Fields {
		if f.Key == "evidence" {
			t.Errorf("evidence should be omitted: %+v", f)
		}
	}
	// Required keys: target, by, ts.
	for _, k := range []string{"target", "by", "ts"} {
		if rec.Get(k) == "" {
			t.Errorf("missing %q", k)
		}
	}
}

func TestBuildConfirm_IncludesEvidenceWhenSet(t *testing.T) {
	rec := BuildConfirm("1-target", "agent-a", "data shows X", "ts")
	if rec.Get("evidence") != "data shows X" {
		t.Errorf("evidence=%q", rec.Get("evidence"))
	}
}

func TestBuildRefute_RequiresReason(t *testing.T) {
	rec := BuildRefute("1-target", "agent-a", "contradicts Y", "extra ev", "ts")
	if rec.Type != "refute" {
		t.Errorf("Type=%q", rec.Type)
	}
	if rec.Get("reason") != "contradicts Y" {
		t.Errorf("reason=%q", rec.Get("reason"))
	}
	if rec.Get("evidence") != "extra ev" {
		t.Errorf("evidence=%q", rec.Get("evidence"))
	}
}

func TestBuildRefute_OmitsEvidenceWhenEmpty(t *testing.T) {
	rec := BuildRefute("1-target", "agent-a", "wrong", "", "ts")
	for _, f := range rec.Fields {
		if f.Key == "evidence" {
			t.Errorf("evidence should be omitted: %+v", f)
		}
	}
}

func TestAppend_CreatesFile(t *testing.T) {
	root := t.TempDir()
	rec := BuildConfirm("1-target", "agent-a", "", "ts")
	if err := Append(root, "1-target", rec); err != nil {
		t.Fatalf("Append: %v", err)
	}
	target := filepath.Join(root, "live", "confirms", "1-target.gdl")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("file not at expected path: %v", err)
	}
}

func TestAppend_AppendsMultipleRecords(t *testing.T) {
	root := t.TempDir()
	for _, agent := range []string{"agent-a", "agent-b", "agent-c"} {
		rec := BuildConfirm("1-target", agent, "", "ts")
		if err := Append(root, "1-target", rec); err != nil {
			t.Fatal(err)
		}
	}
	bs, _ := os.ReadFile(filepath.Join(root, "live", "confirms", "1-target.gdl"))
	if c := strings.Count(string(bs), "@confirm|"); c != 3 {
		t.Errorf("expected 3 @confirm lines, got %d:\n%s", c, bs)
	}
}

func TestReadAll_MissingFileReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	got, err := ReadAll(root, "1-missing")
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if len(got.Confirms) != 0 || len(got.Refutes) != 0 {
		t.Errorf("got=%+v want empty Tally", got)
	}
}

func TestReadAll_DeduplicatesAuthors(t *testing.T) {
	root := t.TempDir()
	// agent-a confirms twice (should dedupe to 1)
	_ = Append(root, "1", BuildConfirm("1", "agent-a", "", "ts"))
	_ = Append(root, "1", BuildConfirm("1", "agent-a", "more ev", "ts"))
	_ = Append(root, "1", BuildConfirm("1", "agent-b", "", "ts"))
	_ = Append(root, "1", BuildRefute("1", "agent-c", "wrong", "", "ts"))

	got, err := ReadAll(root, "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Confirms) != 2 {
		t.Errorf("confirms len=%d, want 2 (agent-a deduped)", len(got.Confirms))
	}
	if len(got.Refutes) != 1 {
		t.Errorf("refutes len=%d, want 1", len(got.Refutes))
	}
}

func TestReadAll_SortedOutput(t *testing.T) {
	root := t.TempDir()
	for _, a := range []string{"agent-c", "agent-a", "agent-b"} {
		_ = Append(root, "1", BuildConfirm("1", a, "", "ts"))
	}
	got, _ := ReadAll(root, "1")
	want := []string{"agent-a", "agent-b", "agent-c"}
	for i := range want {
		if got.Confirms[i] != want[i] {
			t.Errorf("Confirms[%d]=%q want %q (full: %v)", i, got.Confirms[i], want[i], got.Confirms)
		}
	}
}

func TestConfidence_AllConfirmsReturns1(t *testing.T) {
	tally := Tally{Confirms: []string{"a", "b", "c"}}
	if c := tally.Confidence(); c != 1.0 {
		t.Errorf("Confidence=%v want 1.0", c)
	}
}

func TestConfidence_EmptyReturns0(t *testing.T) {
	if c := (Tally{}).Confidence(); c != 0.0 {
		t.Errorf("Confidence=%v want 0.0", c)
	}
}

func TestConfidence_Mixed(t *testing.T) {
	tally := Tally{Confirms: []string{"a", "b", "c"}, Refutes: []string{"d"}}
	// 3 / (3 + 1) = 0.75
	if c := tally.Confidence(); c < 0.749 || c > 0.751 {
		t.Errorf("Confidence=%v want ~0.75", c)
	}
}

// Compile-time use of gdl import so unused-import doesn't bite if a
// future refactor drops gdl.Record references from the test body.
var _ = gdl.Record{}

// G/#R28: --reason on `rufio refute` accepted multi-line free-text.
// Appending it to live/confirms/<id>.gdl wedged the next read by
// emitting raw newlines mid-record.
func TestRefute_MultilineReason_DoesNotPoisonSubstrate(t *testing.T) {
	root := t.TempDir()
	multiline := "main reason\nwith details\n- bullet point"
	rec := BuildRefute("1-target", "agent-b", multiline, "", "ts")
	if err := Append(root, "1-target", rec); err != nil {
		t.Fatalf("Append: %v", err)
	}
	bs, err := os.ReadFile(filepath.Join(root, "live", "confirms", "1-target.gdl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// File body (sans trailing \n) must be one GDL line.
	body := strings.TrimRight(string(bs), "\n")
	if strings.Contains(body, "\n") || strings.Contains(body, "\r") {
		t.Fatalf("confirm file body contains raw newline (poisoned): %q", string(bs))
	}
	records, err := ReadRecords(root, "1-target")
	if err != nil {
		t.Fatalf("ReadRecords errored after multi-line refute: %v\nfile: %q", err, string(bs))
	}
	if len(records) != 1 || records[0].Reason != multiline {
		t.Errorf("refute reason round-trip mismatch:\n got=%+v\nwant reason=%q", records, multiline)
	}
}
