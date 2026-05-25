// Package cli — tests for the AutoPromoteHandler wiring in dev.go.
//
// These are tripwire tests asserting the dispatch table routes
// live/confirms/<id>.gdl events (both Create and Write) to
// autopromote.Handle. Each test seeds a confirms file with enough
// distinct @confirm authors to cross the promotion threshold (≥3
// distinct `by:` agents, confidence ≥ 0.85) but does NOT seed the
// matching @thought in live/outbox/. With the dispatch correctly wired,
// autopromote.Handle reaches ExecutePromote → findThought → returns a
// *NoSuchThoughtError. Observing that specific error proves the dispatch
// entry was reached. A nil return or a different error type means the
// wiring is broken (the handler never reached autopromote.Handle, or
// the threshold gate short-circuited it).
//
// End-to-end behaviour (confirms → thought present → promotion writes
// @observation + @auto-promote marker) is covered by the integration
// suite in test/integration/.
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// seedConfirmsAtThreshold writes a live/confirms/<targetID>.gdl file with
// three @confirm records from distinct agents (the threshold for
// promotion). No matching @thought is seeded — that's the deliberate
// trigger for findThought → *NoSuchThoughtError.
func seedConfirmsAtThreshold(t *testing.T, root, targetID string) {
	t.Helper()
	for _, by := range []string{"agent-a", "agent-b", "agent-c"} {
		rec := gdl.Record{Type: "confirm", Fields: []gdl.RecordField{
			{Key: "target", Value: targetID},
			{Key: "by", Value: by},
			{Key: "ts", Value: "2026-05-12T00:00:00Z"},
		}}
		if err := confirm.Append(root, targetID, rec); err != nil {
			t.Fatalf("seed confirm by=%s: %v", by, err)
		}
	}
}

// TestDefaultEventHandler_RoutesConfirmsAdd_ToAutoPromote asserts that an
// fsnotify Create event on live/confirms/<id>.gdl is dispatched to
// autopromote.Handle. With confirms seeded at threshold but no matching
// thought, Handle reaches ExecutePromote → returns *NoSuchThoughtError —
// observing that error proves the dispatch path was reached.
func TestDefaultEventHandler_RoutesConfirmsAdd_ToAutoPromote(t *testing.T) {
	root := t.TempDir()
	targetID := "1727000000-fake12"
	seedConfirmsAtThreshold(t, root, targetID)

	h := defaultEventHandler(root)
	err := h(FileEvent{Kind: "add", Path: "live/confirms/" + targetID + ".gdl"})
	if err == nil {
		t.Fatal("expected NoSuchThoughtError, got nil — dispatch entry missing")
	}
	var nstErr *rufioerr.NoSuchThoughtError
	if !errors.As(err, &nstErr) {
		t.Errorf("expected *NoSuchThoughtError, got %T: %v", err, err)
	}
}

// TestDefaultEventHandler_RoutesConfirmsChange_ToAutoPromote asserts that
// an fsnotify Write event on live/confirms/<id>.gdl (the 2nd+ append) is
// dispatched to autopromote.Handle. Same proof-by-error-type as the add
// variant.
func TestDefaultEventHandler_RoutesConfirmsChange_ToAutoPromote(t *testing.T) {
	root := t.TempDir()
	targetID := "1727000000-fake34"
	seedConfirmsAtThreshold(t, root, targetID)

	h := defaultEventHandler(root)
	err := h(FileEvent{Kind: "change", Path: "live/confirms/" + targetID + ".gdl"})
	if err == nil {
		t.Fatal("expected NoSuchThoughtError, got nil — dispatch entry missing")
	}
	var nstErr *rufioerr.NoSuchThoughtError
	if !errors.As(err, &nstErr) {
		t.Errorf("expected *NoSuchThoughtError, got %T: %v", err, err)
	}
}

// TestDefaultEventHandler_IgnoresConfirmsWrongSuffix asserts that a file
// in live/confirms/ that doesn't end in .gdl does NOT trigger
// autopromote.Handle. The path suffix guard in the dispatch table must
// reject it so non-confirm sidecar files (e.g., editor swap files) are
// silently ignored.
func TestDefaultEventHandler_IgnoresConfirmsWrongSuffix(t *testing.T) {
	root := t.TempDir()
	// No seeding needed — the dispatch should short-circuit on suffix
	// mismatch before any handler runs. Create the dir so we're testing
	// the suffix guard rather than missing-dir behaviour.
	if err := os.MkdirAll(filepath.Join(root, "live", "confirms"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := defaultEventHandler(root)
	err := h(FileEvent{Kind: "add", Path: "live/confirms/x.txt"})
	if err != nil {
		t.Errorf("expected nil for wrong-suffix path, got %v", err)
	}
}
