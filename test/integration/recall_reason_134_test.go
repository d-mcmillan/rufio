package integration_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// GH #134 regression suite. The cold-start round 6 vet report:
//
//	$ rufio recall --types=reason
//	2026-05-20T07:13:26  reason  cold-r6  step A
//	2026-05-20T07:14:21  reason  cold-r6  orphaned, no --decision
//	# no IDs, no decision-linkage
//	$ rufio recall --types=reason --json | jq '.[0] | {id, decision, path}'
//	# id="", decision absent — only `path` reveals decision linkage
//
// A cold agent could not (a) extract a reason's own id to feed a follow-up
// `--parent=`, nor (b) tell which decision a reason belonged to without
// substring-parsing the file path. The H1c renderer + scanReasoning
// populate ID and decision (8186dc3); these tests pin the end-to-end
// contract so a future refactor cannot silently regress.
//
// Wire-contract notes (the Python SDK + MCP adapter consume the JSON):
//   - text row shape after H1c is TAB-separated:
//     <reltime>\t<type[:short-decision-id]>\t<author>\t<id>\t<key>\t<scope>
//     Decision linkage rides in the type column (`reason:<short-did>`);
//     the reason's own id is the 4th field. This is what the cold agent
//     `awk -F '\t' '{print $4}'` already does.
//   - JSON: `id` is always populated for reason records; `decision` is
//     present (omitempty) ONLY when the reason was written with
//     --decision=<id>. Orphan reasons omit the key entirely.

// TestRecall_Reason_134_TextSurfacesIDAndDecision pins the user-facing
// `rufio recall --types=reason` text output: both linked and orphan
// reasons surface their own id in the unified id column, and a linked
// reason carries the decision short-id in the type column.
func TestRecall_Reason_134_TextSurfacesIDAndDecision(t *testing.T) {
	root := initProject(t)

	// Seed a real decision (--decision must point at a real type:decision
	// thought per GH #77). The returned id is the full canonical token.
	decisionID := mustWriteDecision(t, root, "agent-a", "customer:1", "approve", "fleet")

	// Linked reason: nests under live/reasoning/agent-a/<decisionID>/.
	resLinked := testutil.RunCLI(t, []string{
		"reason", "--content=step A", "--decision=" + decisionID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if resLinked.Code != 0 {
		t.Fatalf("seed linked reason: exit=%d stderr=%q", resLinked.Code, resLinked.Stderr)
	}

	// Orphan reason: lives directly under live/reasoning/agent-a/ with
	// no decision-dir layer. The writer contract (D7.1) allows --decision
	// to be omitted; #134's suggestion was explicitly NOT to forbid this.
	resOrphan := testutil.RunCLI(t, []string{
		"reason", "--content=orphaned, no --decision",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if resOrphan.Code != 0 {
		t.Fatalf("seed orphan reason: exit=%d stderr=%q", resOrphan.Code, resOrphan.Stderr)
	}

	// Cross-check the on-disk layout to nail down which file each reason
	// landed in — the file basename is the canonical reason-id the recall
	// layer must surface.
	linkedMatches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "agent-a", decisionID, "*.gdl"))
	if len(linkedMatches) != 1 {
		t.Fatalf("want 1 linked reason file under decision dir, got %d (%v)", len(linkedMatches), linkedMatches)
	}
	linkedReasonID := strings.TrimSuffix(filepath.Base(linkedMatches[0]), ".gdl")
	orphanMatches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "agent-a", "*.gdl"))
	// orphanMatches will also include the linked one's directory entry on
	// some filesystems if the glob crosses dir boundaries; Go's filepath.Glob
	// is shell-style and does NOT recurse, so only top-level *.gdl matches.
	if len(orphanMatches) != 1 {
		t.Fatalf("want exactly 1 orphan reason file at top level, got %d (%v)", len(orphanMatches), orphanMatches)
	}
	orphanReasonID := strings.TrimSuffix(filepath.Base(orphanMatches[0]), ".gdl")

	// `rufio recall --types=reason` — text output. RunCLI defaults
	// RUFIO_FULL_IDS=1 so the canonical ids appear literally (the
	// short-id default is exercised separately by the H1c unit tests).
	res := testutil.RunCLI(t, []string{"recall", "--types=reason"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("recall --types=reason: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 reason rows, got %d:\n%s", len(lines), res.Stdout)
	}

	// Locate each row by its id (sort order is ts-DESC across types but
	// both reasons land in the same second; the test stays robust by
	// looking each up by id rather than by index).
	var linkedRow, orphanRow string
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			t.Fatalf("reason row has <4 TAB fields, breaks the unified contract: %q", line)
		}
		switch fields[3] {
		case linkedReasonID:
			linkedRow = line
		case orphanReasonID:
			orphanRow = line
		}
	}
	if linkedRow == "" {
		t.Fatalf("linked reason id %q not surfaced in text output (#134):\n%s", linkedReasonID, res.Stdout)
	}
	if orphanRow == "" {
		t.Fatalf("orphan reason id %q not surfaced in text output (#134):\n%s", orphanReasonID, res.Stdout)
	}

	// Linked row: the type column MUST carry the decision linkage
	// (`reason:<decisionID>`). This is the #134 fix: the cold agent
	// can scan column 2 to learn which decision a reason belongs to,
	// instead of substring-parsing the file path.
	linkedFields := strings.Split(linkedRow, "\t")
	if linkedFields[1] != "reason:"+decisionID {
		t.Errorf("linked reason type column = %q, want %q (#134 decision-linkage):\nrow=%q", linkedFields[1], "reason:"+decisionID, linkedRow)
	}

	// Orphan row: type column is the bare `reason` literal (no trailing
	// colon — that was an early bug fixed by H1c). NO decisionID anywhere
	// on the row (it doesn't belong to one).
	orphanFields := strings.Split(orphanRow, "\t")
	if orphanFields[1] != "reason" {
		t.Errorf("orphan reason type column = %q, want bare %q:\nrow=%q", orphanFields[1], "reason", orphanRow)
	}
	if strings.Contains(orphanRow, decisionID) {
		t.Errorf("orphan reason row leaks unrelated decisionID %q:\nrow=%q", decisionID, orphanRow)
	}
}

