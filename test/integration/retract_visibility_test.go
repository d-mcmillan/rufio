package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// Integration tests for #141 (retract silent in `thoughts list`) and
// #148 (lineage of retracted decision renders chain with no marker;
// reason --decision succeeds silently against retracted ids).
//
// Mirrors the lineage_confirm_visibility shape: seed everything through
// the real CLI, then assert text + JSON contracts on the read surface.

// retractThought runs `rufio retract <id> --reason=<r>` as the named
// agent (= the author; non-authors can't retract).
func retractThought(t *testing.T, root, id, agent, reason string) {
	t.Helper()
	res := testutil.RunCLI(t, []string{
		"retract", id, "--reason=" + reason,
	}, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("retract %s by %s: exit=%d stderr=%q", id, agent, res.Code, res.Stderr)
	}
}

// --- #141 thoughts list ---

// TestThoughtsList_RetractedThought_RendersBracketPrefix_Text — #141:
// retracted thoughts surface inline with a [RETRACTED] prefix in the
// DEFAULT text output (no --include-expired needed; that flag is for
// TTL-expired only). This is the focused author-audit view — retract
// is signal here, not noise.
func TestThoughtsList_RetractedThought_RendersBracketPrefix_Text(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "to be retracted")
	retractThought(t, root, id, "agent-a", "superseded")

	res := testutil.RunCLI(t, []string{"thoughts", "list"}, root, map[string]string{"RUFIO_FULL_IDS": "1"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, id) {
		t.Fatalf("stdout missing id=%s\n%s", id, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[RETRACTED]") {
		t.Errorf("stdout missing [RETRACTED] prefix:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, `retract_reason:"superseded"`) {
		t.Errorf(`stdout missing retract_reason:"superseded":`+"\n%s", res.Stdout)
	}
}

// TestThoughtsList_RetractedThought_PopulatesFields_JSON — #141: the
// retracted thought's JSON row gets retracted_at, retracted_by,
// retract_reason populated in the DEFAULT JSON output (no
// --include-expired needed).
func TestThoughtsList_RetractedThought_PopulatesFields_JSON(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "to be retracted")
	retractThought(t, root, id, "agent-a", "superseded")

	res := testutil.RunCLI(t, []string{"thoughts", "list", "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	var found map[string]interface{}
	for _, line := range lines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, line)
		}
		if obj["id"] == id {
			found = obj
			break
		}
	}
	if found == nil {
		t.Fatalf("thought %s not in thoughts list output:\n%s", id, res.Stdout)
	}
	if found["retract_reason"] != "superseded" {
		t.Errorf("retract_reason=%v want 'superseded'", found["retract_reason"])
	}
	if found["retracted_by"] != "agent-a" {
		t.Errorf("retracted_by=%v want 'agent-a'", found["retracted_by"])
	}
	if ts, _ := found["retracted_at"].(string); ts == "" {
		t.Errorf("retracted_at empty: %+v", found)
	}
}

// TestThoughtsList_NonRetractedThought_FieldsPresentNull_JSON — every
// row has retracted_at, retracted_by, retract_reason keys for stable
// shape; present-but-null for non-retracted thoughts.
func TestThoughtsList_NonRetractedThought_FieldsPresentNull_JSON(t *testing.T) {
	root := initProject(t)
	_ = mustWriteThought(t, root, "agent-a", "live thought")

	res := testutil.RunCLI(t, []string{"thoughts", "list", "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) < 1 {
		t.Fatalf("no rows: %s", res.Stdout)
	}
	for i, line := range lines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("row %d invalid JSON: %v\n%s", i, err, line)
		}
		for _, key := range []string{"retracted_at", "retracted_by", "retract_reason"} {
			v, present := obj[key]
			if !present {
				t.Errorf("row %d missing %q key (must be present, null when absent)", i, key)
				continue
			}
			if v != nil {
				t.Errorf("row %d %q=%v, want null for non-retracted", i, key, v)
			}
		}
	}
}

