// H1c — the v1.1 aesthetic-pass recall-render rewrite (R25's worst
// offender). Sort ts-DESC across types, unified columns, preserve
// thought subtype, every row gets an id column, TAB separator.
package recall

import (
	"bytes"
	"strings"
	"testing"
)

// Sort: every other read surface (thoughts list, fleet) is ts-DESC.
// recall used to group by type then ts-ASC — defying the convention and
// burying the most recent record below older noise of every other kind.
func TestRecall_SortedDesc_AcrossAllTypes(t *testing.T) {
	// Pin RUFIO_FULL_IDS=1 so the test assertions can reference the full
	// id literally. Short-id rendering is exercised in the CLI tests.
	t.Setenv("RUFIO_FULL_IDS", "1")
	var buf bytes.Buffer
	// Intentionally mixed types in non-sorted order so the renderer's
	// internal sort is exercised (not just the input order).
	recs := []RecallRecord{
		{Type: "thought", ID: "001-aaaaaa", TS: "2026-05-20T10:00:00Z", Author: "a", Subject: "x:1", Content: "early thought", ThoughtType: "decision"},
		{Type: "observation", ID: "obs-bbbbbb", TS: "2026-05-20T12:00:00Z", Author: "a", Subject: "x:1", Predicate: "is", Object: "active"},
		{Type: "reason", ID: "rea-cccccc", TS: "2026-05-20T11:00:00Z", Author: "a", Content: "because step A"},
		{Type: "thought", ID: "002-dddddd", TS: "2026-05-20T13:00:00Z", Author: "a", Subject: "x:2", Content: "later thought", ThoughtType: "focus"},
	}
	if err := RenderColumnar(&buf, recs); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	// First line must be the most recent (13:00), last the oldest (10:00).
	// We assert ID presence in each line in order so the test stays
	// resilient to other column tweaks.
	wantOrder := []string{"002-dddddd", "obs-bbbbbb", "rea-cccccc", "001-aaaaaa"}
	for i, want := range wantOrder {
		if !strings.Contains(lines[i], want) {
			t.Errorf("row %d missing id %q (out-of-order sort?): %q", i, want, lines[i])
		}
	}
}

// Row shape MUST be unified across types — same columns in the same
// order. Prior recall emitted 6/7/4 columns depending on kind so an
// awk/grep consumer could not address fields positionally. The unified
// shape is: <reltime> <type[:subtype]> <author> <id> <subject> <scope>
// [<content-snippet>]. Reason rows substitute their content for the
// subject column so the rightmost cells stay populated.
func TestRecall_RowShape_UnifiedColumns(t *testing.T) {
	var buf bytes.Buffer
	recs := []RecallRecord{
		{Type: "thought", ID: "001-aaaaaa", TS: "2026-05-20T13:00:00Z", Author: "agent-a", Subject: "x:1", Content: "thoughtful", ThoughtType: "decision", Scope: "agent"},
		{Type: "observation", ID: "obs-bbbbbb", TS: "2026-05-20T12:00:00Z", Author: "agent-a", Subject: "x:1", Predicate: "is", Object: "active", Scope: "agent"},
		{Type: "reason", ID: "rea-cccccc", TS: "2026-05-20T11:00:00Z", Author: "agent-a", Content: "step A"},
	}
	if err := RenderColumnar(&buf, recs); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		fields := strings.Split(line, "\t")
		// At least 4: reltime, type, author, id. Subject/scope/content may
		// be empty trailing columns for some types but the LEADING four
		// must always be present. We allow >=4 so a content snippet
		// trailing column doesn't break the contract — but it MUST be at
		// least 4 separated by TAB (the unified machine contract).
		if len(fields) < 4 {
			t.Errorf("row %q has %d TAB-separated fields, want >=4", line, len(fields))
		}
	}
}

