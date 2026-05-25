package lineage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// --- helpers ---

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func seedDecisionFile(t *testing.T, root, stage, author, id, thoughtType string, withBundle bool, bundleRefs string) {
	t.Helper()
	body := "@thought|id:" + id + "|author:" + author + "|type:" + thoughtType +
		"|subject:agent:" + author + "|content:approve refund|scope:fleet|ts:2026-05-12T10:00:00Z|ttl:0\n"
	if withBundle {
		body += "@context-bundle|decision:" + id + "|refs:" + bundleRefs + "\n"
	}
	path := filepath.Join(root, "live", stage, author, id+".gdl")
	writeFile(t, path, body)
}

func seedRefFile(t *testing.T, root, contentPath, sha string, version int, stage string) {
	t.Helper()
	body := "@ref|path:" + contentPath +
		"|version:" + itoaTest(version) +
		"|sha256:" + sha +
		"|stage:" + stage +
		"|ts:2026-05-10T08:00:00Z|author:agent-a\n"
	path := filepath.Join(root, ".rufio", "refs", contentPath+".gdl")
	writeFile(t, path, body)
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func seedReasonFile(t *testing.T, root, author, decisionID, id, parent, ts string) {
	t.Helper()
	body := "@reason|id:" + id + "|author:" + author + "|content:step " + id + "|"
	if parent != "" {
		body += "parent:" + parent + "|"
	}
	body += "decision:" + decisionID + "|ts:" + ts + "\n"
	path := filepath.Join(root, "live", "reasoning", author, decisionID, id+".gdl")
	writeFile(t, path, body)
}

// --- LookupDecision (6) ---

func TestLookupDecision_HappyPath_Outbox(t *testing.T) {
	root := t.TempDir()
	id := "1727000000-dec001"
	seedDecisionFile(t, root, "outbox", "agent-a", id, "decision", true, "abc123")
	dec, err := LookupDecision(root, id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Author != "agent-a" {
		t.Errorf("author=%q, want agent-a", dec.Author)
	}
	if dec.Expired {
		t.Errorf("Expired=true, want false")
	}
	if len(dec.Bundle) != 1 {
		t.Fatalf("Bundle len=%d, want 1", len(dec.Bundle))
	}
	if dec.Bundle[0].Get("refs") != "abc123" {
		t.Errorf("bundle refs=%q, want abc123", dec.Bundle[0].Get("refs"))
	}
	if dec.Subject != "agent:agent-a" {
		t.Errorf("subject=%q", dec.Subject)
	}
	if dec.Scope != "fleet" {
		t.Errorf("scope=%q", dec.Scope)
	}
}

func TestLookupDecision_HappyPath_Expired(t *testing.T) {
	root := t.TempDir()
	id := "1727000000-dec002"
	seedDecisionFile(t, root, "expired", "agent-a", id, "decision", true, "abc123")
	dec, err := LookupDecision(root, id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dec.Expired {
		t.Errorf("Expired=false, want true")
	}
	if dec.Author != "agent-a" {
		t.Errorf("author=%q, want agent-a", dec.Author)
	}
}

func TestLookupDecision_NoSuch_ReturnsError(t *testing.T) {
	root := t.TempDir()
	_, err := LookupDecision(root, "1727000000-missing")
	var got *rufioerr.NoSuchDecisionError
	if !errors.As(err, &got) {
		t.Fatalf("want *NoSuchDecisionError, got %T %v", err, err)
	}
	if got.ID != "1727000000-missing" {
		t.Errorf("ID=%q", got.ID)
	}
}

func TestLookupDecision_NotADecision_Hypothesis(t *testing.T) {
	root := t.TempDir()
	id := "1727000000-hyp001"
	seedDecisionFile(t, root, "outbox", "agent-a", id, "hypothesis", false, "")
	_, err := LookupDecision(root, id)
	var got *rufioerr.NotADecisionError
	if !errors.As(err, &got) {
		t.Fatalf("want *NotADecisionError, got %T %v", err, err)
	}
	if got.Type != "hypothesis" {
		t.Errorf("Type=%q, want hypothesis", got.Type)
	}
	if got.ID != id {
		t.Errorf("ID=%q, want %s", got.ID, id)
	}
}

func TestLookupDecision_NotADecision_Observation(t *testing.T) {
	root := t.TempDir()
	id := "1727000000-obs001"
	seedDecisionFile(t, root, "outbox", "agent-a", id, "observation", false, "")
	_, err := LookupDecision(root, id)
	var got *rufioerr.NotADecisionError
	if !errors.As(err, &got) {
		t.Fatalf("want *NotADecisionError, got %T %v", err, err)
	}
	if got.Type != "observation" {
		t.Errorf("Type=%q, want observation", got.Type)
	}
}

func TestLookupDecision_MalformedRecord(t *testing.T) {
	root := t.TempDir()
	id := "1727000000-malf01"
	// File present, but first record is not @thought.
	path := filepath.Join(root, "live", "outbox", "agent-a", id+".gdl")
	writeFile(t, path, "@route|target:agent-b|ts:2026-05-12T10:00:00Z\n")
	_, err := LookupDecision(root, id)
	var got *rufioerr.NoSuchDecisionError
	if !errors.As(err, &got) {
		t.Fatalf("want *NoSuchDecisionError, got %T %v", err, err)
	}
}

func TestLookupDecision_OutboxPrecedesExpired(t *testing.T) {
	// If the same id exists in both outbox and expired (race during a
	// sweep), Lookup must prefer the outbox copy and report
	// Expired=false. Defensive — outbox is the canonical view.
	root := t.TempDir()
	id := "1727000000-dec099"
	seedDecisionFile(t, root, "outbox", "agent-a", id, "decision", true, "abc1")
	seedDecisionFile(t, root, "expired", "agent-a", id, "decision", true, "abc1")
	dec, err := LookupDecision(root, id)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if dec.Expired {
		t.Errorf("Expired=true, want false (outbox should win over expired)")
	}
}

// --- ResolveBundleRefs (5) ---

func TestResolveBundleRefs_AllResolved(t *testing.T) {
	root := t.TempDir()
	sha := "abc1234567890"
	seedRefFile(t, root, "given/policy.md", sha, 1, "live")
	bundle := []gdl.Record{
		{Type: "context-bundle", Fields: []gdl.RecordField{
			{Key: "decision", Value: "1727000000-dec001"},
			{Key: "refs", Value: sha},
		}},
	}
	refs, err := ResolveBundleRefs(root, bundle)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("len=%d, want 1", len(refs))
	}
	if !refs[0].Resolved {
		t.Errorf("Resolved=false, want true")
	}
	if refs[0].Path != "given/policy.md" {
		t.Errorf("Path=%q, want given/policy.md", refs[0].Path)
	}
	if refs[0].Version != 1 {
		t.Errorf("Version=%d, want 1", refs[0].Version)
	}
	if refs[0].SHA256 != sha {
		t.Errorf("SHA256=%q", refs[0].SHA256)
	}
}

func TestResolveBundleRefs_PartialUnresolved(t *testing.T) {
	root := t.TempDir()
	sha1 := "aaa111"
	sha2 := "bbb222" // not seeded
	seedRefFile(t, root, "given/policy.md", sha1, 1, "live")
	bundle := []gdl.Record{
		{Type: "context-bundle", Fields: []gdl.RecordField{
			{Key: "decision", Value: "1727000000-dec001"},
			{Key: "refs", Value: sha1 + "," + sha2},
		}},
	}
	refs, err := ResolveBundleRefs(root, bundle)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("len=%d, want 2", len(refs))
	}
	if !refs[0].Resolved || refs[0].Path != "given/policy.md" {
		t.Errorf("refs[0]=%+v, want resolved given/policy.md", refs[0])
	}
	if refs[1].Resolved {
		t.Errorf("refs[1].Resolved=true, want false")
	}
	if refs[1].SHA256 != sha2 {
		t.Errorf("refs[1].SHA256=%q, want %s", refs[1].SHA256, sha2)
	}
}

func TestResolveBundleRefs_EmptyBundle(t *testing.T) {
	root := t.TempDir()
	refs, err := ResolveBundleRefs(root, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("len=%d, want 0", len(refs))
	}
}

func TestResolveBundleRefs_MultiSegmentPath(t *testing.T) {
	root := t.TempDir()
	sha := "deadbeef"
	seedRefFile(t, root, "given/customer/data.json", sha, 3, "staged")
	bundle := []gdl.Record{
		{Type: "context-bundle", Fields: []gdl.RecordField{
			{Key: "refs", Value: sha},
		}},
	}
	refs, err := ResolveBundleRefs(root, bundle)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("len=%d", len(refs))
	}
	if refs[0].Path != "given/customer/data.json" {
		t.Errorf("Path=%q, want given/customer/data.json", refs[0].Path)
	}
	if refs[0].Version != 3 {
		t.Errorf("Version=%d, want 3", refs[0].Version)
	}
	if refs[0].Stage != "staged" {
		t.Errorf("Stage=%q, want staged", refs[0].Stage)
	}
}

