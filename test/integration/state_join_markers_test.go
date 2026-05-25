package integration_test

// Integration tests for H2 — inline state-join markers on `thoughts list`
// and `recall`. R24 found that agents do 6-command scavenger hunts
// (lineage → confirms → refutes → retracts) to see whether a thought is
// still live; this PR injects a compact `+N/-M [RETRACTED] [PROMOTED]`
// state badge into every row so the join happens once, in the list.
//
// All seeding goes through the real CLI (`rufio think`, `rufio confirm`,
// `rufio refute`, `rufio retract`); promotion is simulated by writing the
// @auto-promote marker directly to live/promoted/<id>.gdl because the
// auto-promote engine runs only under the `rufio dev` daemon — this is
// the exact same shortcut the existing autopromote unit tests use.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// writePromotedMarker simulates the daemon's @auto-promote write so we can
// assert the [PROMOTED] badge without spawning `rufio dev`. The shape
// matches autopromote.buildAutoPromote (D13.8).
func writePromotedMarker(t *testing.T, root, thoughtID, observationID string) {
	t.Helper()
	dir := filepath.Join(root, "live", "promoted")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir promoted: %v", err)
	}
	line := fmt.Sprintf(
		"@auto-promote|thought:%s|observation:%s|by:auto-promote|ts:2026-05-20T00:00:00Z\n",
		thoughtID, observationID,
	)
	path := filepath.Join(dir, thoughtID+".gdl")
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("write promoted marker: %v", err)
	}
}

// --- (A) thoughts list TEXT ---------------------------------------------------

// TestThoughtsList_RowShowsConfirmCountInline_Text — a single @confirm on
// a thought lights up `+1` inline on its row in `thoughts list` text
// output. The "+N" token must be present so cold agents see social state
// without running `lineage`.
func TestThoughtsList_RowShowsConfirmCountInline_Text(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "to be confirmed", "deployment")
	confirmThought(t, root, id, "agent-peer", "")

	res := testutil.RunCLI(t, []string{"thoughts", "list"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, id) {
		t.Fatalf("stdout missing id=%s:\n%s", id, res.Stdout)
	}
	// Locate the row carrying this id and assert +1 lands on it.
	row := ""
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.Contains(line, id) {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("could not isolate row for id=%s in:\n%s", id, res.Stdout)
	}
	if !strings.Contains(row, "+1") {
		t.Errorf("row for confirmed thought missing +1 marker:\n%s", row)
	}
}

