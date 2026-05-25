// Cross-surface privacy tests for #147. Each one closes a confirmed
// scope:agent leak surfaced by the 2026-05-20 vet (R8/R10/R12) on top of
// the #139 fix that addressed listen/stream/Poll. The five read+write
// surfaces are: goals list, recall, fleet, confirm/refute, thoughts list.
//
// The fifth (thoughts list) was added under the pre-tag v1.0.6 audit
// when `scanThoughts` was confirmed to walk live/outbox/<agent>/ for
// every agent — leaking scope:agent rows authored by peers in the
// highest-volume interactive read verb.
//
// The shared `privacy.IsVisible` / `privacy.CanWriteAgainst` predicate
// lives in internal/lib/privacy and is exercised in table-driven tests
// there; these tests pin the end-to-end CLI behaviour.
package integration_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// mustWriteThoughtWithScope is a scope-parameterised variant of
// mustWriteThought (which hardcodes scope=agent). Privacy tests need
// both scope=agent (private) and scope=deployment (visible to peers)
// seeds to compare visibility.
func mustWriteThoughtWithScope(t *testing.T, root, agent, content, scope string) string {
	t.Helper()
	pattern := filepath.Join(root, "live", "outbox", agent, "*.gdl")
	before, _ := filepath.Glob(pattern)
	beforeSet := make(map[string]bool, len(before))
	for _, p := range before {
		beforeSet[p] = true
	}
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=customer:1",
		"--content=" + content, "--scope=" + scope,
	}, root, map[string]string{"RUFIO_AGENT_ID": agent})
	if res.Code != 0 {
		t.Fatalf("seed think failed: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	after, _ := filepath.Glob(pattern)
	for _, p := range after {
		if !beforeSet[p] {
			return strings.TrimSuffix(filepath.Base(p), ".gdl")
		}
	}
	t.Fatalf("seed think did not produce a new file")
	return ""
}

// --- 1. goals list ---

// TestGoalsList_PrivacyFilter_HidesOthersScopeAgent: alice writes a
// scope:agent goal; bob's `goals list` (text + JSON) must NOT include it.
func TestGoalsList_PrivacyFilter_HidesOthersScopeAgent(t *testing.T) {
	root := initProject(t)
	aliceID := mustSeedGoal(t, root, "alice", "alice private goal", "agent")
	bobID := mustSeedGoal(t, root, "bob", "bob fleet goal", "fleet")

	bobEnv := map[string]string{"RUFIO_AGENT_ID": "bob"}

	// Text output.
	res := testutil.RunCLI(t, []string{"goals", "list"}, root, bobEnv)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stdout, aliceID) {
		t.Errorf("LEAK: bob sees alice's scope:agent goal id=%s:\n%s", aliceID, res.Stdout)
	}
	if !strings.Contains(res.Stdout, bobID) {
		t.Errorf("bob does not see own goal id=%s:\n%s", bobID, res.Stdout)
	}
	if strings.Contains(res.Stdout, "alice private goal") {
		t.Errorf("LEAK: bob sees alice's private statement:\n%s", res.Stdout)
	}

	// JSON output.
	resJ := testutil.RunCLI(t, []string{"goals", "list", "--json"}, root, bobEnv)
	if resJ.Code != 0 {
		t.Fatalf("--json exit=%d stderr=%q", resJ.Code, resJ.Stderr)
	}
	if strings.Contains(resJ.Stdout, aliceID) {
		t.Errorf("LEAK JSON: bob sees alice's goal id=%s:\n%s", aliceID, resJ.Stdout)
	}
}

