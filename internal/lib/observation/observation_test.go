package observation

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

func TestValidatePredicate_RejectsEmpty(t *testing.T) {
	err := ValidatePredicate("")
	var got *rufioerr.InvalidPredicateError
	if !errors.As(err, &got) || got.Predicate != "" {
		t.Errorf("want *InvalidPredicateError{}; got %T %v", err, err)
	}
}

func TestValidatePredicate_RejectsMalformed(t *testing.T) {
	cases := []string{"HAS-STATUS", "-leading", "has status", "has,comma", "1start"}
	for _, p := range cases {
		err := ValidatePredicate(p)
		var got *rufioerr.InvalidPredicateError
		if !errors.As(err, &got) || got.Predicate != p {
			t.Errorf("ValidatePredicate(%q): want *InvalidPredicateError{Predicate:%q}, got %T %v", p, p, err, err)
		}
	}
}

func TestValidatePredicate_AcceptsValid(t *testing.T) {
	for _, p := range []string{"has-status", "owns_account", "is", "x1", "long-multi_segment-token"} {
		if err := ValidatePredicate(p); err != nil {
			t.Errorf("ValidatePredicate(%q): unexpected %v", p, err)
		}
	}
}

func TestValidateObject_RejectsEmpty(t *testing.T) {
	for _, raw := range []string{"", "   ", "\t\n"} {
		err := ValidateObject(raw)
		var got *rufioerr.InvalidContentError
		if !errors.As(err, &got) {
			t.Fatalf("want *InvalidContentError, got %T %v", err, err)
		}
		if got.Field != "object" {
			t.Errorf("Field=%q want object", got.Field)
		}
	}
}

func TestValidateObject_AcceptsNonEmpty(t *testing.T) {
	for _, v := range []string{"active", "5821", "Customer Name with spaces", "true", `{"json":"object"}`} {
		if err := ValidateObject(v); err != nil {
			t.Errorf("ValidateObject(%q): unexpected %v", v, err)
		}
	}
}

func TestParseConfidence_AbsentReturnsOne(t *testing.T) {
	got, err := ParseConfidence("")
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if got != 1.0 {
		t.Errorf("ParseConfidence(\"\")=%v, want 1.0", got)
	}
}

func TestParseConfidence_RejectsOutOfRange(t *testing.T) {
	for _, raw := range []string{"-0.1", "1.1", "2", "-1", "10"} {
		_, err := ParseConfidence(raw)
		var got *rufioerr.InvalidConfidenceError
		if !errors.As(err, &got) {
			t.Errorf("ParseConfidence(%q): want *InvalidConfidenceError, got %T %v", raw, err, err)
		}
	}
}

func TestParseConfidence_RejectsMalformed(t *testing.T) {
	for _, raw := range []string{"abc", "0.5x", " 0.5 ", "1/2"} {
		_, err := ParseConfidence(raw)
		var got *rufioerr.InvalidConfidenceError
		if !errors.As(err, &got) {
			t.Errorf("ParseConfidence(%q): want *InvalidConfidenceError, got %T %v", raw, err, err)
		}
	}
}

func TestParseConfidence_AcceptsBoundary(t *testing.T) {
	for _, raw := range []string{"0", "0.0", "0.5", "1", "1.0", "0.999999"} {
		got, err := ParseConfidence(raw)
		if err != nil {
			t.Errorf("ParseConfidence(%q): unexpected %v", raw, err)
			continue
		}
		expected, _ := strconv.ParseFloat(raw, 64)
		if got != expected {
			t.Errorf("ParseConfidence(%q)=%v, want %v", raw, got, expected)
		}
	}
}

func TestSubjectPath_SingleColonSubject(t *testing.T) {
	got := SubjectPath("/tmp/root", "customer:5821", "1727000000-abc123")
	want := filepath.Join("/tmp/root", "learned", "customer", "5821", "1727000000-abc123.gdlm")
	if got != want {
		t.Errorf("SubjectPath:\n got %q\nwant %q", got, want)
	}
}

func TestSubjectPath_MultiSegmentSubject(t *testing.T) {
	got := SubjectPath("/tmp/root", "agent:foo:bar", "1727000000-abc123")
	want := filepath.Join("/tmp/root", "learned", "agent", "foo", "bar", "1727000000-abc123.gdlm")
	if got != want {
		t.Errorf("SubjectPath:\n got %q\nwant %q", got, want)
	}
}

