// Package stream — tests for the v1.0.3 auto-promote stream event
// enrichment. The wire shape is LOCKED at first ship; future
// enrichments bump _version. Drift here breaks the listen-event
// contract that TUI / third-party watchers consume.
package stream

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFileToEvents_AutoPromoteEnriched pins the parser side of the
// contract. A GDL @auto-promote record with v1.0.3 fields parses into
// an Event with PromotedID / SourceThoughtID / Confirmers / counts /
// Confidence populated, and Author set to `origin` (not `by`).
func TestFileToEvents_AutoPromoteEnriched(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "promoted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := "@auto-promote|version:1|thought:1727000000-aaaaaa|" +
		"observation:1727000001-bbbbbb|origin:agent-a|subject:customer:5821|" +
		"scope:fleet|confirmers:b,c,d|confirm-count:3|refute-count:0|" +
		"confidence:1|by:auto-promote|ts:2026-05-21T00:00:00Z\n"
	abs := filepath.Join(dir, "1727000000-aaaaaa.gdl")
	if err := os.WriteFile(abs, []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}

	evs, err := FileToEvents(abs, "live/promoted/1727000000-aaaaaa.gdl")
	if err != nil {
		t.Fatalf("FileToEvents: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	ev := evs[0]
	if ev.Type != "auto-promote" {
		t.Errorf("_type=%q want auto-promote", ev.Type)
	}
	if ev.Version != 1 {
		t.Errorf("_version=%d want 1", ev.Version)
	}
	if ev.Author != "agent-a" {
		t.Errorf("Author=%q want agent-a (from origin)", ev.Author)
	}
	if ev.Subject != "customer:5821" {
		t.Errorf("Subject=%q want customer:5821", ev.Subject)
	}
	if ev.Scope != "fleet" {
		t.Errorf("Scope=%q want fleet", ev.Scope)
	}
	if ev.SourceThoughtID != "1727000000-aaaaaa" {
		t.Errorf("SourceThoughtID=%q", ev.SourceThoughtID)
	}
	if ev.PromotedID != "1727000001-bbbbbb" {
		t.Errorf("PromotedID=%q", ev.PromotedID)
	}
	if got := strings.Join(ev.Confirmers, ","); got != "b,c,d" {
		t.Errorf("Confirmers=%q want b,c,d", got)
	}
	if ev.ConfirmCount != 3 {
		t.Errorf("ConfirmCount=%d want 3", ev.ConfirmCount)
	}
	if ev.RefuteCount != 0 {
		t.Errorf("RefuteCount=%d want 0", ev.RefuteCount)
	}
	if ev.Confidence != 1.0 {
		t.Errorf("Confidence=%v want 1.0", ev.Confidence)
	}
}

// TestEmitCatchUp_AutoPromoteEvent_JSONShape pins the wire JSON shape
// for a listen --catch-up consumer. The locked payload schema:
//
//	{_type:"auto-promote", _version:1, promoted_id, source_thought_id,
//	 subject, confirmers, confirm_count, refute_count, confidence, ts, ...}
//
// Future watchers (TUI / SDK) gate on _version + _type and parse the
// payload keys directly.
func TestEmitCatchUp_AutoPromoteEvent_JSONShape(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "promoted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := "@auto-promote|version:1|thought:1727000000-aaaaaa|" +
		"observation:1727000001-bbbbbb|origin:agent-a|subject:customer:5821|" +
		"scope:fleet|confirmers:b,c,d|confirm-count:3|refute-count:0|" +
		"confidence:1|by:auto-promote|ts:2026-05-21T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, "1727000000-aaaaaa.gdl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	// CurrentAgent set to a non-matching id; scope=fleet → visible to all.
	if err := EmitCatchUp(&buf, root, []string{"live/promoted"}, FilterParams{CurrentAgent: "agent-z"}); err != nil {
		t.Fatalf("EmitCatchUp: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("no events emitted")
	}
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nout=%q", err, buf.String())
	}
	if got["_type"] != "auto-promote" {
		t.Errorf("_type=%v want auto-promote", got["_type"])
	}
	if got["_version"].(float64) != 1 {
		t.Errorf("_version=%v want 1", got["_version"])
	}
	if got["source_thought_id"] != "1727000000-aaaaaa" {
		t.Errorf("source_thought_id=%v", got["source_thought_id"])
	}
	if got["promoted_id"] != "1727000001-bbbbbb" {
		t.Errorf("promoted_id=%v", got["promoted_id"])
	}
	if got["confirm_count"].(float64) != 3 {
		t.Errorf("confirm_count=%v want 3", got["confirm_count"])
	}
	if got["confidence"].(float64) != 1.0 {
		t.Errorf("confidence=%v want 1.0", got["confidence"])
	}
	confirmers, _ := got["confirmers"].([]any)
	if len(confirmers) != 3 {
		t.Errorf("confirmers len=%d want 3", len(confirmers))
	}
}

// TestEmitCatchUp_AutoPromote_PrivacyScopeAgent pins the privacy floor
// (#147) for auto-promote events. A scope=agent promotion is visible
// ONLY to the original author (Event.Author = origin) — same rule
// every other read surface applies.
func TestEmitCatchUp_AutoPromote_PrivacyScopeAgent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "promoted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := "@auto-promote|version:1|thought:1727000000-aaaaaa|" +
		"observation:1727000001-bbbbbb|origin:agent-a|subject:customer:5821|" +
		"scope:agent|confirmers:b,c,d|confirm-count:3|refute-count:0|" +
		"confidence:1|by:auto-promote|ts:2026-05-21T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, "1727000000-aaaaaa.gdl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-author current agent — must NOT see the event.
	var bufOther bytes.Buffer
	if err := EmitCatchUp(&bufOther, root, []string{"live/promoted"}, FilterParams{CurrentAgent: "agent-z"}); err != nil {
		t.Fatalf("EmitCatchUp: %v", err)
	}
	if bufOther.Len() != 0 {
		t.Errorf("scope=agent event leaked to non-author: %q", bufOther.String())
	}

	// Original author — MUST see it.
	var bufAuthor bytes.Buffer
	if err := EmitCatchUp(&bufAuthor, root, []string{"live/promoted"}, FilterParams{CurrentAgent: "agent-a"}); err != nil {
		t.Fatalf("EmitCatchUp: %v", err)
	}
	if bufAuthor.Len() == 0 {
		t.Errorf("scope=agent event hidden from its author")
	}
}