// TestGoalsList_PrivacyFilter_OwnScopeAgentVisible (regression guard):
// alice MUST still see her own scope:agent goal.
func TestGoalsList_PrivacyFilter_OwnScopeAgentVisible(t *testing.T) {
	root := initProject(t)
	aliceID := mustSeedGoal(t, root, "alice", "alice private goal", "agent")

	res := testutil.RunCLI(t, []string{"goals", "list"}, root,
		map[string]string{"RUFIO_AGENT_ID": "alice"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, aliceID) {
		t.Errorf("alice cannot see her own scope:agent goal:\n%s", res.Stdout)
	}
}

// --- 2. recall ---

// TestRecall_PrivacyFilter_HidesOthersScopeAgent: alice's scope:agent
// thought + observation must NOT appear in bob's recall.
func TestRecall_PrivacyFilter_HidesOthersScopeAgent(t *testing.T) {
	root := initProject(t)
	aliceThoughtID := mustWriteThoughtWithScope(t, root, "alice", "alice secret thought", "agent")
	// Seed an alice observation at scope=agent.
	seedObservation(t, root, "alice", "customer:1", "prefers", "email", "agent")
	// Bob's own scope:agent observation should be visible to bob.
	seedObservation(t, root, "bob", "order:42", "stage", "draft", "agent")

	bobEnv := map[string]string{"RUFIO_AGENT_ID": "bob"}

	// Text output — thoughts only.
	res := testutil.RunCLI(t, []string{"recall", "--types=thought"}, root, bobEnv)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stdout, aliceThoughtID) {
		t.Errorf("LEAK: bob sees alice's scope:agent thought id=%s:\n%s", aliceThoughtID, res.Stdout)
	}
	if strings.Contains(res.Stdout, "alice secret thought") {
		t.Errorf("LEAK: bob sees alice's thought content:\n%s", res.Stdout)
	}

	// Observations.
	resObs := testutil.RunCLI(t, []string{"recall", "--types=observation"}, root, bobEnv)
	if resObs.Code != 0 {
		t.Fatalf("obs exit=%d stderr=%q", resObs.Code, resObs.Stderr)
	}
	// Alice's scope:agent observation should be hidden from bob. Match by
	// the predicate+object — `prefers` + `email` — which only appears on
	// alice's seed.
	if strings.Contains(resObs.Stdout, `prefers="email"`) {
		t.Errorf("LEAK: bob sees alice's scope:agent observation:\n%s", resObs.Stdout)
	}
	// Bob's own scope:agent observation should still be visible.
	if !strings.Contains(resObs.Stdout, "order:42") {
		t.Errorf("bob cannot see own scope:agent observation:\n%s", resObs.Stdout)
	}
}

// TestRecall_PrivacyFilter_FleetRecordsStillVisible (regression guard):
// alice's scope:fleet thought must remain visible to bob via recall.
func TestRecall_PrivacyFilter_FleetRecordsStillVisible(t *testing.T) {
	root := initProject(t)
	seedThought(t, root, "alice", "customer:1", "alice fleet thought", "fleet")

	res := testutil.RunCLI(t, []string{"recall", "--types=thought"}, root,
		map[string]string{"RUFIO_AGENT_ID": "bob"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "alice fleet thought") {
		t.Errorf("regression: bob does not see alice's scope:fleet thought:\n%s", res.Stdout)
	}
}

// TestRecall_AnonymousCallerStillSeesEverything: when RUFIO_AGENT_ID is
// unset (anonymous firehose), the privacy filter is opt-in — every
// record passes regardless of scope. Mirrors the stream.Match opt-in
// semantic.
func TestRecall_AnonymousCallerStillSeesEverything(t *testing.T) {
	root := initProject(t)
	seedThought(t, root, "alice", "customer:1", "alice secret", "agent")

	res := testutil.RunCLI(t, []string{"recall", "--types=thought"}, root,
		map[string]string{"RUFIO_AGENT_ID": ""})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "alice secret") {
		t.Errorf("anonymous caller missed firehose record:\n%s", res.Stdout)
	}
}

// --- 3. fleet ---

