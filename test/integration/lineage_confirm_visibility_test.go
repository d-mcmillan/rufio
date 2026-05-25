package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// Integration tests for issue #132 — surface confirmations and refutations
// in the `rufio lineage` text + JSON output, and add `confirmed_by` /
// `refuted_by` arrays to `rufio thoughts list --json`.
//
// All seeding goes through the real CLI (`rufio confirm`, `rufio refute`)
// so the on-disk shape matches what the production writers emit. Reads
// are the only thing under test here — the writers are exercised
// transitively.

// confirmThought runs `rufio confirm <id>` as the named agent. evidence
// may be empty.
func confirmThought(t *testing.T, root, id, agent, evidence string) {
	t.Helper()
	args := []string{"confirm", id}
	if evidence != "" {
		args = append(args, "--evidence="+evidence)
	}
	res := testutil.RunCLI(t, args, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("confirm %s by %s: exit=%d stderr=%q", id, agent, res.Code, res.Stderr)
	}
}

// refuteThought runs `rufio refute <id> --reason=<reason>` as the named
// agent. evidence may be empty.
func refuteThought(t *testing.T, root, id, agent, reason, evidence string) {
	t.Helper()
	args := []string{"refute", id, "--reason=" + reason}
	if evidence != "" {
		args = append(args, "--evidence="+evidence)
	}
	res := testutil.RunCLI(t, args, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("refute %s by %s: exit=%d stderr=%q", id, agent, res.Code, res.Stderr)
	}
}

// --- (A) lineage text output ---

// TestLineage_Text_NoConfirmationsOrRefutations_NoSections — when no
// @confirm or @refute exists for the decision, neither header is rendered.
func TestLineage_Text_NoConfirmationsOrRefutations_NoSections(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stdout, "Confirmations:") {
		t.Errorf("unexpected Confirmations: header in empty case:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "Refutations:") {
		t.Errorf("unexpected Refutations: header in empty case:\n%s", res.Stdout)
	}
}

// TestLineage_Text_WithConfirmation_RendersConfirmationsSection — one
// confirm exists; render header + agent + evidence excerpt.
func TestLineage_Text_WithConfirmation_RendersConfirmationsSection(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")
	confirmThought(t, root, id, "agent-peer", "agree, argon2id is the right call")

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	for _, want := range []string{
		"Confirmations:",
		"agent-peer",
		"agree, argon2id is the right call",
	} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, res.Stdout)
		}
	}
	if strings.Contains(res.Stdout, "Refutations:") {
		t.Errorf("unexpected Refutations: header when no refute exists:\n%s", res.Stdout)
	}
}

// TestLineage_Text_WithRefutation_RendersRefutationsSection — symmetric.
func TestLineage_Text_WithRefutation_RendersRefutationsSection(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")
	refuteThought(t, root, id, "agent-skeptic", "scrypt is acceptable too", "")

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	for _, want := range []string{
		"Refutations:",
		"agent-skeptic",
		"scrypt is acceptable too",
	} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, res.Stdout)
		}
	}
	if strings.Contains(res.Stdout, "Confirmations:") {
		t.Errorf("unexpected Confirmations: header when no confirm exists:\n%s", res.Stdout)
	}
}

// TestLineage_Text_WithBoth_RendersBothSections — both kinds present.
func TestLineage_Text_WithBoth_RendersBothSections(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")
	confirmThought(t, root, id, "agent-peer", "concur, OWASP-aligned")
	refuteThought(t, root, id, "agent-skeptic", "scrypt also acceptable", "see policy.md")

	res := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	for _, want := range []string{
		"Confirmations:",
		"agent-peer",
		"concur, OWASP-aligned",
		"Refutations:",
		"agent-skeptic",
		"scrypt also acceptable",
	} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, res.Stdout)
		}
	}
}

// --- (B) lineage JSON output ---

