package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func TestReason_HappyPath_NoDecision_WritesAtTopLevel(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"reason", "--content=because the user said so",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "agent-a", "*.gdl"))
	if len(matches) != 1 {
		t.Fatalf("want 1 .gdl in live/reasoning/agent-a/, got %d", len(matches))
	}
	bs, _ := os.ReadFile(matches[0])
	for _, want := range []string{"@reason|", "author:agent-a", "content:because the user said so"} {
		if !strings.Contains(string(bs), want) {
			t.Errorf("file missing %q.\n%s", want, bs)
		}
	}
}

func TestReason_HappyPath_WithDecision_NestsUnderDecisionDir(t *testing.T) {
	root := initProject(t)
	// --decision must name a real type:decision thought (GH #77); seed
	// one through the real CLI rather than a synthetic id.
	id := mustWriteDecision(t, root, "agent-a", "customer:1", "approve", "fleet")
	res := testutil.RunCLI(t, []string{
		"reason", "--content=step within decision",
		"--decision=" + id,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "agent-a", id, "*.gdl"))
	if len(matches) != 1 {
		t.Fatalf("want 1 .gdl in nested decision dir, got %d (%v)", len(matches), matches)
	}
}

func TestReason_WithParent_PersistedToRecord(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"reason", "--content=child step", "--parent=1727000000-par456",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "agent-a", "*.gdl"))
	bs, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(bs), "parent:1727000000-par456") {
		t.Errorf("parent missing: %s", bs)
	}
}

func TestReason_WithTopicsAndAllFlags(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:1", "approve", "fleet")
	res := testutil.RunCLI(t, []string{
		"reason", "--content=full step",
		"--topics=audit,p1", "--parent=1727000000-par456", "--decision=" + id,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "agent-a", id, "*.gdl"))
	bs, _ := os.ReadFile(matches[0])
	for _, want := range []string{"topics:audit,p1", "parent:1727000000-par456", "decision:" + id} {
		if !strings.Contains(string(bs), want) {
			t.Errorf("missing %q: %s", want, bs)
		}
	}
}

func TestReason_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"reason", "--content=x",
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio reason:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

func TestReason_MissingContent_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"reason"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--content must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestReason_EmptyContent_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"reason", "--content=   "}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--content must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestReason_InvalidParent_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"reason", "--content=x", "--parent=BAD"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --parent") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestReason_InvalidDecision_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"reason", "--content=x", "--decision=BAD"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --decision") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// TestReason_DecisionTarget_RealDecision_HappyPath is the regression
// guard for the unchanged happy path: --decision pointing at a real
// type:decision thought succeeds and nests under that decision dir.
func TestReason_DecisionTarget_RealDecision_HappyPath(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")
	res := testutil.RunCLI(t, []string{
		"reason", "--content=because policy allows it", "--decision=" + id,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "agent-a", id, "*.gdl"))
	if len(matches) != 1 {
		t.Fatalf("want 1 .gdl under decision dir, got %d (%v)", len(matches), matches)
	}
}

// TestReason_DecisionTarget_Hypothesis_Exit1_NoOrphan is the core bug
// repro (GH #77): --decision pointing at a type:hypothesis thought must
// error with the canonical NotADecisionError wording AND write no reason
// record (no permanently-unviewable orphan).
func TestReason_DecisionTarget_Hypothesis_Exit1_NoOrphan(t *testing.T) {
	root := initProject(t)
	id := mustWriteHypothesis(t, root, "agent-a", "customer:5821", "still thinking", "fleet")
	res := testutil.RunCLI(t, []string{
		"reason", "--content=should not be written", "--decision=" + id,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d want 1, stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stderr, "not 'decision'") {
		t.Errorf("stderr missing \"not 'decision'\":\n%s", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio reason:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
	// No orphan: nothing written under the (would-be) decision dir, and
	// no stray top-level reason file either.
	if matches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "agent-a", id, "*.gdl")); len(matches) != 0 {
		t.Errorf("orphan reason written under rejected decision dir: %v", matches)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "agent-a", "*.gdl")); len(matches) != 0 {
		t.Errorf("stray reason written at top level: %v", matches)
	}
}

