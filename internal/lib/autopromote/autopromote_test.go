package autopromote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// seedThought writes a @thought record to live/outbox/<author>/<id>.gdl with
// the supplied fields. Returns the id (caller may pin it).
func seedThought(t *testing.T, root, author, id, subject, content, scope string, topics []string) {
	t.Helper()
	in := thought.ThoughtInput{
		ID:      id,
		Author:  author,
		Type:    "hypothesis",
		Subject: subject,
		Content: content,
		Scope:   scope,
		Topics:  topics,
		TS:      "2026-05-12T12:00:00Z",
		TTL:     0,
	}
	rec := thought.BuildThoughtRecord(in)
	if err := thought.Write(root, author, id, []gdl.Record{rec}); err != nil {
		t.Fatalf("seedThought: %v", err)
	}
}

// seedConfirms writes one @confirm per author via confirm.Append (the same
// path the real CLI uses).
func seedConfirms(t *testing.T, root, targetID string, authors ...string) {
	t.Helper()
	for _, a := range authors {
		rec := confirm.BuildConfirm(targetID, a, "", "2026-05-12T12:00:00Z")
		if err := confirm.Append(root, targetID, rec); err != nil {
			t.Fatalf("seedConfirms %s: %v", a, err)
		}
	}
}

// seedRefutes writes one @refute per author.
func seedRefutes(t *testing.T, root, targetID string, authors ...string) {
	t.Helper()
	for _, a := range authors {
		rec := confirm.BuildRefute(targetID, a, "wrong", "", "2026-05-12T12:00:00Z")
		if err := confirm.Append(root, targetID, rec); err != nil {
			t.Fatalf("seedRefutes %s: %v", a, err)
		}
	}
}

// seedRetracted writes live/retracted/<id>.gdl so the engine sees the
// thought as retracted.
func seedRetracted(t *testing.T, root, targetID string) {
	t.Helper()
	dir := filepath.Join(root, "live", "retracted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, targetID+".gdl")
	body := "@retract|target:" + targetID + "|reason:wrong|by:agent-a|ts:ts\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write retracted: %v", err)
	}
}

// readLearned scans learned/<subject-path>/ for *.gdlm files and returns
// their full paths.
func readLearned(t *testing.T, root, subject string) []string {
	t.Helper()
	segs := strings.Split(subject, ":")
	parts := append([]string{root, "learned"}, segs...)
	dir := filepath.Join(parts...)
	matches, err := filepath.Glob(filepath.Join(dir, "*.gdlm"))
	if err != nil {
		t.Fatalf("glob learned: %v", err)
	}
	return matches
}

// readObservation reads and parses the first @observation record in path.
func readObservation(t *testing.T, path string) gdl.Record {
	t.Helper()
	bs, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read observation: %v", err)
	}
	recs, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("parse observation: %v", err)
	}
	for _, r := range recs {
		if r.Type == "observation" {
			return r
		}
	}
	t.Fatalf("no @observation record in %s:\n%s", path, bs)
	return gdl.Record{}
}

// readPromotedMarker reads live/promoted/<id>.gdl and returns the first
// record (either @auto-promote or @promote-skipped).
func readPromotedMarker(t *testing.T, root, targetID string) gdl.Record {
	t.Helper()
	path := filepath.Join(root, "live", "promoted", targetID+".gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read promoted: %v", err)
	}
	recs, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("parse promoted: %v", err)
	}
	if len(recs) == 0 {
		t.Fatalf("no records in %s:\n%s", path, bs)
	}
	return recs[0]
}

// -- Evaluate tests -----------------------------------------------------------

func TestEvaluate_NoConfirms_ReturnsNoop(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa", "customer:5821", "x is true", "agent", nil)

	d, tally, err := Evaluate(root, "1727000000-aaaaaa")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d != DecisionNoop {
		t.Errorf("decision=%v want DecisionNoop", d)
	}
	if len(tally.Confirms) != 0 || len(tally.Refutes) != 0 {
		t.Errorf("tally=%+v want empty", tally)
	}
}

func TestEvaluate_BelowAuthorThreshold_ReturnsNoop(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa", "customer:5821", "x is true", "agent", nil)
	seedConfirms(t, root, "1727000000-aaaaaa", "agent-b", "agent-c") // only 2 distinct

	d, _, err := Evaluate(root, "1727000000-aaaaaa")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d != DecisionNoop {
		t.Errorf("decision=%v want DecisionNoop (only 2 confirmers)", d)
	}
}

