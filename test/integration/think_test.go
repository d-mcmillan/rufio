package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

func TestThink_HappyPath_NonDecision_WritesSingleThought(t *testing.T) {
	root := initProject(t)

	res := testutil.RunCLI(t, []string{
		"think",
		"--type=hypothesis",
		"--subject=customer:5821",
		"--content=showing churn signals",
		"--scope=fleet",
		"--topics=churn,p1",
		"--ttl=300",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "outbox", "agent-a", "*.gdl"))
	if len(matches) != 1 {
		t.Fatalf("want 1 .gdl in outbox, got %d (%v)", len(matches), matches)
	}
	bs, _ := os.ReadFile(matches[0])
	content := string(bs)
	for _, expect := range []string{
		"@thought|",
		"author:agent-a",
		"type:hypothesis",
		"content:showing churn signals",
		"scope:fleet",
		"topics:churn,p1",
		"ttl:300",
	} {
		if !strings.Contains(content, expect) {
			t.Errorf("file missing %q.\nFull:\n%s", expect, content)
		}
	}
	// Exactly one record (not a decision; only @thought present).
	if c := strings.Count(content, "@"); c != 1 {
		t.Errorf("expected exactly 1 record line, got %d", c)
	}
}

func TestThink_HappyPath_DefaultTTL_RendersZero(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=focus", "--subject=x:1", "--content=c", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "outbox", "agent-a", "*.gdl"))
	bs, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(bs), "ttl:0") {
		t.Errorf("default-ttl render: want ttl:0, file=%s", bs)
	}
	for _, k := range []string{"topics:", "parent:"} {
		if strings.Contains(string(bs), k) {
			t.Errorf("unexpected %q in file: %s", k, bs)
		}
	}
}

func TestThink_DecisionType_WritesBothRecordsToSameFile(t *testing.T) {
	root := initProject(t)
	// Set up a given/ file with a live ref so the context bundle has
	// content to capture.
	mustPushFile(t, root, "given/policy.md", "policy v1")

	res := testutil.RunCLI(t, []string{
		"think",
		"--type=decision",
		"--subject=order:42",
		"--content=approve refund",
		"--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})

	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}

	matches, _ := filepath.Glob(filepath.Join(root, "live", "outbox", "agent-a", "*.gdl"))
	if len(matches) != 1 {
		t.Fatalf("want 1 .gdl, got %d", len(matches))
	}
	bs, _ := os.ReadFile(matches[0])
	content := string(bs)
	if !strings.Contains(content, "@thought|") {
		t.Error("missing @thought line")
	}
	if !strings.Contains(content, "@context-bundle|") {
		t.Error("missing @context-bundle line for decision type")
	}
	if !strings.Contains(content, "refs:") {
		t.Error("bundle missing refs: field")
	}
	// L2.9 ordering contract: thought must precede context-bundle on disk
	// so the read-side scanner (PR #20 lineage) can rely on positional
	// indexing.
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d:\n%s", len(lines), content)
	}
	if !strings.HasPrefix(lines[0], "@thought|") {
		t.Errorf("line 0 must be @thought, got: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "@context-bundle|") {
		t.Errorf("line 1 must be @context-bundle, got: %q", lines[1])
	}
}

func TestThink_ParentFlag_PersistedToRecord(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=x:1",
		"--content=child thought", "--scope=agent",
		"--parent=1727000000-abc123",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "outbox", "agent-a", "*.gdl"))
	bs, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(bs), "parent:1727000000-abc123") {
		t.Errorf("parent missing: %s", bs)
	}
}

// Helper: push a file via the CLI so the ref system is exercised the
// same way a user would, and the resulting refs are available for the
// decision-type context bundle.
func mustPushFile(t *testing.T, root, contentPath, body string) {
	t.Helper()
	abs := filepath.Join(root, contentPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Decision-type @context-bundle captures latest-LIVE refs;
	// explicit --stage=live now that the bare push default is draft
	// (#123).
	res := testutil.RunCLI(t, []string{"push", contentPath, "--stage=live"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("seed push %s failed: exit=%d stderr=%q", contentPath, res.Code, res.Stderr)
	}
}

// --- Error-path tests ---
//
// Exit code semantics: 1 for runtime/project errors (NoIdentity, NotInProject);
// 2 for validation errors (Invalid*). Single-prefix invariant — every error
// stderr starts with "rufio think: " via HandleError.

func TestThink_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=x:1",
		"--content=c", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})

	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio think:") {
		t.Errorf("stderr missing single-prefix invariant: %q", res.Stderr)
	}
}

