// Package cli — unit tests for #141 `[RETRACTED]` marker in
// `thoughts list`. After `rufio retract <id>`, the row MUST surface
// inline in the default listing with a [RETRACTED] prefix (text) and
// retracted: true with retracted_at/by/reason populated (JSON).
package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/output"
)

func TestThoughtsList_RetractedThought_DefaultShowsMarker_Text(t *testing.T) {
	root := scopeTestProject(t, "alice")

	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:1", Content: "X",
		Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed think: %v", err)
	}
	id := mustGetSoleThoughtID(t, root)

	if err := runRetract(root, id, "superseded", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runRetract: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runThoughtsList(root, "", false, false, output.RenderOpts{}); err != nil {
			t.Fatalf("runThoughtsList: %v", err)
		}
	})

	if !strings.Contains(out, "[RETRACTED]") {
		t.Errorf("default thoughts list missing [RETRACTED] marker for retracted row:\n%s", out)
	}
	if !strings.Contains(out, `retract_reason:"superseded"`) {
		t.Errorf("default thoughts list missing retract_reason superseded:\n%s", out)
	}
}

func TestThoughtsList_RetractedThought_DefaultShowsMarker_JSON(t *testing.T) {
	root := scopeTestProject(t, "alice")

	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:1", Content: "X",
		Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed think: %v", err)
	}
	id := mustGetSoleThoughtID(t, root)

	if err := runRetract(root, id, "superseded", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runRetract: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runThoughtsList(root, "", false, false, output.RenderOpts{JSON: true}); err != nil {
			t.Fatalf("runThoughtsList: %v", err)
		}
	})

	var found map[string]interface{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("invalid JSON %q: %v", line, err)
		}
		if obj["id"] == id {
			found = obj
			break
		}
	}
	if found == nil {
		t.Fatalf("retracted thought %s not in default JSON output:\n%s", id, out)
	}
	if found["retract_reason"] != "superseded" {
		t.Errorf("retract_reason=%v want superseded", found["retract_reason"])
	}
	if found["retracted_by"] != "alice" {
		t.Errorf("retracted_by=%v want alice", found["retracted_by"])
	}
	if ts, _ := found["retracted_at"].(string); ts == "" {
		t.Errorf("retracted_at empty: %+v", found)
	}
}

func TestThoughtsList_NonRetractedRow_NoBracketMarker_Text(t *testing.T) {
	root := scopeTestProject(t, "alice")

	if err := runThink(root, thinkArgs{
		Type: "hypothesis", Subject: "test:1", Content: "X",
		Scope: "fleet",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("seed think: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runThoughtsList(root, "", false, false, output.RenderOpts{}); err != nil {
			t.Fatalf("runThoughtsList: %v", err)
		}
	})

	if strings.Contains(out, "[RETRACTED]") {
		t.Errorf("non-retracted row leaked [RETRACTED] artifact:\n%s", out)
	}
	if strings.Contains(out, "retract_reason:") {
		t.Errorf("non-retracted row leaked retract_reason column:\n%s", out)
	}
}

func TestThoughtsList_EmptyProject_NoOutput(t *testing.T) {
	root := scopeTestProject(t, "alice")

	out := captureStdout(t, func() {
		if err := runThoughtsList(root, "", false, false, output.RenderOpts{}); err != nil {
			t.Fatalf("runThoughtsList: %v", err)
		}
	})

	if strings.TrimSpace(out) != "" {
		t.Errorf("empty project must produce no rows; got:\n%s", out)
	}
}

// mustGetSoleThoughtID parses live/outbox/alice/*.gdl and returns the
// id of the single @thought it expects to find.
func mustGetSoleThoughtID(t *testing.T, root string) string {
	t.Helper()
	var captured string
	out := captureStdout(t, func() {
		if err := runThoughtsList(root, "", false, false, output.RenderOpts{JSON: true}); err != nil {
			t.Fatalf("runThoughtsList probe: %v", err)
		}
	})
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("probe parse: %v", err)
		}
		if id, _ := obj["id"].(string); id != "" {
			captured = id
			break
		}
	}
	if captured == "" {
		t.Fatalf("no thought id found in probe output:\n%s", out)
	}
	return captured
}
