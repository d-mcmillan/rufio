package thought

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

func TestValidateType_RejectsUnknown(t *testing.T) {
	cases := []string{"", "thought", "HYPOTHESIS", "decisions", "focused"}
	for _, v := range cases {
		err := ValidateType(v)
		var got *rufioerr.InvalidTypeError
		if !errors.As(err, &got) {
			t.Errorf("ValidateType(%q): want *InvalidTypeError, got %T (%v)", v, err, err)
			continue
		}
		if got.Value != v {
			t.Errorf("ValidateType(%q): Value=%q, want %q", v, got.Value, v)
		}
	}
}

func TestValidateType_AcceptsAllEnumValues(t *testing.T) {
	for _, v := range []string{"hypothesis", "observation", "decision", "question", "focus"} {
		if err := ValidateType(v); err != nil {
			t.Errorf("ValidateType(%q): unexpected %v", v, err)
		}
	}
}

func TestValidateSubject_RejectsEmpty(t *testing.T) {
	err := ValidateSubject("")
	var got *rufioerr.InvalidSubjectError
	if !errors.As(err, &got) {
		t.Fatalf("want *InvalidSubjectError, got %T (%v)", err, err)
	}
	if got.Subject != "" {
		t.Errorf("Subject=%q, want empty", got.Subject)
	}
}

func TestValidateSubject_RejectsMalformed(t *testing.T) {
	cases := []string{"customer", "5cust:42", "CUSTOMER:42", "customer:", ":42", "cust omer:1"}
	for _, s := range cases {
		err := ValidateSubject(s)
		var got *rufioerr.InvalidSubjectError
		if !errors.As(err, &got) {
			t.Errorf("ValidateSubject(%q): want *InvalidSubjectError, got %T (%v)", s, err, err)
			continue
		}
		if got.Subject != s {
			t.Errorf("Subject=%q, want %q", got.Subject, s)
		}
	}
}

func TestValidateSubject_AcceptsValid(t *testing.T) {
	for _, s := range []string{"customer:5821", "agent:foo-bar", "order:abc_def", "a:1", "x:1:y:2"} {
		if err := ValidateSubject(s); err != nil {
			t.Errorf("ValidateSubject(%q): unexpected %v", s, err)
		}
	}
}

func TestValidateScope_RejectsUnknown(t *testing.T) {
	for _, v := range []string{"", "AGENT", "global", "private"} {
		err := ValidateScope(v)
		var got *rufioerr.InvalidScopeError
		if !errors.As(err, &got) {
			t.Errorf("ValidateScope(%q): want *InvalidScopeError, got %T (%v)", v, err, err)
		}
	}
}

func TestValidateScope_AcceptsAllEnumValues(t *testing.T) {
	for _, v := range []string{"agent", "deployment", "fleet"} {
		if err := ValidateScope(v); err != nil {
			t.Errorf("ValidateScope(%q): unexpected %v", v, err)
		}
	}
}

func TestValidateContent_RejectsEmpty(t *testing.T) {
	for _, raw := range []string{"", "   ", "\t\n"} {
		err := ValidateContent(raw)
		var got *rufioerr.InvalidContentError
		if !errors.As(err, &got) {
			t.Fatalf("ValidateContent(%q): want *InvalidContentError, got %T (%v)", raw, err, err)
		}
		if got.Field != "content" {
			t.Errorf("Field=%q, want %q", got.Field, "content")
		}
	}
}

func TestValidateContent_AcceptsNonEmpty(t *testing.T) {
	if err := ValidateContent("a real thought"); err != nil {
		t.Errorf("unexpected %v", err)
	}
}

func TestParseTTL_AbsentReturnsZero(t *testing.T) {
	got, err := ParseTTL("")
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if got != 0 {
		t.Errorf("ParseTTL(\"\")=%d, want 0", got)
	}
}

func TestParseTTL_RejectsZeroOrNegative(t *testing.T) {
	for _, raw := range []string{"0", "-1", "-100"} {
		_, err := ParseTTL(raw)
		var got *rufioerr.InvalidTTLError
		if !errors.As(err, &got) {
			t.Errorf("ParseTTL(%q): want *InvalidTTLError, got %T (%v)", raw, err, err)
		}
	}
}

