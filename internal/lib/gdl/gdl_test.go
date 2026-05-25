package gdl

import (
	"strings"
	"testing"
)

func TestEscapeValue_EscapesPipeColonBackslash(t *testing.T) {
	got := EscapeValue(`a|b:c\d`)
	want := `a\|b\:c\\d`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEscapeUnescape_RoundTrips(t *testing.T) {
	orig := "value with | and : and \\ characters"
	if got := UnescapeValue(EscapeValue(orig)); got != orig {
		t.Errorf("round-trip mismatch: got %q, want %q", got, orig)
	}
}

func TestParseLine_ParsesSimpleRecord(t *testing.T) {
	r, err := ParseLine("@config|name:demo|version:1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("got nil record")
	}
	if r.Type != "config" {
		t.Errorf("type: got %q, want %q", r.Type, "config")
	}
	if r.Get("name") != "demo" || r.Get("version") != "1" {
		t.Errorf("fields: got %+v, want name=demo, version=1", r.Fields)
	}
}

func TestParseLine_IgnoresHashComments(t *testing.T) {
	r, err := ParseLine("# a comment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Errorf("expected nil for comment, got %+v", r)
	}
}

func TestParseLine_IgnoresSlashComments(t *testing.T) {
	r, err := ParseLine("// also a comment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Errorf("expected nil for comment, got %+v", r)
	}
}

func TestParseLine_IgnoresBlankLines(t *testing.T) {
	r, err := ParseLine("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r != nil {
		t.Errorf("expected nil for blank, got %+v", r)
	}
	r2, _ := ParseLine("   ")
	if r2 != nil {
		t.Errorf("expected nil for whitespace-only, got %+v", r2)
	}
}

func TestParseLine_HandlesEscapedPipesInValues(t *testing.T) {
	r, err := ParseLine(`@product|id:P001|desc:10\|20\|30 pack`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Get("desc") != "10|20|30 pack" {
		t.Errorf("desc: got %q, want %q", r.Get("desc"), "10|20|30 pack")
	}
}

func TestParseLine_HandlesEscapedColonsInTimestamps(t *testing.T) {
	r, err := ParseLine(`@event|ts:2026-05-09T14\:32\:00Z|kind:push`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Get("ts") != "2026-05-09T14:32:00Z" {
		t.Errorf("ts: got %q, want %q", r.Get("ts"), "2026-05-09T14:32:00Z")
	}
}

func TestParseLine_ThrowsOnMalformed(t *testing.T) {
	_, err := ParseLine("not a gdl line")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "must start with @") {
		t.Errorf("error message: got %q, want substring %q", err.Error(), "must start with @")
	}
}

func TestParseDocument_ParsesMultipleRecords(t *testing.T) {
	text := "@a|x:1\n# comment\n\n@b|y:2\n"
	recs, err := ParseDocument(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if recs[0].Type != "a" || recs[1].Type != "b" {
		t.Errorf("types: got %q, %q; want a, b", recs[0].Type, recs[1].Type)
	}
}

func TestRenderLine_RendersWithFieldOrder(t *testing.T) {
	r := Record{Type: "ref", Fields: []RecordField{
		{"path", "given/foo.md"},
		{"version", "2"},
	}}
	got := RenderLine(r)
	want := "@ref|path:given/foo.md|version:2"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderLine_EscapesValues(t *testing.T) {
	r := Record{Type: "x", Fields: []RecordField{{"v", "a|b"}}}
	got := RenderLine(r)
	want := `@x|v:a\|b`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAppendRecord_AppendsWithNewlineWhenMissing(t *testing.T) {
	_, result := AppendRecord("@a|x:1", Record{Type: "b", Fields: []RecordField{{"y", "2"}}})
	want := "@a|x:1\n@b|y:2\n"
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestAppendRecord_AppendsWithoutExtraNewlineWhenPresent(t *testing.T) {
	_, result := AppendRecord("@a|x:1\n", Record{Type: "b", Fields: []RecordField{{"y", "2"}}})
	want := "@a|x:1\n@b|y:2\n"
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestAppendRecord_AppendsToEmpty(t *testing.T) {
	_, result := AppendRecord("", Record{Type: "a", Fields: []RecordField{{"x", "1"}}})
	want := "@a|x:1\n"
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

// G/#R28: line-oriented GDL must escape newlines in field values so a
// multi-line --content doesn't poison the substrate. Without this, write
// emits multiple lines under a single @record header and the next read
// errors with "malformed GDL line (must start with @): <embedded line>".

func TestEscapeValue_EscapesNewline(t *testing.T) {
	got := EscapeValue("line1\nline2")
	want := `line1\nline2`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEscapeValue_EscapesCarriageReturn(t *testing.T) {
	got := EscapeValue("a\rb")
	want := `a\rb`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEscapeValue_EscapesAllControlCharsTogether(t *testing.T) {
	got := EscapeValue("a|b:c\\d\ne\rf")
	want := `a\|b\:c\\d\ne\rf`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUnescapeValue_UnescapesNewline(t *testing.T) {
	got := UnescapeValue(`line1\nline2`)
	want := "line1\nline2"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUnescapeValue_UnescapesCarriageReturn(t *testing.T) {
	got := UnescapeValue(`a\rb`)
	want := "a\rb"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRoundtrip_ContentWithNewlines(t *testing.T) {
	orig := "line1\nline2\n- nested"
	rec := Record{Type: "thought", Fields: []RecordField{
		{"id", "abc"}, {"content", orig},
	}}
	line := RenderLine(rec)
	// Rendered line must NOT contain a raw newline — round-trip would
	// otherwise wedge the parser at "(must start with @): line2".
	for _, ch := range line {
		if ch == '\n' || ch == '\r' {
			t.Fatalf("RenderLine output contains raw newline/CR: %q", line)
		}
	}
	parsed, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine error after RenderLine: %v\nline=%q", err, line)
	}
	if got := parsed.Get("content"); got != orig {
		t.Errorf("content round-trip: got %q, want %q", got, orig)
	}
}

func TestRoundtrip_ContentWithPipes(t *testing.T) {
	orig := "a|b|c"
	rec := Record{Type: "thought", Fields: []RecordField{{"content", orig}}}
	line := RenderLine(rec)
	parsed, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if got := parsed.Get("content"); got != orig {
		t.Errorf("content round-trip: got %q, want %q", got, orig)
	}
}

func TestRoundtrip_ContentWithColons(t *testing.T) {
	orig := "key:value:nested"
	rec := Record{Type: "thought", Fields: []RecordField{{"content", orig}}}
	line := RenderLine(rec)
	parsed, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if got := parsed.Get("content"); got != orig {
		t.Errorf("content round-trip: got %q, want %q", got, orig)
	}
}

func TestRoundtrip_ContentWithBackslashThenN_PreservesLiteralBackslashN(t *testing.T) {
	// Distinguishes literal "\\n" (backslash-n in source) from an actual
	// newline. After symmetric escape, the rendered line should encode
	// this as a backslash so it round-trips byte-identically.
	orig := `not a newline: \n`
	rec := Record{Type: "x", Fields: []RecordField{{"v", orig}}}
	line := RenderLine(rec)
	parsed, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine error: %v", err)
	}
	if got := parsed.Get("v"); got != orig {
		t.Errorf("v round-trip: got %q, want %q", got, orig)
	}
}

func TestUnescapeValue_BackwardCompat_LegacyEscapesStillStripBackslash(t *testing.T) {
	// The TS-era behaviour stripped backslash before any character. We
	// preserve that for known escape sequences (\\, \|, \:, \n, \r). For
	// any OTHER backslash-prefixed byte the unescaper must NOT introduce a
	// regression — historically `\x` decoded as `x`. Keep that contract so
	// hand-written legacy GDL with `\x` still parses to `x`.
	got := UnescapeValue(`\x`)
	want := "x"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderLine_DoesNotContainRawNewlines_WithMultilineValue(t *testing.T) {
	r := Record{Type: "thought", Fields: []RecordField{
		{"content", "first\nsecond\nthird"},
	}}
	got := RenderLine(r)
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("RenderLine produced line with raw newline/CR: %q", got)
	}
}

func TestParseDocument_AfterMultilineWrite_Succeeds(t *testing.T) {
	// Simulates the bug: write a multi-line value, then parse the resulting
	// document. With the fix, the document is one well-formed line; without
	// it, parsing errors on the embedded line.
	r := Record{Type: "thought", Fields: []RecordField{
		{"id", "1"}, {"content", "line1\nline2\n- nested"},
	}}
	_, doc := AppendRecord("", r)
	recs, err := ParseDocument(doc)
	if err != nil {
		t.Fatalf("ParseDocument errored on doc written by AppendRecord: %v\ndoc=%q", err, doc)
	}
	if len(recs) != 1 {
		t.Fatalf("len(recs)=%d, want 1; doc=%q", len(recs), doc)
	}
	if got := recs[0].Get("content"); got != "line1\nline2\n- nested" {
		t.Errorf("content round-trip: got %q", got)
	}
}
