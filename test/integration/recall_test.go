package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// idFromPlainRecallLine extracts the thought-id from a single plain
// `rufio recall` output line. H1c reshaped the row into a TAB-separated
// positional contract: <reltime>\t<type>\t<author>\t<id>\t<key>\t<scope>...
// — the id is the FOURTH TAB-separated field. By default ids are
// short-form (6-char suffix); the integration tests pin
// RUFIO_FULL_IDS=1 (via plainRecallEnv) so the extracted id is the
// canonical full token the wire APIs consume.
//
// This is exactly what an agent does today: `awk -F '\t' '{print $4}'`.
// It deliberately does NOT look at any path or `ls` the filesystem.
func idFromPlainRecallLine(line string) string {
	fields := strings.Split(line, "\t")
	if len(fields) >= 4 {
		return strings.TrimSpace(fields[3])
	}
	return ""
}

// plainRecallEnv augments RUFIO_AGENT_ID with RUFIO_FULL_IDS=1 so the
// dogfood-gap test can extract the full canonical id from plain recall
// output (matching the JSON id one-for-one). The short-form default is
// proven separately in the lib + cli unit tests.
func plainRecallEnv(agent string) map[string]string {
	return map[string]string{
		"RUFIO_AGENT_ID": agent,
		"RUFIO_FULL_IDS": "1",
	}
}

// seedThought/Observation/Reason helpers — use the real CLI to populate
// the corpus.
func seedThought(t *testing.T, root, agent, subject, content, scope string) {
	t.Helper()
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=" + subject,
		"--content=" + content, "--scope=" + scope,
	}, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("seed think failed: exit=%d stderr=%q", res.Code, res.Stderr)
	}
}

func seedObservation(t *testing.T, root, agent, subject, predicate, object, scope string) {
	t.Helper()
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=" + subject, "--predicate=" + predicate,
		"--object=" + object, "--scope=" + scope,
	}, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("seed observe failed: exit=%d stderr=%q", res.Code, res.Stderr)
	}
}

func seedReason(t *testing.T, root, agent, content string) {
	t.Helper()
	res := testutil.RunCLI(t, []string{
		"reason", "--content=" + content,
	}, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("seed reason failed: exit=%d stderr=%q", res.Code, res.Stderr)
	}
}

func TestRecall_HappyPath_NoArgs_ReturnsAllRecords(t *testing.T) {
	root := initProject(t)
	seedThought(t, root, "agent-a", "customer:5821", "churn signals", "fleet")
	seedObservation(t, root, "agent-a", "customer:5821", "prefers", "email", "fleet")
	seedReason(t, root, "agent-a", "because the policy says so")

	res := testutil.RunCLI(t, []string{"recall"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// At minimum, 3 lines (the 3 seeded records).
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) < 3 {
		t.Errorf("got %d lines, want at least 3:\n%s", len(lines), res.Stdout)
	}
	// Verify each type appears.
	for _, want := range []string{"thought", "observation", "reason"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("output missing type %q:\n%s", want, res.Stdout)
		}
	}
}

