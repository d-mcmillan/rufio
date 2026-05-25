// Unit tests for the swarm package. Cover the four pure-function
// concerns first (Validate*, FormatAgentID, GenerateBatch, NextSeq,
// BuildSpawnedRecord), then exercise the on-disk round-trip
// (ReadAll/Append) under t.TempDir to hit the lock + atomic-rename path
// for real. No mocks — same discipline as week-1 (CLAUDE.md §Testing).
package swarm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

func TestValidatePersona_Happy(t *testing.T) {
	for _, p := range []string{"support", "qa-bot", "pr-reviewer-2", "a"} {
		if err := ValidatePersona(p); err != nil {
			t.Errorf("ValidatePersona(%q) = %v, want nil", p, err)
		}
	}
}

func TestValidatePersona_Empty(t *testing.T) {
	err := ValidatePersona("")
	var ipe *rufioerr.InvalidPersonaError
	if !errors.As(err, &ipe) {
		t.Fatalf("got %T, want *InvalidPersonaError", err)
	}
	if ipe.Persona != "" {
		t.Errorf("ipe.Persona = %q, want empty", ipe.Persona)
	}
	if ipe.ExitCode() != 2 {
		t.Errorf("ExitCode = %d, want 2", ipe.ExitCode())
	}
}

func TestValidatePersona_Malformed(t *testing.T) {
	for _, p := range []string{"Foo", "1bad", "-leading", "has_underscore", "with space", "UPPER"} {
		err := ValidatePersona(p)
		var ipe *rufioerr.InvalidPersonaError
		if !errors.As(err, &ipe) {
			t.Errorf("ValidatePersona(%q) = %v, want *InvalidPersonaError", p, err)
			continue
		}
		if ipe.Persona != p {
			t.Errorf("ipe.Persona = %q, want %q", ipe.Persona, p)
		}
	}
}

func TestValidateCount_Valid(t *testing.T) {
	for _, n := range []int{1, 2, 25, 50} {
		if err := ValidateCount(n); err != nil {
			t.Errorf("ValidateCount(%d) = %v, want nil", n, err)
		}
	}
}

func TestValidateCount_Invalid(t *testing.T) {
	for _, n := range []int{0, -1, 51, 100} {
		err := ValidateCount(n)
		var ice *rufioerr.InvalidCountError
		if !errors.As(err, &ice) {
			t.Errorf("ValidateCount(%d) = %v, want *InvalidCountError", n, err)
			continue
		}
		if ice.Count != n {
			t.Errorf("ice.Count = %d, want %d", ice.Count, n)
		}
		if ice.ExitCode() != 2 {
			t.Errorf("ExitCode = %d, want 2", ice.ExitCode())
		}
	}
}

func TestBuildSpawnedRecord_FieldOrder(t *testing.T) {
	rec := BuildSpawnedRecord("support", "support-001", "2026-05-14T10:00:00Z")
	if rec.Type != "spawned" {
		t.Errorf("Type = %q, want %q", rec.Type, "spawned")
	}
	wantKeys := []string{"persona", "agent", "ts"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields) = %d, want %d", len(rec.Fields), len(wantKeys))
	}
	for i, want := range wantKeys {
		if rec.Fields[i].Key != want {
			t.Errorf("Fields[%d].Key = %q, want %q", i, rec.Fields[i].Key, want)
		}
	}
	if rec.Fields[0].Value != "support" {
		t.Errorf("persona = %q", rec.Fields[0].Value)
	}
	if rec.Fields[1].Value != "support-001" {
		t.Errorf("agent = %q", rec.Fields[1].Value)
	}
	if rec.Fields[2].Value != "2026-05-14T10:00:00Z" {
		t.Errorf("ts = %q", rec.Fields[2].Value)
	}
}

func TestFormatAgentID_ZeroPadded(t *testing.T) {
	cases := []struct {
		persona string
		seq     int
		want    string
	}{
		{"support", 1, "support-001"},
		{"qa-bot", 9, "qa-bot-009"},
		{"x", 42, "x-042"},
		{"x", 100, "x-100"},
	}
	for _, c := range cases {
		if got := FormatAgentID(c.persona, c.seq); got != c.want {
			t.Errorf("FormatAgentID(%q,%d) = %q, want %q", c.persona, c.seq, got, c.want)
		}
	}
}

