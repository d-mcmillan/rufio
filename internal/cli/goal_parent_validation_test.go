// Package cli — tests for #133 (MED): `rufio goal --parent=<id>` must
// resolve and verify the parent exists in live/goals/{active,completed,
// abandoned}/ and reject with a clear error if missing. Mirrors the
// contract `reason --decision` enforces (ValidateDecisionTarget): pure
// shape regex first (thought.ValidateParent, exit 2), then existence
// (NoSuchGoalError, exit 1).
//
// Before the fix: a format-valid-but-missing id was silently accepted
// and a dangling-reference goal was written. Cold-start round-6 vet repro:
//
//	$ rufio goal --statement=child --scope=deployment --parent=1779261326011-fakeid
//	goal: id=1779261337779-q5nnq5 scope=deployment
//	EXIT=0
//
// After the fix: same call returns *NoSuchGoalError (exit 1) and writes
// nothing.
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// TestGoalParent_MissingCanonicalID_RejectsWithNoSuchGoal is the headline
// #133 case: a canonical-shape parent id that points at no on-disk goal
// must produce *NoSuchGoalError (exit 1), NOT a silent write.
func TestGoalParent_MissingCanonicalID_RejectsWithNoSuchGoal(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	// No parent goal seeded — the id below is format-valid but absent.
	missing := "1779261326011-fakeid"

	err := runGoalWrite(root, "child of well-formed but absent parent",
		"", missing, "deployment", output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("expected *NoSuchGoalError for missing canonical parent id, got nil")
	}
	var nsg *rufioerr.NoSuchGoalError
	if !errors.As(err, &nsg) {
		t.Fatalf("expected *NoSuchGoalError, got %T: %v", err, err)
	}
	if nsg.ID != missing {
		t.Errorf("error must carry the missing id; got %q want %q", nsg.ID, missing)
	}
	if nsg.ExitCode() != 1 {
		t.Errorf("NoSuchGoalError must exit 1 (consistent with reason --decision NoSuchThoughtError); got %d", nsg.ExitCode())
	}
	// No dangling child goal should have been written.
	matches, _ := filepath.Glob(filepath.Join(root, "live", "goals", "active", "*.gdl"))
	if len(matches) != 0 {
		t.Errorf("expected 0 active goal files after rejection, got %d (dangling write!): %v", len(matches), matches)
	}
}

// TestGoalParent_ValidParent_StillWrites is the regression guard: an
// existing parent goal must still allow the child to be written. Without
// this, an over-eager fix would block every cross-reference.
func TestGoalParent_ValidParent_StillWrites(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	parent := "1779261326011-realid"
	seedActiveGoal(t, root, parent, "alice", "parent statement", "")

	if err := runGoalWrite(root, "child", "", parent, "fleet", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runGoalWrite with valid parent: %v", err)
	}
	// Two active files: parent + child.
	matches, _ := filepath.Glob(filepath.Join(root, "live", "goals", "active", "*.gdl"))
	if len(matches) != 2 {
		t.Errorf("expected 2 active goal files (parent + child), got %d", len(matches))
	}
}

// TestGoalParent_CompletedParentAccepted: parent existence is checked
// across all three state directories (active/completed/abandoned). A
// completed parent must be acceptable as a --parent — the lineage is
// historical, not blocking. This mirrors the brief's "walk
// live/goals/{active,completed,abandoned}/".
func TestGoalParent_CompletedParentAccepted(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	parent := "1779261326011-cmpltd"
	// Seed directly into completed/ to skip the move dance.
	dir := filepath.Join(root, "live", "goals", "completed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir completed: %v", err)
	}
	seedActiveGoalScoped(t, root, parent, "alice", "completed parent", "", "fleet")
	if err := os.Rename(
		filepath.Join(root, "live", "goals", "active", parent+".gdl"),
		filepath.Join(dir, parent+".gdl"),
	); err != nil {
		t.Fatalf("move parent to completed/: %v", err)
	}

	if err := runGoalWrite(root, "child of completed", "", parent, "fleet", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runGoalWrite with completed parent: %v", err)
	}
}

// TestGoalParent_AbandonedParentAccepted: symmetric to the completed
// case — an abandoned parent must also satisfy the existence check.
func TestGoalParent_AbandonedParentAccepted(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	parent := "1779261326011-abndnd"
	seedActiveGoalScoped(t, root, parent, "alice", "abandoned parent", "", "fleet")
	dir := filepath.Join(root, "live", "goals", "abandoned")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir abandoned: %v", err)
	}
	if err := os.Rename(
		filepath.Join(root, "live", "goals", "active", parent+".gdl"),
		filepath.Join(dir, parent+".gdl"),
	); err != nil {
		t.Fatalf("move parent to abandoned/: %v", err)
	}

	if err := runGoalWrite(root, "child of abandoned", "", parent, "fleet", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runGoalWrite with abandoned parent: %v", err)
	}
}

// TestGoalParent_EmptyParent_NoCheck: empty --parent is a no-op (free
// top-level goal). The existence check MUST NOT run, MUST NOT error.
func TestGoalParent_EmptyParent_NoCheck(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	if err := runGoalWrite(root, "top-level goal", "", "", "fleet", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runGoalWrite with empty parent: %v", err)
	}
}

// TestGoalParent_InvalidShape_StillExits2: regression guard. A
// non-canonical, non-short-id-suffix string MUST still surface
// *InvalidParentError (exit 2) — the existence check is the
// AFTER-shape layer. We must not regress that error type to a
// NoSuchGoalError "miss" by accident.
func TestGoalParent_InvalidShape_StillExits2(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	err := runGoalWrite(root, "child", "", "GARBAGE-not-an-id", "fleet", output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("expected *InvalidParentError for malformed parent id, got nil")
	}
	var inv *rufioerr.InvalidParentError
	if !errors.As(err, &inv) {
		t.Fatalf("expected *InvalidParentError, got %T: %v", err, err)
	}
	if inv.ExitCode() != 2 {
		t.Errorf("InvalidParentError must exit 2 (usage error); got %d", inv.ExitCode())
	}
}

// TestGoalParent_NonAuthorScopeAgent_LooksLikeMiss: privacy floor. A
// non-author who guesses (or pastes) the canonical id of an
// other-author scope:agent parent MUST see *NoSuchGoalError — not a
// "found" success and not a more specific privacy error. Existence must
// not be leaked via differential error wording.
func TestGoalParent_NonAuthorScopeAgent_LooksLikeMiss(t *testing.T) {
	root := hierarchyTestProject(t, "bob")
	// Alice's private parent goal — bob must not see it.
	hidden := "1779261326011-hidden"
	seedActiveGoalScoped(t, root, hidden, "alice", "alice private", "", "agent")

	err := runGoalWrite(root, "bob's child", "", hidden, "fleet", output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("expected *NoSuchGoalError when non-author references scope:agent parent, got nil")
	}
	var nsg *rufioerr.NoSuchGoalError
	if !errors.As(err, &nsg) {
		t.Fatalf("expected *NoSuchGoalError (privacy floor: must look like a miss), got %T: %v", err, err)
	}
	// No child written.
	matches, _ := filepath.Glob(filepath.Join(root, "live", "goals", "active", "*.gdl"))
	// Only alice's hidden parent should remain (bob's child must not have landed).
	if len(matches) != 1 {
		t.Errorf("expected 1 active goal file (alice's hidden parent only), got %d", len(matches))
	}
}
