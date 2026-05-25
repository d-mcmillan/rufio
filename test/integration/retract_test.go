package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// mustWriteThought seeds a thought via the real `rufio think` command,
// returning the generated thought-id. Tolerates repeated calls in the
// same outbox by diffing the file set before and after.
func mustWriteThought(t *testing.T, root, agent, content string) string {
	t.Helper()
	pattern := filepath.Join(root, "live", "outbox", agent, "*.gdl")
	before, _ := filepath.Glob(pattern)
	beforeSet := make(map[string]bool, len(before))
	for _, p := range before {
		beforeSet[p] = true
	}
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=customer:1",
		"--content=" + content, "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("seed think failed: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	after, _ := filepath.Glob(pattern)
	var fresh []string
	for _, p := range after {
		if !beforeSet[p] {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) != 1 {
		t.Fatalf("seed think did not produce exactly one new file: got %d", len(fresh))
	}
	return strings.TrimSuffix(filepath.Base(fresh[0]), ".gdl")
}

func TestRetract_HappyPath_WritesRetractedFile(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "to be retracted")

	res := testutil.RunCLI(t, []string{
		"retract", id, "--reason=outdated",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	target := filepath.Join(root, "live", "retracted", id+".gdl")
	bs, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("retracted file missing: %v", err)
	}
	for _, want := range []string{"@retract|", "target:" + id, "reason:outdated", "by:agent-a"} {
		if !strings.Contains(string(bs), want) {
			t.Errorf("file missing %q.\n%s", want, bs)
		}
	}
}

func TestRetract_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "x")
	res := testutil.RunCLI(t, []string{
		"retract", id, "--reason=x", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, res.Stdout)
	}
	if got["_type"] != "retract" {
		t.Errorf("_type=%v", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v", got["_version"])
	}
	if got["target"] != id {
		t.Errorf("target=%v", got["target"])
	}
	if got["reason"] != "x" {
		t.Errorf("reason=%v", got["reason"])
	}
	if got["by"] != "agent-a" {
		t.Errorf("by=%v", got["by"])
	}
}

// --- Error-path tests ---

func TestRetract_MissingReason_Exit2(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "x")
	res := testutil.RunCLI(t, []string{"retract", id}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--reason must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestRetract_EmptyReason_Exit2(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "x")
	res := testutil.RunCLI(t, []string{"retract", id, "--reason=   "}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--reason must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestRetract_NoSuchThought_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"retract", "1727000000-fake12", "--reason=x",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such record") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestRetract_ForeignThought_Exit1(t *testing.T) {
	root := initProject(t)
	// agent-a writes a thought.
	id := mustWriteThought(t, root, "agent-a", "x")
	// agent-b tries to retract it.
	res := testutil.RunCLI(t, []string{
		"retract", id, "--reason=nope",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "cannot retract") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "agent-a") {
		t.Errorf("stderr=%q (expected author 'agent-a' mention)", res.Stderr)
	}
}

func TestRetract_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "x")
	res := testutil.RunCLI(t, []string{
		"retract", id, "--reason=x",
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio retract:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

func TestRetract_NotInProject_Exit1(t *testing.T) {
	root := t.TempDir()
	res := testutil.RunCLI(t, []string{
		"retract", "1727000000-fake12", "--reason=x",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestRetract_QuietSuppressesChatterButNotJSON(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "x")
	env := map[string]string{"RUFIO_AGENT_ID": "agent-a"}

	q := testutil.RunCLI(t, []string{"retract", id, "--reason=r", "--quiet"}, root, env)
	if q.Code != 0 {
		t.Fatalf("--quiet exit=%d stderr=%q", q.Code, q.Stderr)
	}
	if strings.TrimSpace(q.Stdout) != "" {
		t.Errorf("--quiet stdout=%q, want empty", q.Stdout)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "retracted", id+".gdl")); err != nil {
		t.Errorf("--quiet did not write file: %v", err)
	}

	// --json --quiet on a SECOND thought (the first is now retracted; retracting
	// it again would still succeed since retract is idempotent at the file level,
	// but for clarity use a fresh id).
	id2 := mustWriteThought(t, root, "agent-a", "y")
	j := testutil.RunCLI(t, []string{"retract", id2, "--reason=r", "--json", "--quiet"}, root, env)
	if j.Code != 0 {
		t.Fatalf("--json --quiet exit=%d stderr=%q", j.Code, j.Stderr)
	}
	stdout := strings.TrimSpace(j.Stdout)
	if stdout == "" {
		t.Errorf("--json --quiet stdout empty")
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Errorf("--json --quiet not valid JSON: %v\n%q", err, j.Stdout)
	}
}

func TestRetract_ConfirmationLine_NotPrefixed(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "x")
	res := testutil.RunCLI(t, []string{"retract", id, "--reason=r"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio retract:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
	// H3d (#125): echo prefix normalized "retracted:" → "retract: ".
	if !strings.Contains(res.Stdout, "retract: ") {
		t.Errorf("missing canonical confirmation: %q", res.Stdout)
	}
}
