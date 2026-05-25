package goal

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// RenderJSON is the single source of truth for the goals JSON shape
// (consumed by `rufio goals list --json` AND the MCP goals_list tool).
// These tests pin the locked-key contract, the conditional audit-key
// emission, and the JSONL framing so the two consumers can never drift.

func TestRenderJSON_EmptyInput_EmptyOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("buf=%q want empty", buf.String())
	}
}

func TestRenderJSON_ActiveGoal_LockedKeysOnly_NoAuditKeys(t *testing.T) {
	var buf bytes.Buffer
	g := Goal{
		ID: "1-aaaaaa", Author: "agent-a", Statement: "ship it",
		Scope: "fleet", TS: "2026-05-19T12:00:00Z", State: StateActive,
	}
	if err := RenderJSON(&buf, []Goal{g}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%q", err, buf.String())
	}
	want := map[string]interface{}{
		"_type": "goal", "_version": "1", "id": "1-aaaaaa",
		"author": "agent-a", "statement": "ship it", "scope": "fleet",
		"ts": "2026-05-19T12:00:00Z", "state": "active",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s=%v want %v", k, got[k], v)
		}
	}
	// No audit key may appear for an active goal with empty optional fields.
	for _, k := range []string{
		"by", "parent", "completed_by", "completed_at", "outcome",
		"abandoned_by", "abandoned_at", "reason",
	} {
		if _, present := got[k]; present {
			t.Errorf("active goal must not emit audit key %q, got %v", k, got[k])
		}
	}
}

func TestRenderJSON_CompletedGoal_EmitsCompletionAuditKeys(t *testing.T) {
	var buf bytes.Buffer
	g := Goal{
		ID: "2-bbbbbb", Author: "agent-a", Statement: "done thing",
		By: "EOW", Parent: "1-aaaaaa", Scope: "agent",
		TS: "2026-05-19T12:00:00Z", State: StateCompleted,
		CompletedBy: "agent-a", CompletedAt: "2026-05-19T13:00:00Z",
		Outcome: "shipped",
	}
	if err := RenderJSON(&buf, []Goal{g}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	for k, v := range map[string]interface{}{
		"by": "EOW", "parent": "1-aaaaaa", "completed_by": "agent-a",
		"completed_at": "2026-05-19T13:00:00Z", "outcome": "shipped",
		"state": "completed",
	} {
		if got[k] != v {
			t.Errorf("%s=%v want %v", k, got[k], v)
		}
	}
	// Abandonment keys must NOT appear on a completed goal.
	for _, k := range []string{"abandoned_by", "abandoned_at", "reason"} {
		if _, present := got[k]; present {
			t.Errorf("completed goal must not emit %q", k)
		}
	}
}

func TestRenderJSON_AbandonedGoal_EmitsAbandonmentAuditKeys(t *testing.T) {
	var buf bytes.Buffer
	g := Goal{
		ID: "3-cccccc", Author: "agent-a", Statement: "dropped thing",
		Scope: "agent", TS: "2026-05-19T12:00:00Z", State: StateAbandoned,
		AbandonedBy: "agent-a", AbandonedAt: "2026-05-19T14:00:00Z",
		Reason: "deprioritised",
	}
	if err := RenderJSON(&buf, []Goal{g}); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got)
	for k, v := range map[string]interface{}{
		"abandoned_by": "agent-a", "abandoned_at": "2026-05-19T14:00:00Z",
		"reason": "deprioritised", "state": "abandoned",
	} {
		if got[k] != v {
			t.Errorf("%s=%v want %v", k, got[k], v)
		}
	}
	for _, k := range []string{"completed_by", "completed_at", "outcome"} {
		if _, present := got[k]; present {
			t.Errorf("abandoned goal must not emit %q", k)
		}
	}
}

func TestRenderJSON_JSONLFraming_OneLinePerGoalTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	rows := []Goal{
		{ID: "1", Author: "a", Statement: "x", Scope: "agent", TS: "t1", State: StateActive},
		{ID: "2", Author: "a", Statement: "y", Scope: "agent", TS: "t2", State: StateActive},
	}
	if err := RenderJSON(&buf, rows); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.HasSuffix(s, "\n") {
		t.Errorf("output must end with a newline (JSONL framing): %q", s)
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 JSONL lines, got %d:\n%q", len(lines), s)
	}
	for i, l := range lines {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Errorf("line %d not valid JSON: %v\n%q", i, err, l)
		}
	}
}
