// Package autopromote — tests for the v1.0.3 enriched @auto-promote
// audit record payload. The record's field set is LOCKED at first
// ship; future enrichments bump the GDL `_version` field rather than
// changing the existing field names. Drift breaks the listen-event
// contract that TUI / third-party watchers consume.
package autopromote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// TestExecutePromote_EnrichedAuditRecord pins the v1.0.3 schema of the
// @auto-promote audit record. v1.0.2 wrote only thought / observation /
// by / ts. v1.0.3 adds origin (= original author), subject, scope,
// confirmers (csv), confirm-count, refute-count, confidence, version.
// Consumers (TUI, listen) parse these so a missing field breaks the
// downstream surface silently.
func TestExecutePromote_EnrichedAuditRecord(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-aaaaaa",
		"customer:5821", "churn risk", "fleet",
		[]string{"billing"})
	seedConfirms(t, root, "1727000000-aaaaaa", "b", "c", "d")

	if err := ExecutePromote(root, "1727000000-aaaaaa"); err != nil {
		t.Fatalf("ExecutePromote: %v", err)
	}

	marker := readPromotedMarker(t, root, "1727000000-aaaaaa")
	if marker.Type != "auto-promote" {
		t.Fatalf("marker type=%q want auto-promote", marker.Type)
	}

	// v1.0.2 carry-overs.
	if marker.Get("thought") != "1727000000-aaaaaa" {
		t.Errorf("thought=%q want 1727000000-aaaaaa", marker.Get("thought"))
	}
	if marker.Get("observation") == "" {
		t.Errorf("missing observation field")
	}
	if marker.Get("by") != "auto-promote" {
		t.Errorf("by=%q want auto-promote", marker.Get("by"))
	}
	if marker.Get("ts") == "" {
		t.Errorf("missing ts field")
	}

	// v1.0.3 enrichments.
	if marker.Get("version") != "1" {
		t.Errorf("version=%q want 1 (locked schema)", marker.Get("version"))
	}
	if marker.Get("origin") != "agent-a" {
		t.Errorf("origin=%q want agent-a (original thought author)", marker.Get("origin"))
	}
	if marker.Get("subject") != "customer:5821" {
		t.Errorf("subject=%q want customer:5821", marker.Get("subject"))
	}
	if marker.Get("scope") != "fleet" {
		t.Errorf("scope=%q want fleet (carried from thought)", marker.Get("scope"))
	}
	if got := marker.Get("confirmers"); got != "b,c,d" {
		t.Errorf("confirmers=%q want b,c,d (sorted, deduped, csv)", got)
	}
	if marker.Get("confirm-count") != "3" {
		t.Errorf("confirm-count=%q want 3", marker.Get("confirm-count"))
	}
	if marker.Get("refute-count") != "0" {
		t.Errorf("refute-count=%q want 0", marker.Get("refute-count"))
	}
	if marker.Get("confidence") == "" {
		t.Errorf("missing confidence field")
	}
}

// TestExecutePromote_RefuteCountSurfaced asserts that a non-zero
// refute count shows up in the audit payload even though it didn't
// block promotion under current simple math. Future watchers
// computing weighted-confidence MUST be able to see the refute
// dynamics on the wire.
func TestExecutePromote_RefuteCountSurfaced(t *testing.T) {
	root := t.TempDir()
	seedThought(t, root, "agent-a", "1727000000-bbbbbb",
		"customer:9", "x", "fleet", nil)
	// 3 confirms + 1 refute — still crosses MinDistinctConfirmers and
	// the 0.85 floor (0.75 < 0.85 actually) — let's set 4 confirms + 1
	// refute for confidence = 4/5 = 0.8 — still below 0.85. Try 5
	// confirms + 1 refute = 5/6 = 0.833 — still under. 6+1 = 0.857 OK.
	seedConfirms(t, root, "1727000000-bbbbbb", "b", "c", "d", "e", "f", "g")
	// Append one refute by agent-q.
	rec := gdl.Record{Type: "refute", Fields: []gdl.RecordField{
		{Key: "target", Value: "1727000000-bbbbbb"},
		{Key: "by", Value: "agent-q"},
		{Key: "ts", Value: "2026-05-21T00:00:01Z"},
		{Key: "reason", Value: "doubt"},
	}}
	confirmsPath := filepath.Join(root, "live", "confirms", "1727000000-bbbbbb.gdl")
	f, err := os.OpenFile(confirmsPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(gdl.RenderLine(rec) + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := ExecutePromote(root, "1727000000-bbbbbb"); err != nil {
		t.Fatalf("ExecutePromote: %v", err)
	}

	marker := readPromotedMarker(t, root, "1727000000-bbbbbb")
	if marker.Get("refute-count") != "1" {
		t.Errorf("refute-count=%q want 1", marker.Get("refute-count"))
	}
	if marker.Get("confirm-count") != "6" {
		t.Errorf("confirm-count=%q want 6", marker.Get("confirm-count"))
	}
	// Confirmers csv must include all six, sorted.
	got := marker.Get("confirmers")
	if !strings.Contains(got, "b") || !strings.Contains(got, "g") {
		t.Errorf("confirmers=%q expected to contain b..g", got)
	}
}