// TestLineage_JSON_AlwaysIncludesConfirmedByAndRefutedByArrays — even
// when there are zero confirms/refutes, the decision object MUST include
// both keys as [] (never nil, never missing).
func TestLineage_JSON_AlwaysIncludesConfirmedByAndRefutedByArrays(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")

	res := testutil.RunCLI(t, []string{"lineage", id, "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &obj); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, res.Stdout)
	}
	decision, ok := obj["decision"].(map[string]interface{})
	if !ok {
		t.Fatalf("decision field not an object: %T", obj["decision"])
	}
	for _, key := range []string{"confirmed_by", "refuted_by"} {
		v, present := decision[key]
		if !present {
			t.Errorf("decision missing %q key (should be [] when empty)", key)
			continue
		}
		if v == nil {
			t.Errorf("decision.%q is null, expected []", key)
			continue
		}
		arr, ok := v.([]interface{})
		if !ok {
			t.Errorf("decision.%q is %T, expected []interface{}", key, v)
			continue
		}
		if len(arr) != 0 {
			t.Errorf("decision.%q len=%d, expected 0", key, len(arr))
		}
	}
}

// TestLineage_JSON_ConfirmedByPopulated — one confirm populates the
// confirmed_by array with {agent, ts, evidence}.
func TestLineage_JSON_ConfirmedByPopulated(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")
	confirmThought(t, root, id, "agent-peer", "agree, argon2id is the right call")

	res := testutil.RunCLI(t, []string{"lineage", id, "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &obj); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, res.Stdout)
	}
	decision := obj["decision"].(map[string]interface{})
	arr, ok := decision["confirmed_by"].([]interface{})
	if !ok {
		t.Fatalf("confirmed_by not an array: %T", decision["confirmed_by"])
	}
	if len(arr) != 1 {
		t.Fatalf("confirmed_by len=%d want 1\n%s", len(arr), res.Stdout)
	}
	first := arr[0].(map[string]interface{})
	if first["agent"] != "agent-peer" {
		t.Errorf("agent=%v want agent-peer", first["agent"])
	}
	if first["evidence"] != "agree, argon2id is the right call" {
		t.Errorf("evidence=%v", first["evidence"])
	}
	if ts, _ := first["ts"].(string); ts == "" {
		t.Errorf("ts is empty: %+v", first)
	}
}

// TestLineage_JSON_RefutedByPopulated — symmetric for refute.
func TestLineage_JSON_RefutedByPopulated(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "customer:5821", "approve refund", "fleet")
	refuteThought(t, root, id, "agent-skeptic", "scrypt is acceptable too", "see policy.md")

	res := testutil.RunCLI(t, []string{"lineage", id, "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &obj); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, res.Stdout)
	}
	decision := obj["decision"].(map[string]interface{})
	arr, ok := decision["refuted_by"].([]interface{})
	if !ok {
		t.Fatalf("refuted_by not an array: %T", decision["refuted_by"])
	}
	if len(arr) != 1 {
		t.Fatalf("refuted_by len=%d want 1\n%s", len(arr), res.Stdout)
	}
	first := arr[0].(map[string]interface{})
	if first["agent"] != "agent-skeptic" {
		t.Errorf("agent=%v want agent-skeptic", first["agent"])
	}
	// Refute evidence is the --evidence flag (NOT the reason). Reason
	// goes into the @refute record's reason field; we surface evidence
	// here for symmetry with confirm.
	if first["evidence"] != "see policy.md" {
		t.Errorf("evidence=%v", first["evidence"])
	}
	if ts, _ := first["ts"].(string); ts == "" {
		t.Errorf("ts is empty: %+v", first)
	}
}

// --- (C) thoughts list --json ---

// TestThoughtsList_JSON_AlwaysIncludesConfirmedByAndRefutedByArrays —
// every thought emitted by `thoughts list --json` MUST carry both keys
// as [] (never null, never absent).
func TestThoughtsList_JSON_AlwaysIncludesConfirmedByAndRefutedByArrays(t *testing.T) {
	root := initProject(t)
	_ = mustWriteThought(t, root, "agent-a", "a vanilla thought")
	_ = mustWriteThought(t, root, "agent-a", "another vanilla thought")

	res := testutil.RunCLI(t, []string{"thoughts", "list", "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected >=2 JSONL rows, got %d:\n%s", len(lines), res.Stdout)
	}
	for i, line := range lines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("row %d invalid JSON: %v\n%s", i, err, line)
		}
		for _, key := range []string{"confirmed_by", "refuted_by"} {
			v, present := obj[key]
			if !present {
				t.Errorf("row %d missing %q key (should be [] when empty)", i, key)
				continue
			}
			if v == nil {
				t.Errorf("row %d %q is null, expected []", i, key)
				continue
			}
			arr, ok := v.([]interface{})
			if !ok {
				t.Errorf("row %d %q is %T, expected []interface{}", i, key, v)
				continue
			}
			if len(arr) != 0 {
				t.Errorf("row %d %q len=%d, expected 0", i, key, len(arr))
			}
		}
	}
}

// TestThoughtsList_JSON_PopulatedForConfirmedThought — a confirmed
// thought shows up with its confirm rendered in the array.
func TestThoughtsList_JSON_PopulatedForConfirmedThought(t *testing.T) {
	root := initProject(t)
	// scope=deployment so a non-author may confirm (#147).
	id := mustWriteThoughtWithScope(t, root, "agent-a", "to be confirmed", "deployment")
	confirmThought(t, root, id, "agent-peer", "looks good")

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
	arr, ok := found["confirmed_by"].([]interface{})
	if !ok {
		t.Fatalf("confirmed_by not an array: %T", found["confirmed_by"])
	}
	if len(arr) != 1 {
		t.Fatalf("confirmed_by len=%d want 1\n%v", len(arr), found)
	}
	first := arr[0].(map[string]interface{})
	if first["agent"] != "agent-peer" {
		t.Errorf("agent=%v want agent-peer", first["agent"])
	}
	if first["evidence"] != "looks good" {
		t.Errorf("evidence=%v", first["evidence"])
	}
}