func TestThink_EmptyContent_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=x:1",
		"--content=   ", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--content must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestThink_MissingType_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--subject=x:1", "--content=c", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --type") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestThink_InvalidType_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=bogus", "--subject=x:1",
		"--content=c", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --type") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestThink_MissingSubject_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--content=c", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--subject must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestThink_InvalidSubject_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=BAD-FORMAT",
		"--content=c", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --subject") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// TestThink_MissingScope_DefaultsFleet asserts the H3a (#125) contract:
// `rufio think` with no --scope now defaults to fleet (was: required
// flag, exit 2). The unified write-verb rule is "default --scope=fleet;
// pass --scope=agent for private."
func TestThink_MissingScope_DefaultsFleet(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=x:1", "--content=c",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "outbox", "agent-a", "*.gdl"))
	if len(matches) != 1 {
		t.Fatalf("want 1 outbox .gdl, got %d (%v)", len(matches), matches)
	}
	bs, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(bs), "scope:fleet") {
		t.Errorf("default scope should be fleet (H3a); got: %s", bs)
	}
}

func TestThink_InvalidScope_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=x:1",
		"--content=c", "--scope=global",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --scope") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestThink_TTLZero_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=x:1",
		"--content=c", "--scope=agent", "--ttl=0",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --ttl") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestThink_TTLNegative_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=x:1",
		"--content=c", "--scope=agent", "--ttl=-5",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --ttl") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestThink_TTLNonInteger_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=x:1",
		"--content=c", "--scope=agent", "--ttl=abc",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --ttl") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestThink_InvalidParent_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=x:1",
		"--content=c", "--scope=agent", "--parent=bad",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --parent") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestThink_NotInProject_Exit1(t *testing.T) {
	root := t.TempDir() // intentionally NOT a rufio project
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=x:1",
		"--content=c", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// NotInProjectError canonical message is "not inside a Rufio project"
	// (verified for PR #4 attend tests at WK2-ATTEND-deviation note).
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// --- --json / --quiet / single-prefix tests ---