func TestEvaluate_BelowConfidenceThreshold_ReturnsNoop(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa", "customer:5821", "x is true", "agent", nil)
	// 3 confirms + 1 refute = 3/4 = 0.75 — below 0.85.
	seedConfirms(t, root, "1727000000-aaaaaa", "agent-b", "agent-c", "agent-d")
	seedRefutes(t, root, "1727000000-aaaaaa", "agent-e")

	d, tally, err := Evaluate(root, "1727000000-aaaaaa")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d != DecisionNoop {
		t.Errorf("decision=%v want DecisionNoop (confidence 0.75 < 0.85)", d)
	}
	if c := tally.Confidence(); c >= 0.85 {
		t.Errorf("confidence=%v should be < 0.85", c)
	}
}

func TestEvaluate_ThresholdMet_ReturnsPromote(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa", "customer:5821", "x is true", "agent", nil)
	seedConfirms(t, root, "1727000000-aaaaaa", "agent-b", "agent-c", "agent-d")

	d, tally, err := Evaluate(root, "1727000000-aaaaaa")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d != DecisionPromote {
		t.Errorf("decision=%v want DecisionPromote", d)
	}
	if tally.Confidence() != 1.0 {
		t.Errorf("confidence=%v want 1.0", tally.Confidence())
	}
}

func TestEvaluate_FiveConfirmsOneRefute_ReturnsNoop(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa", "customer:5821", "x", "agent", nil)
	// 5 / (5+1) = 0.8333... — just below 0.85.
	seedConfirms(t, root, "1727000000-aaaaaa", "b", "c", "d", "e", "f")
	seedRefutes(t, root, "1727000000-aaaaaa", "g")

	d, _, err := Evaluate(root, "1727000000-aaaaaa")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d != DecisionNoop {
		t.Errorf("decision=%v want DecisionNoop (0.833 < 0.85)", d)
	}
}

func TestEvaluate_FiveConfirmsZeroRefutes_ReturnsPromote(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa", "customer:5821", "x", "agent", nil)
	seedConfirms(t, root, "1727000000-aaaaaa", "b", "c", "d", "e", "f")

	d, _, err := Evaluate(root, "1727000000-aaaaaa")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d != DecisionPromote {
		t.Errorf("decision=%v want DecisionPromote", d)
	}
}

func TestEvaluate_RetractedThought_ReturnsSkipRetracted(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa", "customer:5821", "x", "agent", nil)
	seedConfirms(t, root, "1727000000-aaaaaa", "b", "c", "d")
	seedRetracted(t, root, "1727000000-aaaaaa")

	d, _, err := Evaluate(root, "1727000000-aaaaaa")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d != DecisionSkipRetracted {
		t.Errorf("decision=%v want DecisionSkipRetracted", d)
	}
}

func TestEvaluate_AlreadyPromoted_ReturnsNoop(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa", "customer:5821", "x", "agent", nil)
	seedConfirms(t, root, "1727000000-aaaaaa", "b", "c", "d")

	// Simulate a prior promotion.
	promotedDir := filepath.Join(root, "live", "promoted")
	if err := os.MkdirAll(promotedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(promotedDir, "1727000000-aaaaaa.gdl"),
		[]byte("@auto-promote|thought:1727000000-aaaaaa|observation:x|by:auto-promote|ts:ts\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	d, _, err := Evaluate(root, "1727000000-aaaaaa")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d != DecisionNoop {
		t.Errorf("decision=%v want DecisionNoop (already promoted)", d)
	}
}

// -- ExecutePromote tests -----------------------------------------------------

func TestExecutePromote_WritesObservation(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa",
		"customer:5821", "prefers email", "deployment",
		[]string{"billing", "comms"})
	seedConfirms(t, root, "1727000000-aaaaaa", "b", "c", "d")

	if err := ExecutePromote(root, "1727000000-aaaaaa"); err != nil {
		t.Fatalf("ExecutePromote: %v", err)
	}

	// Observation file should exist under learned/customer/5821/.
	obs := readLearned(t, root, "customer:5821")
	if len(obs) != 1 {
		t.Fatalf("want 1 observation file, got %d: %v", len(obs), obs)
	}
	rec := readObservation(t, obs[0])
	if rec.Get("author") != "auto-promote" {
		t.Errorf("author=%q want auto-promote", rec.Get("author"))
	}
	if rec.Get("subject") != "customer:5821" {
		t.Errorf("subject=%q want customer:5821", rec.Get("subject"))
	}
	if rec.Get("predicate") != "asserted" {
		t.Errorf("predicate=%q want asserted", rec.Get("predicate"))
	}
	if rec.Get("object") != "prefers email" {
		t.Errorf("object=%q want prefers email", rec.Get("object"))
	}
	if rec.Get("scope") != "deployment" {
		t.Errorf("scope=%q want deployment", rec.Get("scope"))
	}
	if rec.Get("topics") != "billing,comms" {
		t.Errorf("topics=%q want billing,comms", rec.Get("topics"))
	}
	if rec.Get("confidence") != "1" {
		t.Errorf("confidence=%q want 1 (3 confirms, no refutes)", rec.Get("confidence"))
	}

	// Audit @auto-promote marker should exist.
	marker := readPromotedMarker(t, root, "1727000000-aaaaaa")
	if marker.Type != "auto-promote" {
		t.Errorf("marker type=%q want auto-promote", marker.Type)
	}
	if marker.Get("thought") != "1727000000-aaaaaa" {
		t.Errorf("marker thought=%q", marker.Get("thought"))
	}
	if marker.Get("observation") == "" {
		t.Errorf("marker missing observation field")
	}
	if marker.Get("by") != "auto-promote" {
		t.Errorf("marker by=%q want auto-promote", marker.Get("by"))
	}
}

