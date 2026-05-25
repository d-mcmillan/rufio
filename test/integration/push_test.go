package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// pushFixture creates a workdir, initialises it as a Rufio project, and
// returns the workdir path.
func pushFixture(t *testing.T) string {
	t.Helper()
	workdir := mkProject(t)
	r := testutil.RunCLI(t, []string{"init", "test"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("init failed: %s", r.Stderr)
	}
	return workdir
}

func TestRufioPush_HappyPath(t *testing.T) {
	workdir := pushFixture(t)
	if err := os.MkdirAll(filepath.Join(workdir, "given", "policy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "given", "policy", "refund.md"), []byte("Refund threshold: $500\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Bare `push` defaults to --stage=draft (issue #123); the
	// approve+promote workflow advances to live.
	r := testutil.RunCLI(t, []string{"push", "given/policy/refund.md"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stdout, `v1`)
	mustMatch(t, r.Stdout, `stage:draft`)

	refsFile := filepath.Join(workdir, ".rufio", "refs", "given", "policy", "refund.md.gdl")
	refs, err := os.ReadFile(refsFile)
	if err != nil {
		t.Fatalf("read refs: %v", err)
	}
	mustMatch(t, string(refs), `@ref\|`)
	mustMatch(t, string(refs), `version:1`)
	mustMatch(t, string(refs), `stage:draft`)
	mustMatch(t, string(refs), `sha256:[0-9a-f]{64}`)
}

func TestRufioPush_SecondIncrementsToV2(t *testing.T) {
	workdir := pushFixture(t)
	if err := os.MkdirAll(filepath.Join(workdir, "given"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(workdir, "given", "voice.md")
	_ = os.WriteFile(file, []byte("v1\n"), 0o644)
	_ = testutil.RunCLI(t, []string{"push", "given/voice.md"}, workdir, nil)
	_ = os.WriteFile(file, []byte("v2\n"), 0o644)

	r := testutil.RunCLI(t, []string{"push", "given/voice.md"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stdout, `v2`)

	refs, _ := os.ReadFile(filepath.Join(workdir, ".rufio", "refs", "given", "voice.md.gdl"))
	mustMatch(t, string(refs), `version:1`)
	mustMatch(t, string(refs), `version:2`)
}

func TestRufioPush_StageDraft(t *testing.T) {
	workdir := pushFixture(t)
	_ = os.MkdirAll(filepath.Join(workdir, "given"), 0o755)
	_ = os.WriteFile(filepath.Join(workdir, "given", "draft.md"), []byte("wip\n"), 0o644)

	r := testutil.RunCLI(t, []string{"push", "given/draft.md", "--stage=draft"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stdout, `stage:draft`)
}

func TestRufioPush_RejectsTraversal(t *testing.T) {
	workdir := pushFixture(t)
	r := testutil.RunCLI(t, []string{"push", "../escape.md"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `outside the project root`)
	mustNotMatch(t, r.Stderr, `rufio push: rufio push:`)
}

func TestRufioPush_RejectsRufioPath(t *testing.T) {
	workdir := pushFixture(t)
	r := testutil.RunCLI(t, []string{"push", ".rufio/history/foo"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `reserved`)
}

func TestRufioPush_RejectsInternal(t *testing.T) {
	workdir := pushFixture(t)
	_ = os.MkdirAll(filepath.Join(workdir, "internal"), 0o755)
	_ = os.WriteFile(filepath.Join(workdir, "internal", "private.md"), []byte("secret\n"), 0o644)

	r := testutil.RunCLI(t, []string{"push", "internal/private.md"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `reserved`)
}

func TestRufioPush_RejectsMissingFile(t *testing.T) {
	workdir := pushFixture(t)
	r := testutil.RunCLI(t, []string{"push", "given/nonexistent.md"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(strings.ToLower(r.Stderr), "not found") &&
		!strings.Contains(r.Stderr, "no such file") {
		t.Errorf("expected 'not found' or 'no such file' in stderr; got %q", r.Stderr)
	}
}

func TestRufioPush_JSONShape(t *testing.T) {
	workdir := pushFixture(t)
	_ = os.MkdirAll(filepath.Join(workdir, "given"), 0o755)
	_ = os.WriteFile(filepath.Join(workdir, "given", "x.md"), []byte("x\n"), 0o644)

	r := testutil.RunCLI(t, []string{"push", "given/x.md", "--json"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	lines := nonEmptyLines(r.Stdout)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1; stdout: %q", len(lines), r.Stdout)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatalf("json: %v", err)
	}
	if obj["path"] != "given/x.md" {
		t.Errorf("path: got %v", obj["path"])
	}
	if v, ok := obj["version"].(float64); !ok || int(v) != 1 {
		t.Errorf("version: got %v", obj["version"])
	}
	if obj["stage"] != "draft" {
		t.Errorf("stage: got %v", obj["stage"])
	}
	sha, ok := obj["sha256"].(string)
	if !ok || len(sha) != 64 {
		t.Errorf("sha256: got %v", obj["sha256"])
	}
}

func TestRufioPush_RejectsUnknownFlag(t *testing.T) {
	workdir := pushFixture(t)
	r := testutil.RunCLI(t, []string{"push", "given/x.md", "--bogus-flag"}, workdir, nil)
	if r.Code != 2 {
		t.Errorf("exit code: got %d, want 2; stderr: %s", r.Code, r.Stderr)
	}
	mustNotMatch(t, r.Stderr, `rufio push: rufio push:`)
}

func TestRufioPush_AcceptsArbitraryPath(t *testing.T) {
	// Locks the contract: paths outside given/ but not in reserved trees
	// (.rufio, internal, .git) are pushable. Phase 6 (history) and Phase
	// 8 (rollback) need to round-trip arbitrary paths.
	workdir := pushFixture(t)
	_ = os.MkdirAll(filepath.Join(workdir, "topics"), 0o755)
	_ = os.WriteFile(filepath.Join(workdir, "topics", "x.md"), []byte("outside given/\n"), 0o644)

	r := testutil.RunCLI(t, []string{"push", "topics/x.md"}, workdir, nil)
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	mustMatch(t, r.Stdout, `topics/x\.md@v1`)
	if _, err := os.Stat(filepath.Join(workdir, ".rufio", "refs", "topics", "x.md.gdl")); err != nil {
		t.Errorf("ref file not at expected path: %v", err)
	}
}

func TestRufioPush_RejectsSymlinkEscape(t *testing.T) {
	workdir := pushFixture(t)
	outside, err := os.MkdirTemp("", "rufio-outside-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outside)
	_ = os.WriteFile(filepath.Join(outside, "secret.md"), []byte("outside\n"), 0o644)
	if err := os.Symlink(outside, filepath.Join(workdir, "link")); err != nil {
		t.Fatal(err)
	}

	r := testutil.RunCLI(t, []string{"push", "link/secret.md"}, workdir, nil)
	if r.Code == 0 {
		t.Fatal("expected non-zero exit")
	}
	mustMatch(t, r.Stderr, `outside the project root`)
}

func TestPush_RecordsCurrentAgent_WhenIdentitySet(t *testing.T) {
	root := initProject(t)
	// Write content + push as agent-a
	contentPath := filepath.Join(root, "given", "x.md")
	os.MkdirAll(filepath.Dir(contentPath), 0o755)
	os.WriteFile(contentPath, []byte("hello"), 0o644)

	res := testutil.RunCLI(t, []string{"push", "given/x.md"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("push: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	// Verify the ref records agent-a, not "unknown".
	bs, _ := os.ReadFile(filepath.Join(root, ".rufio", "refs", "given", "x.md.gdl"))
	if !strings.Contains(string(bs), "author:agent-a") {
		t.Errorf("ref author not set to agent-a:\n%s", bs)
	}
	if strings.Contains(string(bs), "author:unknown") {
		t.Errorf("ref still has author:unknown:\n%s", bs)
	}
}

func TestPush_RecordsUnknown_WhenNoIdentity(t *testing.T) {
	root := initProject(t)
	contentPath := filepath.Join(root, "given", "x.md")
	os.MkdirAll(filepath.Dir(contentPath), 0o755)
	os.WriteFile(contentPath, []byte("hello"), 0o644)

	res := testutil.RunCLI(t, []string{"push", "given/x.md"}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 0 {
		t.Fatalf("push: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	bs, _ := os.ReadFile(filepath.Join(root, ".rufio", "refs", "given", "x.md.gdl"))
	if !strings.Contains(string(bs), "author:unknown") {
		t.Errorf("ref author not 'unknown' when identity absent:\n%s", bs)
	}
}