func TestResolveBundleRefs_LearnedRefs(t *testing.T) {
	root := t.TempDir()
	sha := "feedface"
	seedRefFile(t, root, "learned/agent-a/insights.md", sha, 2, "live")
	bundle := []gdl.Record{
		{Type: "context-bundle", Fields: []gdl.RecordField{
			{Key: "refs", Value: sha},
		}},
	}
	refs, err := ResolveBundleRefs(root, bundle)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(refs) != 1 || !refs[0].Resolved {
		t.Fatalf("refs=%+v", refs)
	}
	if refs[0].Path != "learned/agent-a/insights.md" {
		t.Errorf("Path=%q, want learned/agent-a/insights.md", refs[0].Path)
	}
}

// --- WalkReasoning (6) ---

func TestWalkReasoning_EmptyDir_ReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	steps, err := WalkReasoning(root, "1727000000-dec001")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(steps) != 0 {
		t.Errorf("len=%d, want 0", len(steps))
	}
}

func TestWalkReasoning_SingleStep(t *testing.T) {
	root := t.TempDir()
	decID := "1727000000-dec001"
	seedReasonFile(t, root, "agent-a", decID, "1727000001-step01", "", "2026-05-12T10:00:00Z")
	steps, err := WalkReasoning(root, decID)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("len=%d, want 1", len(steps))
	}
	if steps[0].Depth != 0 {
		t.Errorf("Depth=%d, want 0", steps[0].Depth)
	}
	if steps[0].Parent != "" {
		t.Errorf("Parent=%q, want empty", steps[0].Parent)
	}
}

