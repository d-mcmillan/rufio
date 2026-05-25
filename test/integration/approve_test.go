package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// pushDraft helper: write file, push at draft.
func pushDraft(t *testing.T, root, contentPath, body, agent string) {
	t.Helper()
	full := filepath.Join(root, contentPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	res := testutil.RunCLI(t, []string{"push", contentPath, "--stage=draft"}, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("seed push: exit=%d stderr=%q", res.Code, res.Stderr)
	}
}

func TestApprove_HappyPath_AdvancesToStaged(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1 content", "agent-a")

	res := testutil.RunCLI(t, []string{
		"approve", "given/policy.md@v1",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	bs, err := os.ReadFile(filepath.Join(root, ".rufio", "refs", "given", "policy.md.gdl"))
	if err != nil {
		t.Fatalf("read refs: %v", err)
	}
	// Two @ref records now: v1 draft + v2 staged with approved-by.
	if c := strings.Count(string(bs), "@ref|"); c != 2 {
		t.Errorf("expected 2 @ref lines, got %d:\n%s", c, bs)
	}
	if !strings.Contains(string(bs), "stage:staged") {
		t.Errorf("missing stage:staged: %s", bs)
	}
	if !strings.Contains(string(bs), "approved-by:agent-a") {
		t.Errorf("missing approved-by:agent-a: %s", bs)
	}
}

func TestApprove_AsFlag_RecordsExplicitActor(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")

	res := testutil.RunCLI(t, []string{
		"approve", "given/policy.md@v1", "--as=lead-1",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	bs, err := os.ReadFile(filepath.Join(root, ".rufio", "refs", "given", "policy.md.gdl"))
	if err != nil {
		t.Fatalf("read refs: %v", err)
	}
	if !strings.Contains(string(bs), "approved-by:lead-1") {
		t.Errorf("--as override not recorded: %s", bs)
	}
}

func TestApprove_NoSuchVersion_Exit1(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	res := testutil.RunCLI(t, []string{
		"approve", "given/policy.md@v99",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Errorf("exit: got %d, want 1; stderr=%q", res.Code, res.Stderr)
	}
	mustMatch(t, res.Stderr, `no version 'v99'`)
}

func TestApprove_AlreadyStaged_Exit2(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	// Approve once → v2 is staged.
	first := testutil.RunCLI(t, []string{"approve", "given/policy.md@v1"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if first.Code != 0 {
		t.Fatalf("seed approve: %s", first.Stderr)
	}
	// Try to approve v2 (already staged) — InvalidStageTransitionError.
	res := testutil.RunCLI(t, []string{"approve", "given/policy.md@v2"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Errorf("exit: got %d, want 2; stderr=%q", res.Code, res.Stderr)
	}
	mustMatch(t, res.Stderr, `cannot transition`)
}

func TestApprove_InvalidAsActor_Exit2(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	res := testutil.RunCLI(t, []string{
		"approve", "given/policy.md@v1", "--as=BAD_FORMAT",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Errorf("exit: got %d, want 2; stderr=%q", res.Code, res.Stderr)
	}
	mustMatch(t, res.Stderr, `invalid agent id`)
}

func TestApprove_JSON_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	res := testutil.RunCLI(t, []string{
		"approve", "given/policy.md@v1", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := nonEmptyLines(res.Stdout)
	if len(lines) != 1 {
		t.Fatalf("want 1 stdout line, got %d: %q", len(lines), res.Stdout)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	wantKeys := []string{"_type", "_version", "path", "version", "stage", "sha256", "approved_by", "ts"}
	for _, k := range wantKeys {
		if _, ok := obj[k]; !ok {
			t.Errorf("missing key %q in payload: %v", k, obj)
		}
	}
	if obj["_type"] != "approve" {
		t.Errorf("_type: got %v, want approve", obj["_type"])
	}
	if obj["stage"] != "staged" {
		t.Errorf("stage: got %v, want staged", obj["stage"])
	}
	if obj["approved_by"] != "agent-a" {
		t.Errorf("approved_by: got %v, want agent-a", obj["approved_by"])
	}
}

func TestApprove_QuietSuppressesChatterButNotJSON(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	// --quiet alone: no chatter line.
	res := testutil.RunCLI(t, []string{
		"approve", "given/policy.md@v1", "--quiet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "" {
		t.Errorf("--quiet must suppress chatter, got stdout=%q", res.Stdout)
	}

	// Second project: --quiet + --json: JSON wins, line still emitted.
	root2 := initProject(t)
	pushDraft(t, root2, "given/policy.md", "v1", "agent-a")
	res2 := testutil.RunCLI(t, []string{
		"approve", "given/policy.md@v1", "--quiet", "--json",
	}, root2, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res2.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res2.Code, res2.Stderr)
	}
	if len(nonEmptyLines(res2.Stdout)) != 1 {
		t.Errorf("--quiet --json: want 1 stdout line, got %q", res2.Stdout)
	}
}

func TestApprove_ConfirmationLine_NotPrefixed(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	res := testutil.RunCLI(t, []string{
		"approve", "given/policy.md@v1",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// Only HandleError adds the "rufio approve: " prefix. Success output
	// must NOT carry it.
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio approve:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "approved:") {
		t.Errorf("missing canonical confirmation: %q", res.Stdout)
	}
}

func TestApprove_NotInProject_Exit1(t *testing.T) {
	// bare tempdir (no init) → NotInProjectError exit 1.
	workdir := mkProject(t)
	res := testutil.RunCLI(t, []string{
		"approve", "given/policy.md@v1",
	}, workdir, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Errorf("exit: got %d, want 1; stderr=%q", res.Code, res.Stderr)
	}
	mustMatch(t, res.Stderr, `(?i)not.*rufio`)
}