// TestReason_DecisionTarget_Nonexistent_Exit1_NoRecord covers a
// well-formed but unknown decision-id: NoSuchThoughtError, exit 1, no
// record written.
func TestReason_DecisionTarget_Nonexistent_Exit1_NoRecord(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"reason", "--content=x", "--decision=9999999999-zzzzzz",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d want 1, stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such record") {
		t.Errorf("stderr missing 'no such record':\n%s", res.Stderr)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "agent-a", "9999999999-zzzzzz", "*.gdl")); len(matches) != 0 {
		t.Errorf("record written for nonexistent decision: %v", matches)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "agent-a", "*.gdl")); len(matches) != 0 {
		t.Errorf("stray reason written at top level: %v", matches)
	}
}

func TestReason_NotInProject_Exit1(t *testing.T) {
	root := t.TempDir()
	res := testutil.RunCLI(t, []string{"reason", "--content=x"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestReason_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	decID := mustWriteDecision(t, root, "agent-a", "customer:1", "approve", "fleet")
	res := testutil.RunCLI(t, []string{
		"reason", "--content=step", "--topics=t1",
		"--parent=1727000000-par456", "--decision=" + decID, "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	stdout := strings.TrimSpace(res.Stdout)
	if strings.Contains(stdout, "\n") {
		t.Errorf("expected single JSONL line: %q", stdout)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, stdout)
	}
	if got["_type"] != "reason" {
		t.Errorf("_type=%v", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v", got["_version"])
	}
	if got["author"] != "agent-a" {
		t.Errorf("author=%v", got["author"])
	}
	if got["content"] != "step" {
		t.Errorf("content=%v", got["content"])
	}
	if got["parent"] != "1727000000-par456" {
		t.Errorf("parent=%v", got["parent"])
	}
	if got["decision"] != decID {
		t.Errorf("decision=%v", got["decision"])
	}
	if id, ok := got["id"].(string); !ok || id == "" {
		t.Errorf("id missing/empty: %v", got["id"])
	}
	if ts, ok := got["ts"].(string); !ok || ts == "" {
		t.Errorf("ts missing/empty: %v", got["ts"])
	}
	topics, ok := got["topics"].([]interface{})
	if !ok || len(topics) != 1 {
		t.Errorf("topics=%v (want 1-element array)", got["topics"])
	}
}

func TestReason_JSONNullablesWhenAbsent(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"reason", "--content=x", "--json"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parent, present := got["parent"]; !present || parent != nil {
		t.Errorf("parent: want present+null; got present=%v val=%v", present, parent)
	}
	if decision, present := got["decision"]; !present || decision != nil {
		t.Errorf("decision: want present+null; got present=%v val=%v", present, decision)
	}
	topics, ok := got["topics"].([]interface{})
	if !ok || len(topics) != 0 {
		t.Errorf("topics: want empty array, got %v", got["topics"])
	}
}

func TestReason_QuietSuppressesChatterButNotJSON(t *testing.T) {
	root := initProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "agent-a"}

	q := testutil.RunCLI(t, []string{"reason", "--content=q", "--quiet"}, root, env)
	if q.Code != 0 {
		t.Fatalf("--quiet exit=%d stderr=%q", q.Code, q.Stderr)
	}
	if strings.TrimSpace(q.Stdout) != "" {
		t.Errorf("--quiet stdout=%q, want empty", q.Stdout)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "agent-a", "*.gdl"))
	if len(matches) != 1 {
		t.Errorf("--quiet did not write file: got %d matches", len(matches))
	}

	j := testutil.RunCLI(t, []string{"reason", "--content=jq", "--json", "--quiet"}, root, env)
	if j.Code != 0 {
		t.Fatalf("--json --quiet exit=%d stderr=%q", j.Code, j.Stderr)
	}
	stdout := strings.TrimSpace(j.Stdout)
	if stdout == "" {
		t.Errorf("--json --quiet stdout empty (expected JSONL line)")
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Errorf("--json --quiet not valid JSON: %v\n%q", err, j.Stdout)
	}
}

func TestReason_ConfirmationLine_NotPrefixed(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"reason", "--content=poke"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio reason:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
	// H3d (#125): echo prefix normalized "reason set:" → "reason: ".
	if !strings.Contains(res.Stdout, "reason: ") {
		t.Errorf("missing canonical confirmation: %q", res.Stdout)
	}
}