func TestGenerateBatch(t *testing.T) {
	// count=3 starting at 1 (the per-plan baseline)
	got := GenerateBatch("support", 3, 1)
	want := []string{"support-001", "support-002", "support-003"}
	if !sliceEq(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// count=2 starting at 5 (per-plan)
	got = GenerateBatch("qa", 2, 5)
	want = []string{"qa-005", "qa-006"}
	if !sliceEq(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// count=0 produces an empty slice (caller-tolerant — Validate
	// fires first in the CLI path; the function itself must not panic).
	if got := GenerateBatch("any", 0, 1); len(got) != 0 {
		t.Errorf("count=0: got %v, want empty", got)
	}
}

func TestNextSeq_NoExistingRecords(t *testing.T) {
	if got := NextSeq(nil, "support"); got != 1 {
		t.Errorf("NextSeq(nil,support) = %d, want 1", got)
	}
	if got := NextSeq([]Spawned{}, "support"); got != 1 {
		t.Errorf("NextSeq([],support) = %d, want 1", got)
	}
}

func TestNextSeq_OtherPersonaIgnored(t *testing.T) {
	existing := []Spawned{
		{Persona: "qa", Agent: "qa-001"},
		{Persona: "qa", Agent: "qa-002"},
	}
	if got := NextSeq(existing, "support"); got != 1 {
		t.Errorf("NextSeq(qa-only, support) = %d, want 1", got)
	}
}

func TestNextSeq_ContiguousAndGap(t *testing.T) {
	// Contiguous 1+2 → 3
	existing := []Spawned{
		{Persona: "support", Agent: "support-001"},
		{Persona: "support", Agent: "support-002"},
	}
	if got := NextSeq(existing, "support"); got != 3 {
		t.Errorf("contiguous: NextSeq = %d, want 3", got)
	}

	// Gap 1+3 → 4 (NOT gap-filling; max+1 per D21.4 + NextSeq docs)
	existing = []Spawned{
		{Persona: "support", Agent: "support-001"},
		{Persona: "support", Agent: "support-003"},
	}
	if got := NextSeq(existing, "support"); got != 4 {
		t.Errorf("gap: NextSeq = %d, want 4 (not 2 — no gap fill)", got)
	}
}

func TestNextSeq_MixedPersonas(t *testing.T) {
	existing := []Spawned{
		{Persona: "qa", Agent: "qa-005"},
		{Persona: "support", Agent: "support-001"},
		{Persona: "qa", Agent: "qa-010"},
		{Persona: "support", Agent: "support-007"},
	}
	if got := NextSeq(existing, "support"); got != 8 {
		t.Errorf("support: NextSeq = %d, want 8", got)
	}
	if got := NextSeq(existing, "qa"); got != 11 {
		t.Errorf("qa: NextSeq = %d, want 11", got)
	}
}

func TestReadAll_MissingFile(t *testing.T) {
	root := t.TempDir()
	got, err := ReadAll(root)
	if err != nil {
		t.Fatalf("ReadAll on missing file: err = %v, want nil", err)
	}
	if got != nil && len(got) != 0 {
		t.Errorf("ReadAll on missing file: got %v, want empty", got)
	}
}

func TestAppend_HappyPath_WritesFile(t *testing.T) {
	root := t.TempDir()
	added, skipped, err := Append(root, "support", []string{"support-001", "support-002"}, "2026-05-14T10:00:00Z")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !sliceEq(added, []string{"support-001", "support-002"}) {
		t.Errorf("added = %v", added)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want empty", skipped)
	}

	// File on disk parses back as 2 @spawned records, in order.
	bs, err := os.ReadFile(filepath.Join(root, ".rufio", "swarm.local.gdl"))
	if err != nil {
		t.Fatalf("read swarm.local.gdl: %v", err)
	}
	recs, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("len(records) = %d, want 2: %s", len(recs), bs)
	}
	if recs[0].Get("agent") != "support-001" || recs[1].Get("agent") != "support-002" {
		t.Errorf("record order broken: %s", bs)
	}
	if recs[0].Get("persona") != "support" {
		t.Errorf("persona = %q", recs[0].Get("persona"))
	}
	if recs[0].Get("ts") != "2026-05-14T10:00:00Z" {
		t.Errorf("ts = %q", recs[0].Get("ts"))
	}
}

func TestAppend_PreservesExistingRecords(t *testing.T) {
	root := t.TempDir()
	if _, _, err := Append(root, "support", []string{"support-001"}, "ts1"); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if _, _, err := Append(root, "qa", []string{"qa-001", "qa-002"}, "ts2"); err != nil {
		t.Fatalf("second append: %v", err)
	}

	got, err := ReadAll(root)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %+v", len(got), got)
	}
	if got[0].Agent != "support-001" || got[1].Agent != "qa-001" || got[2].Agent != "qa-002" {
		t.Errorf("append order broken: %+v", got)
	}
}

