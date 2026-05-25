package recall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// --- scanGiven ---

func TestScanGiven_MissingDirReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	got, err := scanGiven(root)
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len=%d want 0", len(got))
	}
}

func TestScanGiven_WalksRecursively(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"given/a.md", "given/sub/b.txt"} {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte("content of "+p), 0o644)
	}
	got, err := scanGiven(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 (got=%v)", len(got), got)
	}
	subjects := []string{got[0].Subject, got[1].Subject}
	// Order may vary; check both expected subjects are present.
	wantSet := map[string]bool{"given/a.md": false, "given/sub/b.txt": false}
	for _, s := range subjects {
		wantSet[s] = true
	}
	for s, found := range wantSet {
		if !found {
			t.Errorf("missing subject %q", s)
		}
	}
	for _, r := range got {
		if r.Type != "given" {
			t.Errorf("Type=%q want given", r.Type)
		}
	}
}

func TestScanGiven_BinaryFile_ContentEmpty(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "given"), 0o755)
	binary := append([]byte{0x00, 0x01, 0x02}, []byte("trailing")...)
	os.WriteFile(filepath.Join(root, "given", "binary.dat"), binary, 0o644)
	got, _ := scanGiven(root)
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Content != "" {
		t.Errorf("Content=%q want empty (binary heuristic)", got[0].Content)
	}
	if got[0].Subject != "given/binary.dat" {
		t.Errorf("Subject=%q", got[0].Subject)
	}
}

// --- scanLearned ---

func TestScanLearned_MissingDirReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	got, _ := scanLearned(root)
	if len(got) != 0 {
		t.Errorf("len=%d", len(got))
	}
}

func TestScanLearned_ParsesObservationRecords(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "learned", "customer", "5821")
	os.MkdirAll(dir, 0o755)
	rec := gdl.Record{Type: "observation", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-aaaaaa"},
		{Key: "author", Value: "agent-a"},
		{Key: "subject", Value: "customer:5821"},
		{Key: "predicate", Value: "has-status"},
		{Key: "object", Value: "active"},
		{Key: "scope", Value: "fleet"},
		{Key: "confidence", Value: "0.9"},
		{Key: "ts", Value: "2026-05-12T12:00:00Z"},
	}}
	os.WriteFile(filepath.Join(dir, "1-aaaaaa.gdlm"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)

	got, err := scanLearned(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	r := got[0]
	if r.Type != "observation" {
		t.Error("type")
	}
	if r.Subject != "customer:5821" {
		t.Errorf("subject=%q", r.Subject)
	}
	if r.Predicate != "has-status" {
		t.Errorf("predicate=%q", r.Predicate)
	}
	if r.Object != "active" {
		t.Errorf("object=%q", r.Object)
	}
	if r.Scope != "fleet" {
		t.Errorf("scope=%q", r.Scope)
	}
	if r.Author != "agent-a" {
		t.Errorf("author=%q", r.Author)
	}
}

// TestScanLearned_ExtractsProvenance is the #76 scan gate: a promoted
// @observation carries origin/confirmed-by/source; scanLearned must lift
// them into the RecallRecord so the renderer can surface them.
func TestScanLearned_ExtractsProvenance(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "learned", "customer", "5821")
	os.MkdirAll(dir, 0o755)
	rec := gdl.Record{Type: "observation", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-aaaaaa"},
		{Key: "author", Value: "auto-promote"},
		{Key: "subject", Value: "customer:5821"},
		{Key: "predicate", Value: "asserted"},
		{Key: "object", Value: "prefers email"},
		{Key: "scope", Value: "deployment"},
		{Key: "confidence", Value: "1"},
		{Key: "ts", Value: "2026-05-12T12:00:00Z"},
		{Key: "origin", Value: "agent-a"},
		{Key: "confirmed-by", Value: "agent-b,agent-c,agent-d"},
		{Key: "source", Value: "1727000000-aaaaaa"},
	}}
	os.WriteFile(filepath.Join(dir, "1-aaaaaa.gdlm"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)

	got, err := scanLearned(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	r := got[0]
	if r.Origin != "agent-a" {
		t.Errorf("Origin=%q want agent-a", r.Origin)
	}
	if strings.Join(r.ConfirmedBy, ",") != "agent-b,agent-c,agent-d" {
		t.Errorf("ConfirmedBy=%v want [agent-b agent-c agent-d]", r.ConfirmedBy)
	}
	if r.Source != "1727000000-aaaaaa" {
		t.Errorf("Source=%q want 1727000000-aaaaaa", r.Source)
	}
}

// TestScanLearned_NonPromoted_NoProvenance: a plain observation .gdlm has
// no provenance keys → RecallRecord provenance stays zero-valued.
func TestScanLearned_NonPromoted_NoProvenance(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "learned", "x", "1")
	os.MkdirAll(dir, 0o755)
	rec := gdl.Record{Type: "observation", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-a"}, {Key: "author", Value: "a"},
		{Key: "subject", Value: "x:1"}, {Key: "predicate", Value: "is"},
		{Key: "object", Value: "y"}, {Key: "scope", Value: "agent"},
		{Key: "confidence", Value: "1"}, {Key: "ts", Value: "ts"},
	}}
	os.WriteFile(filepath.Join(dir, "1-a.gdlm"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)

	got, err := scanLearned(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Origin != "" || got[0].Source != "" || len(got[0].ConfirmedBy) != 0 {
		t.Errorf("non-promoted record must have zero provenance, got %+v", got[0])
	}
}

// --- scanOutbox ---

func TestScanOutbox_ParsesThoughtRecords(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "outbox", "agent-a")
	os.MkdirAll(dir, 0o755)
	rec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-aaaaaa"},
		{Key: "author", Value: "agent-a"},
		{Key: "type", Value: "hypothesis"},
		{Key: "subject", Value: "customer:5821"},
		{Key: "content", Value: "churn signals"},
		{Key: "scope", Value: "fleet"},
		{Key: "ts", Value: "2026-05-12T12:00:00Z"},
		{Key: "ttl", Value: "0"},
	}}
	os.WriteFile(filepath.Join(dir, "1-aaaaaa.gdl"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)

	got, err := scanOutbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	r := got[0]
	if r.Type != "thought" {
		t.Error("type")
	}
	if r.Subject != "customer:5821" {
		t.Errorf("subject=%q", r.Subject)
	}
	if r.Content != "churn signals" {
		t.Errorf("content=%q", r.Content)
	}
	if r.Author != "agent-a" {
		t.Errorf("author=%q", r.Author)
	}
	// The id confirm/refute/--parent consume is the file basename stem of
	// live/outbox/<author>/<id>.gdl (retract.Lookup globs by exactly this).
	// recall must surface it so an agent can act on a recalled thought.
	if r.ID != "1-aaaaaa" {
		t.Errorf("ID=%q want 1-aaaaaa (path-basename-stem id)", r.ID)
	}
}