// TestThoughtsList_RowShowsRefuteCountInline_Text — symmetric refute.
func TestThoughtsList_RowShowsRefuteCountInline_Text(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "to be refuted", "deployment")
	refuteThought(t, root, id, "agent-skeptic", "wrong approach", "")

	res := testutil.RunCLI(t, []string{"thoughts", "list"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	row := ""
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.Contains(line, id) {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("could not isolate row for id=%s in:\n%s", id, res.Stdout)
	}
	if !strings.Contains(row, "-1") {
		t.Errorf("row for refuted thought missing -1 marker:\n%s", row)
	}
}

// TestThoughtsList_RetractedThought_ShowsRetractedMarker_Text — regression
// guard from #141 (in --include-expired mode, [RETRACTED] must survive
// the H2 marker rework).
func TestThoughtsList_RetractedThought_ShowsRetractedMarker_Text(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "to be retracted")
	retractThought(t, root, id, "agent-a", "superseded")

	res := testutil.RunCLI(t, []string{"thoughts", "list", "--include-expired"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "[RETRACTED]") {
		t.Errorf("stdout missing [RETRACTED] marker:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, id) {
		t.Errorf("stdout missing id=%s:\n%s", id, res.Stdout)
	}
}

// TestThoughtsList_PromotedDecision_ShowsPromotedMarker_Text — a thought
// whose live/promoted/<id>.gdl carries an @auto-promote record must
// surface [PROMOTED] inline.
func TestThoughtsList_PromotedDecision_ShowsPromotedMarker_Text(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "auto promoted", "deployment")
	writePromotedMarker(t, root, id, "9999999999999-zzzzzz")

	res := testutil.RunCLI(t, []string{"thoughts", "list"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	row := ""
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.Contains(line, id) {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("could not isolate row for id=%s in:\n%s", id, res.Stdout)
	}
	if !strings.Contains(row, "[PROMOTED]") {
		t.Errorf("row for promoted thought missing [PROMOTED] marker:\n%s", row)
	}
}

// TestThoughtsList_JSON_ContainsConfirmedByRefutedByPromotedAtFields —
// JSON shape gains promoted_at, promoted_by, promoted_observation fields
// (always present, null when absent — matches the #132 stability contract).
func TestThoughtsList_JSON_ContainsConfirmedByRefutedByPromotedAtFields(t *testing.T) {
	root := initProject(t)
	// Vanilla (non-promoted, non-retracted) → all three keys must be null.
	id := mustWriteThought(t, root, "agent-a", "vanilla")
	_ = id

	res := testutil.RunCLI(t, []string{"thoughts", "list", "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) < 1 {
		t.Fatalf("no JSON rows:\n%s", res.Stdout)
	}
	for i, line := range lines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("row %d invalid JSON: %v\n%s", i, err, line)
		}
		// Stability: every row carries promoted_at/by/observation keys.
		for _, key := range []string{"promoted_at", "promoted_by", "promoted_observation"} {
			v, present := obj[key]
			if !present {
				t.Errorf("row %d missing %q key (must be present, null when absent)", i, key)
				continue
			}
			if v != nil {
				t.Errorf("row %d %q=%v, want null for non-promoted thought", i, key, v)
			}
		}
	}

	// Now promote it via the marker shortcut and assert the keys populate.
	writePromotedMarker(t, root, id, "9999999999999-zzzzzz")
	res = testutil.RunCLI(t, []string{"thoughts", "list", "--json"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var found map[string]interface{}
	for _, line := range strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n") {
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
		t.Fatalf("thought %s not in JSON output:\n%s", id, res.Stdout)
	}
	if found["promoted_by"] != "auto-promote" {
		t.Errorf("promoted_by=%v want auto-promote", found["promoted_by"])
	}
	if found["promoted_observation"] != "9999999999999-zzzzzz" {
		t.Errorf("promoted_observation=%v want 9999999999999-zzzzzz", found["promoted_observation"])
	}
	if ts, _ := found["promoted_at"].(string); ts == "" {
		t.Errorf("promoted_at empty:\n%+v", found)
	}
}

// --- (B) recall TEXT ----------------------------------------------------------

// TestRecall_RowShowsConfirmCountInline_Text — a confirmed thought's
// recall row gains `+1`.
func TestRecall_RowShowsConfirmCountInline_Text(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "recall confirm", "deployment")
	confirmThought(t, root, id, "agent-peer", "")

	res := testutil.RunCLI(t, []string{"recall", "--types=thought"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	row := ""
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.Contains(line, id) {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("could not isolate row for id=%s in:\n%s", id, res.Stdout)
	}
	if !strings.Contains(row, "+1") {
		t.Errorf("recall row for confirmed thought missing +1:\n%s", row)
	}
}

// TestRecall_RowShowsRefuteCountInline_Text — symmetric refute.
func TestRecall_RowShowsRefuteCountInline_Text(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "agent-a", "recall refute", "deployment")
	refuteThought(t, root, id, "agent-skeptic", "no", "")

	res := testutil.RunCLI(t, []string{"recall", "--types=thought"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	row := ""
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.Contains(line, id) {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("could not isolate row for id=%s in:\n%s", id, res.Stdout)
	}
	if !strings.Contains(row, "-1") {
		t.Errorf("recall row for refuted thought missing -1:\n%s", row)
	}
}

// TestRecall_RetractedRow_ShowsRetractedMarker_Text — recall already
// hides retracted thoughts by default; with --include-expired the
// [RETRACTED] marker must surface inline.
func TestRecall_RetractedRow_ShowsRetractedMarker_Text(t *testing.T) {
	root := initProject(t)
	id := mustWriteThought(t, root, "agent-a", "recall retracted")
	retractThought(t, root, id, "agent-a", "superseded")

	res := testutil.RunCLI(t, []string{"recall", "--types=thought", "--include-expired"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, id) {
		t.Errorf("recall missing retracted id=%s under --include-expired:\n%s", id, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "[RETRACTED]") {
		t.Errorf("recall missing [RETRACTED] marker:\n%s", res.Stdout)
	}
}

// TestRecall_JSON_PreservesShape — regression guard: every key the
// existing recall --json consumer relied on (_type, id, ts, author,
// subject, predicate, object, content, scope, path, retracted,
// confirmed_by, origin, source) MUST still be present. New keys may be
// added; old keys must not be removed.
func TestRecall_JSON_PreservesShape(t *testing.T) {
	root := initProject(t)
	seedThought(t, root, "agent-a", "customer:5821", "shape probe", "fleet")

	res := testutil.RunCLI(t, []string{"recall", "--json"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) < 1 {
		t.Fatalf("no JSON rows:\n%s", res.Stdout)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, lines[0])
	}
	for _, key := range []string{
		"_type", "id", "ts", "author", "subject", "predicate",
		"object", "content", "scope", "path", "retracted",
		"confirmed_by", "origin", "source",
	} {
		if _, present := obj[key]; !present {
			t.Errorf("recall --json row missing legacy key %q (shape regression):\n%v", key, obj)
		}
	}
}
