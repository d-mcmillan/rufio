// Package cli — R30 short-id-suffix tests for the goal verb family.
//
// R30 verdict identified the same asymmetry R29 fixed for thought-verbs
// (confirm/refute/retract/lineage/reason --decision) but for goals:
//
//   - `rufio goal complete <short>` — PR #172 wired short-id resolution
//     into `goal.LoadAnyState`, so the read succeeds, but the subsequent
//     `goal.MoveToCompleted(root, goalID, ...)` call was still passed the
//     ORIGINAL short id by the CLI handler. The move resolves
//     <root>/live/goals/active/<short>.gdl (which doesn't exist) and
//     returns *NoSuchGoalError. Net effect: load works, move fails, user
//     sees "no such goal: <short>" — same end-state as if the resolver
//     had never run. Tests pin the canonical-id propagation contract.
//
//   - `rufio goal abandon <short>` — symmetric to complete, same bug.
//
//   - `rufio goal --parent=<short>` — `thought.ValidateParent` rejects
//     the short shape before any resolver runs, with exit code 2. The
//     fix resolves the suffix first, THEN validates the canonical shape.
//
// Privacy floor (#147) applies on the suffix path: other-author
// scope:agent goals must NOT surface as suffix candidates for non-author
// callers — existence is leaked otherwise. resolveSuffix grew a privacy
// filter to match retract.Resolve's posture.
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// seedActiveGoalScoped is the scope-aware seeding helper (the existing
// seedActiveGoal in goal_hierarchy_test.go hardcodes scope=fleet). Used
// here to pin author + scope for the privacy-floor test.
func seedActiveGoalScoped(t *testing.T, root, id, author, statement, parent, scope string) {
	t.Helper()
	dir := filepath.Join(root, "live", "goals", "active")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir active: %v", err)
	}
	rec := goal.BuildGoalRecord(id, author, statement, "", parent, scope, versioning.NowISO())
	if err := os.WriteFile(filepath.Join(dir, id+".gdl"),
		[]byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatalf("write goal: %v", err)
	}
}

