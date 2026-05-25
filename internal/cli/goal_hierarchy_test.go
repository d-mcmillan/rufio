// Package cli — tests for goal hierarchy integrity (#130) and goals-list
// hierarchy visibility (#131).
//
// Defects being guarded:
//
//	#130: An author could `goal complete` or `goal abandon` a parent
//	while its children were still active, silently orphaning them.
//	The fix refuses the transition (with a clear error listing the
//	active children) and provides a `--force` escape hatch. A
//	cross-author `--parent` attach is allowed (collaborative
//	hierarchies are a feature) but emits an advisory stderr warning so
//	the relationship isn't established silently. `--quiet` suppresses
//	the advisory warning so JSON/scripted callers stay clean.
//
//	#131: `rufio goals list` text output dropped the parent relationship
//	entirely. The default columnar line now carries `parent:<id>` when
//	a goal has one; a new `--tree` flag renders nested goals indented
//	by depth; and a new `--parent=<id>` filter narrows the list to
//	direct children of <id>. JSON output is untouched.
//
// Tests drive the run* handlers directly, mirroring scope_consistency_test.go.
package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// hierarchyTestProject scaffolds the minimal rufio project shape that
// FindProjectRoot + identity.Resolve expect, with the local agent set to
// `agent`. RUFIO_AGENT_ID is cleared so the local file is authoritative.
func hierarchyTestProject(t *testing.T, agent string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rufio.gdl"), []byte(""), 0o644); err != nil {
		t.Fatalf("write rufio.gdl: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".rufio"), 0o755); err != nil {
		t.Fatalf("mkdir .rufio: %v", err)
	}
	rec := gdl.Record{Type: "identity", Fields: []gdl.RecordField{
		{Key: "agent", Value: agent},
		{Key: "set-at", Value: versioning.NowISO()},
	}}
	if err := os.WriteFile(filepath.Join(root, ".rufio", "identity.local.gdl"),
		[]byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	t.Setenv("RUFIO_AGENT_ID", "")
	return root
}

// seedActiveGoal writes a single-record @goal file under
// live/goals/active/<id>.gdl with the given fields. Mirrors the test seed
// helper in internal/lib/goal/goal_test.go but at the CLI test boundary.
func seedActiveGoal(t *testing.T, root, id, author, statement, parent string) {
	t.Helper()
	dir := filepath.Join(root, "live", "goals", "active")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir active: %v", err)
	}
	rec := goal.BuildGoalRecord(id, author, statement, "", parent, "fleet", versioning.NowISO())
	if err := os.WriteFile(filepath.Join(dir, id+".gdl"),
		[]byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatalf("write goal: %v", err)
	}
}

// captureStderr swaps os.Stderr for a pipe, runs fn, and returns whatever
// was written. Mirrors the pipe pattern in tui_test.go. Reads on a
// goroutine so a writer that exceeds the pipe buffer doesn't deadlock.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return <-done
}

// (captureStdout lives in dev_quiet_test.go — reused here.)

// ---- #130: complete/abandon refuse with active children --------------------

