package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// read_trust_test.go — paired tests for the read-trust cluster:
//   #149  recall --include-expired surfaces TTL-expired records
//   #150  retract walks learned/ for observation records
//   #151  thoughts list + recall types=thought agree on count when
//         given the same visibility scope

// --- helpers ---

// writeRawTTLThought is like writeRawThought (thoughts_test.go) but
// accepts an explicit ttl seconds value. Used to seed a thought whose
// ts+ttl is in the past — i.e. TTL-expired by the time the test reads it.
func writeRawTTLThought(t *testing.T, root, agent, id, content, ts string, ttl int) {
	t.Helper()
	dir := filepath.Join(root, "live", "outbox", agent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir outbox/%s: %v", agent, err)
	}
	line := "@thought|" + strings.Join([]string{
		"id:" + id,
		"author:" + agent,
		"type:hypothesis",
		`subject:customer\:1`,
		"content:" + content,
		"scope:agent",
		"ts:" + ts,
		"ttl:" + strconv.Itoa(ttl),
	}, "|") + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".gdl"), []byte(line), 0o644); err != nil {
		t.Fatalf("write raw thought: %v", err)
	}
}

// mustObserve seeds an @observation via the real CLI and returns the
// generated observation id (extracted from the columnar output line
// "observe: id=<id> ..." — H3d normalized echo prefix).
func mustObserve(t *testing.T, root, agent, subject, predicate, object, scope string) string {
	t.Helper()
	res := testutil.RunCLI(t, []string{
		"observe", "--subject=" + subject, "--predicate=" + predicate,
		"--object=" + object, "--scope=" + scope,
	}, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("observe failed: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// Parse `observe: id=<id> subject=... predicate=...`.
	for _, f := range strings.Fields(res.Stdout) {
		if strings.HasPrefix(f, "id=") {
			return strings.TrimPrefix(f, "id=")
		}
	}
	t.Fatalf("observe output missing id=: %q", res.Stdout)
	return ""
}

// --- #149: recall --include-expired surfaces TTL-expired thoughts ---

// TestRecall_IncludeExpired_SurfacesTTLExpiredThoughts seeds a thought
// whose ts+ttl is in the past, and asserts `recall --include-expired`
// returns it (currently the bug: returns nothing).
func TestRecall_IncludeExpired_SurfacesTTLExpiredThoughts(t *testing.T) {
	root := initProject(t)

	// Seed a thought that expired 10 minutes ago: ts is 1h old, ttl=60s.
	expiredTS := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	expiredID := "1000000000001-expird"
	writeRawTTLThought(t, root, "agent-a", expiredID, "expired payload", expiredTS, 60)

	// Sanity: file is on disk.
	if _, err := os.Stat(filepath.Join(root, "live", "outbox", "agent-a", expiredID+".gdl")); err != nil {
		t.Fatalf("seed file missing: %v", err)
	}

	res := testutil.RunCLI(t, []string{
		"recall", "--types=thought", "--include-expired", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, expiredID) {
		t.Errorf("recall --include-expired did not surface TTL-expired thought %q:\n%s", expiredID, res.Stdout)
	}
}

// TestRecall_DefaultExcludesTTLExpired asserts the default `recall`
// view DOES filter out TTL-expired records (so --include-expired is
// meaningfully different).
func TestRecall_DefaultExcludesTTLExpired(t *testing.T) {
	root := initProject(t)

	expiredTS := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	expiredID := "1000000000002-expird"
	writeRawTTLThought(t, root, "agent-a", expiredID, "expired payload", expiredTS, 60)

	// Also seed a live (ttl=0) thought so we know the recall path is
	// otherwise producing rows.
	liveID := mustThink(t, root, "agent-a", "still-alive payload")

	res := testutil.RunCLI(t, []string{
		"recall", "--types=thought", "--json",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stdout, expiredID) {
		t.Errorf("default recall surfaced TTL-expired thought %q:\n%s", expiredID, res.Stdout)
	}
	if !strings.Contains(res.Stdout, liveID) {
		t.Errorf("default recall missing the live thought %q:\n%s", liveID, res.Stdout)
	}
}

// --- #150: retract walks learned/ for observation records ---

// TestRetract_Observation_Succeeds seeds a real observation via
// `rufio observe`, then runs `rufio retract <id>` and asserts:
//   - exit 0
//   - a retract marker exists on disk
func TestRetract_Observation_Succeeds(t *testing.T) {
	root := initProject(t)
	id := mustObserve(t, root, "agent-a", "test:1", "ok", "value", "fleet")

	res := testutil.RunCLI(t, []string{
		"retract", id, "--reason=measurement error",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q stdout=%q", res.Code, res.Stderr, res.Stdout)
	}
	// Marker lands at live/retracted/<id>.gdl (same path as thought
	// retracts — single source of truth for the marker).
	marker := filepath.Join(root, "live", "retracted", id+".gdl")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("retract marker missing at %s: %v", marker, err)
	}
}

// TestRetract_UnknownId_ImprovedError asserts the new error message
// names BOTH lookup roots (outbox AND learned).
func TestRetract_UnknownId_ImprovedError(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"retract", "1779261206879-noexist", "--reason=x",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 1 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "learned/") {
		t.Errorf("stderr must mention learned/: %q", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "outbox") {
		t.Errorf("stderr must mention outbox: %q", res.Stderr)
	}
}

// --- #151 + #141: thoughts list and recall types=thought agree on
// TTL-visibility but diverge on retract-visibility ---

// TestThoughtsList_RecallTypesThought_AgreeOnCount_OwnScope seeds a
// mixed corpus and asserts the scoped visibility contract:
//
//	#151 (TTL): both surfaces hide TTL-expired records by default.
//	#141 (retract): `thoughts list` surfaces retracted thoughts inline
//	   with [RETRACTED]; `recall` keeps them out of the default broad-
//	   corpus view (retract is signal at the per-author audit layer,
//	   noise at the broad-recall layer).
//
// The scoped retract-divergence is the v1.0.6 B2 walkback of #151's
// "unify all visibility" rule — see internal/cli/thoughts.go file
// godoc for the rationale.
func TestThoughtsList_RecallTypesThought_AgreeOnCount_OwnScope(t *testing.T) {
	root := initProject(t)
	env := map[string]string{"RUFIO_AGENT_ID": "agent-a"}

	// Seed a mixed corpus that EXERCISES the visibility predicate:
	//   - 2 live thoughts authored by agent-a (agent-scope)
	//   - 1 thought authored by agent-a then retracted
	//   - 1 TTL-expired thought (seeded raw with ttl=60, ts=1h ago)
	// Expected post-#141 visibility:
	//   thoughts list (default): 2 live + 1 retracted-with-marker = 3
	//   recall --types=thought --scope=agent (default): 2 live = 2
	idLive1 := mustThink(t, root, "agent-a", "live one")
	idLive2 := mustThink(t, root, "agent-a", "live two")

	idRetracted := mustThink(t, root, "agent-a", "to-retract")
	rRes := testutil.RunCLI(t, []string{"retract", idRetracted, "--reason=test"}, root, env)
	if rRes.Code != 0 {
		t.Fatalf("retract seed: exit=%d stderr=%q", rRes.Code, rRes.Stderr)
	}

	expiredTS := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	idExpired := "1000000000003-ownsco"
	writeRawTTLThought(t, root, "agent-a", idExpired, "expired", expiredTS, 60)

	listRes := testutil.RunCLI(t, []string{"thoughts", "list", "--json"}, root, env)
	if listRes.Code != 0 {
		t.Fatalf("thoughts list exit=%d stderr=%q", listRes.Code, listRes.Stderr)
	}
	listIDs := jsonIDSet(t, listRes.Stdout)

	recallRes := testutil.RunCLI(t, []string{"recall", "--types=thought", "--scope=agent", "--json"}, root, env)
	if recallRes.Code != 0 {
		t.Fatalf("recall exit=%d stderr=%q", recallRes.Code, recallRes.Stderr)
	}
	recallIDs := jsonIDSet(t, recallRes.Stdout)

	// Both surfaces must surface live rows.
	for _, want := range []string{idLive1, idLive2} {
		if !listIDs[want] {
			t.Errorf("thoughts list missing live id %s: %v", want, keys(listIDs))
		}
		if !recallIDs[want] {
			t.Errorf("recall missing live id %s: %v", want, keys(recallIDs))
		}
	}

	// #151 TTL contract: TTL-expired stays hidden in BOTH surfaces.
	if listIDs[idExpired] {
		t.Errorf("thoughts list surfaced TTL-expired id %s by default (should require --include-expired): %v", idExpired, keys(listIDs))
	}
	if recallIDs[idExpired] {
		t.Errorf("recall surfaced TTL-expired id %s by default: %v", idExpired, keys(recallIDs))
	}

	// #141 retract contract: `thoughts list` MUST surface the retracted
	// row inline (focused author-audit view — signal here, not noise);
	// `recall` keeps it out of the default broad-corpus view.
	if !listIDs[idRetracted] {
		t.Errorf("thoughts list missing retracted id %s (must surface inline per #141): %v", idRetracted, keys(listIDs))
	}
	if recallIDs[idRetracted] {
		t.Errorf("recall surfaced retracted id %s by default (recall keeps retract hidden — opt in via --include-expired): %v", idRetracted, keys(recallIDs))
	}

	// Counts diverge by exactly one (the retracted row) — that's the
	// scoped intentional divergence.
	if len(listIDs) != len(recallIDs)+1 {
		t.Errorf("expected thoughts list to surface exactly 1 more row (the retracted one) than recall; got list=%d (%v) vs recall=%d (%v)",
			len(listIDs), keys(listIDs), len(recallIDs), keys(recallIDs))
	}
}

// TestThoughtsList_RecallTypesThought_AgreeOnCount_FleetScope is the
// fleet-visibility variant: thoughts list (which walks all outboxes
// + all inboxes, dedup) and `recall --types=thought` (no --scope
// filter — the privacy gate excludes other agents' agent-scoped
// records, but fleet thoughts from peers ARE visible) should return
// the same set.
//
// Note: `recall --scope=fleet` is NOT used here — that flag means
// "fleet-scoped records authored by me", which is a different (and
// existing-by-design) intent. The contract under test is "thoughts
// list and recall agree on default fleet visibility".
func TestThoughtsList_RecallTypesThought_AgreeOnCount_FleetScope(t *testing.T) {
	root := initProject(t)
	envA := map[string]string{"RUFIO_AGENT_ID": "agent-a"}

	// Seed: agent-a writes one fleet thought; agent-b writes one fleet
	// thought. Both should appear in BOTH read surfaces.
	idA := seedFleetThought(t, root, "agent-a", "from-a")
	idB := seedFleetThought(t, root, "agent-b", "from-b")

	listRes := testutil.RunCLI(t, []string{"thoughts", "list", "--all-agents", "--json"}, root, envA)
	if listRes.Code != 0 {
		t.Fatalf("thoughts list exit=%d stderr=%q", listRes.Code, listRes.Stderr)
	}
	listIDs := jsonIDSet(t, listRes.Stdout)

	recallRes := testutil.RunCLI(t, []string{"recall", "--types=thought", "--json"}, root, envA)
	if recallRes.Code != 0 {
		t.Fatalf("recall exit=%d stderr=%q", recallRes.Code, recallRes.Stderr)
	}
	recallIDs := jsonIDSet(t, recallRes.Stdout)

	for _, want := range []string{idA, idB} {
		if !listIDs[want] {
			t.Errorf("thoughts list --all-agents missing %s: %v", want, keys(listIDs))
		}
		if !recallIDs[want] {
			t.Errorf("recall missing %s: %v", want, keys(recallIDs))
		}
	}
	if len(listIDs) != len(recallIDs) {
		t.Errorf("count divergence in fleet view: thoughts list=%d vs recall=%d",
			len(listIDs), len(recallIDs))
	}
}

// seedFleetThought is a fleet-scoped seed helper (mustThink is agent-scope).
func seedFleetThought(t *testing.T, root, agent, content string) string {
	t.Helper()
	pattern := filepath.Join(root, "live", "outbox", agent, "*.gdl")
	before, _ := filepath.Glob(pattern)
	set := make(map[string]bool, len(before))
	for _, p := range before {
		set[p] = true
	}
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=customer:1",
		"--content=" + content, "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("seed fleet think: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	after, _ := filepath.Glob(pattern)
	for _, p := range after {
		if !set[p] {
			return strings.TrimSuffix(filepath.Base(p), ".gdl")
		}
	}
	t.Fatalf("seed fleet think: no new file in %s", pattern)
	return ""
}

// jsonIDSet parses JSONL stdout and returns a set of "id" field values.
// Non-JSON lines are tolerated (skipped).
func jsonIDSet(t *testing.T, stdout string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, ln := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(ln), &obj); err != nil {
			continue
		}
		if id, ok := obj["id"].(string); ok && id != "" {
			out[id] = true
		}
	}
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