func TestWalkReasoning_ParentChain_TwoSteps(t *testing.T) {
	root := t.TempDir()
	decID := "1727000000-dec001"
	rootID := "1727000001-step01"
	childID := "1727000002-step02"
	seedReasonFile(t, root, "agent-a", decID, rootID, "", "2026-05-12T10:00:00Z")
	seedReasonFile(t, root, "agent-a", decID, childID, rootID, "2026-05-12T10:01:00Z")
	steps, err := WalkReasoning(root, decID)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("len=%d, want 2", len(steps))
	}
	if steps[0].ID != rootID {
		t.Errorf("steps[0].ID=%q, want %s", steps[0].ID, rootID)
	}
	if steps[0].Depth != 0 {
		t.Errorf("steps[0].Depth=%d, want 0", steps[0].Depth)
	}
	if steps[1].ID != childID {
		t.Errorf("steps[1].ID=%q, want %s", steps[1].ID, childID)
	}
	if steps[1].Depth != 1 {
		t.Errorf("steps[1].Depth=%d, want 1", steps[1].Depth)
	}
}

func TestWalkReasoning_ParentChain_ThreeSteps_DepthOrder(t *testing.T) {
	root := t.TempDir()
	decID := "1727000000-dec001"
	a, b, c := "1727000001-stepaa", "1727000002-stepbb", "1727000003-stepcc"
	seedReasonFile(t, root, "agent-a", decID, a, "", "2026-05-12T10:00:00Z")
	seedReasonFile(t, root, "agent-a", decID, b, a, "2026-05-12T10:01:00Z")
	seedReasonFile(t, root, "agent-a", decID, c, b, "2026-05-12T10:02:00Z")
	steps, err := WalkReasoning(root, decID)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("len=%d, want 3", len(steps))
	}
	wantOrder := []string{a, b, c}
	wantDepth := []int{0, 1, 2}
	for i, s := range steps {
		if s.ID != wantOrder[i] {
			t.Errorf("steps[%d].ID=%q, want %s", i, s.ID, wantOrder[i])
		}
		if s.Depth != wantDepth[i] {
			t.Errorf("steps[%d].Depth=%d, want %d", i, s.Depth, wantDepth[i])
		}
	}
}

