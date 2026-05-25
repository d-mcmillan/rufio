package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// TestQuickstart_PrintsCard pins the human path: `rufio quickstart`
// prints the locked cold-start card to stdout with exit 0. Cold agents
// reach for this verb before they know what else exists; failing it
// silently breaks the onboarding contract.
func TestQuickstart_PrintsCard(t *testing.T) {
	res := testutil.RunCLI(t, []string{"quickstart"}, t.TempDir(), nil)
	if res.Code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "Rufio — quickstart") {
		t.Errorf("output missing card header; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "rufio attend") {
		t.Errorf("output missing the first verb")
	}
	if !strings.Contains(res.Stdout, "rufio listen --catch-up") {
		t.Errorf("output missing the listen verb")
	}
}

// TestQuickstart_NoProjectNeeded pins that the verb runs outside a
// rufio project. Cold-start = pre-project; if the verb required a
// project root it could never be invoked first.
func TestQuickstart_NoProjectNeeded(t *testing.T) {
	res := testutil.RunCLI(t, []string{"quickstart"}, t.TempDir(), nil)
	if res.Code != 0 {
		t.Fatalf("exit = %d, want 0 (no project root needed); stderr=%q", res.Code, res.Stderr)
	}
}

// TestQuickstart_JSONMode pins the machine path: --json emits a single
// JSON object matching the locked schema {_type, _version, content,
// card_version}. MCP tool #21 ships the same shape.
func TestQuickstart_JSONMode(t *testing.T) {
	res := testutil.RunCLI(t, []string{"quickstart", "--json"}, t.TempDir(), nil)
	if res.Code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", res.Code, res.Stderr)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(res.Stdout)), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nstdout=%q", err, res.Stdout)
	}
	if got["_type"] != "quickstart" {
		t.Errorf("_type = %v, want %q", got["_type"], "quickstart")
	}
	if got["_version"].(float64) != 1 {
		t.Errorf("_version = %v, want 1", got["_version"])
	}
	if got["card_version"].(float64) != 1 {
		t.Errorf("card_version = %v, want 1", got["card_version"])
	}
	content, ok := got["content"].(string)
	if !ok || content == "" {
		t.Fatalf("content missing or wrong type: %v", got["content"])
	}
	if !strings.Contains(content, "rufio attend") {
		t.Errorf("content missing first verb")
	}
}
