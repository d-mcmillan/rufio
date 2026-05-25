package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// Integration tests for `rufio goal --statement=<text>` — the write-side
// of the parent Cobra command. Subcommands (complete/abandon) are
// exercised separately (Tasks 5+6).
//
// Exit-code semantics (D17.13 + the dispatcher contract):
//   - 0 success
//   - 1 runtime/project errors (NoIdentity, NotInProject, NoSuchGoal)
//   - 2 validation errors (InvalidStatement, InvalidScope, InvalidParent)

func TestGoal_HappyPath_WritesActiveFile(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=reduce churn",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}

	matches, _ := filepath.Glob(filepath.Join(root, "live", "goals", "active", "*.gdl"))
	if len(matches) != 1 {
		t.Fatalf("want 1 .gdl in live/goals/active, got %d (%v)", len(matches), matches)
	}
	bs, _ := os.ReadFile(matches[0])
	content := string(bs)
	for _, expect := range []string{
		"@goal|",
		"author:agent-a",
		"statement:reduce churn",
		// H3a (#125): default --scope changed agent → fleet so goal matches
		// the unified write-verb rule (broadcast default).
		"scope:fleet",
	} {
		if !strings.Contains(content, expect) {
			t.Errorf("file missing %q.\nFull:\n%s", expect, content)
		}
	}
}

func TestGoal_WithBy_StoredVerbatim(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=ship v1", "--by=EOW",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "goals", "active", "*.gdl"))
	bs, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(bs), "by:EOW") {
		t.Errorf("want by:EOW, got: %s", bs)
	}
}