func TestWalkReasoning_MultipleRoots_SortedByTS(t *testing.T) {
	root := t.TempDir()
	decID := "1727000000-dec001"
	// Two orphans (parent empty). The one with the later TS must come last.
	early := "1727000001-stepea"
	late := "1727000099-steple"
	seedReasonFile(t, root, "agent-a", decID, late, "", "2026-05-12T11:00:00Z")
	seedReasonFile(t, root, "agent-a", decID, early, "", "2026-05-12T10:00:00Z")
	steps, err := WalkReasoning(root, decID)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("len=%d, want 2", len(steps))
	}
	if steps[0].ID != early {
		t.Errorf("steps[0].ID=%q, want %s (earlier TS first)", steps[0].ID, early)
	}
	if steps[1].ID != late {
		t.Errorf("steps[1].ID=%q, want %s", steps[1].ID, late)
	}
}

func TestWalkReasoning_MalformedFile_SkippedSilently(t *testing.T) {
	root := t.TempDir()
	decID := "1727000000-dec001"
	good := "1727000001-stepgg"
	seedReasonFile(t, root, "agent-a", decID, good, "", "2026-05-12T10:00:00Z")
	// Hand-write a malformed file alongside the good one.
	bad := filepath.Join(root, "live", "reasoning", "agent-a", decID, "bad.gdl")
	writeFile(t, bad, "this is not a gdl record\n")
	steps, err := WalkReasoning(root, decID)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("len=%d, want 1 (malformed skipped)", len(steps))
	}
	if steps[0].ID != good {
		t.Errorf("steps[0].ID=%q, want %s", steps[0].ID, good)
	}
}

// TestWalkReasoning_GlobsAllAuthors covers the #138 fix: a reasoning
// chain whose @reason records live under TWO different agents'
// live/reasoning/<author>/<decisionID>/ dirs must both come back from a
// single WalkReasoning call. The CLI previously passed the decision's
// author when invoking this, so any cross-agent @reason was dropped
// silently — invisible reasoning is the worst kind of bug for the
// shared-cognition primitive.
func TestWalkReasoning_GlobsAllAuthors(t *testing.T) {
	root := t.TempDir()
	decID := "1727000000-dec001"
	rA := "1727000001-stepaa"
	rB := "1727000002-stepbb"
	seedReasonFile(t, root, "agent-a", decID, rA, "", "2026-05-12T10:00:00Z")
	seedReasonFile(t, root, "agent-b", decID, rB, "", "2026-05-12T10:01:00Z")

	steps, err := WalkReasoning(root, decID)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("len=%d, want 2 (both authors' reasons), steps=%+v", len(steps), steps)
	}
	authors := map[string]bool{steps[0].Author: true, steps[1].Author: true}
	if !authors["agent-a"] || !authors["agent-b"] {
		t.Errorf("authors=%v, want both agent-a and agent-b present", authors)
	}
}

// --- WalkTopicAdjacent (K1 / R28 — cognition-vocabulary inclusivity) ---

// seedThoughtFile is a thin helper that writes a @thought record into
// live/outbox/<author>/<id>.gdl with arbitrary subject/type/scope/ts.
// Mirrors seedDecisionFile but is parameterised for non-decision types
// (hypothesis / observation / focus / question) so the topic-adjacent
// walker has a richer corpus to filter against.
func seedThoughtFile(t *testing.T, root, author, id, thoughtType, subject, scope, content, ts string) {
	t.Helper()
	body := "@thought|id:" + id +
		"|author:" + author +
		"|type:" + thoughtType +
		"|subject:" + subject +
		"|content:" + content +
		"|scope:" + scope +
		"|ts:" + ts +
		"|ttl:0\n"
	path := filepath.Join(root, "live", "outbox", author, id+".gdl")
	writeFile(t, path, body)
}