// TestGoalComplete_RefusesWithActiveChildren asserts that running
// `goal complete <parent>` while children are active surfaces a
// HierarchyError (or sentinel) and does NOT move the parent file out of
// live/goals/active/.
func TestGoalComplete_RefusesWithActiveChildren(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	parent := "111-parent"
	seedActiveGoal(t, root, parent, "alice", "ship v1", "")
	seedActiveGoal(t, root, "222-childA", "alice", "design", parent)
	seedActiveGoal(t, root, "333-childB", "alice", "docs", parent)

	err := runGoalComplete(root, parent, "premature", false, output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("expected error refusing complete with active children, got nil")
	}
	msg := err.Error()
	// The error must name both active children so the user can act on
	// them without grepping. Order isn't significant.
	if !strings.Contains(msg, "active children") {
		t.Errorf("error %q must say 'active children'", msg)
	}
	if !strings.Contains(msg, "222-childA") || !strings.Contains(msg, "333-childB") {
		t.Errorf("error %q must list both child ids (222-childA, 333-childB)", msg)
	}
	if !strings.Contains(msg, "--force") {
		t.Errorf("error %q must mention --force escape hatch", msg)
	}
	// Parent must still be active — no state change on the refusal path.
	if _, err := os.Stat(filepath.Join(root, "live", "goals", "active", parent+".gdl")); err != nil {
		t.Errorf("parent active file should still exist after refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "goals", "completed", parent+".gdl")); !os.IsNotExist(err) {
		t.Errorf("parent must NOT be in completed/ after refusal: %v", err)
	}
}

// TestGoalComplete_ForceFlagBypasses asserts that --force bypasses the
// hierarchy check. The parent moves to completed/ even with active
// children — escape hatch for the rare legitimate case (e.g. parent
// became unblocking, children are independent now).
func TestGoalComplete_ForceFlagBypasses(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	parent := "111-parent"
	seedActiveGoal(t, root, parent, "alice", "ship v1", "")
	seedActiveGoal(t, root, "222-childA", "alice", "design", parent)

	if err := runGoalComplete(root, parent, "force-completed", true, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("unexpected error with --force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "goals", "completed", parent+".gdl")); err != nil {
		t.Errorf("parent should be in completed/ after --force: %v", err)
	}
}

// TestGoalAbandon_RefusesWithActiveChildren mirrors the complete test for
// abandon — the R10 expansion on #130 calls out the same bug.
func TestGoalAbandon_RefusesWithActiveChildren(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	parent := "111-parent"
	seedActiveGoal(t, root, parent, "alice", "ship v1", "")
	seedActiveGoal(t, root, "222-childA", "alice", "design", parent)

	err := runGoalAbandon(root, parent, "deprioritised", false, output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("expected error refusing abandon with active children, got nil")
	}
	msg := err.Error()
	// Either "active child" or "active children" — singular form is
	// correct when there's exactly one.
	if !strings.Contains(msg, "active child") {
		t.Errorf("error %q must mention 'active child(ren)'", msg)
	}
	if !strings.Contains(msg, "222-childA") {
		t.Errorf("error %q must list child id 222-childA", msg)
	}
	if !strings.Contains(msg, "--force") {
		t.Errorf("error %q must mention --force", msg)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "goals", "active", parent+".gdl")); err != nil {
		t.Errorf("parent active file should still exist after refusal: %v", err)
	}
}

// TestGoalAbandon_ForceFlagBypasses mirrors the complete --force test.
func TestGoalAbandon_ForceFlagBypasses(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	parent := "111-parent"
	seedActiveGoal(t, root, parent, "alice", "ship v1", "")
	seedActiveGoal(t, root, "222-childA", "alice", "design", parent)

	if err := runGoalAbandon(root, parent, "force-abandoned", true, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("unexpected error with --force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "goals", "abandoned", parent+".gdl")); err != nil {
		t.Errorf("parent should be in abandoned/ after --force: %v", err)
	}
}

// ---- #130: cross-author attach advisory warning ---------------------------

// TestGoal_CrossAuthorParentAttach_StderrWarning — bob attaching a child
// to alice's parent goal succeeds (collaborative hierarchies are a
// feature) but emits an advisory stderr warning so the relationship is
// surfaced, not silent. The warning names the parent's author.
func TestGoal_CrossAuthorParentAttach_StderrWarning(t *testing.T) {
	root := hierarchyTestProject(t, "bob")
	// Seed alice's active parent goal.
	seedActiveGoal(t, root, "111-parent", "alice", "alice's parent", "")

	stderr := captureStderr(t, func() {
		err := runGoalWrite(root, "bob's child", "", "111-parent", "fleet", output.RenderOpts{Quiet: false})
		if err != nil {
			t.Fatalf("runGoalWrite: unexpected error %v", err)
		}
	})
	if !strings.Contains(stderr, "warning") {
		t.Errorf("expected stderr to contain 'warning', got %q", stderr)
	}
	if !strings.Contains(stderr, "alice") {
		t.Errorf("expected stderr to name parent author 'alice', got %q", stderr)
	}
	// The child must still be written.
	matches, _ := filepath.Glob(filepath.Join(root, "live", "goals", "active", "*.gdl"))
	if len(matches) != 2 {
		t.Errorf("expected 2 active goal files (alice's parent + bob's child), got %d", len(matches))
	}
}

// TestGoal_CrossAuthorParentAttach_QuietSuppresses — --quiet must
// suppress the advisory stderr line so machine-readable callers stay
// clean. The relationship is still established.
func TestGoal_CrossAuthorParentAttach_QuietSuppresses(t *testing.T) {
	root := hierarchyTestProject(t, "bob")
	seedActiveGoal(t, root, "111-parent", "alice", "alice's parent", "")

	stderr := captureStderr(t, func() {
		err := runGoalWrite(root, "bob's child", "", "111-parent", "fleet", output.RenderOpts{Quiet: true})
		if err != nil {
			t.Fatalf("runGoalWrite: unexpected error %v", err)
		}
	})
	if strings.Contains(strings.ToLower(stderr), "warning") {
		t.Errorf("expected --quiet to suppress cross-author warning, got %q", stderr)
	}
}

// TestGoal_SameAuthorParentAttach_NoWarning — alice attaching a child to
// her own parent goal must NOT trigger the advisory warning.
func TestGoal_SameAuthorParentAttach_NoWarning(t *testing.T) {
	root := hierarchyTestProject(t, "alice")
	seedActiveGoal(t, root, "111-parent", "alice", "alice's parent", "")

	stderr := captureStderr(t, func() {
		err := runGoalWrite(root, "alice's child", "", "111-parent", "fleet", output.RenderOpts{Quiet: false})
		if err != nil {
			t.Fatalf("runGoalWrite: unexpected error %v", err)
		}
	})
	if strings.Contains(strings.ToLower(stderr), "warning") {
		t.Errorf("same-author attach must not warn, got %q", stderr)
	}
}

// ---- #131: goals list visibility ------------------------------------------

// TestGoalsList_TextShowsParentField — when a goal has a parent, the
// default columnar text rendering MUST surface it. We don't pin the exact
// column position; we just require `parent:<id>` to appear on the child's
// line.
func TestGoalsList_TextShowsParentField(t *testing.T) {
	// H1b: text mode shortens ids by default. Pin RUFIO_FULL_IDS=1 so
	// the synthetic ids (`111-parent`, `222-child`) appear verbatim in
	// the assertion substrings.
	t.Setenv("RUFIO_FULL_IDS", "1")
	root := hierarchyTestProject(t, "alice")
	seedActiveGoal(t, root, "111-parent", "alice", "parent goal", "")
	seedActiveGoal(t, root, "222-child", "alice", "child goal", "111-parent")

	out := captureStdout(t, func() {
		if err := runGoalsList(root, "", "", "", false, output.RenderOpts{Quiet: false}); err != nil {
			t.Fatalf("runGoalsList: %v", err)
		}
	})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out)
	}
	// Find the child row.
	var childLine string
	for _, l := range lines {
		if strings.Contains(l, "222-child") {
			childLine = l
			break
		}
	}
	if childLine == "" {
		t.Fatalf("no child row in output: %q", out)
	}
	if !strings.Contains(childLine, "parent:111-parent") {
		t.Errorf("child row must contain 'parent:111-parent', got %q", childLine)
	}
	// A goal without a parent should NOT carry the `parent:` token — empty
	// parent fields shouldn't render as `parent:` with nothing after.
	var parentLine string
	for _, l := range lines {
		if strings.Contains(l, "111-parent") && !strings.Contains(l, "222-child") {
			parentLine = l
			break
		}
	}
	if parentLine == "" {
		t.Fatalf("no parent row in output: %q", out)
	}
	if strings.Contains(parentLine, "parent:") {
		t.Errorf("parent (parentless) row must not include 'parent:' token, got %q", parentLine)
	}
}

