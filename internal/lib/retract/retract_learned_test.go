package retract

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
)

// --- #150: Lookup must walk learned/ for observation records ---
//
// Observations live at learned/<seg1>/<seg2>/.../<id>.gdlm
// (observation.SubjectPath). retract.Lookup today only globs
// live/outbox/*/<id>.gdl, so observations are unreachable from retract.

// TestLookup_FoundInLearned_ReturnsAuthor seeds an @observation at
// learned/test/1/<id>.gdlm and asserts Lookup finds it and returns the
// on-disk author (parsed from the record).
func TestLookup_FoundInLearned_ReturnsAuthor(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "learned", "test", "1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "1779261206879-ve0dg5"
	line := "@observation|id:" + id + "|author:agent-a|subject:test\\:1|" +
		"predicate:ok|object:value|scope:fleet|confidence:0.8|" +
		"ts:2026-05-20T12:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, id+".gdlm"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	author, err := Lookup(root, id)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if author != "agent-a" {
		t.Errorf("author=%q want agent-a", author)
	}
}

// TestLookupTarget_FoundInLearned_ReturnsAuthorAndScope mirrors
// TestLookup_FoundInLearned but uses LookupTarget which also returns
// the parsed scope: field (confirm/refute privacy gate).
func TestLookupTarget_FoundInLearned_ReturnsAuthorAndScope(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "learned", "test", "1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "1779261206879-ve0dg5"
	line := "@observation|id:" + id + "|author:agent-a|subject:test\\:1|" +
		"predicate:ok|object:value|scope:agent|confidence:0.8|" +
		"ts:2026-05-20T12:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, id+".gdlm"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	author, scope, err := LookupTarget(root, id)
	if err != nil {
		t.Fatalf("LookupTarget: %v", err)
	}
	if author != "agent-a" {
		t.Errorf("author=%q want agent-a", author)
	}
	if scope != "agent" {
		t.Errorf("scope=%q want agent", scope)
	}
}

// TestLookup_NoMatch_ImprovedErrorMessage asserts that when an id is
// missing from BOTH live/outbox/ AND learned/, the error message names
// both directories (the misleading "thought" phrasing is fixed per #150).
func TestLookup_NoMatch_ImprovedErrorMessage(t *testing.T) {
	root := t.TempDir()
	_, err := Lookup(root, "1779261206879-missing")
	var got *rufioerr.NoSuchThoughtError
	if !errors.As(err, &got) {
		t.Fatalf("want *NoSuchThoughtError, got %T %v", err, err)
	}
	msg := err.Error()
	// New message names both lookup roots so the agent knows the
	// observation case was considered (not just outbox).
	if !strings.Contains(msg, "learned/") {
		t.Errorf("error message must mention learned/: %q", msg)
	}
	if !strings.Contains(msg, "outbox") {
		t.Errorf("error message must mention outbox: %q", msg)
	}
}

// TestLookup_OutboxStillFound_Unbroken regression-tests the existing
// outbox-lookup path — extending to learned/ must not break the
// existing thought-retract case.
func TestLookup_OutboxStillFound_Unbroken(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "outbox", "agent-a")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1-thought.gdl"), []byte("@thought|...\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	author, err := Lookup(root, "1-thought")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if author != "agent-a" {
		t.Errorf("author=%q want agent-a", author)
	}
}
