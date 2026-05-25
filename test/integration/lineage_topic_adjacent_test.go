package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// Integration tests for K1 (R28 cognition-vocabulary inclusivity).
//
// `rufio lineage <decision-id>` now surfaces topic-adjacent voices —
// any @thought or @observation record sharing the decision's subject
// AND posted strictly after the decision's ts AND visible to the
// caller. The point: non-Claude / non-structured-reasoner agents whose
// primary cognitive output is `think --type=focus|hypothesis` (rather
// than `reason --decision=`) become first-class voices in the audit
// trail instead of dark matter.
//
// All seeding goes through the real CLI so the on-disk shape matches
// what the production writers emit.

// TestLineage_TopicAdjacent_IncludesPostDecisionFleetThoughts —
// agent-b posts a hypothesis on the same subject AFTER the decision.
// The lineage text output must contain a "Topic-adjacent voices:"
// header listing it.
func TestLineage_TopicAdjacent_IncludesPostDecisionFleetThoughts_Text(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "svc:auth", "adopt argon2id", "fleet")

	// agent-b posts a hypothesis on the same subject AFTER the decision.
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=svc:auth",
		"--content=what about scrypt — also OWASP-acceptable", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("seed hypothesis: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	out := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if out.Code != 0 {
		t.Fatalf("lineage exit=%d stderr=%q", out.Code, out.Stderr)
	}
	for _, want := range []string{
		"Topic-adjacent voices:",
		"agent-b",
		"what about scrypt",
	} {
		if !strings.Contains(out.Stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, out.Stdout)
		}
	}
}