// TestExecutePromote_CarriesProvenance is the #76 gate: the durable
// learned/ @observation MUST record WHO originated the thought (origin =
// thought author), WHO corroborated it (confirmed-by = the sorted-deduped
// distinct confirmer ids straight from confirm.Tally), and the SOURCE
// thought-id (source = targetID). Without these, quorum's auditable
// ≥3-distinct-confirmer value is erased exactly on the path that persists.
func TestExecutePromote_CarriesProvenance(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa",
		"customer:5821", "prefers email", "deployment", nil)
	// Confirm out of lexical order + a duplicate confirmer; the durable
	// record must reflect the tally's sorted-deduped set, not raw order.
	seedConfirms(t, root, "1727000000-aaaaaa", "agent-d", "agent-b", "agent-c", "agent-b")

	if err := ExecutePromote(root, "1727000000-aaaaaa"); err != nil {
		t.Fatalf("ExecutePromote: %v", err)
	}

	obs := readLearned(t, root, "customer:5821")
	if len(obs) != 1 {
		t.Fatalf("want 1 observation file, got %d: %v", len(obs), obs)
	}
	rec := readObservation(t, obs[0])

	// author is STILL auto-promote (the daemon is the writer of the
	// crowd-confirmed fact — documented contract D13.7); provenance is
	// ADDITIONAL, not a replacement.
	if rec.Get("author") != "auto-promote" {
		t.Errorf("author=%q want auto-promote (provenance is additive)", rec.Get("author"))
	}
	if rec.Get("origin") != "agent-a" {
		t.Errorf("origin=%q want agent-a (originating thought author)", rec.Get("origin"))
	}
	if rec.Get("confirmed-by") != "agent-b,agent-c,agent-d" {
		t.Errorf("confirmed-by=%q want agent-b,agent-c,agent-d (sorted/deduped)", rec.Get("confirmed-by"))
	}
	if rec.Get("source") != "1727000000-aaaaaa" {
		t.Errorf("source=%q want 1727000000-aaaaaa (source thought-id)", rec.Get("source"))
	}
}

func TestExecutePromote_Idempotent(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa", "customer:5821", "x", "agent", nil)
	seedConfirms(t, root, "1727000000-aaaaaa", "b", "c", "d")

	if err := ExecutePromote(root, "1727000000-aaaaaa"); err != nil {
		t.Fatalf("ExecutePromote#1: %v", err)
	}
	if err := ExecutePromote(root, "1727000000-aaaaaa"); err != nil {
		t.Fatalf("ExecutePromote#2: %v", err)
	}

	obs := readLearned(t, root, "customer:5821")
	if len(obs) != 1 {
		t.Errorf("want exactly 1 observation file after 2 calls, got %d: %v", len(obs), obs)
	}
}

// -- ExecuteSkip tests --------------------------------------------------------

