package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func TestObserve_HappyPath_WritesNestedObservationFile(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"observe",
		"--subject=customer:5821",
		"--predicate=has-status",
		"--object=active",
		"--scope=fleet",
		"--topics=crm,p1",
		"--confidence=0.9",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "learned", "customer", "5821", "*.gdlm"))
	if len(matches) != 1 {
		t.Fatalf("want 1 .gdlm in learned/customer/5821/, got %d (%v)", len(matches), matches)
	}
	bs, _ := os.ReadFile(matches[0])
	content := string(bs)
	for _, expect := range []string{
		"@observation|",
		"author:agent-a",
		"predicate:has-status",
		"object:active",
		"scope:fleet",
		"topics:crm,p1",
		"confidence:0.9",
	} {
		if !strings.Contains(content, expect) {
			t.Errorf("file missing %q.\nFull:\n%s", expect, content)
		}
	}
}

func TestObserve_DefaultConfidence_RendersOne(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=y", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "learned", "x", "1", "*.gdlm"))
	bs, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(bs), "confidence:1") {
		t.Errorf("default confidence: want confidence:1, file=%s", bs)
	}
	if strings.Contains(string(bs), "topics:") {
		t.Errorf("unexpected topics: in file: %s", bs)
	}
}

func TestObserve_MultiSegmentSubject_NestsDirectories(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=agent:foo:bar", "--predicate=is",
		"--object=baz", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "learned", "agent", "foo", "bar", "*.gdlm"))
	if len(matches) != 1 {
		t.Fatalf("want 1 .gdlm in learned/agent/foo/bar/, got %d (%v)", len(matches), matches)
	}
}

// --- Error-path tests ---
//
// Exit code semantics: 1 for runtime/project errors (NoIdentity, NotInProject);
// 2 for validation errors (Invalid*).

func TestObserve_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=y", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio observe:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
}

func TestObserve_MissingSubject_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--predicate=is", "--object=y", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--subject must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestObserve_InvalidSubject_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=BAD-FORMAT", "--predicate=is", "--object=y", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --subject") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestObserve_MissingPredicate_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--object=y", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--predicate must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestObserve_InvalidPredicate_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=BAD-CASE", "--object=y", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --predicate") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestObserve_MissingObject_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--object must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// TestObserve_MissingScope_DefaultsFleet — symmetric to
// TestThink_MissingScope_DefaultsFleet. H3a (#125) unified default.
func TestObserve_MissingScope_DefaultsFleet(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=y",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "learned", "x", "1", "*.gdlm"))
	if len(matches) != 1 {
		t.Fatalf("want 1 learned .gdlm, got %d (%v)", len(matches), matches)
	}
	bs, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(bs), "scope:fleet") {
		t.Errorf("default scope should be fleet (H3a); got: %s", bs)
	}
}

func TestObserve_InvalidScope_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=y", "--scope=global",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --scope") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestObserve_ConfidenceOutOfRange_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=y", "--scope=agent", "--confidence=1.5",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --confidence") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestObserve_ConfidenceNegative_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=y", "--scope=agent", "--confidence=-0.1",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --confidence") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestObserve_ConfidenceMalformed_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=y", "--scope=agent", "--confidence=abc",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --confidence") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestObserve_NotInProject_Exit1(t *testing.T) {
	root := t.TempDir()
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=y", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// --- --json / --quiet / single-prefix tests ---

func TestObserve_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe",
		"--subject=customer:5821",
		"--predicate=has-status",
		"--object=active",
		"--scope=fleet",
		"--topics=crm,p1",
		"--confidence=0.9",
		"--json",
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
	if got["_type"] != "observe" {
		t.Errorf("_type=%v", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v", got["_version"])
	}
	if got["author"] != "agent-a" {
		t.Errorf("author=%v", got["author"])
	}
	if got["subject"] != "customer:5821" {
		t.Errorf("subject=%v", got["subject"])
	}
	if got["predicate"] != "has-status" {
		t.Errorf("predicate=%v", got["predicate"])
	}
	if got["object"] != "active" {
		t.Errorf("object=%v", got["object"])
	}
	if got["scope"] != "fleet" {
		t.Errorf("scope=%v", got["scope"])
	}
	if id, ok := got["id"].(string); !ok || id == "" {
		t.Errorf("id missing/empty: %v", got["id"])
	}
	if ts, ok := got["ts"].(string); !ok || ts == "" {
		t.Errorf("ts missing/empty: %v", got["ts"])
	}
	if conf, ok := got["confidence"].(float64); !ok || conf != 0.9 {
		t.Errorf("confidence=%v (want 0.9)", got["confidence"])
	}
	topics, ok := got["topics"].([]interface{})
	if !ok || len(topics) != 2 {
		t.Errorf("topics=%v (want 2-element array)", got["topics"])
	}
}

func TestObserve_JSONTopicsEmpty_RendersEmptyArray(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=y", "--scope=agent", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	topics, ok := got["topics"].([]interface{})
	if !ok {
		t.Fatalf("topics not an array: %T %v", got["topics"], got["topics"])
	}
	if len(topics) != 0 {
		t.Errorf("topics len=%d, want 0", len(topics))
	}
}

func TestObserve_JSONConfidenceDefault_RendersOne(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=y", "--scope=agent", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if conf, ok := got["confidence"].(float64); !ok || conf != 1.0 {
		t.Errorf("default confidence in JSON=%v, want 1.0", got["confidence"])
	}
}

func TestObserve_QuietSuppressesChatterButNotJSON(t *testing.T) {
	root := initProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "agent-a"}

	q := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=y", "--scope=agent", "--quiet",
	}, root, env)
	if q.Code != 0 {
		t.Fatalf("--quiet exit=%d stderr=%q", q.Code, q.Stderr)
	}
	if strings.TrimSpace(q.Stdout) != "" {
		t.Errorf("--quiet stdout=%q, want empty", q.Stdout)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "learned", "x", "1", "*.gdlm"))
	if len(matches) != 1 {
		t.Errorf("--quiet did not write the file: got %d matches", len(matches))
	}

	j := testutil.RunCLI(t, []string{
		"observe", "--subject=x:2", "--predicate=is", "--object=y", "--scope=agent", "--json", "--quiet",
	}, root, env)
	if j.Code != 0 {
		t.Fatalf("--json --quiet exit=%d stderr=%q", j.Code, j.Stderr)
	}
	stdout := strings.TrimSpace(j.Stdout)
	if stdout == "" {
		t.Errorf("--json --quiet stdout empty (expected JSONL line)")
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Errorf("--json --quiet stdout not valid JSON: %v\nstdout=%q", err, j.Stdout)
	}
}

func TestObserve_ConfirmationLine_NotPrefixed(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=x:1", "--predicate=is", "--object=poke", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio observe:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
	// H3d (#125): echo prefix normalized "observation set:" → "observe: ".
	if !strings.Contains(res.Stdout, "observe: ") {
		t.Errorf("missing canonical confirmation: %q", res.Stdout)
	}
}