func TestParseTTL_RejectsMalformed(t *testing.T) {
	for _, raw := range []string{"abc", "1.5", "300s", "0x10", " 300 "} {
		_, err := ParseTTL(raw)
		var got *rufioerr.InvalidTTLError
		if !errors.As(err, &got) {
			t.Errorf("ParseTTL(%q): want *InvalidTTLError, got %T (%v)", raw, err, err)
		}
	}
}

func TestParseTTL_AcceptsPositiveInteger(t *testing.T) {
	for _, tc := range []struct {
		in  string
		out int
	}{{"1", 1}, {"300", 300}, {"86400", 86400}} {
		got, err := ParseTTL(tc.in)
		if err != nil {
			t.Errorf("ParseTTL(%q): unexpected %v", tc.in, err)
			continue
		}
		if got != tc.out {
			t.Errorf("ParseTTL(%q)=%d, want %d", tc.in, got, tc.out)
		}
	}
}

func TestValidateParent_AbsentIsOK(t *testing.T) {
	if err := ValidateParent(""); err != nil {
		t.Errorf("ValidateParent(\"\"): unexpected %v", err)
	}
}

func TestValidateParent_RejectsMalformed(t *testing.T) {
	cases := []string{"abc", "123", "123-abc", "123-abcdefg", "1727000000-AB12CD", "1727000000-abc12$"}
	for _, p := range cases {
		err := ValidateParent(p)
		var got *rufioerr.InvalidParentError
		if !errors.As(err, &got) {
			t.Errorf("ValidateParent(%q): want *InvalidParentError, got %T (%v)", p, err, err)
		}
	}
}

func TestValidateParent_AcceptsCanonical(t *testing.T) {
	for _, p := range []string{"1727000000-a1b2c3", "1-abcdef", "9999999999999-z9y8x7"} {
		if err := ValidateParent(p); err != nil {
			t.Errorf("ValidateParent(%q): unexpected %v", p, err)
		}
	}
}

func TestGenerateID_ShapeMatchesParentRegex(t *testing.T) {
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	if err := ValidateParent(id); err != nil {
		t.Errorf("GenerateID() = %q failed parent regex: %v", id, err)
	}
}

func TestGenerateID_UnixMillisIsCurrent(t *testing.T) {
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	parts := strings.SplitN(id, "-", 2)
	if len(parts) != 2 {
		t.Fatalf("malformed id %q", id)
	}
	millis, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("non-int millis prefix: %v", err)
	}
	nowMillis := time.Now().UnixMilli()
	// Allow ±5s window for clock skew between GenerateID and the assertion.
	if diff := nowMillis - millis; diff < -5000 || diff > 5000 {
		t.Errorf("millis prefix %d differs from now %d by more than 5s", millis, nowMillis)
	}
}

func TestGenerateID_NoCollisionsAcross1000Calls(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id, err := GenerateID()
		if err != nil {
			t.Fatalf("GenerateID #%d: %v", i, err)
		}
		if seen[id] {
			t.Fatalf("collision at iteration %d: %q already seen", i, id)
		}
		seen[id] = true
	}
}

func TestGenerateIDFromSource_DeterministicWithSeededReader(t *testing.T) {
	// Test seam: with a deterministic clock + reader the output must be
	// predictable. Used by integration tests in later tasks to pin a known id.
	now := func() int64 { return 1727000000123 }
	src := strings.NewReader("seed-12345-extra-bytes")
	id, err := generateIDFromSource(now, src)
	if err != nil {
		t.Fatalf("generateIDFromSource: %v", err)
	}
	// The deterministic suffix depends on the rune→alphabet mapping (see
	// generateIDFromSource impl). Don't hard-code the suffix here — instead
	// assert (a) the millis prefix is exact, and (b) the suffix is 6 chars
	// from [a-z0-9] and the full id matches parentRegex.
	prefix := "1727000000123-"
	if !strings.HasPrefix(id, prefix) {
		t.Errorf("id=%q missing prefix %q", id, prefix)
	}
	suffix := strings.TrimPrefix(id, prefix)
	if len(suffix) != 6 {
		t.Errorf("suffix len=%d, want 6 (id=%q)", len(suffix), id)
	}
	for _, ch := range suffix {
		if !(ch >= 'a' && ch <= 'z') && !(ch >= '0' && ch <= '9') {
			t.Errorf("suffix has non-alphabet char %q in %q", ch, suffix)
		}
	}
	if err := ValidateParent(id); err != nil {
		t.Errorf("deterministic id %q fails parent regex: %v", id, err)
	}

	// Same input twice → same output (the determinism property).
	src2 := strings.NewReader("seed-12345-extra-bytes")
	id2, _ := generateIDFromSource(now, src2)
	if id != id2 {
		t.Errorf("non-deterministic: id=%q id2=%q", id, id2)
	}
}