// R25 collapse: every @thought record rendered as bare `thought`, losing
// the on-disk type (decision/focus/hypothesis/question). The unified
// renderer must preserve the subtype as `thought:<subtype>`.
func TestRecall_ThoughtSubtypePreserved(t *testing.T) {
	var buf bytes.Buffer
	recs := []RecallRecord{
		{Type: "thought", ID: "001-aaaaaa", TS: "2026-05-20T13:00:00Z", Author: "a", Subject: "x:1", Content: "c", ThoughtType: "decision"},
		{Type: "thought", ID: "002-bbbbbb", TS: "2026-05-20T12:00:00Z", Author: "a", Subject: "x:2", Content: "c", ThoughtType: "focus"},
		{Type: "thought", ID: "003-cccccc", TS: "2026-05-20T11:00:00Z", Author: "a", Subject: "x:3", Content: "c", ThoughtType: "hypothesis"},
		{Type: "thought", ID: "004-dddddd", TS: "2026-05-20T10:00:00Z", Author: "a", Subject: "x:4", Content: "c"}, // no subtype
	}
	if err := RenderColumnar(&buf, recs); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"thought:decision", "thought:focus", "thought:hypothesis"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing subtype-preserving label %q:\n%s", want, out)
		}
	}
	// A bare-`thought` row WITHOUT a subtype should NOT emit a trailing
	// colon. Either render bare `thought` or omit the column appropriately.
	if strings.Contains(out, "thought:\t") || strings.Contains(out, "thought: ") {
		t.Errorf("subtype-less thought emitted empty trailing colon:\n%s", out)
	}
}

// R25 #134-equivalent: a `reason` row's id and decision-linkage were both
// dropped from text output. Spec says reason rows render `type=reason:<decision-id-short>`.
// At minimum, the row MUST surface the reason's own id (no empty id column).
func TestRecall_ReasonRows_ShowDecisionID(t *testing.T) {
	t.Setenv("RUFIO_FULL_IDS", "1")
	var buf bytes.Buffer
	rec := RecallRecord{
		Type:    "reason",
		ID:      "rea-cccccc",
		TS:      "2026-05-20T12:00:00Z",
		Author:  "a",
		Content: "step A",
		// Path embeds the decision id by convention:
		// live/reasoning/<author>/<decision-id>/<reason-id>.gdl
		Path: "/tmp/x/live/reasoning/a/dec-aaaaaa/rea-cccccc.gdl",
	}
	if err := RenderColumnar(&buf, []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "rea-cccccc") {
		t.Errorf("reason row missing its own id (R25 #134):\n%s", out)
	}
	// Decision linkage SHOULD surface either as `reason:dec-aaaaaa` (short
	// form) or in some unambiguous way. We assert the decision-id suffix
	// is somewhere on the row so the agent can wire `--parent`.
	if !strings.Contains(out, "dec-aaaaaa") {
		t.Errorf("reason row missing decision linkage 'dec-aaaaaa':\n%s", out)
	}
}

// Every row gets a non-empty id column. Previously reason rows had ""
// (R25 #134); the unified renderer mandates an id column for all.
func TestRecall_AllRowsHaveIDColumn(t *testing.T) {
	var buf bytes.Buffer
	recs := []RecallRecord{
		{Type: "thought", ID: "001-aaaaaa", TS: "2026-05-20T13:00:00Z", Author: "a", Subject: "x:1", Content: "c", ThoughtType: "decision"},
		{Type: "observation", ID: "obs-bbbbbb", TS: "2026-05-20T12:00:00Z", Author: "a", Subject: "x:1", Predicate: "is", Object: "active"},
		{Type: "reason", ID: "rea-cccccc", TS: "2026-05-20T11:00:00Z", Author: "a", Content: "step A", Path: "/r/live/reasoning/a/dec-aaaaaa/rea-cccccc.gdl"},
		{Type: "given", ID: "", TS: "2026-05-20T10:00:00Z", Author: "unknown", Subject: "given/policy.md", Path: "/r/given/policy.md"},
	}
	if err := RenderColumnar(&buf, recs); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	// Each row's 4th TAB field is the id slot. For records that genuinely
	// have no actionable id (given/), the path basename is the next-best
	// stable handle, so the column must be non-empty. The exact contents
	// are flexible; emptiness is the failure.
	for i, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			t.Errorf("row %d %q has %d fields, want >=4", i, line, len(fields))
			continue
		}
		if fields[3] == "" {
			t.Errorf("row %d %q has empty id column", i, line)
		}
	}
}

// Pipe-friendly separator: a single TAB. Awk/cut consumers need a
// machine-stable field delimiter. Old recall used "  " (two spaces) which
// is hostile to `awk -F '\t'`.
func TestRecall_TabSeparated_AwkCompatible(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type: "thought", ID: "001-aaaaaa", TS: "2026-05-20T13:00:00Z",
		Author: "a", Subject: "x:1", Content: "c", ThoughtType: "decision",
	}
	if err := RenderColumnar(&buf, []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimRight(buf.String(), "\n")
	if !strings.Contains(line, "\t") {
		t.Errorf("row %q has no TAB separator", line)
	}
	// And we should NOT regress to two-space gutters now that we're TAB.
	if strings.Contains(line, "  ") {
		t.Errorf("row %q has double-space gutter (regression to old format)", line)
	}
}