func TestThink_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think",
		"--type=hypothesis",
		"--subject=customer:5821",
		"--content=churn risk",
		"--scope=fleet",
		"--topics=churn,p1",
		"--ttl=300",
		"--parent=1727000000-abc123",
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
	// 13 keys when --parent + --topics + --ttl are all set.
	if got["_type"] != "think" {
		t.Errorf("_type=%v", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v", got["_version"])
	}
	if got["author"] != "agent-a" {
		t.Errorf("author=%v", got["author"])
	}
	if got["type"] != "hypothesis" {
		t.Errorf("type=%v", got["type"])
	}
	if got["subject"] != "customer:5821" {
		t.Errorf("subject=%v", got["subject"])
	}
	if got["content"] != "churn risk" {
		t.Errorf("content=%v", got["content"])
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
	// JSON number → float64 by default.
	if ttl, ok := got["ttl"].(float64); !ok || int(ttl) != 300 {
		t.Errorf("ttl=%v (want 300)", got["ttl"])
	}
	if got["parent"] != "1727000000-abc123" {
		t.Errorf("parent=%v", got["parent"])
	}
	topics, ok := got["topics"].([]interface{})
	if !ok || len(topics) != 2 {
		t.Errorf("topics=%v (want 2-element array)", got["topics"])
	}
	if bundleRefs, ok := got["bundle_refs"].([]interface{}); !ok {
		t.Errorf("bundle_refs not an array: %T %v", got["bundle_refs"], got["bundle_refs"])
	} else if len(bundleRefs) != 0 {
		// hypothesis (not decision) → no refs collected
		t.Errorf("bundle_refs len=%d, want 0 for non-decision", len(bundleRefs))
	}
}

func TestThink_JSONParentNullWhenAbsent(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=focus", "--subject=x:1",
		"--content=c", "--scope=agent", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// parent key MUST be present with nil value when --parent not given —
	// D5.12 contract. JSON consumers should not have to .has-key check.
	parent, present := got["parent"]
	if !present {
		t.Errorf("parent key absent; D5.12 requires it always present with string-or-null value")
	}
	if parent != nil {
		t.Errorf("parent=%v, want nil/null", parent)
	}
}

func TestThink_JSONBundleRefsAlwaysArray(t *testing.T) {
	root := initProject(t)
	// Non-decision: bundle_refs must be [] (empty array, NOT null/absent).
	nonDec := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=x:1",
		"--content=c", "--scope=agent", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if nonDec.Code != 0 {
		t.Fatalf("non-decision exit=%d stderr=%q", nonDec.Code, nonDec.Stderr)
	}
	var nd map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(nonDec.Stdout)), &nd); err != nil {
		t.Fatalf("non-decision invalid JSON: %v", err)
	}
	if ndRefs, ok := nd["bundle_refs"].([]interface{}); !ok {
		t.Errorf("non-decision bundle_refs not an array: %T", nd["bundle_refs"])
	} else if len(ndRefs) != 0 {
		t.Errorf("non-decision bundle_refs len=%d, want 0", len(ndRefs))
	}

	// Decision (with a tracked file): bundle_refs MUST be a populated array.
	mustPushFile(t, root, "given/policy.md", "policy v1")

	dec := testutil.RunCLI(t, []string{
		"think", "--type=decision", "--subject=order:42",
		"--content=approve", "--scope=fleet", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if dec.Code != 0 {
		t.Fatalf("decision exit=%d stderr=%q", dec.Code, dec.Stderr)
	}
	var d map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(dec.Stdout)), &d); err != nil {
		t.Fatalf("decision invalid JSON: %v", err)
	}
	dRefs, ok := d["bundle_refs"].([]interface{})
	if !ok {
		t.Fatalf("decision bundle_refs not an array: %T %v", d["bundle_refs"], d["bundle_refs"])
	}
	if len(dRefs) != 1 {
		t.Errorf("decision bundle_refs len=%d, want 1 (one pushed file)", len(dRefs))
	}
}

func TestThink_QuietSuppressesChatterButNotJSON(t *testing.T) {
	root := initProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "agent-a"}

	// --quiet alone: stdout empty (chatter suppressed); record still on disk.
	q := testutil.RunCLI(t, []string{
		"think", "--type=focus", "--subject=x:1",
		"--content=c", "--scope=agent", "--quiet",
	}, root, env)
	if q.Code != 0 {
		t.Fatalf("--quiet exit=%d stderr=%q", q.Code, q.Stderr)
	}
	if strings.TrimSpace(q.Stdout) != "" {
		t.Errorf("--quiet stdout=%q, want empty", q.Stdout)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "outbox", "agent-a", "*.gdl"))
	if len(matches) != 1 {
		t.Errorf("--quiet did not write the file: got %d matches", len(matches))
	}

	// --json --quiet: JSON wins; stdout is the JSONL line.
	j := testutil.RunCLI(t, []string{
		"think", "--type=focus", "--subject=x:2",
		"--content=c", "--scope=agent", "--json", "--quiet",
	}, root, env)
	if j.Code != 0 {
		t.Fatalf("--json --quiet exit=%d stderr=%q", j.Code, j.Stderr)
	}
	stdout := strings.TrimSpace(j.Stdout)
	if stdout == "" {
		t.Errorf("--json --quiet stdout empty (expected JSONL line — --json wins over --quiet)")
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Errorf("--json --quiet stdout is not valid JSON: %v\nstdout=%q", err, j.Stdout)
	}
}

func TestThink_ConfirmationLine_NotPrefixed(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=focus", "--subject=x:1",
		"--content=poke", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// The single-prefix invariant: only HandleError adds "rufio think: ".
	// Success output must NOT carry the prefix.
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio think:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
	// And the confirmation line must contain the canonical phrase.
	// H3d (#125): echo prefix normalized "thought set:" → "think: ".
	if !strings.Contains(res.Stdout, "think: ") {
		t.Errorf("success stdout missing canonical confirmation: %q", res.Stdout)
	}
}