func TestGenerateIDFromSource_RejectsShortReader(t *testing.T) {
	now := func() int64 { return 1 }
	src := strings.NewReader("abc") // only 3 bytes; we need 6
	_, err := generateIDFromSource(now, src)
	if err == nil {
		t.Error("expected error from short reader")
	}
}

func TestBuildThoughtRecord_RendersWithFieldOrder(t *testing.T) {
	rec := BuildThoughtRecord(ThoughtInput{
		ID: "1727000000-abc123", Author: "agent-a",
		Type: "hypothesis", Subject: "customer:5821",
		Content: "showing churn signals", Scope: "fleet",
		Topics: []string{"churn", "p1"},
		TS:     "2026-05-11T12:00:00.123456789Z",
		TTL:    300, Parent: "1726999999-xyz789",
	})
	if rec.Type != "thought" {
		t.Fatalf("Type=%q, want thought", rec.Type)
	}
	want := []string{"id", "author", "type", "subject", "content", "scope", "topics", "ts", "ttl", "parent"}
	got := keysOf(rec)
	if !equalStrings(got, want) {
		t.Errorf("field order=%v, want %v", got, want)
	}
	if rec.Get("id") != "1727000000-abc123" {
		t.Errorf("id=%q", rec.Get("id"))
	}
	if rec.Get("topics") != "churn,p1" {
		t.Errorf("topics=%q", rec.Get("topics"))
	}
	if rec.Get("ttl") != "300" {
		t.Errorf("ttl=%q, want 300", rec.Get("ttl"))
	}
}

func TestBuildThoughtRecord_OmitsTopicsAndParentWhenEmpty(t *testing.T) {
	rec := BuildThoughtRecord(ThoughtInput{
		ID: "1-aaaaaa", Author: "a", Type: "focus",
		Subject: "x:1", Content: "c", Scope: "agent",
		TS: "ts", TTL: 0,
	})
	for _, k := range []string{"topics", "parent"} {
		for _, f := range rec.Fields {
			if f.Key == k {
				t.Errorf("expected no %q field, got %+v", k, f)
			}
		}
	}
	// ttl:0 IS present (not optional in the GDL record per D5.1).
	foundTTL := false
	for _, f := range rec.Fields {
		if f.Key == "ttl" {
			foundTTL = true
			if f.Value != "0" {
				t.Errorf("ttl=%q, want \"0\"", f.Value)
			}
		}
	}
	if !foundTTL {
		t.Error("ttl field missing — must always be present (D5.1)")
	}
}

func TestBuildThoughtRecord_OmitsTopicsWhenZeroLengthSlice(t *testing.T) {
	rec := BuildThoughtRecord(ThoughtInput{
		ID: "1-a", Author: "a", Type: "focus", Subject: "x:1", Content: "c",
		Scope: "agent", Topics: []string{}, TS: "ts", TTL: 0,
	})
	for _, f := range rec.Fields {
		if f.Key == "topics" {
			t.Errorf("expected no topics field, got %+v", f)
		}
	}
}

func TestBuildContextBundle_RendersWithDecisionAndRefs(t *testing.T) {
	rec := BuildContextBundle("1727000000-abc123", []string{"sha-1", "sha-2", "sha-3"})
	if rec.Type != "context-bundle" {
		t.Fatalf("Type=%q, want context-bundle", rec.Type)
	}
	want := []string{"decision", "refs"}
	got := keysOf(rec)
	if !equalStrings(got, want) {
		t.Errorf("field order=%v, want %v", got, want)
	}
	if rec.Get("decision") != "1727000000-abc123" {
		t.Errorf("decision=%q", rec.Get("decision"))
	}
	if rec.Get("refs") != "sha-1,sha-2,sha-3" {
		t.Errorf("refs=%q", rec.Get("refs"))
	}
}