// TestGoalsList_TreeFlag_RendersNestedHierarchy — --tree renders nested
// goals indented under their parent. We require the child line to start
// with at least 2 spaces of indent and to follow the parent line (DFS
// order).
func TestGoalsList_TreeFlag_RendersNestedHierarchy(t *testing.T) {
	t.Setenv("RUFIO_FULL_IDS", "1")
	root := hierarchyTestProject(t, "alice")
	seedActiveGoal(t, root, "111-parent", "alice", "parent goal", "")
	seedActiveGoal(t, root, "222-childA", "alice", "child A", "111-parent")
	seedActiveGoal(t, root, "333-childB", "alice", "child B", "111-parent")
	seedActiveGoal(t, root, "444-grandchild", "alice", "grandchild", "222-childA")

	out := captureStdout(t, func() {
		if err := runGoalsList(root, "", "", "", true, output.RenderOpts{Quiet: false}); err != nil {
			t.Fatalf("runGoalsList tree: %v", err)
		}
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %q", len(lines), out)
	}
	// Line 0: parent — no leading whitespace.
	if strings.HasPrefix(lines[0], " ") || strings.HasPrefix(lines[0], "\t") {
		t.Errorf("first line should be the parent (no indent), got %q", lines[0])
	}
	if !strings.Contains(lines[0], "111-parent") {
		t.Errorf("first line should be parent 111-parent, got %q", lines[0])
	}
	// Lines 1-3: descendants — each must have leading indent.
	for i := 1; i < 4; i++ {
		if !strings.HasPrefix(lines[i], "  ") {
			t.Errorf("line %d should be indented (descendant), got %q", i, lines[i])
		}
	}
	// Grandchild line must have deeper indent than its parent's child
	// line. NB: the grandchild line carries `parent:222-childA`, so
	// matching by "222-childA" alone would mis-grab the grandchild row.
	// We require statement:"child A" / statement:"grandchild" to pin
	// the right rows.
	var aLine, gcLine string
	for _, l := range lines {
		if strings.Contains(l, `statement:"child A"`) {
			aLine = l
		}
		if strings.Contains(l, `statement:"grandchild"`) {
			gcLine = l
		}
	}
	if aLine == "" || gcLine == "" {
		t.Fatalf("missing child or grandchild line in tree output: %q", out)
	}
	indentOf := func(s string) int {
		n := 0
		for _, c := range s {
			if c == ' ' {
				n++
				continue
			}
			break
		}
		return n
	}
	if indentOf(gcLine) <= indentOf(aLine) {
		t.Errorf("grandchild (%d) must be more indented than its parent (%d); full output:\n%s",
			indentOf(gcLine), indentOf(aLine), out)
	}
}

// TestGoalsList_ParentFilter_ReturnsOnlyChildren — --parent=<id> narrows
// the list to direct children of <id>.
func TestGoalsList_ParentFilter_ReturnsOnlyChildren(t *testing.T) {
	t.Setenv("RUFIO_FULL_IDS", "1")
	root := hierarchyTestProject(t, "alice")
	seedActiveGoal(t, root, "111-parent", "alice", "parent goal", "")
	seedActiveGoal(t, root, "222-childA", "alice", "child A", "111-parent")
	seedActiveGoal(t, root, "333-childB", "alice", "child B", "111-parent")
	seedActiveGoal(t, root, "999-unrelated", "alice", "unrelated", "")

	out := captureStdout(t, func() {
		if err := runGoalsList(root, "", "", "111-parent", false, output.RenderOpts{Quiet: false}); err != nil {
			t.Fatalf("runGoalsList parent filter: %v", err)
		}
	})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 child lines, got %d: %q", len(lines), out)
	}
	for _, l := range lines {
		if !strings.Contains(l, "222-childA") && !strings.Contains(l, "333-childB") {
			t.Errorf("line %q must be one of the two children", l)
		}
		if strings.Contains(l, "999-unrelated") || (strings.Contains(l, "111-parent") && !strings.Contains(l, "222-") && !strings.Contains(l, "333-")) {
			t.Errorf("unexpected line in filtered output: %q", l)
		}
	}
}
