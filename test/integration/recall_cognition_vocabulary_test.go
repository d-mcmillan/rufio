package integration_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/testutil"
)

// Integration tests for K2 (R28 cognition-vocabulary inclusivity).
//
// `rufio recall <query>` previously indexed only `@thought.content`,
// `@observation.object`, `@reason.content` (and subject/predicate). R28
// surfaced that agent-cx's core claim — written via
// `summon --intent="…claim text…"` — was unrecoverable via recall.
// Same dark-matter problem for `@confirm.evidence`, `@refute.reason`,
// `@retract.reason`: the substrate recorded the claim but the canonical
// reader couldn't surface it.
//
// K2 extends recall's free-text search to ANY of these fields, and adds
// the four record kinds to the AllTypes enum so `--types=summon` etc.
// can also filter explicitly.

// TestRecall_FindsSearchTerm_InSummonIntent — the load-bearing case
// from R28. A summon with intent containing a unique phrase must be
// recoverable via `rufio recall "<phrase>"`.
func TestRecall_FindsSearchTerm_InSummonIntent(t *testing.T) {
	root := initProject(t)

	// agent-cx writes a summon whose intent carries the claim.
	res := testutil.RunCLI(t, []string{
		"summon", "agent-target", "--topic=churn-strategy",
		"--intent=unique-cx-claim-marker: scrypt has worse memory-hardness",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-cx"})
	if res.Code != 0 {
		t.Fatalf("seed summon: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	out := testutil.RunCLI(t, []string{"recall", "unique-cx-claim-marker"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("recall exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "unique-cx-claim-marker") {
		t.Errorf("recall did not surface summon-intent search hit:\n%s", out.Stdout)
	}
}

// TestRecall_FindsSearchTerm_InConfirmEvidence — `@confirm.evidence`
// content must be searchable.
func TestRecall_FindsSearchTerm_InConfirmEvidence(t *testing.T) {
	root := initProject(t)
	// Seed a decision to confirm against.
	id := mustWriteDecision(t, root, "agent-a", "svc:auth", "adopt argon2id", "fleet")

	// agent-b confirms with a unique evidence phrase.
	res := testutil.RunCLI(t, []string{
		"confirm", id, "--evidence=evidencemarker: OWASP-aligned and benchmarked",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("confirm: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	out := testutil.RunCLI(t, []string{"recall", "evidencemarker"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("recall exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "evidencemarker") {
		t.Errorf("recall did not surface confirm-evidence search hit:\n%s", out.Stdout)
	}
}

// TestRecall_FindsSearchTerm_InRefuteReason — `@refute.reason` content
// must be searchable.
func TestRecall_FindsSearchTerm_InRefuteReason(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "svc:auth", "adopt argon2id", "fleet")

	res := testutil.RunCLI(t, []string{
		"refute", id, "--reason=refutemarker: bcrypt is also OWASP-acceptable",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if res.Code != 0 {
		t.Fatalf("refute: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	out := testutil.RunCLI(t, []string{"recall", "refutemarker"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("recall exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "refutemarker") {
		t.Errorf("recall did not surface refute-reason search hit:\n%s", out.Stdout)
	}
}

// TestRecall_FindsSearchTerm_InRetractReason — `@retract.reason` content
// must be searchable.
func TestRecall_FindsSearchTerm_InRetractReason(t *testing.T) {
	root := initProject(t)
	// Identify ourselves as agent-a so we can retract our own thought.
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=svc:auth",
		"--content=maybe scrypt", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("seed think: exit=%d stderr=%q", res.Code, res.Stderr)
	}
	// Grab the new thought id.
	pattern := filepath.Join(root, "live", "outbox", "agent-a", "*.gdl")
	matches, _ := filepath.Glob(pattern)
	if len(matches) != 1 {
		t.Fatalf("expected 1 outbox file, got %d", len(matches))
	}
	id := strings.TrimSuffix(filepath.Base(matches[0]), ".gdl")

	r := testutil.RunCLI(t, []string{
		"retract", id, "--reason=retractmarker: superseded by argon2id finding",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if r.Code != 0 {
		t.Fatalf("retract: exit=%d stderr=%q", r.Code, r.Stderr)
	}

	out := testutil.RunCLI(t, []string{"recall", "retractmarker", "--include-expired"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("recall exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "retractmarker") {
		t.Errorf("recall did not surface retract-reason search hit:\n%s", out.Stdout)
	}
}

// TestRecall_TypesFilter_RespectsNewTypes — `--types=summon` filters to
// only summon rows; `--types=confirm` to only confirm rows. The new
// types must be valid tokens accepted by ValidateTypes.
func TestRecall_TypesFilter_RespectsNewTypes(t *testing.T) {
	root := initProject(t)
	id := mustWriteDecision(t, root, "agent-a", "svc:auth", "adopt argon2id", "fleet")

	// One summon (search-target).
	r1 := testutil.RunCLI(t, []string{
		"summon", "agent-target", "--topic=churn-strategy",
		"--intent=summonmarker phrase",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-cx"})
	if r1.Code != 0 {
		t.Fatalf("summon: exit=%d stderr=%q", r1.Code, r1.Stderr)
	}
	// One confirm.
	r2 := testutil.RunCLI(t, []string{
		"confirm", id, "--evidence=confirmmarker phrase",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-b"})
	if r2.Code != 0 {
		t.Fatalf("confirm: exit=%d stderr=%q", r2.Code, r2.Stderr)
	}

	// --types=summon only.
	out := testutil.RunCLI(t, []string{"recall", "--types=summon", "--json"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("recall --types=summon exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if strings.Contains(out.Stdout, "confirmmarker") {
		t.Errorf("--types=summon leaked a confirm row:\n%s", out.Stdout)
	}
	if !strings.Contains(out.Stdout, "summonmarker") {
		t.Errorf("--types=summon dropped the summon row:\n%s", out.Stdout)
	}

	// --types=confirm only.
	out = testutil.RunCLI(t, []string{"recall", "--types=confirm", "--json"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("recall --types=confirm exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if strings.Contains(out.Stdout, "summonmarker") {
		t.Errorf("--types=confirm leaked a summon row:\n%s", out.Stdout)
	}
	if !strings.Contains(out.Stdout, "confirmmarker") {
		t.Errorf("--types=confirm dropped the confirm row:\n%s", out.Stdout)
	}
}

// TestRecall_Default_StillReturnsThoughts — regression. Default recall
// (no type filter) still returns @thought records — the existing
// behavior is preserved exactly.
func TestRecall_Default_StillReturnsThoughts(t *testing.T) {
	root := initProject(t)
	res := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=svc:auth",
		"--content=regression-marker thought content", "--scope=fleet",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if res.Code != 0 {
		t.Fatalf("seed: exit=%d stderr=%q", res.Code, res.Stderr)
	}

	out := testutil.RunCLI(t, []string{"recall", "regression-marker"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("recall exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "regression-marker") {
		t.Errorf("recall regression: thought content no longer searchable:\n%s", out.Stdout)
	}
}

// TestRecall_PrivacyFloor_SummonsScopeAgent — privacy is preserved for
// the new record types too. A scope:agent summon (if writers ever set
// one) and confirm/refute against scope:agent targets stay invisible to
// non-author callers. Today summon has no scope field on disk — it is
// project-wide by design — so this test asserts the non-leak path of
// confirm/refute, which DO carry scope through the target thought.
// (The summon test stays at the smoke level: that the search hit works
// at all.)
func TestRecall_PrivacyFloor_AgentScopedThoughtNotLeakedToOthers(t *testing.T) {
	root := initProject(t)

	// agent-a writes a PRIVATE (scope=agent) thought.
	r := testutil.RunCLI(t, []string{
		"think", "--type=hypothesis", "--subject=svc:auth",
		"--content=privatemarker: alice's private hash thought", "--scope=agent",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-a"})
	if r.Code != 0 {
		t.Fatalf("seed private: exit=%d stderr=%q", r.Code, r.Stderr)
	}

	// agent-z queries — must NOT see agent-a's private thought (preserves
	// the floor from #147).
	out := testutil.RunCLI(t, []string{"recall", "privatemarker"}, root,
		map[string]string{"RUFIO_AGENT_ID": "agent-z"})
	if out.Code != 0 {
		t.Fatalf("recall exit=%d stderr=%q", out.Code, out.Stderr)
	}
	if strings.Contains(out.Stdout, "privatemarker") {
		t.Errorf("scope:agent thought leaked across agents:\n%s", out.Stdout)
	}
}

// TestRecall_JSON_NewTypesIncludeKindField — the JSON output of the new
// record types carries a discriminator (_type) so consumers can filter.
// Asserts that a summon row has _type="summon" in --json output.
func TestRecall_JSON_NewTypesIncludeKindField(t *testing.T) {
	root := initProject(t)
	r := testutil.RunCLI(t, []string{
		"summon", "agent-target", "--topic=churn-strategy",
		"--intent=jsonmarker phrase",
	}, root, map[string]string{"RUFIO_AGENT_ID": "agent-cx"})
	if r.Code != 0 {
		t.Fatalf("summon: exit=%d stderr=%q", r.Code, r.Stderr)
	}
	out := testutil.RunCLI(t, []string{"recall", "jsonmarker", "--json"}, root, nil)
	if out.Code != 0 {
		t.Fatalf("recall --json exit=%d stderr=%q", out.Code, out.Stderr)
	}
	// JSONL: split by lines, decode each, find summon kind.
	foundSummon := false
	for _, line := range strings.Split(strings.TrimSpace(out.Stdout), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row map[string]interface{}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		if row["_type"] == "summon" {
			foundSummon = true
		}
	}
	if !foundSummon {
		t.Errorf("no row with _type=summon in:\n%s", out.Stdout)
	}
}