func TestBuildContextBundle_EmptyRefsRendersEmptyValue(t *testing.T) {
	rec := BuildContextBundle("1-aaaaaa", nil)
	if rec.Get("refs") != "" {
		t.Errorf("refs=%q, want empty", rec.Get("refs"))
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

func TestCollectGivenLearnedSHAs_EmptyProjectReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	// No .rufio/refs/ at all.
	got, err := CollectGivenLearnedSHAs(root)
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got=%v, want empty", got)
	}
}

func TestCollectGivenLearnedSHAs_CollectsLatestLiveSHAs(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "given", "policy.md"), []byte("v1"))
	if _, err := versioning.AppendRef(root, versioning.RefIntent{
		Path: "given/policy.md", SHA256: "sha-policy-1",
		Stage: versioning.StageLive, Timestamp: "2026-05-11T01:00:00Z", Author: "test",
	}); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(root, "learned", "customer", "5821.gdlm"), []byte("@observation|x"))
	if _, err := versioning.AppendRef(root, versioning.RefIntent{
		Path: "learned/customer/5821.gdlm", SHA256: "sha-learned-1",
		Stage: versioning.StageLive, Timestamp: "2026-05-11T02:00:00Z", Author: "test",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := CollectGivenLearnedSHAs(root)
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2 (got=%v)", len(got), got)
	}
	want := map[string]bool{"sha-policy-1": true, "sha-learned-1": true}
	for _, sha := range got {
		if !want[sha] {
			t.Errorf("unexpected sha %q", sha)
		}
	}
}