func TestGoal_WithParent_RecordsParent(t *testing.T) {
	root := initProject(t)

	// #133: --parent now requires the parent to actually exist. Seed
	// a real parent goal first via the CLI, lift its id from the JSON
	// payload, then write the child against the canonical id.
	parentRes := testutil.RunCLI(t, []string{
		"goal", "--statement=parent goal", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if parentRes.Code != 0 {
		t.Fatalf("seed parent: exit=%d stderr=%q", parentRes.Code, parentRes.Stderr)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(parentRes.Stdout)), &payload); err != nil {
		t.Fatalf("seed parent: parse JSON: %v\nstdout=%q", err, parentRes.Stdout)
	}
	parent, ok := payload["id"].(string)
	if !ok || parent == "" {
		t.Fatalf("seed parent: missing id in JSON payload: %v", payload)
	}

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=sub-goal", "--parent=" + parent,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// Two active goals now: parent + child. Find the child by reading
	// each file and matching on statement (the canonical-id paths are
	// indeterminate).
	matches, _ := filepath.Glob(filepath.Join(root, "live", "goals", "active", "*.gdl"))
	if len(matches) != 2 {
		t.Fatalf("want 2 .gdl in live/goals/active, got %d (%v)", len(matches), matches)
	}
	var childContent string
	for _, p := range matches {
		bs, _ := os.ReadFile(p)
		if strings.Contains(string(bs), "statement:sub-goal") {
			childContent = string(bs)
			break
		}
	}
	if childContent == "" {
		t.Fatalf("did not find child goal in %v", matches)
	}
	if !strings.Contains(childContent, "parent:"+parent) {
		t.Errorf("want parent:%s, got: %s", parent, childContent)
	}
}

// TestGoal_NoSuchParent_Exit1 — #133: a format-valid-but-absent parent
// id must surface *NoSuchGoalError (exit 1), NOT a silent write. This
// is the integration-level form of the bug; the cli-level coverage is
// in internal/cli/goal_parent_validation_test.go.
func TestGoal_NoSuchParent_Exit1(t *testing.T) {
	root := initProject(t)
	// Format-valid (<unix-millis>-<rand6>) but no goal with this id has
	// ever been written under live/goals/.
	missing := "1779261326011-fakeid"

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=child of nothing", "--parent=" + missing,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 1 {
		t.Fatalf("exit=%d (want 1) stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stderr, "no such goal") {
		t.Errorf("stderr must mention 'no such goal'; got: %q", res.Stderr)
	}
	if !strings.Contains(res.Stderr, missing) {
		t.Errorf("stderr must carry the missing id %q; got: %q", missing, res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio goal:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
	// No goal must have been written.
	matches, _ := filepath.Glob(filepath.Join(root, "live", "goals", "active", "*.gdl"))
	if len(matches) != 0 {
		t.Errorf("want 0 active goals after rejection (no dangling write), got %d", len(matches))
	}
}

func TestGoal_WithoutBy_OmitsByField(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=no deadline",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "goals", "active", "*.gdl"))
	bs, _ := os.ReadFile(matches[0])
	if strings.Contains(string(bs), "by:") {
		t.Errorf("want no by: substring, got: %s", bs)
	}
}

// TestGoal_DefaultScope_IsFleet asserts the H3a (#125) contract: goal's
// default --scope is fleet (was: "agent"). Unified write-verb default —
// pass --scope=agent for private.
func TestGoal_DefaultScope_IsFleet(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=default scope",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "goals", "active", "*.gdl"))
	bs, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(bs), "scope:fleet") {
		t.Errorf("want scope:fleet (H3a default), got: %s", bs)
	}
}

func TestGoal_FleetScope_HappyPath(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=fleet wide", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "goals", "active", "*.gdl"))
	bs, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(bs), "scope:fleet") {
		t.Errorf("want scope:fleet, got: %s", bs)
	}
}

func TestGoal_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=ship goals", "--scope=fleet", "--by=EOW", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	stdout := strings.TrimSpace(res.Stdout)
	if strings.Contains(stdout, "\n") {
		t.Errorf("expected single JSONL line, got embedded newlines: %q", stdout)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nstdout=%q", err, stdout)
	}
	if got["_type"] != "goal" {
		t.Errorf("_type=%v", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v", got["_version"])
	}
	if got["author"] != "agent-a" {
		t.Errorf("author=%v", got["author"])
	}
	if got["statement"] != "ship goals" {
		t.Errorf("statement=%v", got["statement"])
	}
	if got["scope"] != "fleet" {
		t.Errorf("scope=%v", got["scope"])
	}
	if id, ok := got["id"].(string); !ok || id == "" {
		t.Errorf("id missing or empty: %v", got["id"])
	}
	if ts, ok := got["ts"].(string); !ok || ts == "" {
		t.Errorf("ts missing or empty: %v", got["ts"])
	}
	if got["by"] != "EOW" {
		t.Errorf("by=%v", got["by"])
	}
	// parent key MUST be present with nil value when --parent not given —
	// mirrors think's D5.12 contract so consumers don't have to .has-key check.
	parent, present := got["parent"]
	if !present {
		t.Errorf("parent key absent; want key present with null value")
	}
	if parent != nil {
		t.Errorf("parent=%v, want nil", parent)
	}
}

func TestGoal_ConfirmationLine_HasCanonicalPrefix(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=ship",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	out := strings.TrimSpace(res.Stdout)
	if !strings.HasPrefix(out, "goal: id=") {
		t.Errorf("confirmation missing canonical prefix; got: %q", out)
	}
	// Single-prefix invariant: success output must NOT carry the
	// "rufio <cmd>: " error prefix (only HandleError adds that).
	if strings.HasPrefix(out, "rufio goal:") {
		t.Errorf("success stdout carries error prefix: %q", out)
	}
}

func TestGoal_MissingStatement_Exit2(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{"goal"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--statement must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio goal:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
}

func TestGoal_InvalidScope_Exit2(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=x", "--scope=banana",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// InvalidScopeError surfaces the allowed enum so the user sees the set.
	if !strings.Contains(res.Stderr, "invalid --scope") {
		t.Errorf("stderr missing 'invalid --scope': %q", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "agent") ||
		!strings.Contains(res.Stderr, "deployment") ||
		!strings.Contains(res.Stderr, "fleet") {
		t.Errorf("stderr missing scope enum members: %q", res.Stderr)
	}
}

func TestGoal_InvalidParent_Exit2(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=x", "--parent=not-an-id",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --parent") {
		t.Errorf("stderr missing 'invalid --parent': %q", res.Stderr)
	}
}

func TestGoal_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"goal", "--statement=x",
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})

	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio goal:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}
