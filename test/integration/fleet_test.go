package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// mustAttend invokes `rufio attend` as the named agent to seed an
// attention record. Used by both fleet and attention inspection tests.
// Entities default to "customer:1" when empty so attend's validator
// (entities are required) is happy.
func mustAttend(t *testing.T, root, agent, intent string, entities []string) {
	t.Helper()
	if len(entities) == 0 {
		entities = []string{"customer:1"}
	}
	args := []string{
		"attend",
		"--intent=" + intent,
		"--entities=" + strings.Join(entities, ","),
	}
	res := testutil.RunCLI(t, args, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("attend %s: exit=%d stderr=%q", agent, res.Code, res.Stderr)
	}
}

// TestFleet_HappyPath_ListsActiveAgents seeds two agents with distinct
// attention records and asserts `rufio fleet` lists both in stdout.
func TestFleet_HappyPath_ListsActiveAgents(t *testing.T) {
	root := initProject(t)
	mustAttend(t, root, "agent-a", "debugging auth", []string{"customer:1"})
	mustAttend(t, root, "agent-b", "watching orders", []string{"order:42"})

	res := testutil.RunCLI(t, []string{"fleet"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "agent-a") {
		t.Errorf("stdout missing agent-a:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "agent-b") {
		t.Errorf("stdout missing agent-b:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "debugging auth") {
		t.Errorf("stdout missing intent for agent-a:\n%s", res.Stdout)
	}
	lines := nonEmptyLines(res.Stdout)
	if len(lines) != 2 {
		t.Errorf("want 2 lines, got %d:\n%s", len(lines), res.Stdout)
	}
}

// TestFleet_EmptyResult_StdoutEmpty asserts exit 0 + empty stdout when
// the project has no attention records.
func TestFleet_EmptyResult_StdoutEmpty(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{"fleet"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "" {
		t.Errorf("stdout should be empty for no-agents case:\n%q", res.Stdout)
	}
}

// TestFleet_JSONOutput_HasExpectedShape asserts --json emits valid
// JSONL with every field promised in D20.1.
func TestFleet_JSONOutput_HasExpectedShape(t *testing.T) {
	root := initProject(t)
	mustAttend(t, root, "agent-a", "debugging auth", []string{"customer:5821"})

	res := testutil.RunCLI(t, []string{"fleet", "--json"}, root, nil)
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
	if got["_type"] != "fleet-agent" {
		t.Errorf("_type=%v want fleet-agent", got["_type"])
	}
	if got["_version"] != "1" {
		t.Errorf("_version=%v want 1", got["_version"])
	}
	if got["agent"] != "agent-a" {
		t.Errorf("agent=%v want agent-a", got["agent"])
	}
	if got["intent"] != "debugging auth" {
		t.Errorf("intent=%v want 'debugging auth'", got["intent"])
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

// TestFleet_SortedByLastSeenDesc_TiebreakAgentAsc exercises BOTH arms
// of the post-#115 sort: primary key is LastSeen descending (recently-
// active first), tiebreak is agent-id ascending (deterministic
// ordering when LastSeen ties).
//
// The prior version of this test (TestFleet_SortedByAgentAscending)
// passed by coincidence — it used two `mustAttend` calls back-to-back,
// which produced distinct NowISO timestamps so the LastSeen-desc arm
// alone decided the order. The tiebreak arm was untested.
//
// Setup (hand-seeded outbox so we control the ts: value exactly):
//   - agent-a: outbox @thought with ts=2026-05-20T10:00:00Z (tie with agent-b)
//   - agent-b: outbox @thought with ts=2026-05-20T10:00:00Z (tie with agent-a)
//   - agent-c: outbox @thought with ts=2026-05-20T10:00:01Z (strictly later)
//
// Expected order (desc + asc-tiebreak):
//
//	line[0] = agent-c  (later ts wins the primary key)
//	line[1] = agent-a  (ties with agent-b on ts; agent-id-asc tiebreak)
//	line[2] = agent-b  (loser of the tiebreak)
func TestFleet_SortedByLastSeenDesc_TiebreakAgentAsc(t *testing.T) {
	root := initProject(t)

	// Helper to seed an outbox thought with a pinned ts. Inlined here
	// rather than shared with fleet_broaden_test's writeFile to keep
	// the test self-contained.
	seedThought := func(agent, id, ts string) {
		t.Helper()
		dir := filepath.Join(root, "live", "outbox", agent)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		path := filepath.Join(dir, id+".gdl")
		body := "@thought|id:" + id + "|author:" + agent +
			"|type:hypothesis|subject:customer:1|content:t|scope:fleet|ts:" + ts + "|ttl:0\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	seedThought("agent-a", "1747700000000-aaaaaa", "2026-05-20T10:00:00Z")
	seedThought("agent-b", "1747700000000-bbbbbb", "2026-05-20T10:00:00Z")
	seedThought("agent-c", "1747700001000-cccccc", "2026-05-20T10:00:01Z")

	res := testutil.RunCLI(t, []string{"fleet"}, root, nil)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	lines := nonEmptyLines(res.Stdout)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d:\n%s", len(lines), res.Stdout)
	}

	// Primary key (LastSeen desc): the strictly-later agent-c row
	// must come first.
	if !strings.HasPrefix(lines[0], "agent-c") {
		t.Errorf("line[0] should start with agent-c (later ts wins desc sort), got: %q", lines[0])
	}
	// Tiebreak arm (agent-id asc): agent-a and agent-b share ts, so
	// agent-a (lexicographically smaller) must come before agent-b.
	if !strings.HasPrefix(lines[1], "agent-a") {
		t.Errorf("line[1] should start with agent-a (tiebreak: id asc wins over agent-b), got: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "agent-b") {
		t.Errorf("line[2] should start with agent-b (tiebreak loser), got: %q", lines[2])
	}
}

// TestFleet_NotInProject_Exit1 asserts running outside a project
// surfaces NotInProjectError as exit 1.
func TestFleet_NotInProject_Exit1(t *testing.T) {
	workdir := t.TempDir()
	res := testutil.RunCLI(t, []string{"fleet"}, workdir, nil)
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "not inside a Rufio project") {
		t.Errorf("stderr=%q (want 'not inside a Rufio project')", res.Stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(res.Stderr), "rufio fleet:") {
		t.Errorf("missing single-prefix invariant: %q", res.Stderr)
	}
}
