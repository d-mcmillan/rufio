package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestAttention_HappyPath_RendersBlock asserts the labelled block from
// D20.2: Agent/Intent/Entities/Topics/Updated.
func TestAttention_HappyPath_RendersBlock(t *testing.T) {
	root := initProject(t)
	mustAttend(t, root, "agent-a", "watching the auth pipeline", []string{"customer:5821"})

	res := testutil.RunCLI(t, []string{"attention", "agent-a"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	for _, expect := range []string{
		"Agent: agent-a",
		"Intent: watching the auth pipeline",
		"Entities: customer:5821",
		"Updated: ",
	} {
		if !strings.Contains(res.Stdout, expect) {
			t.Errorf("stdout missing %q\n%s", expect, res.Stdout)
		}
	}
}

// TestAttention_JSONOutput_HasExpectedShape asserts --json emits a
// single JSONL object with every D20.2 field.
func TestAttention_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	mustAttend(t, root, "agent-a", "watching auth", []string{"customer:5821"})

	res := testutil.RunCLI(t, []string{"attention", "agent-a", "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := nonEmptyLines(res.Stdout)
	if len(lines) != 1 {
		t.Fatalf("want 1 JSONL line, got %d:\n%s", len(lines), res.Stdout)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, lines[0])
	}
	if got["_type"] != "attention" {
		t.Errorf("_type=%v want attention", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v want 1", got["_version"])
	}
	if got["agent"] != "agent-a" {
		t.Errorf("agent=%v want agent-a", got["agent"])
	}
	if got["intent"] != "watching auth" {
		t.Errorf("intent=%v want 'watching auth'", got["intent"])
	}
	ents, ok := got["entities"].([]interface{})
	if !ok || len(ents) != 1 || ents[0] != "customer:5821" {
		t.Errorf("entities=%v want [customer:5821]", got["entities"])
	}
	if _, ok := got["topics"].([]interface{}); !ok {
		t.Errorf("topics should be array, got %T", got["topics"])
	}
	if _, ok := got["ts"]; !ok {
		t.Errorf("missing ts field: %v", got)
	}
}

// TestAttention_NoSuchAgent_Exit1 asserts a never-attended agent surfaces
// *NoAttentionError as exit 1 with the canonical envelope.
func TestAttention_NoSuchAgent_Exit1(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"attention", "agent-x"}, root, nil)
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "no attention record for agent agent-x") {
		t.Errorf("stderr=%q (want 'no attention record for agent agent-x')", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio attention:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

// TestAttention_NoEntities_RendersNone is intentionally a "no topics"
// case: attend requires entities (validated upstream) so we can't seed
// an attention record without them. We CAN seed without topics, and
// confirm "Topics: (none)" surfaces.
func TestAttention_NoEntities_RendersNone(t *testing.T) {
	root := initProject(t)
	mustAttend(t, root, "agent-a", "no topics here", []string{"customer:1"})

	res := testutil.RunCLI(t, []string{"attention", "agent-a"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "Topics: (none)") {
		t.Errorf("stdout missing 'Topics: (none)':\n%s", res.Stdout)
	}
}

// TestAttention_NotInProject_Exit1 asserts running outside a project
// surfaces NotInProjectError as exit 1.
func TestAttention_NotInProject_Exit1(t *testing.T) {
	workdir := t.TempDir()
	res := testutil.RunCLI(t, []string{"attention", "agent-a"}, workdir, nil)
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q (want 'not inside a Rufio project')", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio attention:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}

// TestAttention_ConfirmationLine_NotPrefixed asserts the success-path
// stdout doesn't start with "rufio attention:" (which is the error
// envelope). The block lines (Agent/Intent/...) are bare.
func TestAttention_ConfirmationLine_NotPrefixed(t *testing.T) {
	root := initProject(t)
	mustAttend(t, root, "agent-a", "checking", []string{"customer:1"})

	res := testutil.RunCLI(t, []string{"attention", "agent-a"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.HasPrefix(strings.TrimSpace(res.Stdout), "rufio attention:") {
		t.Errorf("success stdout should not start with 'rufio attention:':\n%s", res.Stdout)
	}
}