func TestScanOutbox_IDIsPathBasenameStem_NotRecordIDField(t *testing.T) {
	// The canonical id confirm/refute match against is the FILE name
	// (retract.Lookup globs live/outbox/*/<id>.gdl), not the in-record
	// `id` field. They normally agree, but if they ever diverge the
	// path-derived id is authoritative. Pin that.
	root := t.TempDir()
	dir := filepath.Join(root, "live", "outbox", "agent-a")
	os.MkdirAll(dir, 0o755)
	rec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: "stale-record-id"},
		{Key: "author", Value: "agent-a"},
		{Key: "type", Value: "hypothesis"},
		{Key: "subject", Value: "customer:5821"},
		{Key: "content", Value: "x"},
		{Key: "scope", Value: "fleet"},
		{Key: "ts", Value: "2026-05-12T12:00:00Z"},
		{Key: "ttl", Value: "0"},
	}}
	os.WriteFile(filepath.Join(dir, "1747000000000-ab12cd.gdl"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)

	got, err := scanOutbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].ID != "1747000000000-ab12cd" {
		t.Errorf("ID=%q want path-basename-stem 1747000000000-ab12cd", got[0].ID)
	}
}

// TestScanOutbox_CarriesThoughtType is the #89 scan gate: the on-disk
// @thought `type:` (decision|hypothesis|observation|…) must be lifted into
// RecallRecord.ThoughtType so --json can surface it (previously dropped —
// r.Get("type") was never called in scanOutbox).
func TestScanOutbox_CarriesThoughtType(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "outbox", "agent-a")
	os.MkdirAll(dir, 0o755)
	rec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-aaaaaa"},
		{Key: "author", Value: "agent-a"},
		{Key: "type", Value: "decision"},
		{Key: "subject", Value: "customer:5821"},
		{Key: "content", Value: "approve"},
		{Key: "scope", Value: "fleet"},
		{Key: "ts", Value: "2026-05-12T12:00:00Z"},
		{Key: "ttl", Value: "0"},
	}}
	os.WriteFile(filepath.Join(dir, "1-aaaaaa.gdl"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)

	got, err := scanOutbox(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].ThoughtType != "decision" {
		t.Errorf("ThoughtType=%q want decision (#89: on-disk thought type must be carried)", got[0].ThoughtType)
	}
}

