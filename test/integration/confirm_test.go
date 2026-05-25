package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestConfirm_HappyPath_AppendsToConfirmsFile — agent-a writes a thought;
// agent-b (a NON-author) confirms it. Asserts live/confirms/<id>.gdl exists
// and contains the canonical @confirm fields. This also proves the rule
// that anyone (not just the author) may confirm a thought.
func TestConfirm_HappyPath_AppendsToConfirmsFile(t *testing.T) {
	root := initProject(t)
	// scope=deployment so a non-author may confirm (#147: scope:agent
	// is non-author-writeable). The semantic of this test —
	// "anyone may confirm a thought a peer can see" — is preserved.
	id := mustWriteThoughtWithScope(t, root, "agent-a", "to be confirmed", "deployment")

	res := testutil.RunCLI(t, []string{
		"confirm", id,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	target := filepath.Join(root, "live", "confirms", id+".gdl")
	bs, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("confirms file missing: %v", err)
	}
	for _, want := range []string{"@confirm|", "target:" + id, "by:agent-b"} {
		if !strings.Contains(string(bs), want) {
			t.Errorf("file missing %q.\n%s", want, bs)
		}
	}
}

// TestConfirm_WithEvidence_AppendsEvidenceField — when --evidence is
// provided, the rendered record must contain `evidence:<text>`.
func TestConfirm_WithEvidence_AppendsEvidenceField(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "x", "deployment")

	res := testutil.RunCLI(t, []string{
		"confirm", id, "--evidence=looks correct",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	target := filepath.Join(root, "live", "confirms", id+".gdl")
	bs, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("confirms file missing: %v", err)
	}
	if !strings.Contains(string(bs), "evidence:looks correct") {
		t.Errorf("file missing evidence field.\n%s", bs)
	}
}

// TestConfirm_WithoutEvidence_OmitsEvidenceField — when no --evidence
// flag is passed, the record must NOT contain an `evidence:` field at all
// (sibling-pattern omit). The GDL record is rendered field-by-field, so
// the literal token `evidence:` should not appear.
func TestConfirm_WithoutEvidence_OmitsEvidenceField(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "x", "deployment")

	res := testutil.RunCLI(t, []string{
		"confirm", id,
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

// TestConfirm_JSONOutput_HasExpectedShape — --json prints a single JSONL
// line with the documented shape. Tested both with and without evidence
// to cover the conditional-omit case in JSON output.
func TestConfirm_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)

	// With evidence — `evidence` MUST be present.
	id1 := mustWriteThoughtWithScope(t, root, "agent-a", "x", "deployment")
	res := testutil.RunCLI(t, []string{
		"confirm", id1, "--evidence=looks correct", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("with-evidence exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("with-evidence invalid JSON: %v\n%q", err, res.Stdout)
	}
	if got["_type"] != "confirm" {
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
	if got["evidence"] != "looks correct" {
		t.Errorf("evidence=%v", got["evidence"])
	}

	// Without evidence — `evidence` key MUST be absent.
	id2 := mustWriteThoughtWithScope(t, root, "agent-a", "y", "deployment")
	res2 := testutil.RunCLI(t, []string{
		"confirm", id2, "--json",
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
}

// TestConfirm_ConfirmationLine_HasCanonicalPrefix — stdout contains the
// canonical `confirm: target=<id>` line and does NOT start with the
// error-style `rufio confirm:` prefix. H3d (#125) normalized the success
// prefix to the literal verb (was "confirmed:").
func TestConfirm_ConfirmationLine_HasCanonicalPrefix(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "x", "deployment")

	res := testutil.RunCLI(t, []string{
		"confirm", id,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio confirm:") {
		t.Errorf("success stdout carries error prefix: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "confirm: target="+id) {
		t.Errorf("missing canonical confirmation: %q", res.Stdout)
	}
}

// --- Error-path tests ---

// TestConfirm_NoSuchThought_Exit1 — calling confirm against an id that
// does not resolve to a live or promoted file must fail with exit 1 and
// surface the canonical `no such record` message from retract.Lookup
// (#150: error names BOTH outbox AND learned/ — observations now
// participate in the same lookup).
func TestConfirm_NoSuchThought_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"confirm", "1727000000-fake12",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no such record") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// TestConfirm_NoIdentity_Exit1 — empty RUFIO_AGENT_ID + no identity file
// produces exit 1 with `no identity set` and stderr carries the single
// `rufio confirm:` prefix invariant.
func TestConfirm_NoIdentity_Exit1(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "x")
	res := testutil.RunCLI(t, []string{
		"confirm", id,
	}, root, map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no identity set") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio confirm:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

// TestConfirm_NotInProject_Exit1 — running confirm from a bare TempDir
// (no `rufio init`) must exit 1 with `not inside a Rufio project`.
func TestConfirm_NotInProject_Exit1(t *testing.T) {
	root := t.TempDir()
	res := testutil.RunCLI(t, []string{
		"confirm", "1727000000-fake12",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

// TestConfirm_QuietSuppressesChatterButNotJSON — `--quiet` empties stdout
// while still writing the underlying file; `--json --quiet` retains the
// JSON payload (quiet must not gag machine output).
func TestConfirm_QuietSuppressesChatterButNotJSON(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "x", "deployment")
	env := map[string]string{"RUFIO_AGENT_ID": "agent-b"}

	q := testutil.RunCLI(t, []string{"confirm", id, "--quiet"}, root, env)
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
	j := testutil.RunCLI(t, []string{"confirm", id2, "--json", "--quiet"}, root, env)
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
