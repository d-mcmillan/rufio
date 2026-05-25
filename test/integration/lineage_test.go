package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// Integration tests for `rufio lineage <decision-id>`. Exit-code semantics:
//   - 0 on success (decision found + rendered)
//   - 1 on NoSuchDecisionError, NotADecisionError, NotInProjectError
//
// The command is read-only. All seeding goes through the real CLI to keep
// the data shape honest end-to-end.

// mustWriteDecision runs `rufio think --type=decision` and returns the
// generated decision-id by diffing live/outbox/<author>/ before vs after.
// We can't parse the id from stdout because the confirmation line shape
// is not load-bearing — globbing the new file is the contract.
func mustWriteDecision(t *testing.T, root, author, subject, content, scope string) string {
	t.Helper()
	pattern := filepath.Join(root, "live", "outbox", author, "*.gdl")
	before, _ := filepath.Glob(pattern)
	beforeSet := make(map[string]bool, len(before))
	for _, p := range before {
		beforeSet[p] = true
	}
	res := testutil.RunCLI(t, []string{
		"think", "--type=decision", "--subject=" + subject,
		"--content=" + content, "--scope=" + scope,
	}, root, map[string]string{"RUFIO_AGENT_ID": author})
	if res.Code != 0 {
		t.Fatalf("seed decision: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	after, _ := filepath.Glob(pattern)
	for _, p := range after {
		if !beforeSet[p] {
			return strings.TrimSuffix(filepath.Base(p), ".gdl")
		}
	}
	t.Fatalf("seed decision did not produce a new file")
	return ""
}

// mustWriteHypothesis seeds a `--type=hypothesis` thought (used by the
// NotADecision tripwire test) and returns the new thought-id.
func mustWriteHypothesis(t *testing.T, root, author, subject, content, scope string) string {
	t.Helper()
	pattern := filepath.Join(root, "live", "outbox", author, "*.gdl")
	before, _ := filepath.Glob(pattern)
	beforeSet := make(map[string]bool, len(before))
	for _, p := range before {
		beforeSet[p] = true
	}
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=" + subject,
		"--content=" + content, "--scope=" + scope,
	}, root, map[string]string{"RUFIO_AGENT_ID": author})
	if res.Code != 0 {
		t.Fatalf("seed hypothesis: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	after, _ := filepath.Glob(pattern)
	for _, p := range after {
		if !beforeSet[p] {
			return strings.TrimSuffix(filepath.Base(p), ".gdl")
		}
	}
	t.Fatalf("seed hypothesis did not produce a new file")
	return ""
}

// mustWriteReason runs `rufio reason --decision=<decisionID> --content=...`
// and returns the new reason-id by diffing
// live/reasoning/<author>/<decisionID>/ before vs after. parentReasonID
// is optional ("" for a root step).
func mustWriteReason(t *testing.T, root, author, decisionID, content, parentReasonID string) string {
	t.Helper()
	args := []string{"reason", "--decision=" + decisionID, "--content=" + content}
	if parentReasonID != "" {
		args = append(args, "--parent="+parentReasonID)
	}
	pattern := filepath.Join(root, "live", "reasoning", author, decisionID, "*.gdl")
	before, _ := filepath.Glob(pattern)
	beforeSet := make(map[string]bool, len(before))
	for _, p := range before {
		beforeSet[p] = true
	}
	res := testutil.RunCLI(t, args, root, map[string]string{"RUFIO_AGENT_ID": author})
	if res.Code != 0 {
		t.Fatalf("seed reason: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	after, _ := filepath.Glob(pattern)
	for _, p := range after {
		if !beforeSet[p] {
			return strings.TrimSuffix(filepath.Base(p), ".gdl")
		}
	}
	t.Fatalf("seed reason did not produce a new file")
	return ""
}

// TestLineage_HappyPath_RendersDecisionPlusChain seeds a decision plus
// two reasoning steps (root + child) and asserts the columnar tree
// shape per the spec lines 440-453.
func TestLineage_HappyPath_RendersDecisionPlusChain(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")
	rootReason := mustWriteReason(t, root, "agent-a", id, "Customer requested refund of $400", "")
	_ = mustWriteReason(t, root, "agent-a", id, "Threshold check: $400 < $500", rootReason)

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	for _, want := range []string{
		"Decision: " + id,
		"Made at:",
		"agent-a",
		"Reasoning chain:",
		"1. ",
		"2. ",
	} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, res.Stdout)
		}
	}
}

// TestLineage_JSONOutput_HasExpectedShape covers the --json contract.
// We don't assert every field, just the shape envelope + the three
// arrays so render drift surfaces.
func TestLineage_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")
	_ = mustWriteReason(t, root, "agent-a", id, "root step", "")

	res := testutil.RunCLI(t, []string{"lineage", id, "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &obj); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, res.Stdout)
	}
	if obj["_type"] != "lineage" {
		t.Errorf("_type=%v, want lineage", obj["_type"])
	}
	if obj["_version"] != "1" {
		t.Errorf("_version=%v, want \"1\"", obj["_version"])
	}
	if _, ok := obj["decision"]; !ok {
		t.Errorf("missing decision field")
	}
	if _, ok := obj["bundle"]; !ok {
		t.Errorf("missing bundle field")
	}
	if _, ok := obj["reasoning"]; !ok {
		t.Errorf("missing reasoning field")
	}
}

// TestLineage_ExpiredAnnotation moves a decision from live/outbox/ to
// live/expired/ and asserts the Made-at line carries the (expired)
// annotation per design line 350.
func TestLineage_ExpiredAnnotation(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "expired call", "fleet")
	src := filepath.Join(root, "live", "outbox", "agent-a", id+".gdl")
	dstDir := filepath.Join(root, "live", "expired", "agent-a")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatalf("mkdir expired: %v", err)
	}
	if err := os.Rename(src, filepath.Join(dstDir, id+".gdl")); err != nil {
		t.Fatalf("rename: %v", err)
	}

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "(expired)") {
		t.Errorf("stdout missing (expired) marker:\n%s", res.Stdout)
	}
}