// TestScanLearned_ObservationHasPathStemID is the #89 scan gate:
// observations previously left ID=="" — scanLearned must set ID to the
// file basename stem (idFromPath), consistent with how scanOutbox sets
// thought ids.
func TestScanLearned_ObservationHasPathStemID(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "learned", "customer", "5821")
	os.MkdirAll(dir, 0o755)
	rec := gdl.Record{Type: "observation", Fields: []gdl.RecordField{
		{Key: "id", Value: "in-record-id-ignored"},
		{Key: "author", Value: "agent-a"},
		{Key: "subject", Value: "customer:5821"},
		{Key: "predicate", Value: "has-status"},
		{Key: "object", Value: "active"},
		{Key: "scope", Value: "fleet"},
		{Key: "confidence", Value: "0.9"},
		{Key: "ts", Value: "2026-05-12T12:00:00Z"},
	}}
	os.WriteFile(filepath.Join(dir, "1747000000000-obsabc.gdlm"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)

	got, err := scanLearned(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].ID != "1747000000000-obsabc" {
		t.Errorf("ID=%q want 1747000000000-obsabc (#89: path-basename-stem, NOT in-record id)", got[0].ID)
	}
}

func TestScanOutbox_SkipsContextBundle(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "outbox", "agent-a")
	os.MkdirAll(dir, 0o755)
	// File contains BOTH @thought and @context-bundle (decision case).
	thought := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-a"}, {Key: "author", Value: "a"},
		{Key: "type", Value: "decision"}, {Key: "subject", Value: "x:1"},
		{Key: "content", Value: "approve"}, {Key: "scope", Value: "fleet"},
		{Key: "ts", Value: "ts"}, {Key: "ttl", Value: "0"},
	}}
	bundle := gdl.Record{Type: "context-bundle", Fields: []gdl.RecordField{
		{Key: "decision", Value: "1-a"}, {Key: "refs", Value: "sha-1"},
	}}
	content := gdl.RenderLine(thought) + "\n" + gdl.RenderLine(bundle) + "\n"
	os.WriteFile(filepath.Join(dir, "1-a.gdl"), []byte(content), 0o644)

	got, _ := scanOutbox(root)
	if len(got) != 1 {
		t.Fatalf("len=%d want 1 (context-bundle should be skipped)", len(got))
	}
	if got[0].Type != "thought" {
		t.Errorf("type=%q want thought", got[0].Type)
	}
}

// --- scanReasoning ---

func TestScanReasoning_ParsesReasonRecords(t *testing.T) {
	root := t.TempDir()
	// Both top-level (no decision) and nested-under-decision should be scanned.
	dir1 := filepath.Join(root, "live", "reasoning", "agent-a")
	os.MkdirAll(dir1, 0o755)
	rec1 := gdl.Record{Type: "reason", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-a"}, {Key: "author", Value: "agent-a"},
		{Key: "content", Value: "step one"}, {Key: "ts", Value: "ts"},
	}}
	os.WriteFile(filepath.Join(dir1, "1-a.gdl"), []byte(gdl.RenderLine(rec1)+"\n"), 0o644)

	dir2 := filepath.Join(dir1, "1727000000-dec123")
	os.MkdirAll(dir2, 0o755)
	rec2 := gdl.Record{Type: "reason", Fields: []gdl.RecordField{
		{Key: "id", Value: "2-b"}, {Key: "author", Value: "agent-a"},
		{Key: "content", Value: "step two"}, {Key: "ts", Value: "ts"},
		{Key: "decision", Value: "1727000000-dec123"},
	}}
	os.WriteFile(filepath.Join(dir2, "2-b.gdl"), []byte(gdl.RenderLine(rec2)+"\n"), 0o644)

	got, err := scanReasoning(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	for _, r := range got {
		if r.Type != "reason" {
			t.Errorf("type=%q want reason", r.Type)
		}
	}
}

