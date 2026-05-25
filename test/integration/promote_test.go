package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func TestPromote_HappyPath_FromStagedToLive(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	// Approve first.
	ares := testutil.RunCLI(t, []string{"approve", "given/policy.md@v1"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if ares.Code != 0 {
		t.Fatalf("seed approve: stderr=%q", ares.Stderr)
	}
	// Promote v2 (staged) to live.
	res := testutil.RunCLI(t, []string{"promote", "given/policy.md@v2"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	bs, err := os.ReadFile(filepath.Join(root, ".rufio", "refs", "given", "policy.md.gdl"))
	if err != nil {
		t.Fatalf("read refs: %v", err)
	}
	// 3 refs: v1 draft, v2 staged, v3 live
	if c := strings.Count(string(bs), "@ref|"); c != 3 {
		t.Errorf("expected 3 @ref lines, got %d:\n%s", c, bs)
	}
	if !strings.Contains(string(bs), "stage:live") {
		t.Error("missing stage:live")
	}
	if !strings.Contains(string(bs), "promoted-from:staged") {
		t.Errorf("missing promoted-from:staged:\n%s", bs)
	}
}

func TestPromote_FromDraft_DirectToLive(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	res := testutil.RunCLI(t, []string{"promote", "given/policy.md@v1"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	bs, err := os.ReadFile(filepath.Join(root, ".rufio", "refs", "given", "policy.md.gdl"))
	if err != nil {
		t.Fatalf("read refs: %v", err)
	}
	if !strings.Contains(string(bs), "promoted-from:draft") {
		t.Errorf("missing promoted-from:draft:\n%s", bs)
	}
}

func TestPromote_NoSuchVersion_Exit1(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	res := testutil.RunCLI(t, []string{
		"promote", "given/policy.md@v99",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Errorf("exit: got %d, want 1; stderr=%q", res.Code, res.Stderr)
	}
	mustMatch(t, res.Stderr, `no version 'v99'`)
}

func TestPromote_InvalidTo_Exit2(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	res := testutil.RunCLI(t, []string{
		"promote", "given/policy.md@v1", "--to=bogus",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Errorf("exit: got %d, want 2; stderr=%q", res.Code, res.Stderr)
	}
	mustMatch(t, res.Stderr, `invalid --to`)
}

func TestPromote_AlreadyLive_Exit2(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	// Promote v1 → v2 live.
	first := testutil.RunCLI(t, []string{"promote", "given/policy.md@v1"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if first.Code != 0 {
		t.Fatalf("seed promote: %s", first.Stderr)
	}
	// Try to promote v2 (already live) — InvalidStageTransitionError.
	res := testutil.RunCLI(t, []string{"promote", "given/policy.md@v2"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Errorf("exit: got %d, want 2; stderr=%q", res.Code, res.Stderr)
	}
	mustMatch(t, res.Stderr, `cannot transition`)
}

func TestPromote_JSON_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	res := testutil.RunCLI(t, []string{
		"promote", "given/policy.md@v1", "--json",
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
	wantKeys := []string{"_type", "_version", "path", "version", "stage", "sha256", "promoted_from", "ts"}
	for _, k := range wantKeys {
		if _, ok := obj[k]; !ok {
			t.Errorf("missing key %q in payload: %v", k, obj)
		}
	}
	if obj["_type"] != "promote" {
		t.Errorf("_type: got %v, want promote", obj["_type"])
	}
	if obj["stage"] != "live" {
		t.Errorf("stage: got %v, want live", obj["stage"])
	}
	if obj["promoted_from"] != "draft" {
		t.Errorf("promoted_from: got %v, want draft", obj["promoted_from"])
	}
}

func TestPromote_QuietSuppressesChatterButNotJSON(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	res := testutil.RunCLI(t, []string{
		"promote", "given/policy.md@v1", "--quiet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "" {
		t.Errorf("--quiet must suppress chatter, got stdout=%q", res.Stdout)
	}

	root2 := initProject(t)
	pushDraft(t, root2, "given/policy.md", "v1", "agent-a")
	res2 := testutil.RunCLI(t, []string{
		"promote", "given/policy.md@v1", "--quiet", "--json",
	}, root2, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res2.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res2.Code, res2.Stderr)
	}
	if len(nonEmptyLines(res2.Stdout)) != 1 {
		t.Errorf("--quiet --json: want 1 stdout line, got %q", res2.Stdout)
	}
}

func TestPromote_ConfirmationLine_NotPrefixed(t *testing.T) {
	root := initProject(t)
	pushDraft(t, root, "given/policy.md", "v1", "agent-a")
	res := testutil.RunCLI(t, []string{
		"promote", "given/policy.md@v1",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio promote:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "promoted:") {
		t.Errorf("missing canonical confirmation: %q", res.Stdout)
	}
}