// TestLineage_NoSuchDecision_Exit1 asserts the typed error for a missing
// decision-id surfaces as exit 1 with the canonical stderr prefix.
func TestLineage_NoSuchDecision_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"lineage", "9999999999-zzzzzz"}, root, nil)
	if res.Code != 1 {
		t.Fatalf("exit=%d want 1, stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such decision") {
		t.Errorf("stderr missing 'no such decision':\n%s", res.Stderr)
	}
	if !strings.HasPrefix(res.Stderr, "rufio lineage:") {
		t.Errorf("stderr missing 'rufio lineage:' prefix:\n%s", res.Stderr)
	}
}

// TestLineage_NotADecision_Exit1_Hypothesis seeds a `--type=hypothesis`
// thought and asserts lineage rejects it with NotADecisionError.
func TestLineage_NotADecision_Exit1_Hypothesis(t *testing.T) {
	root := initProject(t)
	id := mustWriteHypothesis(t, root, "agent-a", "customer:5821", "still thinking", "fleet")

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 1 {
		t.Fatalf("exit=%d want 1, stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not 'decision'") {
		t.Errorf("stderr missing \"not 'decision'\":\n%s", res.Stderr)
	}
}

// TestLineage_NoReasoningChain seeds a decision but writes no reasons.
// The lineage render should still succeed and surface the empty-chain
// placeholder.
func TestLineage_NoReasoningChain(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "lone decision", "fleet")

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "(no reasoning chain)") {
		t.Errorf("stdout missing (no reasoning chain) marker:\n%s", res.Stdout)
	}
}

// TestLineage_ContextBundle_ResolvesRefs pushes a given/ file (creating
// a sha-pinned @ref), then seeds a decision (whose @context-bundle
// auto-captures the sha per L2.9). Lineage stdout mentions both the
// path and a short sha — proving the resolver wired correctly.
func TestLineage_ContextBundle_ResolvesRefs(t *testing.T) {
	root := initProject(t)
	if err := os.MkdirAll(filepath.Join(root, "given"), 0o755); err != nil {
		t.Fatalf("mkdir given: %v", err)
	}
	policy := filepath.Join(root, "given", "policy.md")
	if err := os.WriteFile(policy, []byte("refund threshold: $500\n"), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	// CollectGivenLearnedSHAs walks LATEST-LIVE refs only; explicit
	// --stage=live now that the bare push default is draft (#123).
	if r := testutil.RunCLI(t, []string{"push", "given/policy.md", "--stage=live"}, root, nil); r.Code != 0 {
		t.Fatalf("push failed: %s", r.Stderr)
	}
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")

	// Sanity: the decision file should carry the @context-bundle.
	bs, err := os.ReadFile(filepath.Join(root, "live", "outbox", "agent-a", id+".gdl"))
	if err != nil {
		t.Fatalf("read decision: %v", err)
	}
	if !strings.Contains(string(bs), "@context-bundle") {
		t.Fatalf("decision file missing @context-bundle:\n%s", string(bs))
	}

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "given/policy.md") {
		t.Errorf("stdout missing given/policy.md:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "sha:") {
		t.Errorf("stdout missing short sha annotation:\n%s", res.Stdout)
	}
}

// TestLineage_NotInProject_Exit1 confirms the NotInProjectError surfaces
// with exit 1 and the canonical stderr message.
func TestLineage_NotInProject_Exit1(t *testing.T) {
	root := mkProject(t) // no `rufio init`
	res := testutil.RunCLI(t, []string{"lineage", "1000000000-abcdef"}, root, nil)
	if res.Code != 1 {
		t.Fatalf("exit=%d want 1, stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr missing 'not inside a Rufio project':\n%s", res.Stderr)
	}
}

// TestLineage_RendersReasoningFromOtherAgents_Text covers #138: agent-A
// writes a decision and a reason against it; agent-B writes a reason
// against agent-A's decision. `rufio lineage <id>` (no --json) MUST
// include BOTH reasons in the rendered chain, and agent-B's reason must
// surface its author so a reader can tell who wrote it. Pre-fix the
// CLI walked only the decision-author's reasoning subdir and dropped
// agent-B's contribution silently.
func TestLineage_RendersReasoningFromOtherAgents_Text(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")
	_ = mustWriteReason(t, root, "agent-a", id, "Threshold check passed", "")
	_ = mustWriteReason(t, root, "agent-b", id, "5 retries may extend p99", "")

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	for _, want := range []string{
		"Threshold check passed",
		"5 retries may extend p99",
		"agent-b",
	} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout missing %q (cross-agent reasoning dropped?):\n%s", want, res.Stdout)
		}
	}
}