// GH #134 / H1c regression — scanReasoning MUST populate r.ID
// (basename-stem) and r.Decision so the renderer + JSON encoder can
// surface them. Pre-H1c both fields were dropped at the scanner edge,
// breaking the cold-agent loop ("which decision does this reason belong
// to?" and "what id do I feed to --parent on a follow-up?"). This test
// pins the contract independently of the renderer so a future scanner
// refactor cannot regress silently.
//
// Covers both layouts the writer emits:
//   - decision-linked: live/reasoning/<author>/<decision-id>/<reason-id>.gdl
//   - orphan         : live/reasoning/<author>/<reason-id>.gdl (D7.1)
func TestScanReasoning_PopulatesIDAndDecision_GH134(t *testing.T) {
	root := t.TempDir()

	// Orphan layout: top-level under <author>/. r.Decision must be
	// EMPTY (--decision optional per D7.1) and r.ID must equal the
	// file basename-stem.
	authorDir := filepath.Join(root, "live", "reasoning", "agent-a")
	if err := os.MkdirAll(authorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orphanRec := gdl.Record{Type: "reason", Fields: []gdl.RecordField{
		{Key: "id", Value: "orphan-id-from-disk"}, // intentionally different from the basename to
		// prove ID is path-derived (the canonical key
		// confirm/retract/--parent accept), NOT the
		// on-disk `id:` field which can drift.
		{Key: "author", Value: "agent-a"},
		{Key: "content", Value: "no decision"},
		{Key: "ts", Value: "2026-05-20T11:00:00Z"},
	}}
	orphanPath := filepath.Join(authorDir, "1779000000-orphan.gdl")
	if err := os.WriteFile(orphanPath, []byte(gdl.RenderLine(orphanRec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Linked layout: nested under <author>/<decisionID>/. r.Decision
	// is populated from the on-disk `decision:` field AND surfaces in
	// the JSON via the path-derived fallback when the field is absent
	// (covered separately in the render tests).
	decisionID := "1779000000-dec123"
	linkedDir := filepath.Join(authorDir, decisionID)
	if err := os.MkdirAll(linkedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedRec := gdl.Record{Type: "reason", Fields: []gdl.RecordField{
		{Key: "id", Value: "linked-id-from-disk"},
		{Key: "author", Value: "agent-a"},
		{Key: "content", Value: "with decision"},
		{Key: "ts", Value: "2026-05-20T12:00:00Z"},
		{Key: "decision", Value: decisionID},
	}}
	linkedPath := filepath.Join(linkedDir, "1779000001-linked.gdl")
	if err := os.WriteFile(linkedPath, []byte(gdl.RenderLine(linkedRec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := scanReasoning(root)
	if err != nil {
		t.Fatalf("scanReasoning: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}

	// Index by basename-stem so we don't assume walk order.
	byID := make(map[string]RecallRecord, len(got))
	for _, r := range got {
		byID[r.ID] = r
	}

	orphan, ok := byID["1779000000-orphan"]
	if !ok {
		t.Fatalf("scanReasoning did not populate orphan reason ID from path (#134):\ngot=%+v", got)
	}
	if orphan.Decision != "" {
		t.Errorf("orphan reason Decision=%q, want empty (no --decision was passed)", orphan.Decision)
	}
	if orphan.Path != orphanPath {
		t.Errorf("orphan Path=%q, want %q", orphan.Path, orphanPath)
	}

	linked, ok := byID["1779000001-linked"]
	if !ok {
		t.Fatalf("scanReasoning did not populate linked reason ID from path (#134):\ngot=%+v", got)
	}
	if linked.Decision != decisionID {
		t.Errorf("linked reason Decision=%q, want %q (#134)", linked.Decision, decisionID)
	}
	if linked.Path != linkedPath {
		t.Errorf("linked Path=%q, want %q", linked.Path, linkedPath)
	}
}

// decisionIDFromPath is the path-parser used as the fallback when an
// on-disk `decision:` field is absent (legacy rows pre-dating the H1c
// scanner update). The parser must:
//   - return the parent-dir name for nested layout
//     (.../reasoning/<author>/<decision-id>/<reason-id>.gdl)
//   - return "" for the orphan layout
//     (.../reasoning/<author>/<reason-id>.gdl)
//   - return "" for empty input
//
// Test isolates the parser independently of scanReasoning so a future
// refactor that pushes decision resolution elsewhere stays anchored to
// the same behaviour.
func TestDecisionIDFromPath_GH134(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{
			name: "linked nested under decision dir",
			path: "/r/live/reasoning/agent-a/1779000000-dec123/1779000001-linked.gdl",
			want: "1779000000-dec123",
		},
		{
			name: "orphan directly under author dir",
			path: "/r/live/reasoning/agent-a/1779000000-orphan.gdl",
			want: "",
		},
		{
			name: "empty path",
			path: "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decisionIDFromPath(tc.path)
			if got != tc.want {
				t.Errorf("decisionIDFromPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// --- scanRetracted ---

func TestScanRetracted_ReturnsTargetIDs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "retracted")
	os.MkdirAll(dir, 0o755)
	rec := gdl.Record{Type: "retract", Fields: []gdl.RecordField{
		{Key: "target", Value: "1-aaaaaa"},
		{Key: "reason", Value: "outdated"},
		{Key: "by", Value: "agent-a"},
		{Key: "ts", Value: "ts"},
	}}
	os.WriteFile(filepath.Join(dir, "1-aaaaaa.gdl"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)

	got, err := scanRetracted(root)
	if err != nil {
		t.Fatal(err)
	}
	if !got["1-aaaaaa"] {
		t.Errorf("want target 1-aaaaaa in set, got=%v", got)
	}
}

func TestScanRetracted_MissingDirReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	got, _ := scanRetracted(root)
	if len(got) != 0 {
		t.Errorf("len=%d", len(got))
	}
}

// --- Scan (orchestrator) ---

func TestScan_AggregatesAllSources(t *testing.T) {
	root := t.TempDir()
	// Seed one of each source.
	os.MkdirAll(filepath.Join(root, "given"), 0o755)
	os.WriteFile(filepath.Join(root, "given", "x.md"), []byte("hi"), 0o644)

	learnedDir := filepath.Join(root, "learned", "x", "1")
	os.MkdirAll(learnedDir, 0o755)
	obs := gdl.Record{Type: "observation", Fields: []gdl.RecordField{
		{Key: "id", Value: "1"}, {Key: "author", Value: "a"},
		{Key: "subject", Value: "x:1"}, {Key: "predicate", Value: "p"},
		{Key: "object", Value: "o"}, {Key: "scope", Value: "agent"},
		{Key: "confidence", Value: "1"}, {Key: "ts", Value: "ts"},
	}}
	os.WriteFile(filepath.Join(learnedDir, "1.gdlm"), []byte(gdl.RenderLine(obs)+"\n"), 0o644)

	outboxDir := filepath.Join(root, "live", "outbox", "a")
	os.MkdirAll(outboxDir, 0o755)
	th := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-a"}, {Key: "author", Value: "a"},
		{Key: "type", Value: "hypothesis"}, {Key: "subject", Value: "x:1"},
		{Key: "content", Value: "c"}, {Key: "scope", Value: "fleet"},
		{Key: "ts", Value: "ts"}, {Key: "ttl", Value: "0"},
	}}
	os.WriteFile(filepath.Join(outboxDir, "1-a.gdl"), []byte(gdl.RenderLine(th)+"\n"), 0o644)

	got, err := Scan(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 3 {
		t.Errorf("expected at least 3 records (given+learned+outbox), got %d", len(got))
	}
	typeCount := make(map[string]int)
	for _, r := range got {
		typeCount[r.Type]++
	}
	for _, tp := range []string{"given", "observation", "thought"} {
		if typeCount[tp] == 0 {
			t.Errorf("missing type %q in scanned records", tp)
		}
	}
}

func TestScan_IncludeRetracted_MarksRecords(t *testing.T) {
	root := t.TempDir()
	// Seed a thought.
	outboxDir := filepath.Join(root, "live", "outbox", "a")
	os.MkdirAll(outboxDir, 0o755)
	th := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-aaaaaa"}, {Key: "author", Value: "a"},
		{Key: "type", Value: "hypothesis"}, {Key: "subject", Value: "x:1"},
		{Key: "content", Value: "c"}, {Key: "scope", Value: "fleet"},
		{Key: "ts", Value: "ts"}, {Key: "ttl", Value: "0"},
	}}
	os.WriteFile(filepath.Join(outboxDir, "1-aaaaaa.gdl"), []byte(gdl.RenderLine(th)+"\n"), 0o644)

	// Seed a retract for that thought.
	retractDir := filepath.Join(root, "live", "retracted")
	os.MkdirAll(retractDir, 0o755)
	rt := gdl.Record{Type: "retract", Fields: []gdl.RecordField{
		{Key: "target", Value: "1-aaaaaa"},
		{Key: "reason", Value: "outdated"},
		{Key: "by", Value: "a"},
		{Key: "ts", Value: "ts"},
	}}
	os.WriteFile(filepath.Join(retractDir, "1-aaaaaa.gdl"), []byte(gdl.RenderLine(rt)+"\n"), 0o644)

	got, err := Scan(root, true)
	if err != nil {
		t.Fatal(err)
	}
	// Find the thought; verify Retracted=true.
	var foundThought *RecallRecord
	for i := range got {
		if got[i].Type == "thought" {
			foundThought = &got[i]
			break
		}
	}
	if foundThought == nil {
		t.Fatal("thought not found in scan output")
	}
	if !foundThought.Retracted {
		t.Errorf("Retracted=false, want true (matching retracted target)")
	}
}

func TestScan_RetractionMark_OnlyAppliesToLiveRecords(t *testing.T) {
	root := t.TempDir()
	// Seed a given/ file with a name that happens to match a thought-id pattern.
	os.MkdirAll(filepath.Join(root, "given"), 0o755)
	os.WriteFile(filepath.Join(root, "given", "1727000000-collide.md"), []byte("body"), 0o644)

	// Seed a retract for that "id" (which collides with the given/ filename stem).
	retractDir := filepath.Join(root, "live", "retracted")
	os.MkdirAll(retractDir, 0o755)
	rt := gdl.Record{Type: "retract", Fields: []gdl.RecordField{
		{Key: "target", Value: "1727000000-collide"},
		{Key: "reason", Value: "x"},
		{Key: "by", Value: "a"},
		{Key: "ts", Value: "ts"},
	}}
	os.WriteFile(filepath.Join(retractDir, "1727000000-collide.gdl"), []byte(gdl.RenderLine(rt)+"\n"), 0o644)

	got, err := Scan(root, true)
	if err != nil {
		t.Fatal(err)
	}
	// The given/ record must NOT be marked retracted (cross-source collision protection).
	for _, r := range got {
		if r.Type == "given" && r.Retracted {
			t.Errorf("given/ record incorrectly marked retracted: %+v", r)
		}
	}
}

// --- scanSummons ---

// TestScanSummons_TargetFieldPopulated guards the v1.0.5 summon JSON
// projection fix: scanSummons must populate RecallRecord.Target from
// the on-disk @summon `to:` field so SDK/MCP consumers know who a
// summon is addressed to. Pre-fix the to: field was dropped entirely
// in projection — recipients of `recall --types=summon --json` had
// to re-parse the GDL path to find the addressee.
func TestScanSummons_TargetFieldPopulated(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "summons", "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := gdl.Record{Type: "summon", Fields: []gdl.RecordField{
		{Key: "id", Value: "1779000000-test01"},
		{Key: "from", Value: "alice"},
		{Key: "to", Value: "bob"},
		{Key: "topic", Value: "channel:strategy"},
		{Key: "intent", Value: "review Q4 plan"},
		{Key: "ts", Value: "2026-05-22T12:00:00Z"},
	}}
	path := filepath.Join(dir, "1779000000-test01.gdl")
	if err := os.WriteFile(path, []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := scanSummons(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	r := got[0]
	if r.Author != "alice" {
		t.Errorf("Author=%q want %q", r.Author, "alice")
	}
	// Summon addressee lives in To (agent-id), not Target (reserved for
	// @confirm/@refute/@retract thought-ids).
	if r.To != "bob" {
		t.Errorf("To=%q want %q (pre-fix the to: field was dropped — this is the regression guard)", r.To, "bob")
	}
	if r.Target != "" {
		t.Errorf("Target=%q want empty for summon (reserved for thought-id refs on @confirm/@refute/@retract)", r.Target)
	}
	if r.Subject != "channel:strategy" {
		t.Errorf("Subject=%q want %q", r.Subject, "channel:strategy")
	}
	if r.Intent != "review Q4 plan" {
		t.Errorf("Intent=%q want %q", r.Intent, "review Q4 plan")
	}
}
