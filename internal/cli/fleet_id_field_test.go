// Package cli — RED tests for the L4 minor-cleanup: `fleet --json`
// field rename `.agent` → `.id` to match every other command's
// convention. The `.agent` key is preserved as a DEPRECATED alias for
// one version so existing consumers keep working.
//
// R26 finding: `fleet --json` was the only `--json` surface using a
// non-canonical id key — every other command (channels list, channel
// show, summons, recall) uses `.id`. The L4 fix writes BOTH keys with
// the same value during the deprecation window.
package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// fleetRow exposes the JSON shape we care about: both .id (the new
// canonical key) and .agent (the deprecated alias). Both MUST be
// populated for the deprecation window so consumers can migrate without
// breakage.
type fleetRow struct {
	ID    string `json:"id"`
	Agent string `json:"agent"`
}

// runFleetJSONForTest runs renderFleetJSON over a tiny synthetic input
// and returns the captured stdout (newline-separated JSONL).
func runFleetJSONForTest(t *testing.T) string {
	t.Helper()
	rows := []fleetAgent{
		{Agent: "alice", HasAttention: true, Intent: "test", LastSeen: "2026-05-12T12:00:00Z"},
		{Agent: "bob", HasAttention: false, LastSeen: "2026-05-12T11:00:00Z"},
	}
	return captureStdout(t, func() {
		// JSON flag set so renderFleetJSON's WriteJSONL emits.
		if err := renderFleetJSON(rows, output.RenderOpts{JSON: true}); err != nil {
			t.Fatalf("renderFleetJSON: %v", err)
		}
	})
}

// TestFleet_JSON_HasIDField — L4 RED. `.id` MUST be populated for every
// row in `fleet --json` output.
func TestFleet_JSON_HasIDField(t *testing.T) {
	out := runFleetJSONForTest(t)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r fleetRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		if r.ID == "" {
			t.Errorf("fleet --json row is missing .id field: %s", line)
		}
	}
}

// TestFleet_JSON_HasAgentField_DeprecatedAlias — L4 RED. `.agent` MUST
// continue to be populated as a deprecated alias for the full
// deprecation window (one version per the L4 spec).
func TestFleet_JSON_HasAgentField_DeprecatedAlias(t *testing.T) {
	out := runFleetJSONForTest(t)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r fleetRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		if r.Agent == "" {
			t.Errorf("fleet --json row is missing .agent deprecated-alias field: %s", line)
		}
	}
}

// TestFleet_JSON_IDAndAgent_AreEqual — L4 RED. The two keys MUST carry
// the same value; a divergence would mean consumers transitioning from
// .agent to .id silently get different data.
func TestFleet_JSON_IDAndAgent_AreEqual(t *testing.T) {
	out := runFleetJSONForTest(t)
	var rows []fleetRow
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r fleetRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		t.Fatal("no rows captured; renderFleetJSON emitted nothing")
	}
	for _, r := range rows {
		if r.ID != r.Agent {
			t.Errorf(".id=%q .agent=%q must be equal (deprecation alias contract)", r.ID, r.Agent)
		}
	}
}