func TestBuildObservationRecord_RendersWithFieldOrder(t *testing.T) {
	rec := BuildObservationRecord(ObservationInput{
		ID: "1727000000-abc123", Author: "agent-a",
		Subject: "customer:5821", Predicate: "has-status", Object: "active",
		Scope: "fleet", Topics: []string{"crm", "p1"},
		Confidence: 0.9, TS: "2026-05-12T12:00:00Z",
	})
	if rec.Type != "observation" {
		t.Fatalf("Type=%q, want observation", rec.Type)
	}
	want := []string{"id", "author", "subject", "predicate", "object", "scope", "topics", "confidence", "ts"}
	got := keysOf(rec)
	if !equalStrings(got, want) {
		t.Errorf("field order=%v, want %v", got, want)
	}
	if rec.Get("confidence") != "0.9" {
		t.Errorf("confidence=%q, want 0.9", rec.Get("confidence"))
	}
}

func TestBuildObservationRecord_OmitsTopicsWhenEmpty(t *testing.T) {
	rec := BuildObservationRecord(ObservationInput{
		ID: "1-a", Author: "a", Subject: "x:1", Predicate: "is", Object: "y",
		Scope: "agent", Confidence: 1.0, TS: "ts",
	})
	for _, f := range rec.Fields {
		if f.Key == "topics" {
			t.Errorf("unexpected topics field: %+v", f)
		}
	}
	if rec.Get("confidence") != "1" {
		t.Errorf("confidence=%q, want '1' (rendered form of 1.0)", rec.Get("confidence"))
	}
}

func TestBuildObservationRecord_ConfidenceFormatting(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"}, {1, "1"}, {0.5, "0.5"}, {0.9, "0.9"}, {0.123456, "0.123456"},
	}
	for _, tc := range cases {
		rec := BuildObservationRecord(ObservationInput{
			ID: "1-a", Author: "a", Subject: "x:1", Predicate: "is", Object: "y",
			Scope: "agent", Confidence: tc.in, TS: "ts",
		})
		if rec.Get("confidence") != tc.want {
			t.Errorf("Confidence=%v rendered as %q, want %q", tc.in, rec.Get("confidence"), tc.want)
		}
	}
}

func TestWrite_RoundTripsThroughParser(t *testing.T) {
	root := t.TempDir()
	rec := BuildObservationRecord(ObservationInput{
		ID: "1-aaaaaa", Author: "agent-a", Subject: "customer:5821",
		Predicate: "has-status", Object: "active", Scope: "fleet",
		Topics: []string{"crm"}, Confidence: 0.9, TS: "ts",
	})
	if err := Write(root, "customer:5821", "1-aaaaaa", rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	target := SubjectPath(root, "customer:5821", "1-aaaaaa")
	bs, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", target, err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v\nfile: %q", err, string(bs))
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].Type != "observation" {
		t.Errorf("Type=%q", records[0].Type)
	}
	if records[0].Get("id") != "1-aaaaaa" {
		t.Errorf("id=%q", records[0].Get("id"))
	}
}

func TestWrite_CreatesNestedSubjectDirectory(t *testing.T) {
	root := t.TempDir()
	rec := BuildObservationRecord(ObservationInput{
		ID: "1-a", Author: "a", Subject: "agent:foo:bar",
		Predicate: "is", Object: "y", Scope: "agent",
		Confidence: 1.0, TS: "ts",
	})
	if err := Write(root, "agent:foo:bar", "1-a", rec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "learned", "agent", "foo", "bar", "1-a.gdlm")); err != nil {
		t.Errorf("file not at expected nested path: %v", err)
	}
}

func TestWrite_NoTempFileLeftBehind(t *testing.T) {
	root := t.TempDir()
	rec := BuildObservationRecord(ObservationInput{
		ID: "1-a", Author: "a", Subject: "x:1", Predicate: "is",
		Object: "y", Scope: "agent", Confidence: 1.0, TS: "ts",
	})
	if err := Write(root, "x:1", "1-a", rec); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "learned", "x", "1", "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("leftover .tmp: %v", matches)
	}
}

// --- #76 provenance fields (additive, omit-empty) ---------------------------