// TestThoughtsList_DefaultShowsRetractedWithMarker — #141: retracted
// thoughts surface inline by default with a [RETRACTED] marker, so
// operators see withdrawn thinking at a glance without having to
// reach for --include-expired or `recall --include-expired`. Live
// thoughts still surface unchanged.
func TestThoughtsList_DefaultShowsRetractedWithMarker(t *testing.T) {
	root := initProject(t)
	live := mustWriteThought(t, root, "agent-a", "live")
	retracted := mustWriteThought(t, root, "agent-a", "to be retracted")
	retractThought(t, root, retracted, "agent-a", "superseded")

	res := testutil.RunCLI(t, []string{"thoughts", "list"}, root, map[string]string{"RUFIO_FULL_IDS": "1"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, live) {
		t.Errorf("stdout missing live id=%s:\n%s", live, res.Stdout)
	}
	if !strings.Contains(res.Stdout, retracted) {
		t.Errorf("stdout missing retracted id=%s (must surface inline by default per #141):\n%s", retracted, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[RETRACTED]") {
		t.Errorf("stdout missing [RETRACTED] marker on retracted row:\n%s", res.Stdout)
	}
}

// --- #148 lineage ---

// TestLineage_RetractedDecision_RendersRetractedLine_Text — when the
// decision has been retracted, lineage text output includes a
// `Retracted: <ts> by <agent> — "<reason>"` line right under the
// Decision: header.
func TestLineage_RetractedDecision_RendersRetractedLine_Text(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "alice", "test:1", "X", "fleet")
	retractThought(t, root, id, "alice", "superseded")

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "Retracted:") {
		t.Errorf("stdout missing Retracted: line:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "alice") {
		t.Errorf("stdout missing retract agent 'alice':\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "superseded") {
		t.Errorf("stdout missing retract reason 'superseded':\n%s", res.Stdout)
	}
}

// TestLineage_LiveDecision_NoRetractedLine_Text — a non-retracted
// decision MUST NOT render a Retracted: line.
func TestLineage_LiveDecision_NoRetractedLine_Text(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "alice", "test:1", "X", "fleet")

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stdout, "Retracted:") {
		t.Errorf("unexpected Retracted: line on live decision:\n%s", res.Stdout)
	}
}

// TestLineage_RetractedDecision_PopulatesRetractedFields_JSON — the
// JSON `decision` object gets retracted_at, retracted_by, retract_reason.
func TestLineage_RetractedDecision_PopulatesRetractedFields_JSON(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "alice", "test:1", "X", "fleet")
	retractThought(t, root, id, "alice", "superseded")

	res := testutil.RunCLI(t, []string{"lineage", id, "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &obj); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, res.Stdout)
	}
	decision := obj["decision"].(map[string]interface{})
	if decision["retract_reason"] != "superseded" {
		t.Errorf("retract_reason=%v", decision["retract_reason"])
	}
	if decision["retracted_by"] != "alice" {
		t.Errorf("retracted_by=%v", decision["retracted_by"])
	}
	if ts, _ := decision["retracted_at"].(string); ts == "" {
		t.Errorf("retracted_at empty: %+v", decision)
	}
}

// TestLineage_LiveDecision_RetractedFieldsPresentNull_JSON — JSON
// shape must be stable: keys present, null when not retracted.
func TestLineage_LiveDecision_RetractedFieldsPresentNull_JSON(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "alice", "test:1", "X", "fleet")

	res := testutil.RunCLI(t, []string{"lineage", id, "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &obj); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, res.Stdout)
	}
	decision := obj["decision"].(map[string]interface{})
	for _, key := range []string{"retracted_at", "retracted_by", "retract_reason"} {
		v, present := decision[key]
		if !present {
			t.Errorf("decision missing %q key", key)
			continue
		}
		if v != nil {
			t.Errorf("decision.%q=%v, want null for live decision", key, v)
		}
	}
}

// TestLineage_PostRetractReason_TaggedAsPostRetract — a reason that
// landed AFTER the retract gets `[POST-RETRACT]` in lineage text output.
func TestLineage_PostRetractReason_TaggedAsPostRetract(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "alice", "test:1", "X", "fleet")
	retractThought(t, root, id, "alice", "superseded")
	// Bob chains reasoning AFTER the retract.
	mustWriteReason(t, root, "bob", id, "counter", "")

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "[POST-RETRACT]") {
		t.Errorf("stdout missing [POST-RETRACT] tag for bob's reason:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "counter") {
		t.Errorf("stdout missing bob's reason content:\n%s", res.Stdout)
	}
}

// --- #148 advisory stderr warnings on writes against retracted targets ---

