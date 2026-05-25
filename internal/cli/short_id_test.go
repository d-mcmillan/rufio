// Package cli — R29a integration tests: every write verb that takes a
// thought-id positional must accept the 6-char suffix render that
// `thoughts list` and `recall` display in text mode.
//
// R29 verdict identified this asymmetry as the load-bearing friction
// blocking native-feel: read surfaces show short ids; write surfaces
// rejected them. Agents had to dump --json to recover the canonical id.
// These tests pin the resolver-cascade contract — one library change
// (retract.Resolve) reaches every write path.
//
// Also covers R29b: `rufio promote <thought-shaped value>` no longer
// errors with the artifact-track parser message — it nudges toward
// `confirm` with a one-line redirect that names the right verb.
package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/versioning"
)

// shortIDProject scaffolds a rufio project at t.TempDir() pinning agent
// via identity.local.gdl. Mirrors scopeTestProject (kept local to avoid
// coupling the two test files).
func shortIDProject(t *testing.T, agent string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "rufio.gdl"), []byte(""), 0o644); err != nil {
		t.Fatalf("write rufio.gdl: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".rufio"), 0o755); err != nil {
		t.Fatalf("mkdir .rufio: %v", err)
	}
	idFile := filepath.Join(root, ".rufio", "identity.local.gdl")
	rec := gdl.Record{Type: "identity", Fields: []gdl.RecordField{
		{Key: "agent", Value: agent},
		{Key: "set-at", Value: versioning.NowISO()},
	}}
	if err := os.WriteFile(idFile, []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	t.Setenv("RUFIO_AGENT_ID", "")
	return root
}

// seedThought writes a @thought record under live/outbox/<author>/<id>.gdl.
func seedThought(t *testing.T, root, author, id, typ, subject, scope string) {
	t.Helper()
	dir := filepath.Join(root, "live", "outbox", author)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir outbox: %v", err)
	}
	line := "@thought|id:" + id + "|author:" + author +
		"|type:" + typ + "|subject:" + subject +
		"|content:test|scope:" + scope + "|ts:2026-05-20T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, id+".gdl"), []byte(line), 0o644); err != nil {
		t.Fatalf("write outbox: %v", err)
	}
}

// TestConfirm_AcceptsShortIDSuffix_OutboxMatch: `rufio confirm jbgs5l`
// resolves to the canonical id and writes a normal @confirm record.
func TestConfirm_AcceptsShortIDSuffix_OutboxMatch(t *testing.T) {
	root := shortIDProject(t, "bob")
	seedThought(t, root, "alice", "1779321385406-jbgs5l", "decision", "svc:auth", "fleet")

	if err := runConfirm(root, "jbgs5l", "", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runConfirm short id: %v", err)
	}
	// Confirm landed under canonical id.
	bs, err := os.ReadFile(filepath.Join(root, "live", "confirms", "1779321385406-jbgs5l.gdl"))
	if err != nil {
		t.Fatalf("read confirms file: %v", err)
	}
	if !strings.Contains(string(bs), "@confirm") {
		t.Errorf("confirm file missing @confirm record: %q", bs)
	}
	if !strings.Contains(string(bs), "target:1779321385406-jbgs5l") {
		t.Errorf("confirm record target must be canonical full id: %q", bs)
	}
}

// TestRefute_AcceptsShortIDSuffix_OutboxMatch — same contract as
// confirm, symmetric verb.
func TestRefute_AcceptsShortIDSuffix_OutboxMatch(t *testing.T) {
	root := shortIDProject(t, "bob")
	seedThought(t, root, "alice", "1779321385406-jbgs5l", "decision", "svc:auth", "fleet")

	if err := runRefute(root, "jbgs5l", "evidence contradicts", "", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runRefute short id: %v", err)
	}
	bs, err := os.ReadFile(filepath.Join(root, "live", "confirms", "1779321385406-jbgs5l.gdl"))
	if err != nil {
		t.Fatalf("read confirms file: %v", err)
	}
	if !strings.Contains(string(bs), "@refute") {
		t.Errorf("file missing @refute record: %q", bs)
	}
	if !strings.Contains(string(bs), "target:1779321385406-jbgs5l") {
		t.Errorf("refute target must be canonical: %q", bs)
	}
}