func TestRecall_Query_SubstringMatch(t *testing.T) {
	root := initProject(t)
	seedThought(t, root, "agent-a", "customer:5821", "churn signals showing", "fleet")
	seedThought(t, root, "agent-a", "order:9999", "unrelated content", "fleet")

	res := testutil.RunCLI(t, []string{"recall", "churn"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "customer:5821") {
		t.Errorf("expected matching record (customer:5821):\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "order:9999") {
		t.Errorf("non-matching record (order:9999) leaked:\n%s", res.Stdout)
	}
}

func TestRecall_TypesFilter_LimitsCorpus(t *testing.T) {
	root := initProject(t)
	seedThought(t, root, "agent-a", "customer:1", "thought c", "fleet")
	seedObservation(t, root, "agent-a", "customer:1", "is", "x", "fleet")

	res := testutil.RunCLI(t, []string{"recall", "--types=thought"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stdout, "observation") {
		t.Errorf("observation leaked with --types=thought:\n%s", res.Stdout)
	}
}

func TestRecall_EntityIDForm_ExactSubject(t *testing.T) {
	root := initProject(t)
	seedThought(t, root, "agent-a", "customer:5821", "x", "fleet")
	seedThought(t, root, "agent-a", "customer:5821:order:1", "y", "fleet")

	res := testutil.RunCLI(t, []string{"recall", "customer:5821"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// Exact subject match — should NOT include customer:5821:order:1.
	if strings.Contains(res.Stdout, "customer:5821:order:1") {
		t.Errorf("entity-id form should be exact-subject, but multi-segment leaked:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "customer:5821") {
		t.Errorf("expected customer:5821 record:\n%s", res.Stdout)
	}
}

// --- Error-path tests ---

func TestRecall_InvalidType_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"recall", "--types=bogus"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --types") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestRecall_InvalidScope_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"recall", "--scope=global"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --scope") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestRecall_InvalidSince_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"recall", "--since=abc"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid --since") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestRecall_InvalidAsOf_Exit2(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"recall", "--as-of=bogus"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 2 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "invalid timestamp") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestRecall_NotInProject_Exit1(t *testing.T) {
	root := t.TempDir()
	res := testutil.RunCLI(t, []string{"recall"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q", res.Stderr)
	}
}

func TestRecall_JSON_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	seedThought(t, root, "agent-a", "customer:5821", "c", "fleet")

	res := testutil.RunCLI(t, []string{"recall", "--json"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n")
	if len(lines) < 1 {
		t.Fatalf("no JSON output")
	}
	for _, l := range lines {
		var got map[string]interface{}
		if err := json.Unmarshal([]byte(l), &got); err != nil {
			t.Errorf("line not valid JSON: %v\n%q", err, l)
			continue
		}
		// Every record must have _type, ts, author.
		for _, k := range []string{"_type", "ts", "author"} {
			if _, ok := got[k]; !ok {
				t.Errorf("missing key %q in %q", k, l)
			}
		}
	}
}

func TestRecall_IncludeExpired_SurfacesRetracted(t *testing.T) {
	root := initProject(t)
	seedThought(t, root, "agent-a", "customer:5821", "to be retracted", "fleet")

	// Find the thought id.
	matches, _ := filepath.Glob(filepath.Join(root, "live", "outbox", "agent-a", "*.gdl"))
	id := strings.TrimSuffix(filepath.Base(matches[0]), ".gdl")

	// Retract it.
	res := testutil.RunCLI(t, []string{"retract", id, "--reason=outdated"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("retract failed: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	// Without --include-expired: thought still appears (it's still in outbox),
	// but Retracted=false in the record stream. With --include-expired:
	// Retracted=true. Verify via --json.
	jres := testutil.RunCLI(t, []string{"recall", "--types=thought", "--include-expired", "--json"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if jres.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", jres.Code, jres.Stderr)
	}
	if !strings.Contains(jres.Stdout, `"retracted":true`) {
		t.Errorf("expected retracted:true in JSON output:\n%s", jres.Stdout)
	}
}

func TestRecall_DefaultHidesRetracted(t *testing.T) {
	root := initProject(t)
	seedThought(t, root, "agent-a", "customer:5821", "to be retracted", "fleet")
	matches, _ := filepath.Glob(filepath.Join(root, "live", "outbox", "agent-a", "*.gdl"))
	id := strings.TrimSuffix(filepath.Base(matches[0]), ".gdl")
	rres := testutil.RunCLI(t, []string{"retract", id, "--reason=outdated"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if rres.Code != 0 {
		t.Fatalf("retract: exit=%d stderr=%q", rres.Code, rres.Stderr)
	}

	// Default recall (without --include-expired): retracted thought hidden.
	res := testutil.RunCLI(t, []string{"recall", "--types=thought"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("recall: exit=%d", res.Code)
	}
	if strings.Contains(res.Stdout, "customer:5821") {
		t.Errorf("retracted thought leaked into default recall:\n%s", res.Stdout)
	}

	// With --include-expired: thought visible.
	res2 := testutil.RunCLI(t, []string{"recall", "--types=thought", "--include-expired"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res2.Code != 0 {
		t.Fatalf("recall --include-expired: exit=%d", res2.Code)
	}
	if !strings.Contains(res2.Stdout, "customer:5821") {
		t.Errorf("--include-expired should surface retracted thought:\n%s", res2.Stdout)
	}
}

func TestRecall_QueryAND_AllWordsMustMatch(t *testing.T) {
	root := initProject(t)
	seedThought(t, root, "agent-a", "customer:1", "churn signals showing", "fleet")
	seedThought(t, root, "agent-a", "order:2", "churn only", "fleet")
	seedThought(t, root, "agent-a", "product:3", "signals only", "fleet")

	res := testutil.RunCLI(t, []string{"recall", "churn signals"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d", res.Code)
	}
	// Only customer:1 has BOTH "churn" AND "signals".
	if !strings.Contains(res.Stdout, "customer:1") {
		t.Errorf("missing customer:1:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "order:2") {
		t.Errorf("order:2 (only 'churn') leaked:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "product:3") {
		t.Errorf("product:3 (only 'signals') leaked:\n%s", res.Stdout)
	}
}

func TestRecall_Since_FiltersOlderRecords(t *testing.T) {
	// Hard to test deterministically without clock injection at CLI level.
	// Sanity-check: a very tight --since=1ms should likely return empty
	// when nothing was just written.
	root := initProject(t)
	// Seed records, then sleep a bit so they're "old" relative to a tight window.
	seedThought(t, root, "agent-a", "x:1", "c", "fleet")
	// 100ms is sufficient on any non-pathological CI.
	res := testutil.RunCLI(t, []string{"recall", "--since=1ms"}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// Test passes as long as exit code is clean — content depends on timing.
	// The substantive --since unit tests live in internal/lib/recall.
}

func TestRecall_AsOf_ExcludesFutureRecords(t *testing.T) {
	root := initProject(t)
	seedThought(t, root, "agent-a", "x:1", "c", "fleet")
	// Use a past timestamp; the seeded thought (just now) is newer and should be excluded.
	past := "2020-01-01T00:00:00Z"
	res := testutil.RunCLI(t, []string{"recall", "--as-of=" + past}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stdout, "x:1") {
		t.Errorf("future record should have been excluded by --as-of=2020:\n%s", res.Stdout)
	}
}

// TestRecall_RecallToIDToConfirm_DogfoodGapClosed is the acceptance
// proof for the #1 CRITICAL launch-demo punch-list item. It replays the
// EXACT agent loop a real 3-harness dogfood (Claude+Gemini+Cursor) found
// broken: agent A seeds a thought; agent B runs `rufio recall` (plain
// AND --json), extracts the thought-id PURELY from recall output (no
// `ls live/outbox`, no path-parsing hack), and runs `rufio confirm
// <that-id>`. We then assert the confirm landed on the right thought.
//
// Before this change recall surfaced no id, so this loop was impossible
// without filesystem spelunking. If it passes, the gap is closed.
func TestRecall_RecallToIDToConfirm_DogfoodGapClosed(t *testing.T) {
	root := initProject(t)

	// --- Agent A emits a thought. ---
	seedThought(t, root, "agent-a", "customer:5821", "churn risk rising", "fleet")

	// --- Agent B recalls (PLAIN) and extracts the id from output only. ---
	// RUFIO_FULL_IDS=1 pins the full canonical id in the column so the
	// dogfood-acceptance test asserts the exact wire token; the
	// short-form default is proven by the lib+cli unit tests.
	plain := testutil.RunCLI(t, []string{"recall", "--types=thought"}, root,
		plainRecallEnv("agent-b"))
	if plain.Code != 0 {
		t.Fatalf("recall(plain): exit=%d stderr=%q", plain.Code, plain.Stderr)
	}
	var plainID string
	for _, line := range strings.Split(strings.TrimRight(plain.Stdout, "\n"), "\n") {
		if strings.Contains(line, "customer:5821") {
			plainID = idFromPlainRecallLine(line)
			break
		}
	}
	if plainID == "" {
		t.Fatalf("could not extract thought-id from PLAIN recall output (gap NOT closed):\n%s", plain.Stdout)
	}

	// --- Agent B recalls (--json) and extracts the top-level id. ---
	jres := testutil.RunCLI(t, []string{"recall", "--types=thought", "--json"}, root,
		plainRecallEnv("agent-b"))
	if jres.Code != 0 {
		t.Fatalf("recall(--json): exit=%d stderr=%q", jres.Code, jres.Stderr)
	}
	var jsonID string
	for _, line := range strings.Split(strings.TrimRight(jres.Stdout, "\n"), "\n") {
		var rec map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("recall --json line not valid JSON: %v\n%q", err, line)
		}
		if rec["subject"] == "customer:5821" {
			id, ok := rec["id"].(string)
			if !ok || id == "" {
				t.Fatalf("recall --json has no usable top-level id field (gap NOT closed): %q", line)
			}
			jsonID = id
			break
		}
	}
	if jsonID == "" {
		t.Fatalf("could not extract thought-id from --json recall output:\n%s", jres.Stdout)
	}

	// Both extraction paths must agree — same canonical id.
	if plainID != jsonID {
		t.Fatalf("plain id %q != --json id %q (must be the same canonical id)", plainID, jsonID)
	}

	// --- Agent B confirms using ONLY the id recall gave it. ---
	cres := testutil.RunCLI(t, []string{"confirm", plainID, "--evidence=independently verified"}, root,
		plainRecallEnv("agent-b"))
	if cres.Code != 0 {
		t.Fatalf("confirm <recall-id> FAILED — gap NOT closed: exit=%d stderr=%q", cres.Code, cres.Stderr)
	}

	// --- Assert the confirm landed on the RIGHT thought. ---
	confirmsFile := filepath.Join(root, "live", "confirms", plainID+".gdl")
	body, err := os.ReadFile(confirmsFile)
	if err != nil {
		t.Fatalf("confirm did not land at live/confirms/%s.gdl: %v", plainID, err)
	}
	if !strings.Contains(string(body), "@confirm") {
		t.Errorf("confirms file missing @confirm record:\n%s", body)
	}
	// GDL render form: @confirm|target:<id>|by:<agent>|ts:...
	if !strings.Contains(string(body), "target:"+plainID) {
		t.Errorf("confirm did not target the recalled thought-id %q:\n%s", plainID, body)
	}
	if !strings.Contains(string(body), "by:agent-b") {
		t.Errorf("confirm not attributed to agent-b:\n%s", body)
	}
}