func TestCollectGivenLearnedSHAs_OnlyLiveStageIncluded(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "given", "draft.md"), []byte("draft"))
	if _, err := versioning.AppendRef(root, versioning.RefIntent{
		Path: "given/draft.md", SHA256: "sha-draft",
		Stage: versioning.StageDraft, Timestamp: "2026-05-11T01:00:00Z", Author: "test",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := CollectGivenLearnedSHAs(root)
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if len(got) != 0 {
		t.Errorf("draft refs leaked into bundle: %v", got)
	}
}

func TestCollectGivenLearnedSHAs_DeterministicOrder(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"given/a.md", "given/b.md", "learned/c.md"} {
		mustWriteFile(t, filepath.Join(root, p), []byte("x"))
		if _, err := versioning.AppendRef(root, versioning.RefIntent{
			Path: p, SHA256: "sha-" + p, Stage: versioning.StageLive,
			Timestamp: "2026-05-11T01:00:00Z", Author: "test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := CollectGivenLearnedSHAs(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CollectGivenLearnedSHAs(root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(first, second) {
		t.Errorf("non-deterministic order: first=%v second=%v", first, second)
	}
}

// helper
func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWrite_SingleRecord_RoundTripsThroughParser(t *testing.T) {
	root := t.TempDir()
	rec := BuildThoughtRecord(ThoughtInput{
		ID: "1-aaaaaa", Author: "agent-a", Type: "hypothesis",
		Subject: "customer:5821", Content: "churn?", Scope: "fleet",
		Topics: []string{"churn"}, TS: "ts", TTL: 300,
	})
	if err := Write(root, "agent-a", "1-aaaaaa", []gdl.Record{rec}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	bs, err := os.ReadFile(filepath.Join(root, "live", "outbox", "agent-a", "1-aaaaaa.gdl"))
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
	if records[0].Type != "thought" {
		t.Errorf("Type=%q", records[0].Type)
	}
	if records[0].Get("id") != "1-aaaaaa" {
		t.Errorf("id mismatch: %q", records[0].Get("id"))
	}
}

func TestWrite_TwoRecords_BothPresentInFile(t *testing.T) {
	root := t.TempDir()
	thoughtRec := BuildThoughtRecord(ThoughtInput{
		ID: "2-bbbbbb", Author: "a", Type: "decision",
		Subject: "x:1", Content: "approve", Scope: "agent", TS: "ts", TTL: 0,
	})
	bundle := BuildContextBundle("2-bbbbbb", []string{"sha-1", "sha-2"})
	if err := Write(root, "a", "2-bbbbbb", []gdl.Record{thoughtRec, bundle}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	bs, _ := os.ReadFile(filepath.Join(root, "live", "outbox", "a", "2-bbbbbb.gdl"))
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].Type != "thought" || records[1].Type != "context-bundle" {
		t.Errorf("types: [%q, %q]", records[0].Type, records[1].Type)
	}
	if records[1].Get("decision") != "2-bbbbbb" {
		t.Errorf("bundle decision=%q", records[1].Get("decision"))
	}
}

func TestWrite_NoTempFileLeftBehind(t *testing.T) {
	root := t.TempDir()
	rec := BuildThoughtRecord(ThoughtInput{
		ID: "1-a", Author: "a", Type: "focus", Subject: "x:1",
		Content: "x", Scope: "agent", TS: "t", TTL: 0,
	})
	if err := Write(root, "a", "1-a", []gdl.Record{rec}); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "outbox", "a", "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("leftover .tmp: %v", matches)
	}
}

// G/#R28: multi-line --content was poisoning the substrate because the
// gdl writer emitted raw newlines, which then wedged the next read with
// `malformed GDL line (must start with @): <embedded line>`. The fix
// lives in gdl.EscapeValue/UnescapeValue (Option A — one escape layer
// for every free-text field). This regression guard exercises the verb
// path end-to-end: Write a multi-line --content thought, parse the file
// back, assert the content round-trips byte-identical AND parsing the
// document doesn't error.
func TestThink_MultilineContent_DoesNotPoisonSubstrate(t *testing.T) {
	root := t.TempDir()
	multiline := "line1\nline2\n- nested"
	rec := BuildThoughtRecord(ThoughtInput{
		ID: "1-aaaaaa", Author: "alice", Type: "focus",
		Subject: "team:r", Content: multiline, Scope: "fleet",
		TS: "ts", TTL: 0,
	})
	if err := Write(root, "alice", "1-aaaaaa", []gdl.Record{rec}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	bs, err := os.ReadFile(filepath.Join(root, "live", "outbox", "alice", "1-aaaaaa.gdl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// File must contain exactly ONE GDL line — newlines in content must
	// be escaped on write.
	body := strings.TrimRight(string(bs), "\n")
	if strings.Contains(body, "\n") {
		t.Fatalf("file body contains a raw newline (poisons substrate):\n%q", string(bs))
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument errored on multi-line content (substrate poisoned): %v\nfile: %q", err, string(bs))
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if got := records[0].Get("content"); got != multiline {
		t.Errorf("content round-trip mismatch:\n got=%q\nwant=%q", got, multiline)
	}
}

func TestThink_SingleLineContent_StillRoundTripsAfterFix(t *testing.T) {
	// Regression guard: the escape additions must NOT change the on-disk
	// shape for single-line content. Cold-start records read by current
	// agents stay byte-identical to current writer output.
	root := t.TempDir()
	rec := BuildThoughtRecord(ThoughtInput{
		ID: "1-aaaaaa", Author: "alice", Type: "focus",
		Subject: "team:r", Content: "no newlines here", Scope: "fleet",
		TS: "ts", TTL: 0,
	})
	if err := Write(root, "alice", "1-aaaaaa", []gdl.Record{rec}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	bs, _ := os.ReadFile(filepath.Join(root, "live", "outbox", "alice", "1-aaaaaa.gdl"))
	want := "@thought|id:1-aaaaaa|author:alice|type:focus|subject:team\\:r|content:no newlines here|scope:fleet|ts:ts|ttl:0\n"
	if string(bs) != want {
		t.Errorf("single-line shape regressed:\n got=%q\nwant=%q", string(bs), want)
	}
}

func TestWrite_CreatesAgentSubdirectoryIfMissing(t *testing.T) {
	root := t.TempDir()
	// live/outbox/ may not exist yet; the writer must create the per-agent
	// dir even on a fresh project.
	rec := BuildThoughtRecord(ThoughtInput{
		ID: "1-a", Author: "fresh-agent", Type: "focus", Subject: "x:1",
		Content: "x", Scope: "agent", TS: "t", TTL: 0,
	})
	if err := Write(root, "fresh-agent", "1-a", []gdl.Record{rec}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "outbox", "fresh-agent", "1-a.gdl")); err != nil {
		t.Errorf("file not at expected path: %v", err)
	}
}
