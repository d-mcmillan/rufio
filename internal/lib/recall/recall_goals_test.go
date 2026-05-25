package recall

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// F2 (v1.0.6.2): `--types=goal` advertises goal as a valid record kind
// (it is in AllTypes and ValidateTypes accepts it), but Scan had no
// walker for live/goals/{active,completed,abandoned}/. Cold agents who
// wrote a goal and tried to recall it saw empty output, concluded the
// write had failed, and lost trust in the substrate.
//
// These unit tests pin the contract of scanGoals at the package level:
//   - missing dir → empty (fresh project)
//   - active / completed / abandoned dirs all walked
//   - on-disk fields (id/author/statement/scope/ts) projected
//   - state derived from source directory
//   - render goes through the columnar + JSON paths uniformly
//
// Privacy enforcement is on the Filter side (privacy.IsVisible against
// the populated r.Scope + r.Author) and is exercised at the integration
// level in test/integration/recall_goals_*_test.go.

// --- scanGoals ---

func TestScanGoals_MissingDirReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	got, err := scanGoals(root)
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len=%d want 0", len(got))
	}
}

func TestScanGoals_ActiveDirOnly_WalksAndProjects(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "goals", "active")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := gdl.Record{Type: "goal", Fields: []gdl.RecordField{
		{Key: "id", Value: "1779000000000-actv01"},
		{Key: "author", Value: "alice"},
		{Key: "statement", Value: "ship v1.0.7"},
		{Key: "scope", Value: "fleet"},
		{Key: "ts", Value: "2026-05-24T12:00:00Z"},
	}}
	path := filepath.Join(dir, "1779000000000-actv01.gdl")
	if err := os.WriteFile(path, []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := scanGoals(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1 (got=%+v)", len(got), got)
	}
	r := got[0]
	if r.Type != "goal" {
		t.Errorf("Type=%q want %q", r.Type, "goal")
	}
	if r.ID != "1779000000000-actv01" {
		t.Errorf("ID=%q want canonical id", r.ID)
	}
	if r.Author != "alice" {
		t.Errorf("Author=%q want alice", r.Author)
	}
	if r.Subject != "ship v1.0.7" {
		// Statement is rendered as Subject so the existing Match
		// substring search hits a positional query against the
		// statement text (per F2 brief: `recall "ship v1.0.7"` must
		// match a goal with that statement).
		t.Errorf("Subject=%q want statement %q", r.Subject, "ship v1.0.7")
	}
	if r.Content != "ship v1.0.7" {
		// Mirror to Content so substring search indexes statement too
		// (matches scanSummons mirroring Intent to Content).
		t.Errorf("Content=%q want statement mirrored %q", r.Content, "ship v1.0.7")
	}
	if r.Scope != "fleet" {
		t.Errorf("Scope=%q want fleet", r.Scope)
	}
	if r.TS != "2026-05-24T12:00:00Z" {
		t.Errorf("TS=%q", r.TS)
	}
	if r.Path != path {
		t.Errorf("Path=%q want %q", r.Path, path)
	}
}

func TestScanGoals_WalksAllThreeStates(t *testing.T) {
	root := t.TempDir()
	for _, state := range []string{"active", "completed", "abandoned"} {
		dir := filepath.Join(root, "live", "goals", state)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		rec := gdl.Record{Type: "goal", Fields: []gdl.RecordField{
			{Key: "id", Value: "1779000000000-" + state[:6]},
			{Key: "author", Value: "alice"},
			{Key: "statement", Value: "goal in " + state},
			{Key: "scope", Value: "fleet"},
			{Key: "ts", Value: "2026-05-24T12:00:00Z"},
		}}
		path := filepath.Join(dir, "1779000000000-"+state[:6]+".gdl")
		if err := os.WriteFile(path, []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := scanGoals(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3 (one per state dir)", len(got))
	}
	// Each one must carry Type=="goal" and the on-disk statement.
	seen := map[string]bool{}
	for _, r := range got {
		if r.Type != "goal" {
			t.Errorf("Type=%q want goal", r.Type)
		}
		seen[r.Subject] = true
	}
	for _, expect := range []string{"goal in active", "goal in completed", "goal in abandoned"} {
		if !seen[expect] {
			t.Errorf("missing subject %q in scanGoals output", expect)
		}
	}
}

func TestScanGoals_NonGoalRecordsSkipped(t *testing.T) {
	// A completed file carries both @goal AND @goal-complete; only the
	// @goal record should become a RecallRecord. The audit record is
	// metadata and not its own surface — mirroring scanLearned's
	// "non-@observation records skipped" pattern.
	root := t.TempDir()
	dir := filepath.Join(root, "live", "goals", "completed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	goalRec := gdl.Record{Type: "goal", Fields: []gdl.RecordField{
		{Key: "id", Value: "1779000000000-compl1"},
		{Key: "author", Value: "alice"},
		{Key: "statement", Value: "completed goal"},
		{Key: "scope", Value: "fleet"},
		{Key: "ts", Value: "2026-05-24T12:00:00Z"},
	}}
	auditRec := gdl.Record{Type: "goal-complete", Fields: []gdl.RecordField{
		{Key: "id", Value: "1779000000000-compl1"},
		{Key: "by", Value: "alice"},
		{Key: "outcome", Value: "shipped"},
		{Key: "ts", Value: "2026-05-24T13:00:00Z"},
	}}
	contents := gdl.RenderLine(goalRec) + "\n" + gdl.RenderLine(auditRec) + "\n"
	path := filepath.Join(dir, "1779000000000-compl1.gdl")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := scanGoals(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1 (audit record must be skipped)", len(got))
	}
	if got[0].Subject != "completed goal" {
		t.Errorf("Subject=%q want %q (must be the @goal statement, not the audit)", got[0].Subject, "completed goal")
	}
}

// --- end-to-end render exercise ---

// TestRenderColumnar_GoalRecord — a goal flows through the columnar
// renderer without panicking and emits the load-bearing fields on a
// single TAB-separated line. The exact column layout is not pinned
// (defers to the default-case columnarKey path which renders Subject as
// the key), but author/statement/scope must all appear.
func TestRenderColumnar_GoalRecord(t *testing.T) {
	t.Setenv("RUFIO_FULL_IDS", "1")
	var buf bytes.Buffer
	rec := RecallRecord{
		Type:    "goal",
		ID:      "1779000000000-actv01",
		TS:      "2026-05-24T12:00:00Z",
		Author:  "alice",
		Subject: "ship v1.0.7",
		Content: "ship v1.0.7",
		Scope:   "fleet",
	}
	if err := RenderColumnar(&buf, []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"goal", "alice", "ship v1.0.7", "fleet", "1779000000000-actv01"} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderColumnar(goal) missing %q:\n%s", want, out)
		}
	}
}