// TestFleet_PrivacyFilter_HidesPrivateEntitiesFields: an agent's
// `attend --entities` declaration is treated as a private routing hint
// — fleet must omit the entities (and topics) of OTHER agents when the
// caller is identified.
func TestFleet_PrivacyFilter_HidesPrivateEntitiesFields(t *testing.T) {
	root := initProject(t)
	mustAttend(t, root, "alice", "investigating leak", []string{"secret:internal", "project:nda"})
	mustAttend(t, root, "bob", "watching orders", []string{"order:42"})

	bobEnv := map[string]string{"RUFIO_AGENT_ID": "bob"}

	// Text output.
	res := testutil.RunCLI(t, []string{"fleet"}, root, bobEnv)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stdout, "secret:internal") {
		t.Errorf("LEAK: bob sees alice's private entity 'secret:internal':\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "project:nda") {
		t.Errorf("LEAK: bob sees alice's private entity 'project:nda':\n%s", res.Stdout)
	}
	// Bob still sees alice the agent exists, and bob sees own entities.
	if !strings.Contains(res.Stdout, "alice") {
		t.Errorf("bob can no longer see alice exists:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "order:42") {
		t.Errorf("bob can no longer see own entities:\n%s", res.Stdout)
	}

	// JSON: alice's entities array should be empty for bob; bob's own
	// entities array intact.
	resJ := testutil.RunCLI(t, []string{"fleet", "--json"}, root, bobEnv)
	if resJ.Code != 0 {
		t.Fatalf("--json exit=%d stderr=%q", resJ.Code, resJ.Stderr)
	}
	lines := strings.Split(strings.TrimRight(resJ.Stdout, "\n"), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		agentID, _ := obj["agent"].(string)
		ents, _ := obj["entities"].([]interface{})
		if agentID == "alice" {
			for _, e := range ents {
				s, _ := e.(string)
				if s == "secret:internal" || s == "project:nda" {
					t.Errorf("LEAK JSON: bob sees alice's private entity %q", s)
				}
			}
		}
		if agentID == "bob" {
			if len(ents) == 0 {
				t.Errorf("regression: bob's own entities empty in own JSON view")
			}
		}
	}
}

// --- 4. confirm/refute authorization ---

// TestConfirm_AuthzFilter_RejectsScopeAgentNonAuthor: bob attempts to
// confirm alice's scope:agent thought; the write MUST be rejected with
// a clear error and the confirms file MUST NOT be written.
func TestConfirm_AuthzFilter_RejectsScopeAgentNonAuthor(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "alice", "private", "agent")

	res := testutil.RunCLI(t, []string{"confirm", id}, root,
		map[string]string{"RUFIO_AGENT_ID": "bob"})
	if res.Code == 0 {
		t.Fatalf("confirm should have been REJECTED but exit=0; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "cannot confirm") &&
		!strings.Contains(res.Stderr, "scope:agent") &&
		!strings.Contains(res.Stderr, "scope=agent") &&
		!strings.Contains(res.Stderr, "author") {
		t.Errorf("rejection error not informative enough; stderr=%q", res.Stderr)
	}
	// Confirms file must not exist.
	confirmsPath := filepath.Join(root, "live", "confirms", id+".gdl")
	if exists := fileExists(confirmsPath); exists {
		t.Errorf("confirms file written despite rejection: %s", confirmsPath)
	}
}

// TestConfirm_AuthzFilter_OwnScopeAgentAllowed (regression guard):
// alice may confirm her OWN scope:agent thought.
func TestConfirm_AuthzFilter_OwnScopeAgentAllowed(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "alice", "private", "agent")

	res := testutil.RunCLI(t, []string{"confirm", id}, root,
		map[string]string{"RUFIO_AGENT_ID": "alice"})
	if res.Code != 0 {
		t.Fatalf("alice cannot confirm own scope:agent thought; exit=%d stderr=%q", res.Code, res.Stderr)
	}
}

// TestConfirm_AuthzFilter_DeploymentScopeNonAuthorAllowed (regression
// guard): bob may still confirm alice's scope:deployment thought —
// crowd-validation continues to work for non-scope-agent records.
func TestConfirm_AuthzFilter_DeploymentScopeNonAuthorAllowed(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "alice", "broad", "deployment")

	res := testutil.RunCLI(t, []string{"confirm", id}, root,
		map[string]string{"RUFIO_AGENT_ID": "bob"})
	if res.Code != 0 {
		t.Fatalf("bob cannot confirm alice's scope:deployment thought; exit=%d stderr=%q", res.Code, res.Stderr)
	}
}