// TestLineage_TopicAdjacent_IncludesObservations — an @observation on
// the same subject, post-decision, also appears as a topic-adjacent
// voice (observations are the second cognitive mode).
//
// Note: there is no `rufio observation` CLI yet that targets a free
// subject for write — observations are written via `think
// --type=observation`. Using that path keeps the seeding honest end-
// to-end and exercises the outbox walker.
func TestLineage_TopicAdjacent_IncludesObservations(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "svc:auth", "adopt argon2id", "fleet")

	res := testutil.RunCLI(t, []string{
		"think", "--type=observation", "--subject=svc:auth",
		"--content=argon2id benchmarks 2x faster on our message shape",
		"--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-c"})
	if res.Code != 0 {
		t.Fatalf("seed observation: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	out := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if out.Code != 0 {
		t.Fatalf("lineage exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "Topic-adjacent voices:") {
		t.Errorf("missing Topic-adjacent voices header:\n%s", out.Stdout)
	}
	if !strings.Contains(out.Stdout, "argon2id benchmarks") {
		t.Errorf("missing observation content:\n%s", out.Stdout)
	}
}

// TestLineage_TopicAdjacent_ExcludesPreDecisionRecords — a thought
// from BEFORE the decision is context (handled by the bundle), not a
// post-decision voice. Must not surface in the topic-adjacent section.
func TestLineage_TopicAdjacent_ExcludesPreDecisionRecords(t *testing.T) {
	root := initProject(t)
	// Pre-decision hypothesis from agent-b. Posted BEFORE the decision so
	// it must be excluded from the topic-adjacent section.
	preRes := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=svc:auth",
		"--content=PREDECISION SCAN: thinking about hashing", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if preRes.Code != 0 {
		t.Fatalf("seed pre-decision: exit=%d stderr=%q", preRes.Code, preRes.Stderr)
	}

	// Decision lands after the hypothesis.
	id := mustWriteDecision(t, root, "agent-a", "svc:auth", "adopt argon2id", "fleet")

	out := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if out.Code != 0 {
		t.Fatalf("lineage exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if strings.Contains(out.Stdout, "PREDECISION SCAN") {
		t.Errorf("pre-decision hypothesis leaked into topic-adjacent voices:\n%s", out.Stdout)
	}
}

// TestLineage_TopicAdjacent_RespectsPrivacyFloor — another agent's
// scope:agent thought must NOT surface to a third agent reading
// lineage. Mirrors the #147 privacy floor that also applies to the
// reasoning chain (filterReasoningPrivacy).
func TestLineage_TopicAdjacent_RespectsPrivacyFloor(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "svc:auth", "adopt argon2id", "fleet")

	// agent-b posts a PRIVATE (scope=agent) hypothesis on the same subject.
	privRes := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=svc:auth",
		"--content=PRIVATE TO B: keep this off the fleet bus", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if privRes.Code != 0 {
		t.Fatalf("seed private hypothesis: exit=%d stderr=%q", privRes.Code, privRes.Stderr)
	}

	// agent-z reads lineage. Must NOT see agent-b's private hypothesis.
	out := testutil.RunCLI(t, []string{"lineage", id}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-z"})
	if out.Code != 0 {
		t.Fatalf("lineage exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if strings.Contains(out.Stdout, "PRIVATE TO B") {
		t.Errorf("scope:agent thought leaked to agent-z lineage:\n%s", out.Stdout)
	}
}

// TestLineage_TopicAdjacent_DifferentSubject_Excluded — a thought on
// svc:billing must not appear in lineage for a svc:auth decision.
func TestLineage_TopicAdjacent_DifferentSubject_Excluded(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "svc:auth", "adopt argon2id", "fleet")

	// agent-b posts a hypothesis on a DIFFERENT subject, post-decision.
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=svc:billing",
		"--content=BILLING TOPIC: invoice rounding bug", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("seed unrelated hypothesis: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	out := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if out.Code != 0 {
		t.Fatalf("lineage exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if strings.Contains(out.Stdout, "BILLING TOPIC") {
		t.Errorf("different-subject thought leaked into lineage:\n%s", out.Stdout)
	}
}

// TestLineage_JSON_HasTopicAdjacentVoicesArray — JSON output must
// ALWAYS include a topic_adjacent_voices array (possibly empty), never
// null or absent. Matches the confirmed_by/refuted_by stability
// convention from #132.
func TestLineage_JSON_HasTopicAdjacentVoicesArray(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "svc:auth", "adopt argon2id", "fleet")

	// Case A: no topic-adjacent voices — array must still be present.
	out := testutil.RunCLI(t, []string{"lineage", id, "--json"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("lineage --json exit=%d stderr=%q", out.Code, out.Stderr)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(out.Stdout), &payload); err != nil {
		t.Fatalf("decode JSON: %v\nstdout=%q", err, out.Stdout)
	}
	v, ok := payload["topic_adjacent_voices"]
	if !ok {
		t.Fatalf("missing topic_adjacent_voices key:\n%s", out.Stdout)
	}
	arr, ok := v.([]interface{})
	if !ok {
		t.Fatalf("topic_adjacent_voices is not an array: %T (%v)", v, v)
	}
	if len(arr) != 0 {
		t.Errorf("len=%d, want 0 (empty array baseline)", len(arr))
	}

	// Case B: one voice. Array must contain it with author/content/type.
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=svc:auth",
		"--content=scrypt as well", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("seed: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	out = testutil.RunCLI(t, []string{"lineage", id, "--json"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("lineage --json exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if err := json.Unmarshal([]byte(out.Stdout), &payload); err != nil {
		t.Fatalf("decode JSON (case B): %v", err)
	}
	arr, _ = payload["topic_adjacent_voices"].([]interface{})
	if len(arr) != 1 {
		t.Fatalf("len=%d, want 1 voice", len(arr))
	}
	row, _ := arr[0].(map[string]interface{})
	if row["author"] != "agent-b" {
		t.Errorf("author=%v, want agent-b", row["author"])
	}
	if row["content"] != "scrypt as well" {
		t.Errorf("content=%v, want scrypt as well", row["content"])
	}
}

// TestLineage_TopicAdjacent_SortedAscendingByTs_Integration — voices
// section is rendered in ts-ascending order so a cold reader sees the
// conversation in the order it happened.
func TestLineage_TopicAdjacent_SortedAscendingByTs(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "svc:auth", "adopt argon2id", "fleet")

	// Post three voices in random order.
	for _, seed := range []struct{ agent, content string }{
		{"agent-c", "SECOND voice"},
		{"agent-b", "FIRST voice"},
		{"agent-d", "THIRD voice"},
	} {
		// Tiny natural ordering — each `think` invocation regenerates the
		// id (which embeds unix-millis) so ts is monotonically increasing
		// across invocations.
		r := testutil.RunCLI(t, []string{
			"think", "--type=hypothesis", "--subject=svc:auth",
			"--content=" + seed.content, "--scope=fleet",
		}, root, map[string]string{"RUFIO_AGENT_ID": seed.agent})
		if r.Code != 0 {
			t.Fatalf("seed %s: exit=%d stderr=%q", seed.agent, r.Code, r.Stderr)
		}
	}

	out := testutil.RunCLI(t, []string{"lineage", id}, root, nil)
	if out.Code != 0 {
		t.Fatalf("lineage exit=%d stderr=%q", out.Code, out.Stderr)
	}
	// Find the topic-adjacent section and check the order of the three
	// markers. We assert their positional ordering inside the rendered
	// block.
	stdout := out.Stdout
	iSecond := strings.Index(stdout, "SECOND voice")
	iFirst := strings.Index(stdout, "FIRST voice")
	iThird := strings.Index(stdout, "THIRD voice")
	if iFirst < 0 || iSecond < 0 || iThird < 0 {
		t.Fatalf("missing markers: first=%d second=%d third=%d\n%s",
			iFirst, iSecond, iThird, stdout)
	}
	// In the recorded order of `think` invocations (FIRST seeded
	// SECOND, FIRST, THIRD) — ts-ascending corresponds to the actual
	// seeding order, which is: agent-c -> agent-b -> agent-d
	// (i.e. SECOND, FIRST, THIRD in text). We assert that the in-text
	// positions are monotonically ascending in invocation order.
	if !(iSecond < iFirst && iFirst < iThird) {
		t.Errorf("voices not in ts-ascending invocation order: SECOND@%d FIRST@%d THIRD@%d\n%s",
			iSecond, iFirst, iThird, stdout)
	}
}