// TestGoalComplete_AcceptsShortIDSuffix: the load already resolves the
// short id (R29a). This test pins the second half — the subsequent
// MoveToCompleted call also receives the canonical id, so the file
// actually moves from active/ to completed/.
func TestGoalComplete_AcceptsShortIDSuffix(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	full := "1779323387211-yjqccb"
	seedActiveGoalScoped(t, root, full, "alice", "ship v1", "", "fleet")

	if err := runGoalComplete(root, "yjqccb", "shipped", false, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runGoalComplete short id: %v", err)
	}
	// File MUST have moved to completed/ under the canonical id.
	if _, err := os.Stat(filepath.Join(root, "live", "goals", "completed", full+".gdl")); err != nil {
		t.Errorf("completed file at canonical id missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "goals", "active", full+".gdl")); !os.IsNotExist(err) {
		t.Errorf("active file must be gone after complete: %v", err)
	}
}

// TestGoalAbandon_AcceptsShortIDSuffix: symmetric to complete.
func TestGoalAbandon_AcceptsShortIDSuffix(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	full := "1779323387211-yjqccb"
	seedActiveGoalScoped(t, root, full, "alice", "ship v1", "", "fleet")

	if err := runGoalAbandon(root, "yjqccb", "deprioritised", false, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runGoalAbandon short id: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "goals", "abandoned", full+".gdl")); err != nil {
		t.Errorf("abandoned file at canonical id missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "goals", "active", full+".gdl")); !os.IsNotExist(err) {
		t.Errorf("active file must be gone after abandon: %v", err)
	}
}

// TestGoalParent_AcceptsShortIDSuffix: --parent flag accepts a 6-char
// suffix, resolves to the canonical id, AND the resulting child's
// `parent:` field carries the canonical id (not the short).
func TestGoalParent_AcceptsShortIDSuffix(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	parentFull := "1779323387211-yjqccb"
	seedActiveGoalScoped(t, root, parentFull, "alice", "parent goal", "", "fleet")

	// Use the write path directly. The bug was that ValidateParent's
	// regex rejected the short shape before any resolver could run.
	if err := runGoalWrite(root, "child of parent", "", "yjqccb", "fleet", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runGoalWrite --parent=<short>: %v", err)
	}
	// Find the new child file (only one active goal besides parent).
	entries, err := os.ReadDir(filepath.Join(root, "live", "goals", "active"))
	if err != nil {
		t.Fatalf("readdir active: %v", err)
	}
	var childFile string
	for _, e := range entries {
		name := e.Name()
		if name == parentFull+".gdl" {
			continue
		}
		if strings.HasSuffix(name, ".gdl") {
			childFile = name
		}
	}
	if childFile == "" {
		t.Fatal("no child goal file written")
	}
	bs, err := os.ReadFile(filepath.Join(root, "live", "goals", "active", childFile))
	if err != nil {
		t.Fatalf("read child file: %v", err)
	}
	// Canonical id must be on the wire — never the short suffix alone.
	if !strings.Contains(string(bs), "parent:"+parentFull) {
		t.Errorf("child must carry canonical parent id; got: %q", bs)
	}
}

// TestGoalComplete_AmbiguousShortID_ListsCandidates: two active goals
// share the same 6-char suffix. The error must name both with
// disambiguation context so the agent can pick.
func TestGoalComplete_AmbiguousShortID_ListsCandidates(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	a := "1779323387211-yjqccb"
	b := "1779323444221-yjqccb"
	seedActiveGoalScoped(t, root, a, "alice", "first ambiguous", "", "fleet")
	seedActiveGoalScoped(t, root, b, "alice", "second ambiguous", "", "fleet")

	err := runGoalComplete(root, "yjqccb", "shipped", false, output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("ambiguous short id: want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, a) || !strings.Contains(msg, b) {
		t.Errorf("ambiguous error missing one or both canonical ids: %s", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "ambiguous") {
		t.Errorf("ambiguous error should name the condition: %s", msg)
	}
}

// TestGoalParent_AmbiguousShortID_ListsCandidates: --parent=<short>
// with multiple matches lists candidates rather than picking a random
// one or proceeding silently.
func TestGoalParent_AmbiguousShortID_ListsCandidates(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	a := "1779323387211-yjqccb"
	b := "1779323444221-yjqccb"
	seedActiveGoalScoped(t, root, a, "alice", "first ambiguous", "", "fleet")
	seedActiveGoalScoped(t, root, b, "alice", "second ambiguous", "", "fleet")

	err := runGoalWrite(root, "child", "", "yjqccb", "fleet", output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("ambiguous --parent suffix: want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, a) || !strings.Contains(msg, b) {
		t.Errorf("ambiguous --parent error missing one or both canonical ids: %s", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "ambiguous") {
		t.Errorf("ambiguous --parent error should name the condition: %s", msg)
	}
}

// TestGoalComplete_NoMatch_StillSurfacesNotFound: regression guard. A
// short id with zero matches must still surface NoSuchGoalError, NOT
// the disambiguation error.
func TestGoalComplete_NoMatch_StillSurfacesNotFound(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	// No goals seeded.
	err := runGoalComplete(root, "yjqccb", "shipped", false, output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("no-match suffix: want error, got nil")
	}
	if !strings.Contains(err.Error(), "no such goal") {
		t.Errorf("no-match: want canonical 'no such goal' wording, got: %s", err.Error())
	}
}

// TestGoalComplete_FullIDStillWorks: regression guard. The exact
// canonical id MUST keep working alongside the new short-form path.
func TestGoalComplete_FullIDStillWorks(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	full := "1779323387211-yjqccb"
	seedActiveGoalScoped(t, root, full, "alice", "ship v1", "", "fleet")

	if err := runGoalComplete(root, full, "shipped", false, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runGoalComplete full id (regression): %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "goals", "completed", full+".gdl")); err != nil {
		t.Errorf("completed file at canonical path missing: %v", err)
	}
}

// TestGoalComplete_ShortIDSuffix_RespectsPrivacyFloor: bob completing
// his own goal that shares a suffix with alice's scope:agent goal must
// NOT cause an ambiguity error — alice's record is hidden by the
// privacy floor, so bob sees a clean single-candidate resolve. The
// stronger form of the test (bob → alice's scope:agent suffix alone)
// can't be completed by bob anyway because of the author-only authz
// check, but it MUST NOT be the case that the suffix lookup leaks
// existence by upgrading "no match" to "ambiguous" when an other-author
// agent-scope goal shares the suffix.
func TestGoalComplete_ShortIDSuffix_RespectsPrivacyFloor(t *testing.T) {
	root := hierarchyTestProject(t, "bob")
	// alice's private goal shares the suffix.
	seedActiveGoalScoped(t, root, "1779323387211-yjqccb", "alice", "alice private", "", "agent")
	// bob's own visible goal with the SAME suffix.
	bobFull := "1779323444221-yjqccb"
	seedActiveGoalScoped(t, root, bobFull, "bob", "bob's goal", "", "fleet")

	// Bob's complete must resolve cleanly to his own goal — alice's
	// private record must NOT pollute the candidate set into ambiguity.
	if err := runGoalComplete(root, "yjqccb", "shipped", false, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("privacy floor: bob's resolve must succeed despite alice's hidden scope:agent record with same suffix: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "goals", "completed", bobFull+".gdl")); err != nil {
		t.Errorf("bob's completed file missing: %v", err)
	}
}