// seedObservationFile writes an @observation record into
// learned/<subject-path>/<id>.gdlm. observation.SubjectPath nests one
// directory per colon-segment, but for the unit test we only need one
// subject family ("svc:auth") so the path is fixed at learned/svc/auth/.
func seedObservationFile(t *testing.T, root, author, id, subject, scope, object, ts string) {
	t.Helper()
	body := "@observation|id:" + id +
		"|author:" + author +
		"|subject:" + subject +
		"|predicate:noted" +
		"|object:" + object +
		"|scope:" + scope +
		"|ts:" + ts + "\n"
	parts := strings.Split(subject, ":")
	rel := filepath.Join(parts...)
	path := filepath.Join(root, "learned", rel, id+".gdlm")
	writeFile(t, path, body)
}

func TestWalkTopicAdjacent_EmptyDir_ReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	voices, err := WalkTopicAdjacent(root, "svc:auth", "2026-05-12T10:00:00Z", "")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(voices) != 0 {
		t.Errorf("len=%d, want 0", len(voices))
	}
}

// TestWalkTopicAdjacent_IncludesPostDecisionFleetThoughts — the core K1
// promise: a non-decision @thought (e.g. --type=focus / hypothesis) on
// the same subject, posted AFTER the decision, appears as a
// topic-adjacent voice. This is the "bullet-point narrative agent" case
// from R28 — agents whose primary cognitive output is `think` rather
// than `reason`.
func TestWalkTopicAdjacent_IncludesPostDecisionFleetThoughts(t *testing.T) {
	root := t.TempDir()
	decTS := "2026-05-12T10:00:00Z"
	// Hypothesis from a peer, posted after the decision.
	seedThoughtFile(t, root, "agent-b", "1727000010-hypbb", "hypothesis",
		"svc:auth", "fleet", "what about scrypt", "2026-05-12T10:02:00Z")
	voices, err := WalkTopicAdjacent(root, "svc:auth", decTS, "")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(voices) != 1 {
		t.Fatalf("len=%d, want 1 voice", len(voices))
	}
	if voices[0].Author != "agent-b" || voices[0].ThoughtType != "hypothesis" {
		t.Errorf("voice=%+v, want agent-b/hypothesis", voices[0])
	}
}

// TestWalkTopicAdjacent_IncludesObservations — @observation records under
// learned/<subject-path>/ on the same subject + post-decision also count
// as voiced contributions. Observations are the second cognitive mode
// (learned facts) and must not be invisible in lineage either.
func TestWalkTopicAdjacent_IncludesObservations(t *testing.T) {
	root := t.TempDir()
	decTS := "2026-05-12T10:00:00Z"
	seedObservationFile(t, root, "agent-c", "1727000020-obscc",
		"svc:auth", "fleet", "argon2id 2x faster", "2026-05-12T10:05:00Z")
	voices, err := WalkTopicAdjacent(root, "svc:auth", decTS, "")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(voices) != 1 {
		t.Fatalf("len=%d, want 1, voices=%+v", len(voices), voices)
	}
	if voices[0].Type != "observation" || voices[0].Author != "agent-c" {
		t.Errorf("voice=%+v, want observation/agent-c", voices[0])
	}
}

// TestWalkTopicAdjacent_ExcludesPreDecisionRecords — only post-decision
// records count. A hypothesis from BEFORE the decision is context, not
// lineage; the existing context-bundle field handles that.
func TestWalkTopicAdjacent_ExcludesPreDecisionRecords(t *testing.T) {
	root := t.TempDir()
	decTS := "2026-05-12T10:00:00Z"
	// Pre-decision (excluded).
	seedThoughtFile(t, root, "agent-b", "1727000001-hyppre", "hypothesis",
		"svc:auth", "fleet", "pre-decision idea", "2026-05-12T09:00:00Z")
	// Post-decision (included).
	seedThoughtFile(t, root, "agent-b", "1727000010-hyppost", "hypothesis",
		"svc:auth", "fleet", "post-decision idea", "2026-05-12T10:05:00Z")
	voices, err := WalkTopicAdjacent(root, "svc:auth", decTS, "")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(voices) != 1 {
		t.Fatalf("len=%d, want 1 (only post-decision), voices=%+v", len(voices), voices)
	}
	if voices[0].Content != "post-decision idea" {
		t.Errorf("voices[0].Content=%q, want post-decision idea", voices[0].Content)
	}
}