// TestRefute_AuthzFilter_RejectsScopeAgentNonAuthor: same as confirm but
// for refute.
func TestRefute_AuthzFilter_RejectsScopeAgentNonAuthor(t *testing.T) {
	root := initProject(t)
	id := mustWriteThoughtWithScope(t, root, "alice", "private", "agent")

	res := testutil.RunCLI(t, []string{"refute", id, "--reason=disagree"}, root,
		map[string]string{"RUFIO_AGENT_ID": "bob"})
	if res.Code == 0 {
		t.Fatalf("refute should have been REJECTED but exit=0; stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "cannot refute") &&
		!strings.Contains(res.Stderr, "scope:agent") &&
		!strings.Contains(res.Stderr, "scope=agent") &&
		!strings.Contains(res.Stderr, "author") {
		t.Errorf("rejection error not informative enough; stderr=%q", res.Stderr)
	}
	confirmsPath := filepath.Join(root, "live", "confirms", id+".gdl")
	if exists := fileExists(confirmsPath); exists {
		t.Errorf("confirms file written despite rejection: %s", confirmsPath)
	}
}

// fileExists is a tiny helper to keep the privacy tests self-contained.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// --- 5. thoughts list ---

// TestThoughtsList_PrivacyFilter_HidesOthersScopeAgent: alice writes a
// scope:agent thought; bob's `thoughts list` (text + JSON) must NOT
// include it. Closes the v1.0.6 pre-tag audit MAJOR finding — `scanThoughts`
// walks live/outbox/<agent>/ recursively for every agent, so without the
// privacy gate bob saw alice's private content (subject + content +
// retract_reason post-B2). `thoughts list` is the highest-volume
// interactive read verb, so the leak surface was wider than recall and
// worse than goals list.
func TestThoughtsList_PrivacyFilter_HidesOthersScopeAgent(t *testing.T) {
	root := initProject(t)
	aliceID := mustWriteThoughtWithScope(t, root, "alice", "alice secret thought", "agent")
	bobID := mustWriteThoughtWithScope(t, root, "bob", "bob fleet thought", "fleet")

	bobEnv := map[string]string{"RUFIO_AGENT_ID": "bob", "RUFIO_FULL_IDS": "1"}

	// Text output.
	res := testutil.RunCLI(t, []string{"thoughts", "list"}, root, bobEnv)
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if strings.Contains(res.Stdout, aliceID) {
		t.Errorf("LEAK: bob sees alice's scope:agent thought id=%s:\n%s", aliceID, res.Stdout)
	}
	if strings.Contains(res.Stdout, "alice secret thought") {
		t.Errorf("LEAK: bob sees alice's private content:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, bobID) {
		t.Errorf("bob does not see own thought id=%s:\n%s", bobID, res.Stdout)
	}

	// JSON output.
	resJ := testutil.RunCLI(t, []string{"thoughts", "list", "--json"}, root, bobEnv)
	if resJ.Code != 0 {
		t.Fatalf("--json exit=%d stderr=%q", resJ.Code, resJ.Stderr)
	}
	if strings.Contains(resJ.Stdout, aliceID) {
		t.Errorf("LEAK JSON: bob sees alice's thought id=%s:\n%s", aliceID, resJ.Stdout)
	}
	if strings.Contains(resJ.Stdout, "alice secret thought") {
		t.Errorf("LEAK JSON: bob sees alice's content:\n%s", resJ.Stdout)
	}
}

// TestThoughtsList_PrivacyFilter_OwnScopeAgentVisible (regression guard):
// alice MUST still see her own scope:agent thought. Same shape as the
// goals list regression guard.
func TestThoughtsList_PrivacyFilter_OwnScopeAgentVisible(t *testing.T) {
	root := initProject(t)
	aliceID := mustWriteThoughtWithScope(t, root, "alice", "alice private thought", "agent")

	res := testutil.RunCLI(t, []string{"thoughts", "list"}, root,
		map[string]string{"RUFIO_AGENT_ID": "alice", "RUFIO_FULL_IDS": "1"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, aliceID) {
		t.Errorf("alice cannot see her own scope:agent thought:\n%s", res.Stdout)
	}
}

// TestThoughtsList_PrivacyFilter_FleetRecordsStillVisible (regression
// guard): alice's scope:fleet thought must remain visible to bob via
// `thoughts list`. Pins the predicate to scope=agent only — fleet/
// deployment rows continue to flow.
func TestThoughtsList_PrivacyFilter_FleetRecordsStillVisible(t *testing.T) {
	root := initProject(t)
	mustWriteThoughtWithScope(t, root, "alice", "alice fleet thought", "fleet")

	res := testutil.RunCLI(t, []string{"thoughts", "list"}, root,
		map[string]string{"RUFIO_AGENT_ID": "bob", "RUFIO_FULL_IDS": "1"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "alice fleet thought") {
		t.Errorf("regression: bob does not see alice's scope:fleet thought:\n%s", res.Stdout)
	}
}

// TestThoughtsList_AnonymousCallerStillSeesEverything: when RUFIO_AGENT_ID
// is unset (anonymous firehose), the privacy filter is opt-in — every
// thought passes regardless of scope. Mirrors the recall/goals/stream
// opt-in semantic so admin and pre-#147 callers don't regress.
func TestThoughtsList_AnonymousCallerStillSeesEverything(t *testing.T) {
	root := initProject(t)
	aliceID := mustWriteThoughtWithScope(t, root, "alice", "alice secret thought", "agent")

	res := testutil.RunCLI(t, []string{"thoughts", "list"}, root,
		map[string]string{"RUFIO_AGENT_ID": "", "RUFIO_FULL_IDS": "1"})
	if res.Code != 0 {
		t.Fatalf("exit=%d stderr=%q", res.Code, res.Stderr)
	}
	if !strings.Contains(res.Stdout, aliceID) {
		t.Errorf("anonymous caller missed firehose record id=%s:\n%s", aliceID, res.Stdout)
	}
}