// TestRenderJSON_GoalRecord — a goal flows through RenderJSON with
// _type="goal" and the load-bearing fields. Confirms recall --types=goal
// --json downstream consumers (cold readers, mirror snapshot) see the
// kind discriminator correctly.
func TestRenderJSON_GoalRecord(t *testing.T) {
	var buf bytes.Buffer
	rec := RecallRecord{
		Type:    "goal",
		ID:      "1779000000000-actv01",
		TS:      "2026-05-24T12:00:00Z",
		Author:  "alice",
		Subject: "ship v1.0.7",
		Content: "ship v1.0.7",
		Scope:   "fleet",
		Path:    "/tmp/fake-root/live/goals/active/1779000000000-actv01.gdl",
	}
	if err := RenderJSON(&buf, "/tmp/fake-root", []RecallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	if obj["_type"] != "goal" {
		t.Errorf("_type=%v want %q", obj["_type"], "goal")
	}
	if obj["id"] != "1779000000000-actv01" {
		t.Errorf("id=%v", obj["id"])
	}
	if obj["author"] != "alice" {
		t.Errorf("author=%v", obj["author"])
	}
	if obj["subject"] != "ship v1.0.7" {
		t.Errorf("subject=%v", obj["subject"])
	}
	if obj["scope"] != "fleet" {
		t.Errorf("scope=%v", obj["scope"])
	}
	// Path is root-relative POSIX per RelativisePath.
	if obj["path"] != "live/goals/active/1779000000000-actv01.gdl" {
		t.Errorf("path=%v (must be root-relative POSIX)", obj["path"])
	}
}

// --- Filter privacy floor (predicate is called) ---

// TestFilter_GoalPrivacyFloor — a scope:agent goal authored by alice is
// HIDDEN from a recall by bob (privacy.IsVisible == false). The same
// goal IS visible to alice herself. This is the F2 #147 floor: the
// existing Filter privacy gate already runs on every record with a
// populated Scope+Author, so as long as scanGoals fills those, the gate
// fires.
func TestFilter_GoalPrivacyFloor(t *testing.T) {
	rec := RecallRecord{
		Type:    "goal",
		ID:      "1779000000000-priv01",
		TS:      "2026-05-24T12:00:00Z",
		Author:  "alice",
		Subject: "alice's private goal",
		Scope:   "agent",
	}
	// bob must not see it (privacy floor).
	out := Filter([]RecallRecord{rec}, FilterParams{CurrentAgent: "bob"})
	if len(out) != 0 {
		t.Errorf("bob saw alice's scope:agent goal (privacy floor breached):\n%+v", out)
	}
	// alice sees her own.
	out = Filter([]RecallRecord{rec}, FilterParams{CurrentAgent: "alice"})
	if len(out) != 1 {
		t.Errorf("alice must see her own scope:agent goal, got %d rows", len(out))
	}
	// Anonymous firehose preserves visibility (matches privacy.IsVisible).
	out = Filter([]RecallRecord{rec}, FilterParams{CurrentAgent: ""})
	if len(out) != 1 {
		t.Errorf("anonymous caller must see the goal (firehose), got %d rows", len(out))
	}
}