func TestAppend_Idempotent_SkipsDuplicates(t *testing.T) {
	// Defense-in-depth path (D21.7 + Append docstring). In normal flow
	// NextSeq guarantees fresh ids, so this exercises the safety net
	// for hand-edited files or callers that bypass NextSeq.
	root := t.TempDir()
	if _, _, err := Append(root, "support", []string{"support-001", "support-002"}, "ts1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Re-request the same ids — both should land in `skipped`.
	added, skipped, err := Append(root, "support", []string{"support-001", "support-002"}, "ts2")
	if err != nil {
		t.Fatalf("second append: %v", err)
	}
	if len(added) != 0 {
		t.Errorf("added = %v, want empty", added)
	}
	if !sliceEq(skipped, []string{"support-001", "support-002"}) {
		t.Errorf("skipped = %v", skipped)
	}

	// File should still contain only the original 2 records (no
	// duplicates written, no ts overwrite).
	got, err := ReadAll(root)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len after dedup = %d, want 2", len(got))
	}
	if got[0].TS != "ts1" {
		t.Errorf("ts not preserved: got %q, want ts1", got[0].TS)
	}
}

func TestAppend_MixedAddedAndSkipped(t *testing.T) {
	root := t.TempDir()
	if _, _, err := Append(root, "support", []string{"support-001"}, "ts1"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	added, skipped, err := Append(root, "support",
		[]string{"support-001", "support-002", "support-003"}, "ts2")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !sliceEq(added, []string{"support-002", "support-003"}) {
		t.Errorf("added = %v", added)
	}
	if !sliceEq(skipped, []string{"support-001"}) {
		t.Errorf("skipped = %v", skipped)
	}
}

func TestAppend_ConcurrentSafeUnderLock(t *testing.T) {
	// Two goroutines append disjoint id sets to the same file. With
	// .rufio/locks/swarm.lock both must observe the other's writes
	// before composing their own — final file must contain ALL ids
	// from both batches.
	root := t.TempDir()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, _, err := Append(root, "a", []string{"a-001", "a-002"}, "ts-a"); err != nil {
			t.Errorf("goroutine a: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, _, err := Append(root, "b", []string{"b-001", "b-002"}, "ts-b"); err != nil {
			t.Errorf("goroutine b: %v", err)
		}
	}()
	wg.Wait()

	got, err := ReadAll(root)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("len = %d, want 4 (no records lost to race): %+v", len(got), got)
	}
	have := map[string]bool{}
	for _, s := range got {
		have[s.Agent] = true
	}
	for _, want := range []string{"a-001", "a-002", "b-001", "b-002"} {
		if !have[want] {
			t.Errorf("missing %q from concurrent append: %+v", want, got)
		}
	}
}

func TestAppend_EmptyInput_NoFileCreated(t *testing.T) {
	root := t.TempDir()
	added, skipped, err := Append(root, "support", nil, "ts1")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if len(added) != 0 || len(skipped) != 0 {
		t.Errorf("added=%v skipped=%v want both empty", added, skipped)
	}
	// File should NOT have been created — empty input means no work.
	if _, err := os.Stat(filepath.Join(root, ".rufio", "swarm.local.gdl")); !os.IsNotExist(err) {
		t.Errorf("file should not exist for empty input, got err = %v", err)
	}
}

func TestAppend_EndToEnd_NextSeqAcrossCalls(t *testing.T) {
	// End-to-end exercise of the demo workflow: first call seeds
	// support-001..002. Second call uses NextSeq+GenerateBatch+Append
	// and must produce 003..004 — NOT 001..002 again (no
	// over-promotion to the skipped path).
	root := t.TempDir()
	first := GenerateBatch("support", 2, NextSeq(nil, "support"))
	if _, _, err := Append(root, "support", first, "ts1"); err != nil {
		t.Fatalf("first: %v", err)
	}

	existing, err := ReadAll(root)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	next := NextSeq(existing, "support")
	if next != 3 {
		t.Errorf("nextSeq = %d, want 3", next)
	}
	second := GenerateBatch("support", 2, next)
	added, skipped, err := Append(root, "support", second, "ts2")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !sliceEq(added, []string{"support-003", "support-004"}) {
		t.Errorf("added = %v", added)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want empty", skipped)
	}
}

func TestReadAll_TolerantOfUnrelatedRecords(t *testing.T) {
	// A hand-edited file with a non-@spawned record should not poison
	// ReadAll — we skip unknown record types per the ReadAll docs.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rufio"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := strings.Join([]string{
		"@note|text:hand-edited preamble",
		"@spawned|persona:support|agent:support-001|ts:2026-01-01T00:00:00Z",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, ".rufio", "swarm.local.gdl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadAll(root)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 1 || got[0].Agent != "support-001" {
		t.Errorf("got = %+v, want one support-001", got)
	}
}

// sliceEq compares two string slices for value equality.
func sliceEq(a, b []string) bool {
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
