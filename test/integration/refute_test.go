package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestRefute_HappyPath_AppendsToConfirmsFile — agent-a writes a thought;
// agent-b (a NON-author) refutes it. Asserts live/confirms/<id>.gdl exists
// and contains the canonical @refute fields. Confirms and refutes share
// the same per-thought file so the AutoPromote engine can read a single
// tally without cross-file joins.
func TestRefute_HappyPath_AppendsToConfirmsFile(t *testing.T) {
	root := initProject(t)
	// scope=deployment so a non-author may refute (#147: scope:agent
	// is non-author-writeable). The semantic of this test —
	// "anyone may refute a thought a peer can see" — is preserved.
	id := mustWriteThoughtWithScope(t, root, "agent-a", "to be refuted", "deployment")

	res := testutil.RunCLI(t, []string{
		"refute", id, "--reason=wrong",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	target := filepath.Join(root, "live", "confirms", id+".gdl")
	bs, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("confirms file missing: %v", err)
	}
	for _, want := range []string{"@refute|", "target:" + id, "by:agent-b", "reason:wrong"} {
		if !strings.Contains(string(bs), want) {
			t.Errorf("file missing %q.\n%s", want, bs)
		}
	}
}

// TestRefute_WithEvidence_IncludesEvidenceField — when --evidence is
// provided, the rendered record must contain `evidence:<text>`.
func TestRefute_WithEvidence_IncludesEvidenceField(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "x", "deployment")

	res := testutil.RunCLI(t, []string{
		"refute", id, "--reason=wrong", "--evidence=contradicted by X",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	target := filepath.Join(root, "live", "confirms", id+".gdl")
	bs, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("confirms file missing: %v", err)
	}
	if !strings.Contains(string(bs), "evidence:contradicted by X") {
		t.Errorf("file missing evidence field.\n%s", bs)
	}
}

// TestRefute_WithoutEvidence_OmitsEvidenceField — when no --evidence
// flag is passed, the record must NOT contain an `evidence:` field at all
// (sibling-pattern omit). The GDL record is rendered field-by-field, so
// the literal token `evidence:` should not appear.
func TestRefute_WithoutEvidence_OmitsEvidenceField(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "x", "deployment")

	res := testutil.RunCLI(t, []string{
		"refute", id, "--reason=wrong",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	target := filepath.Join(root, "live", "confirms", id+".gdl")
	bs, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("confirms file missing: %v", err)
	}
	if strings.Contains(string(bs), "evidence:") {
		t.Errorf("file unexpectedly contains evidence field.\n%s", bs)
	}
}

// TestRefute_JSONOutput_HasExpectedShape — --json prints a single JSONL
// line with the documented shape. Tested both with and without evidence
// to cover the conditional-omit case in JSON output. `reason` is always
// required; `evidence` only appears when provided.
func TestRefute_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)

	// With evidence — `evidence` MUST be present.
	id1 := mustWriteThoughtWithScope(t, root, "agent-a", "x", "deployment")
	res := testutil.RunCLI(t, []string{
		"refute", id1, "--reason=wrong", "--evidence=contradicted by X", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("with-evidence exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("with-evidence invalid JSON: %v\n%q", err, res.Stdout)
	}
	if got["_type"] != "refute" {
		t.Errorf("_type=%v", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v", got["_version"])
	}
	if got["target"] != id1 {
		t.Errorf("target=%v", got["target"])
	}
	if got["by"] != "agent-b" {
		t.Errorf("by=%v", got["by"])
	}
	if got["reason"] != "wrong" {
		t.Errorf("reason=%v", got["reason"])
	}
	if got["evidence"] != "contradicted by X" {
		t.Errorf("evidence=%v", got["evidence"])
	}

	// Without evidence — `evidence` key MUST be absent. `reason` still required.
	id2 := mustWriteThoughtWithScope(t, root, "agent-a", "y", "deployment")
	res2 := testutil.RunCLI(t, []string{
		"refute", id2, "--reason=wrong", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res2.Code != 0 {
		t.Fatalf("no-evidence exit=%d stderr=%q", res2.Code, res2.Stderr)
	}
	var got2 map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res2.Stdout)), &got2); err != nil {
		t.Fatalf("no-evidence invalid JSON: %v\n%q", err, res2.Stdout)
	}
	if _, present := got2["evidence"]; present {
		t.Errorf("no-evidence JSON unexpectedly contains evidence: %v", got2["evidence"])
	}
	if got2["target"] != id2 {
		t.Errorf("target=%v", got2["target"])
	}
	if got2["reason"] != "wrong" {
		t.Errorf("reason=%v", got2["reason"])
	}
}

// TestRefute_ConfirmationLine_HasCanonicalPrefix — stdout contains the
// canonical `refute: target=<id>` line and does NOT start with the
// error-style `rufio refute:` prefix. H3d (#125) normalized the success
// prefix to the literal verb (was "refuted:").
func TestRefute_ConfirmationLine_HasCanonicalPrefix(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "x", "deployment")

	res := testutil.RunCLI(t, []string{
		"refute", id, "--reason=wrong",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio refute:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "refute: target="+id) {
		t.Errorf("missing canonical refutation: %q", res.Stdout)
	}
}

// --- Error-path tests ---

// TestRefute_MissingReason_Exit2 — refute requires `--reason`. Omitting
// the flag yields a usage error (exit 2) with the canonical message.
func TestRefute_MissingReason_Exit2(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "x")
	res := testutil.RunCLI(t, []string{"refute", id}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--reason must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// TestRefute_EmptyReason_Exit2 — whitespace-only `--reason` value is
// treated identically to a missing flag (TrimSpace).
func TestRefute_EmptyReason_Exit2(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "x")
	res := testutil.RunCLI(t, []string{"refute", id, "--reason=   "}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--reason must not be empty") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// TestRefute_NoSuchThought_Exit1 — refute against an id that does not
// resolve to a live or promoted file must fail with exit 1.
func TestRefute_NoSuchThought_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"refute", "1727000000-fake12", "--reason=wrong",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such record") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// TestRefute_NoIdentity_Exit1 — empty RUFIO_AGENT_ID + no identity file
// produces exit 1 with `no identity set` and stderr carries the single
// `rufio refute:` prefix invariant.
func TestRefute_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "x")
	res := testutil.RunCLI(t, []string{
		"refute", id, "--reason=wrong",
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio refute:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

// TestRefute_NotInProject_Exit1 — running refute from a bare TempDir
// (no `rufio init`) must exit 1 with `not inside a Rufio project`.
func TestRefute_NotInProject_Exit1(t *testing.T) {
	root := t.TempDir()
	res := testutil.RunCLI(t, []string{
		"refute", "1727000000-fake12", "--reason=wrong",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// TestRefute_QuietSuppressesChatterButNotJSON — `--quiet` empties stdout
// while still writing the underlying file; `--json --quiet` retains the
// JSON payload (quiet must not gag machine output).
func TestRefute_QuietSuppressesChatterButNotJSON(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "x", "deployment")
	env := map[string]string{"RUFIO_AGENT_ID": "agent-b"}

	q := testutil.RunCLI(t, []string{"refute", id, "--reason=wrong", "--quiet"}, root, env)
	if q.Code != 0 {
		t.Fatalf("--quiet exit=%d stderr=%q", q.Code, q.Stderr)
	}
	if strings.TrimSpace(q.Stdout) != "" {
		t.Errorf("--quiet stdout=%q, want empty", q.Stdout)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "confirms", id+".gdl")); err != nil {
		t.Errorf("--quiet did not write file: %v", err)
	}

	// Use a fresh thought for --json --quiet so the assertion isolates
	// the second invocation's stdout.
	id2 := mustWriteThoughtWithScope(t, root, "agent-a", "y", "deployment")
	j := testutil.RunCLI(t, []string{"refute", id2, "--reason=wrong", "--json", "--quiet"}, root, env)
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