// TestWalkTopicAdjacent_DifferentSubject_Excluded — only same-subject
// records count. A thought on svc:billing must not appear in the lineage
// of a svc:auth decision.
func TestWalkTopicAdjacent_DifferentSubject_Excluded(t *testing.T) {
	root := t.TempDir()
	decTS := "2026-05-12T10:00:00Z"
	seedThoughtFile(t, root, "agent-b", "1727000010-hypbb", "hypothesis",
		"svc:billing", "fleet", "unrelated topic", "2026-05-12T10:05:00Z")
	voices, err := WalkTopicAdjacent(root, "svc:auth", decTS, "")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(voices) != 0 {
		t.Errorf("len=%d, want 0 (different subject), voices=%+v", len(voices), voices)
	}
}

// TestWalkTopicAdjacent_RespectsPrivacyFloor — another agent's
// scope:agent thought on the same subject must NOT surface to a third
// agent's lineage view (#147 privacy floor). The walker is scope-blind;
// CLI layer applies privacy.IsVisible, mirroring filterReasoningPrivacy.
// At the walker level we still emit the record (so the CLI can apply
// the gate uniformly), but we test the gate end-to-end at the CLI level.
// Here we assert the walker carries the scope+author through so the gate
// can see them.
func TestWalkTopicAdjacent_RespectsPrivacyFloor(t *testing.T) {
	root := t.TempDir()
	decTS := "2026-05-12T10:00:00Z"
	seedThoughtFile(t, root, "agent-b", "1727000010-hypbb", "hypothesis",
		"svc:auth", "agent", "private to b", "2026-05-12T10:05:00Z")
	voices, err := WalkTopicAdjacent(root, "svc:auth", decTS, "")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(voices) != 1 {
		t.Fatalf("len=%d, want 1 (walker is scope-blind, CLI filters)", len(voices))
	}
	if voices[0].Scope != "agent" {
		t.Errorf("Scope=%q, want agent (walker must carry it for CLI privacy gate)", voices[0].Scope)
	}
	if voices[0].Author != "agent-b" {
		t.Errorf("Author=%q, want agent-b", voices[0].Author)
	}
}

// TestWalkTopicAdjacent_SortedAscendingByTs — voices section is sorted
// ts-ascending so a cold reader sees the conversation in the order it
// happened.
func TestWalkTopicAdjacent_SortedAscendingByTs(t *testing.T) {
	root := t.TempDir()
	decTS := "2026-05-12T10:00:00Z"
	seedThoughtFile(t, root, "agent-c", "1727000030-hypcc", "hypothesis",
		"svc:auth", "fleet", "third", "2026-05-12T10:09:00Z")
	seedThoughtFile(t, root, "agent-b", "1727000020-hypbb", "hypothesis",
		"svc:auth", "fleet", "first", "2026-05-12T10:01:00Z")
	seedThoughtFile(t, root, "agent-d", "1727000025-hypdd", "hypothesis",
		"svc:auth", "fleet", "second", "2026-05-12T10:05:00Z")
	voices, err := WalkTopicAdjacent(root, "svc:auth", decTS, "")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(voices) != 3 {
		t.Fatalf("len=%d, want 3", len(voices))
	}
	for i, want := range []string{"first", "second", "third"} {
		if voices[i].Content != want {
			t.Errorf("voices[%d].Content=%q, want %q", i, voices[i].Content, want)
		}
	}
}

// TestWalkTopicAdjacent_ExcludesDecisionItself — the decision @thought
// shares subject + ts with itself; it must NOT appear as its own
// topic-adjacent voice (ts > decision.ts is strict).
func TestWalkTopicAdjacent_ExcludesDecisionItself(t *testing.T) {
	root := t.TempDir()
	decTS := "2026-05-12T10:00:00Z"
	// Seed the decision file itself — same subject, same ts.
	seedDecisionFile(t, root, "outbox", "agent-a", "1727000000-dec001", "decision", false, "")
	voices, err := WalkTopicAdjacent(root, "agent:agent-a", decTS, "")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(voices) != 0 {
		// Note: seedDecisionFile pins ts:2026-05-12T10:00:00Z, identical
		// to decTS — strict "ts > decTS" excludes it.
		t.Errorf("len=%d, want 0 (decision excludes itself via strict ts>), voices=%+v", len(voices), voices)
	}
}