// TestLineage_RendersReasoningFromOtherAgents_JSON covers #138 on the
// --json contract: cross-agent reasons must appear in reasoning[]
// (or reasoning_chain[]) with the correct author field.
func TestLineage_RendersReasoningFromOtherAgents_JSON(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")
	_ = mustWriteReason(t, root, "agent-a", id, "Threshold check passed", "")
	_ = mustWriteReason(t, root, "agent-b", id, "5 retries may extend p99", "")

	res := testutil.RunCLI(t, []string{"lineage", id, "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &obj); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, res.Stdout)
	}
	raw, ok := obj["reasoning"].([]interface{})
	if !ok {
		t.Fatalf("reasoning field missing or wrong shape: %v", obj["reasoning"])
	}
	if len(raw) != 2 {
		t.Fatalf("reasoning len=%d, want 2 (a+b); payload:\n%s", len(raw), res.Stdout)
	}
	authors := map[string]bool{}
	for _, step := range raw {
		m, ok := step.(map[string]interface{})
		if !ok {
			t.Fatalf("step not an object: %v", step)
		}
		a, _ := m["author"].(string)
		if a == "" {
			t.Errorf("step missing author field: %v", m)
		}
		authors[a] = true
	}
	if !authors["agent-a"] || !authors["agent-b"] {
		t.Errorf("authors=%v, want both agent-a and agent-b", authors)
	}
}

// TestLineage_RendersReasoningFromMultipleAgents_AcrossChainTree
// exercises the parent-child sort across THREE agents: agent-A writes
// the decision and reason-1; agent-B writes reason-2 with parent=r1;
// agent-C writes reason-3 with parent=r1. The chain must contain all
// three with reason-1 at depth 0 and reasons 2 + 3 at depth 1.
func TestLineage_RendersReasoningFromMultipleAgents_AcrossChainTree(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")
	r1 := mustWriteReason(t, root, "agent-a", id, "root reason", "")
	_ = mustWriteReason(t, root, "agent-b", id, "agent-b challenge to root", r1)
	_ = mustWriteReason(t, root, "agent-c", id, "agent-c support for root", r1)

	res := testutil.RunCLI(t, []string{"lineage", id, "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &obj); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, res.Stdout)
	}
	raw, ok := obj["reasoning"].([]interface{})
	if !ok {
		t.Fatalf("reasoning field missing or wrong shape: %v", obj["reasoning"])
	}
	if len(raw) != 3 {
		t.Fatalf("reasoning len=%d, want 3 (a+b+c); payload:\n%s", len(raw), res.Stdout)
	}
	depthByAuthor := map[string]float64{}
	for _, step := range raw {
		m := step.(map[string]interface{})
		a, _ := m["author"].(string)
		d, _ := m["depth"].(float64)
		// Last-write-wins is fine here; each agent has one step.
		depthByAuthor[a] = d
	}
	if depthByAuthor["agent-a"] != 0 {
		t.Errorf("agent-a depth=%v, want 0", depthByAuthor["agent-a"])
	}
	if depthByAuthor["agent-b"] != 1 {
		t.Errorf("agent-b depth=%v, want 1", depthByAuthor["agent-b"])
	}
	if depthByAuthor["agent-c"] != 1 {
		t.Errorf("agent-c depth=%v, want 1", depthByAuthor["agent-c"])
	}
}

// TestLineage_NoCrossAgentReasons_StillWorks is the regression guard:
// a single-author decision + reasons must render exactly as before the
// #138 fix. Catches any drift in the existing single-author code path.
func TestLineage_NoCrossAgentReasons_StillWorks(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")
	r1 := mustWriteReason(t, root, "agent-a", id, "Customer requested refund of $400", "")
	_ = mustWriteReason(t, root, "agent-a", id, "Threshold check: $400 < $500", r1)

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	for _, want := range []string{
		"Customer requested refund of $400",
		"Threshold check: $400 < $500",
		"1. ",
		"2. ",
	} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, res.Stdout)
		}
	}
}

// TestLineage_ConfirmationLine_NotPrefixed proves the rendered tree
// (success-side stdout) never carries the `rufio lineage:` error prefix.
// Single-prefix invariant: errors carry the prefix, success doesn't.
func TestLineage_ConfirmationLine_NotPrefixed(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "happy path", "fleet")

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.HasPrefix(res.Stdout, "rufio lineage:") {
		t.Errorf("stdout starts with 'rufio lineage:' (success should not be prefixed):\n%s", res.Stdout)
	}
}