// TestRecall_Reason_134_JSONSurfacesIDAndDecision pins the JSON
// wire-contract consumed by the Python SDK and the MCP adapter:
// `id` is non-empty for every reason record, and `decision` is present
// (with the full decision-id) for linked reasons. Orphan reasons omit
// the `decision` key entirely (omitempty — the consumer can branch on
// presence rather than disambiguating "" vs missing).
func TestRecall_Reason_134_JSONSurfacesIDAndDecision(t *testing.T) {
	root := initProject(t)

	decisionID := mustWriteDecision(t, root, "agent-a", "customer:1", "approve", "fleet")

	// Linked reason.
	if r := testutil.RunCLI(t, []string{
		"reason", "--content=step A", "--decision=" + decisionID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"}); r.Code != 0 {
		t.Fatalf("seed linked reason: exit=%d stderr=%q", r.Code, r.Stderr)
	}
	// Orphan reason.
	if r := testutil.RunCLI(t, []string{
		"reason", "--content=orphaned",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"}); r.Code != 0 {
		t.Fatalf("seed orphan reason: exit=%d stderr=%q", r.Code, r.Stderr)
	}

	res := testutil.RunCLI(t, []string{"recall", "--types=reason", "--json"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("recall --types=reason --json: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	// JSON output is JSONL (one object per line). Decode each.
	type reasonRow struct {
		Type     string `json:"_type"`
		ID       string `json:"id"`
		Content  string `json:"content"`
		Decision string `json:"decision"` // omitempty on the wire
	}
	var rows []reasonRow
	// Also keep raw decoded objects so we can confirm the `decision`
	// key is ABSENT for orphans (an empty string would satisfy the
	// typed decode above; only the raw map distinguishes "missing key"
	// from "empty value").
	var raws []map[string]interface{}
	for _, line := range strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		var row reasonRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("decode reason row %q: %v", line, err)
		}
		rows = append(rows, row)
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("decode reason row raw %q: %v", line, err)
		}
		raws = append(raws, raw)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 reason rows, got %d:\n%s", len(rows), res.Stdout)
	}

	// Pair rows with their raw maps and find linked vs orphan by content
	// (substring is stable; content is what the cold agent originally
	// typed, NOT a substrate-internal id).
	var linkedRow, orphanRow reasonRow
	var linkedRaw, orphanRaw map[string]interface{}
	for i, r := range rows {
		switch {
		case strings.Contains(r.Content, "step A"):
			linkedRow = r
			linkedRaw = raws[i]
		case strings.Contains(r.Content, "orphaned"):
			orphanRow = r
			orphanRaw = raws[i]
		}
	}
	if linkedRow.ID == "" {
		t.Errorf("linked reason JSON id is empty (#134 wire-contract violation): %+v", linkedRow)
	}
	if orphanRow.ID == "" {
		t.Errorf("orphan reason JSON id is empty (#134 wire-contract violation): %+v", orphanRow)
	}
	if linkedRow.Decision != decisionID {
		t.Errorf("linked reason JSON decision=%q, want %q (#134):\n%+v", linkedRow.Decision, decisionID, linkedRow)
	}
	// Orphan: the `decision` key MUST be absent from the wire payload
	// (omitempty), not present-but-empty. SDK consumers branch on
	// presence to detect linkage.
	if _, present := orphanRaw["decision"]; present {
		t.Errorf("orphan reason JSON carries unexpected `decision` key: %+v", orphanRaw)
	}
	// And the linked raw MUST carry it (sanity check the omitempty
	// boundary works in both directions).
	if _, present := linkedRaw["decision"]; !present {
		t.Errorf("linked reason JSON missing `decision` key: %+v", linkedRaw)
	}
}

// TestRecall_Reason_134_DecisionIDPipeableAsParent closes the cold-agent
// loop the issue called out: extract a reason's id from `recall --json`
// and feed it back into a follow-up `reason --parent=<id>` call. If id
// were empty (the bug), this would 500-equivalent at the writer (the
// id-shape validator rejects empty/malformed parent values).
func TestRecall_Reason_134_DecisionIDPipeableAsParent(t *testing.T) {
	root := initProject(t)
	decisionID := mustWriteDecision(t, root, "agent-a", "customer:1", "approve", "fleet")

	if r := testutil.RunCLI(t, []string{
		"reason", "--content=root step", "--decision=" + decisionID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"}); r.Code != 0 {
		t.Fatalf("seed root reason: exit=%d stderr=%q", r.Code, r.Stderr)
	}

	res := testutil.RunCLI(t, []string{"recall", "--types=reason", "--json"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("recall --json: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	var row struct {
		ID string `json:"id"`
	}
	first := strings.SplitN(strings.TrimSpace(res.Stdout), "\n", 2)[0]
	if err := json.Unmarshal([]byte(first), &row); err != nil {
		t.Fatalf("decode first row: %v", err)
	}
	if row.ID == "" {
		t.Fatalf("recall JSON dropped reason id (#134): %q", first)
	}

	// Feed that id back as --parent on a follow-up reason. The writer
	// validates --parent shape (must look like an id); an empty value
	// fails before any file is written.
	r2 := testutil.RunCLI(t, []string{
		"reason", "--content=child step",
		"--parent=" + row.ID,
		"--decision=" + decisionID,
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if r2.Code != 0 {
		t.Fatalf("follow-up reason with --parent=<recalled id>: exit=%d stderr=%q (id=%q)", r2.Code, r2.Stderr, row.ID)
	}
}