// TestRetract_AcceptsShortIDSuffix_OutboxMatch — retract is author-only,
// so we set agent=alice and seed alice's thought.
func TestRetract_AcceptsShortIDSuffix_OutboxMatch(t *testing.T) {
	root := shortIDProject(t, "alice")
	seedThought(t, root, "alice", "1779321385406-jbgs5l", "decision", "svc:auth", "fleet")

	if err := runRetract(root, "jbgs5l", "outdated", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runRetract short id: %v", err)
	}
	bs, err := os.ReadFile(filepath.Join(root, "live", "retracted", "1779321385406-jbgs5l.gdl"))
	if err != nil {
		t.Fatalf("retract file at canonical id: %v", err)
	}
	if !strings.Contains(string(bs), "@retract") {
		t.Errorf("retract file missing record: %q", bs)
	}
}

// TestLineage_AcceptsShortIDSuffix — lineage takes a decision-id
// positional. Same cascade.
func TestLineage_AcceptsShortIDSuffix(t *testing.T) {
	root := shortIDProject(t, "alice")
	seedThought(t, root, "alice", "1779321385406-jbgs5l", "decision", "svc:auth", "fleet")

	// Capture stdout to confirm decision header renders.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runLineage(root, "jbgs5l", output.RenderOpts{})

	_ = w.Close()
	os.Stdout = oldStdout
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if err != nil {
		t.Fatalf("runLineage short id: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "1779321385406-jbgs5l") {
		t.Errorf("lineage output missing canonical id: %s", out)
	}
}

// TestReason_DecisionFlag_AcceptsShortIDSuffix — `reason --decision=jbgs5l`
// must resolve the suffix before ValidateDecisionTarget runs.
func TestReason_DecisionFlag_AcceptsShortIDSuffix(t *testing.T) {
	root := shortIDProject(t, "bob")
	seedThought(t, root, "alice", "1779321385406-jbgs5l", "decision", "svc:auth", "fleet")

	if err := runReason(root, reasonArgs{
		Content:  "chained reasoning under alice's decision",
		Decision: "jbgs5l",
	}, output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runReason --decision=<short>: %v", err)
	}
	// Reason landed under the canonical decision-id dir, not the short.
	matches, _ := filepath.Glob(filepath.Join(root, "live", "reasoning", "bob", "1779321385406-jbgs5l", "*.gdl"))
	if len(matches) == 0 {
		t.Errorf("expected reason file under canonical decision dir; got none.\n  glob=%s",
			filepath.Join(root, "live", "reasoning", "bob", "1779321385406-jbgs5l", "*.gdl"))
	}
}

// TestShortIDSuffix_AmbiguousMatch_ListsCandidates: two outbox records
// share the same 6-char suffix. The CLI error must list both with
// disambiguation context so the agent can pick.
func TestShortIDSuffix_AmbiguousMatch_ListsCandidates(t *testing.T) {
	root := shortIDProject(t, "bob")
	seedThought(t, root, "alice", "1779321385406-jbgs5l", "decision", "svc:auth", "fleet")
	seedThought(t, root, "bob", "1779321444221-jbgs5l", "hypothesis", "retry-pattern", "fleet")

	err := runConfirm(root, "jbgs5l", "", output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("ambiguous suffix: want error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "1779321385406-jbgs5l") || !strings.Contains(msg, "1779321444221-jbgs5l") {
		t.Errorf("ambiguous error missing one or both ids: %s", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "ambiguous") {
		t.Errorf("ambiguous error should name the condition: %s", msg)
	}
}

// TestShortIDSuffix_NoMatch_StillSurfacesNotFound — preserve the
// existing NoSuchThoughtError pathway when nothing matches.
func TestShortIDSuffix_NoMatch_StillSurfacesNotFound(t *testing.T) {
	root := shortIDProject(t, "bob")

	err := runConfirm(root, "abcdef", "", output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("no-match suffix: want error, got nil")
	}
	if !strings.Contains(err.Error(), "no such record") {
		t.Errorf("no-match: expected canonical 'no such record' wording, got: %s", err.Error())
	}
}

// TestFullID_StillWorksAlongsideShortID — regression guard. The exact
// canonical id MUST keep working alongside the new short-form path.
func TestFullID_StillWorksAlongsideShortID(t *testing.T) {
	root := shortIDProject(t, "bob")
	seedThought(t, root, "alice", "1779321385406-jbgs5l", "decision", "svc:auth", "fleet")

	if err := runConfirm(root, "1779321385406-jbgs5l", "", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("runConfirm full id (regression): %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "confirms", "1779321385406-jbgs5l.gdl")); err != nil {
		t.Errorf("confirm file at canonical path missing: %v", err)
	}
}

// TestShortIDSuffix_RespectsPrivacyFloor: bob resolving a 6-char suffix
// that matches alice's scope:agent record must NOT surface that record
// as a candidate — that would leak existence. Bob sees the same error
// as a true no-match (NoSuchThoughtError), not a PrivateRecordAuthzError
// (the latter would itself confirm the record exists).
func TestShortIDSuffix_RespectsPrivacyFloor(t *testing.T) {
	root := shortIDProject(t, "bob")
	seedThought(t, root, "alice", "1779321385406-jbgs5l", "hypothesis", "private", "agent")

	err := runConfirm(root, "jbgs5l", "", output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("bob -> alice's scope:agent suffix: want error")
	}
	if !strings.Contains(err.Error(), "no such record") {
		t.Errorf("privacy leak: error should be 'no such record' for non-author scope:agent suffix; got: %s", err.Error())
	}
}

// --- R29b: promote nudge --------------------------------------------------

// thoughtIDRegex matches the canonical thought-id shape used elsewhere
// (parentRegex in thought.go, decisionRegex in reason.go). Test uses
// it for the look-shape check; production code re-derives from the
// same constant.
var thoughtIDShapeForTest = regexp.MustCompile(`^[0-9]+-[a-z0-9]{6}$`)

// TestPromote_ThoughtIDShaped_NudgesTowardConfirm: a value matching
// <unix-millis>-<rand6> must NOT flow through the artifact-version
// parser. Emit a one-line redirect to `confirm` with a quorum
// explanation, and DO NOT write a ref.
func TestPromote_ThoughtIDShaped_NudgesTowardConfirm(t *testing.T) {
	root := shortIDProject(t, "alice")
	id := "1779321385406-jbgs5l"
	if !thoughtIDShapeForTest.MatchString(id) {
		t.Fatalf("test bug: %q must match thought-id shape", id)
	}

	err := runPromote(root, id, "live", output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("promote thought-id: want nudge error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(strings.ToLower(msg), "confirm") {
		t.Errorf("nudge missing 'confirm': %s", msg)
	}
	if !strings.Contains(msg, "thought-id") && !strings.Contains(msg, "thought id") {
		t.Errorf("nudge should name the thought-id concept so the agent can map the redirect: %s", msg)
	}
	// Promote MUST NOT have written a ref.
	if _, err := os.Stat(filepath.Join(root, ".rufio", "refs")); err == nil {
		// dir may exist from setup — verify it's empty of new ref files.
		matches, _ := filepath.Glob(filepath.Join(root, ".rufio", "refs", "**", "*.gdl"))
		if len(matches) > 0 {
			t.Errorf("promote nudge wrote a ref file (side effect): %v", matches)
		}
	}
}

// TestPromote_ShortIDSuffix_ResolvedToThought_NudgesTowardConfirm: the
// 6-char suffix that resolves to a thought must also nudge, not flow
// through the version parser (where the parser would just error with
// a confusing "promote requires <path>@<version>").
func TestPromote_ShortIDSuffix_ResolvedToThought_NudgesTowardConfirm(t *testing.T) {
	root := shortIDProject(t, "alice")
	seedThought(t, root, "alice", "1779321385406-jbgs5l", "decision", "svc:auth", "fleet")

	err := runPromote(root, "jbgs5l", "live", output.RenderOpts{Quiet: true})
	if err == nil {
		t.Fatal("promote short id: want nudge error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(strings.ToLower(msg), "confirm") {
		t.Errorf("short-id promote nudge missing 'confirm': %s", msg)
	}
}

// TestPromote_ArtifactPath_StillWorks — regression guard. A real
// `given/policy.md@v1` path must still flow through the artifact
// promotion path unchanged.
func TestPromote_ArtifactPath_StillWorks(t *testing.T) {
	root := shortIDProject(t, "alice")
	// Seed a draft ref so promote has something to advance.
	givenDir := filepath.Join(root, "given")
	if err := os.MkdirAll(givenDir, 0o755); err != nil {
		t.Fatalf("mkdir given: %v", err)
	}
	policyContent := []byte("policy text v1\n")
	if err := os.WriteFile(filepath.Join(givenDir, "policy.md"), policyContent, 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	// Use the production push path to materialize a draft ref the way
	// real users would, so promote has a legitimate source ref.
	pushOpts := output.RenderOpts{Quiet: true}
	if err := runPush("given/policy.md", "draft", root, pushOpts); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	_ = pushOpts
	// Promote v1 to live.
	if err := runPromote(root, "given/policy.md@v1", "live", output.RenderOpts{Quiet: true}); err != nil {
		t.Fatalf("promote artifact path (regression): %v", err)
	}
}
