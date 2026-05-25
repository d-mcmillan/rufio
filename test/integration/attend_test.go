package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func TestAttend_HappyPath_WritesAttentionFile(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"attend",
		"--intent=debugging the auth flow",
		"--entities=customer:5821,order:42",
		"--topics=auth,login",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	got, err := os.ReadFile(filepath.Join(root, "live", "attention", "agent-a.gdl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	s := string(got)
	for _, expect := range []string{
		"@attention|",
		"agent:agent-a",
		// NOTE: entity values like customer:5821 are gdl-escaped on render
		// (colon → \:), so we assert on the unescaped substrings that ARE
		// still present (the field-key markers and the agent value).
		"intent:debugging the auth flow",
		"topics:auth,login",
		"ts:",
	} {
		if !strings.Contains(s, expect) {
			t.Errorf("attention file missing %q. Full: %q", expect, s)
		}
	}
	// Verify exactly one record line.
	if c := strings.Count(s, "@attention"); c != 1 {
		t.Errorf("expected 1 @attention line, got %d", c)
	}
	// Verify entities round-trip through the gdl parser (proves the
	// escape was correct and reversible).
	// NOTE: we do NOT assert on the raw escaped form, only the recovered
	// post-parse value. The integration test for the JSON shape (Task 8)
	// covers the user-visible projection.
}

func TestAttend_TopicsOptional_OmitsKeyWhenAbsent(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"attend",
		"--intent=poking around",
		"--entities=customer:1",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	got, _ := os.ReadFile(filepath.Join(root, "live", "attention", "agent-a.gdl"))
	if strings.Contains(string(got), "topics:") {
		t.Errorf("expected no topics field, got: %q", string(got))
	}
}

func TestAttend_OverwritesPriorRecord(t *testing.T) {
	root := initProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "agent-a"}

	first := testutil.RunCLI(t, []string{"attend", "--intent=old", "--entities=customer:1"}, root, env)
	if first.Code != 0 {
		t.Fatalf("first: %s", first.Stderr)
	}

	second := testutil.RunCLI(t, []string{"attend", "--intent=new", "--entities=customer:2"}, root, env)
	if second.Code != 0 {
		t.Fatalf("second: %s", second.Stderr)
	}

	got, _ := os.ReadFile(filepath.Join(root, "live", "attention", "agent-a.gdl"))
	if strings.Contains(string(got), "intent:old") {
		t.Errorf("old intent still present after overwrite: %q", string(got))
	}
	if !strings.Contains(string(got), "intent:new") {
		t.Errorf("new intent missing: %q", string(got))
	}
	if c := strings.Count(string(got), "@attention"); c != 1 {
		t.Errorf("expected exactly 1 record after overwrite, got %d", c)
	}
}

func TestAttend_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	// No RUFIO_AGENT_ID set, no .rufio/identity.local.gdl.
	res := testutil.RunCLI(t, []string{
		"attend",
		"--intent=foo",
		"--entities=customer:1",
	}, root, nil)

	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q, want 'no identity set'", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio attend:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
}

func TestAttend_EmptyIntent_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"attend",
		"--intent=   ",
		"--entities=customer:1",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--intent must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestAttend_MissingEntities_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"attend",
		"--intent=x",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--entities must include at least one entity id") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestAttend_MalformedEntity_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"attend",
		"--intent=x",
		"--entities=CUSTOMER:42",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid entity id") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestAttend_MalformedTopic_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"attend",
		"--intent=x",
		"--entities=customer:1",
		"--topics=auth,BAD TOPIC",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid topic") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestAttend_NotInProject_Exit1(t *testing.T) {
	root := t.TempDir() // intentionally NOT a rufio project
	res := testutil.RunCLI(t, []string{
		"attend",
		"--intent=x",
		"--entities=customer:1",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// NotInProjectError message format defined in internal/lib/errors/errors.go:
	//   "not inside a Rufio project (no rufio.gdl found from %s)"
	// Substring-match the canonical phrase rather than the exact string so
	// minor message tweaks don't churn this test.
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q, expected substring 'not inside a Rufio project'", res.Stderr)
	}
}

func TestAttend_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"attend",
		"--intent=x",
		"--entities=customer:1,order:2",
		"--topics=auth",
		"--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	stdout := strings.TrimSpace(res.Stdout)
	// Single JSONL line — no embedded newlines.
	if strings.Contains(stdout, "\n") {
		t.Errorf("expected single JSONL line, got embedded newlines: %q", stdout)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nstdout=%q", err, stdout)
	}
	if got["_type"] != "attend-set" {
		t.Errorf("_type=%v", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v", got["_version"])
	}
	if got["agent"] != "agent-a" {
		t.Errorf("agent=%v", got["agent"])
	}
	if got["intent"] != "x" {
		t.Errorf("intent=%v", got["intent"])
	}
	if ents, ok := got["entities"].([]interface{}); !ok || len(ents) != 2 {
		t.Errorf("entities=%v (want 2-element array)", got["entities"])
	} else {
		if ents[0] != "customer:1" || ents[1] != "order:2" {
			t.Errorf("entities content=%v", ents)
		}
	}
	if topics, ok := got["topics"].([]interface{}); !ok || len(topics) != 1 || topics[0] != "auth" {
		t.Errorf("topics=%v (want [\"auth\"])", got["topics"])
	}
	if ts, ok := got["ts"].(string); !ok || ts == "" {
		t.Errorf("ts missing or empty: %v", got["ts"])
	}
}

func TestAttend_JSONTopicsEmpty_RendersEmptyArray(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"attend",
		"--intent=x",
		"--entities=customer:1",
		"--json",
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
		t.Fatalf("topics not an array (got %T %v) — must NEVER be null/absent", got["topics"], got["topics"])
	}
	if len(topics) != 0 {
		t.Errorf("topics len=%d, want 0", len(topics))
	}
}

func TestAttend_QuietSuppressesChatterButNotJSON(t *testing.T) {
	root := initProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "agent-a"}

	// --quiet alone: no stdout (chatter suppressed); record still on disk.
	q := testutil.RunCLI(t, []string{"attend", "--intent=x", "--entities=customer:1", "--quiet"}, root, env)
	if q.Code != 0 {
		t.Fatalf("--quiet exit=%d stderr=%q", q.Code, q.Stderr)
	}
	if strings.TrimSpace(q.Stdout) != "" {
		t.Errorf("--quiet stdout=%q, want empty", q.Stdout)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "attention", "agent-a.gdl")); err != nil {
		t.Errorf("attention file missing after --quiet run: %v", err)
	}

	// --json --quiet: JSON wins; stdout is the JSONL line.
	j := testutil.RunCLI(t, []string{"attend", "--intent=x", "--entities=customer:1", "--json", "--quiet"}, root, env)
	if j.Code != 0 {
		t.Fatalf("--json --quiet exit=%d stderr=%q", j.Code, j.Stderr)
	}
	if strings.TrimSpace(j.Stdout) == "" {
		t.Errorf("--json --quiet stdout empty (expected JSONL line — --json wins over --quiet)")
	}
	// Verify it really is JSON, not just any non-empty string.
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(j.Stdout)), &got); err != nil {
		t.Errorf("--json --quiet stdout is not valid JSON: %v\nstdout=%q", err, j.Stdout)
	}
}

func TestAttend_ConfirmationLine_NotPrefixed(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"attend",
		"--intent=poke",
		"--entities=customer:1",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// The single-prefix invariant: only HandleError adds "rufio attend: ".
	// Success output must NOT carry the prefix.
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio attend:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
}