// TestBuildObservationRecord_NoProvenance_ByteIdentical is the regression
// gate for #76: a normal hand-authored `rufio observe` supplies none of the
// new provenance fields, so its rendered record MUST be byte-identical to
// pre-change (NO empty origin:/confirmed-by:/source: keys leak in). The
// literal below was captured at 0642433 (parent of this change) directly
// from gdl.RenderLine(BuildObservationRecord(...)) for the exact same input.
func TestBuildObservationRecord_NoProvenance_ByteIdentical(t *testing.T) {
	rec := BuildObservationRecord(ObservationInput{
		ID: "1-aaaaaa", Author: "agent-a", Subject: "customer:5821",
		Predicate: "has-status", Object: "active", Scope: "fleet",
		Topics: []string{"crm"}, Confidence: 0.9, TS: "2026-05-12T12:00:00Z",
	})
	got := gdl.RenderLine(rec) + "\n"
	// Frozen pre-#76 wire form (subject/ts colons GDL-escaped to \:).
	const want = `@observation|id:1-aaaaaa|author:agent-a|subject:customer\:5821|predicate:has-status|object:active|scope:fleet|topics:crm|confidence:0.9|ts:2026-05-12T12\:00\:00Z` + "\n"
	if got != want {
		t.Errorf("non-promoted observe record changed (must be byte-identical):\n got %q\nwant %q", got, want)
	}
	for _, k := range []string{"origin", "confirmed-by", "source"} {
		for _, f := range rec.Fields {
			if f.Key == k {
				t.Errorf("provenance key %q must be ABSENT on a non-promoted record, got %+v", k, f)
			}
		}
	}
}

// TestBuildObservationRecord_WithProvenance_AppendsFields verifies the
// auto-promote path: when Origin/ConfirmedBy/Source are set they serialize
// additively AFTER ts as origin:/confirmed-by:/source: (confirmed-by is the
// comma-joined sorted-deduped confirmer ids, same join as topics).
func TestBuildObservationRecord_WithProvenance_AppendsFields(t *testing.T) {
	rec := BuildObservationRecord(ObservationInput{
		ID: "1-a", Author: "auto-promote", Subject: "customer:5821",
		Predicate: "asserted", Object: "prefers email", Scope: "deployment",
		Topics: []string{"billing"}, Confidence: 1.0, TS: "ts",
		Origin: "agent-a", ConfirmedBy: []string{"agent-b", "agent-c", "agent-d"},
		Source: "1727000000-aaaaaa",
	})
	want := []string{"id", "author", "subject", "predicate", "object", "scope", "topics", "confidence", "ts", "origin", "confirmed-by", "source"}
	if got := keysOf(rec); !equalStrings(got, want) {
		t.Errorf("field order=%v, want %v", got, want)
	}
	if rec.Get("origin") != "agent-a" {
		t.Errorf("origin=%q want agent-a", rec.Get("origin"))
	}
	if rec.Get("confirmed-by") != "agent-b,agent-c,agent-d" {
		t.Errorf("confirmed-by=%q want agent-b,agent-c,agent-d", rec.Get("confirmed-by"))
	}
	if rec.Get("source") != "1727000000-aaaaaa" {
		t.Errorf("source=%q want 1727000000-aaaaaa", rec.Get("source"))
	}
}

// TestBuildObservationRecord_PartialProvenance_OmitsEmptyOnly confirms each
// provenance field is INDEPENDENTLY omit-empty: setting only Origin must not
// emit confirmed-by:/source: keys.
func TestBuildObservationRecord_PartialProvenance_OmitsEmptyOnly(t *testing.T) {
	rec := BuildObservationRecord(ObservationInput{
		ID: "1-a", Author: "a", Subject: "x:1", Predicate: "is", Object: "y",
		Scope: "agent", Confidence: 1.0, TS: "ts", Origin: "agent-a",
	})
	if rec.Get("origin") != "agent-a" {
		t.Errorf("origin=%q want agent-a", rec.Get("origin"))
	}
	for _, f := range rec.Fields {
		if f.Key == "confirmed-by" || f.Key == "source" {
			t.Errorf("empty provenance key must be omitted, got %+v", f)
		}
	}
}

// G/#R28: --object accepted multi-line free-text. Writing it then
// reading back with `recall --types=observation` blew up at the gdl
// parser. Regression guard via the verb-level path.
func TestObserve_MultilineObject_DoesNotPoisonSubstrate(t *testing.T) {
	root := t.TempDir()
	multiline := "first observation\nsecond detail\n- bullet"
	rec := BuildObservationRecord(ObservationInput{
		ID: "1-a", Author: "alice", Subject: "team:r",
		Predicate: "noticed", Object: multiline, Scope: "fleet",
		Confidence: 1.0, TS: "ts",
	})
	if err := Write(root, "team:r", "1-a", rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	target := filepath.Join(root, "learned", "team", "r", "1-a.gdlm")
	bs, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := string(bs)
	body = body[:len(body)-1] // trim trailing \n
	for _, c := range body {
		if c == '\n' || c == '\r' {
			t.Fatalf("observation file body contains raw newline (poisoned): %q", string(bs))
		}
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument errored after multi-line observe: %v\nfile: %q", err, string(bs))
	}
	if got := records[0].Get("object"); got != multiline {
		t.Errorf("object round-trip mismatch:\n got=%q\nwant=%q", got, multiline)
	}
}

// helpers (sibling-consistent with thought_test.go and attention_test.go)
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