func TestExecuteSkip_WritesPromoteSkipped(t *testing.T) {
	root := t.TempDir()

	if err := ExecuteSkip(root, "1727000000-aaaaaa", "retracted"); err != nil {
		t.Fatalf("ExecuteSkip: %v", err)
	}

	rec := readPromotedMarker(t, root, "1727000000-aaaaaa")
	if rec.Type != "promote-skipped" {
		t.Errorf("type=%q want promote-skipped", rec.Type)
	}
	if rec.Get("target") != "1727000000-aaaaaa" {
		t.Errorf("target=%q", rec.Get("target"))
	}
	if rec.Get("reason") != "retracted" {
		t.Errorf("reason=%q want retracted", rec.Get("reason"))
	}
	if rec.Get("by") != "auto-promote" {
		t.Errorf("by=%q want auto-promote", rec.Get("by"))
	}
	if rec.Get("ts") == "" {
		t.Errorf("ts must be set")
	}
}

func TestExecuteSkip_Idempotent(t *testing.T) {
	root := t.TempDir()

	if err := ExecuteSkip(root, "1727000000-aaaaaa", "retracted"); err != nil {
		t.Fatalf("ExecuteSkip#1: %v", err)
	}
	// Read original content.
	path := filepath.Join(root, "live", "promoted", "1727000000-aaaaaa.gdl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}
	// Second call must not overwrite.
	if err := ExecuteSkip(root, "1727000000-aaaaaa", "different-reason"); err != nil {
		t.Fatalf("ExecuteSkip#2: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("ExecuteSkip overwrote existing marker.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// -- Handle tests -------------------------------------------------------------

func TestHandle_FullFlow_Promotes(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa",
		"customer:5821", "prefers email", "agent", nil)
	seedConfirms(t, root, "1727000000-aaaaaa", "b", "c", "d")

	if err := Handle(root, "1727000000-aaaaaa"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	obs := readLearned(t, root, "customer:5821")
	if len(obs) != 1 {
		t.Errorf("want 1 observation, got %d", len(obs))
	}
	rec := readPromotedMarker(t, root, "1727000000-aaaaaa")
	if rec.Type != "auto-promote" {
		t.Errorf("marker type=%q want auto-promote", rec.Type)
	}
}

func TestHandle_RetractedFlow_Skips(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa",
		"customer:5821", "x", "agent", nil)
	seedConfirms(t, root, "1727000000-aaaaaa", "b", "c", "d")
	seedRetracted(t, root, "1727000000-aaaaaa")

	if err := Handle(root, "1727000000-aaaaaa"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	rec := readPromotedMarker(t, root, "1727000000-aaaaaa")
	if rec.Type != "promote-skipped" {
		t.Errorf("marker type=%q want promote-skipped", rec.Type)
	}
	if rec.Get("reason") != "retracted" {
		t.Errorf("reason=%q want retracted", rec.Get("reason"))
	}
	// No observation file should exist.
	obs := readLearned(t, root, "customer:5821")
	if len(obs) != 0 {
		t.Errorf("want 0 observations, got %d: %v", len(obs), obs)
	}
}

func TestHandle_NoSuchThought_PropagatesError(t *testing.T) {
	root := t.TempDir()
	// No thought seeded, BUT confirms cross threshold — simulates a
	// concurrent confirm-after-cleanup race where the outbox file vanished
	// between the confirm write and the daemon evaluation.
	seedConfirms(t, root, "1727000000-missing", "b", "c", "d")

	err := Handle(root, "1727000000-missing")
	if err == nil {
		t.Fatalf("Handle: want NoSuchThoughtError, got nil")
	}
	// Should specifically be NoSuchThoughtError — the engine surface
	// returns it; the daemon will log+skip at the dispatch layer.
	if !strings.Contains(err.Error(), "no such record") {
		t.Errorf("err=%v want NoSuchThoughtError-shaped", err)
	}
}

func TestHandle_BelowThreshold_NoOp(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa", "customer:5821", "x", "agent", nil)
	seedConfirms(t, root, "1727000000-aaaaaa", "b") // only 1 confirmer

	if err := Handle(root, "1727000000-aaaaaa"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// No promoted marker; no observation.
	if _, err := os.Stat(filepath.Join(root, "live", "promoted", "1727000000-aaaaaa.gdl")); !os.IsNotExist(err) {
		t.Errorf("promoted marker should not exist, stat err=%v", err)
	}
	obs := readLearned(t, root, "customer:5821")
	if len(obs) != 0 {
		t.Errorf("want 0 observations, got %d", len(obs))
	}
}