// TestReason_AgainstRetractedDecision_StderrAdvisory — `rufio reason
// --decision=<retracted-id>` succeeds (exit 0, record written), but
// emits an advisory warning on stderr.
func TestReason_AgainstRetractedDecision_StderrAdvisory(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "alice", "test:1", "X", "fleet")
	retractThought(t, root, id, "alice", "superseded")

	res := testutil.RunCLI(t, []string{
		"reason", "--decision=" + id, "--content=counter",
	}, root, map[string]string{"RUFIO_AGENT_ID": "bob"})
	if res.Code != 0 {
		t.Fatalf("reason exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "was retracted") {
		t.Errorf("stderr missing 'was retracted' advisory:\n%q", res.Stderr)
	}
	if !strings.Contains(res.Stderr, id) {
		t.Errorf("stderr missing decision id %s:\n%q", id, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "alice") {
		t.Errorf("stderr missing retract author 'alice':\n%q", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "superseded") {
		t.Errorf("stderr missing retract reason 'superseded':\n%q", res.Stderr)
	}
}

// TestReason_AgainstRetractedDecision_QuietSuppresses — --quiet drops
// the advisory but the write still succeeds.
func TestReason_AgainstRetractedDecision_QuietSuppresses(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "alice", "test:1", "X", "fleet")
	retractThought(t, root, id, "alice", "superseded")

	res := testutil.RunCLI(t, []string{
		"reason", "--decision=" + id, "--content=counter", "--quiet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "bob"})
	if res.Code != 0 {
		t.Fatalf("reason --quiet exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stderr, "was retracted") {
		t.Errorf("--quiet should suppress advisory, got stderr=%q", res.Stderr)
	}
}

// TestReason_AgainstLiveDecision_NoAdvisory — no retract means no
// advisory; clean stderr.
func TestReason_AgainstLiveDecision_NoAdvisory(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "alice", "test:1", "X", "fleet")

	res := testutil.RunCLI(t, []string{
		"reason", "--decision=" + id, "--content=ok",
	}, root, map[string]string{"RUFIO_AGENT_ID": "bob"})
	if res.Code != 0 {
		t.Fatalf("reason exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stderr, "was retracted") {
		t.Errorf("unexpected advisory on live decision: stderr=%q", res.Stderr)
	}
}

// TestConfirm_AgainstRetractedThought_StderrAdvisory — `rufio confirm
// <retracted-id>` succeeds with an advisory.
func TestConfirm_AgainstRetractedThought_StderrAdvisory(t *testing.T) {
	root := initProject(t)
	// scope=deployment so a non-author may confirm.
	id := mustWriteThoughtWithScope(t, root, "alice", "to be retracted", "deployment")
	retractThought(t, root, id, "alice", "wrong call")

	res := testutil.RunCLI(t, []string{"confirm", id}, root, map[string]string{"RUFIO_AGENT_ID": "bob"})
	if res.Code != 0 {
		t.Fatalf("confirm exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "was retracted") {
		t.Errorf("stderr missing 'was retracted' advisory:\n%q", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "wrong call") {
		t.Errorf("stderr missing retract reason:\n%q", res.Stderr)
	}
}

// TestRefute_AgainstRetractedThought_StderrAdvisory — refute counterpart.
func TestRefute_AgainstRetractedThought_StderrAdvisory(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "alice", "to be retracted", "deployment")
	retractThought(t, root, id, "alice", "wrong call")

	res := testutil.RunCLI(t, []string{
		"refute", id, "--reason=no",
	}, root, map[string]string{"RUFIO_AGENT_ID": "bob"})
	if res.Code != 0 {
		t.Fatalf("refute exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "was retracted") {
		t.Errorf("stderr missing 'was retracted' advisory:\n%q", res.Stderr)
	}
}

// TestConfirm_AgainstRetractedThought_QuietSuppresses — --quiet drops
// the advisory on confirm.
func TestConfirm_AgainstRetractedThought_QuietSuppresses(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "alice", "to be retracted", "deployment")
	retractThought(t, root, id, "alice", "wrong call")

	res := testutil.RunCLI(t, []string{
		"confirm", id, "--quiet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "bob"})
	if res.Code != 0 {
		t.Fatalf("confirm --quiet exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stderr, "was retracted") {
		t.Errorf("--quiet should suppress advisory: stderr=%q", res.Stderr)
	}
}
