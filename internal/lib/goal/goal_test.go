package goal

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// ---- ValidateStatement ------------------------------------------------------

func TestValidateStatement_Empty(t *testing.T) {
	err := ValidateStatement("")
	var got *rufioerr.InvalidStatementError
	if !errors.As(err, &got) {
		t.Fatalf("want *InvalidStatementError, got %T (%v)", err, err)
	}
}

func TestValidateStatement_Whitespace(t *testing.T) {
	for _, raw := range []string{" ", "\t", "\n", "  \t \n "} {
		err := ValidateStatement(raw)
		var got *rufioerr.InvalidStatementError
		if !errors.As(err, &got) {
			t.Errorf("ValidateStatement(%q): want *InvalidStatementError, got %T (%v)", raw, err, err)
		}
	}
}

func TestValidateStatement_HappyPath(t *testing.T) {
	for _, s := range []string{"reduce churn by 10%", "x", "a very long statement with many words"} {
		if err := ValidateStatement(s); err != nil {
			t.Errorf("ValidateStatement(%q): unexpected %v", s, err)
		}
	}
}

// ---- GenerateID -------------------------------------------------------------

func TestGenerateID_FormatMatches(t *testing.T) {
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	re := regexp.MustCompile(`^[0-9]+-[a-z0-9]{6}$`)
	if !re.MatchString(id) {
		t.Errorf("GenerateID returned %q, expected match for ^[0-9]+-[a-z0-9]{6}$", id)
	}
}

// ---- BuildGoalRecord --------------------------------------------------------

func TestBuildGoalRecord_AllFields(t *testing.T) {
	rec := BuildGoalRecord("123-abcdef", "alice", "reduce churn", "2026-06-01", "999-zzzzzz", "agent", "2026-05-12T12:00:00Z")
	if rec.Type != "goal" {
		t.Errorf("Type=%q, want goal", rec.Type)
	}
	wantKeys := []string{"id", "author", "statement", "by", "parent", "scope", "ts"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d (%+v)", len(rec.Fields), len(wantKeys), rec.Fields)
	}
	for i, k := range wantKeys {
		if rec.Fields[i].Key != k {
			t.Errorf("Fields[%d].Key=%q, want %q", i, rec.Fields[i].Key, k)
		}
	}
	if rec.Get("id") != "123-abcdef" || rec.Get("author") != "alice" ||
		rec.Get("statement") != "reduce churn" || rec.Get("by") != "2026-06-01" ||
		rec.Get("parent") != "999-zzzzzz" || rec.Get("scope") != "agent" ||
		rec.Get("ts") != "2026-05-12T12:00:00Z" {
		t.Errorf("field values wrong: %+v", rec.Fields)
	}
}

func TestBuildGoalRecord_OmitsByWhenEmpty(t *testing.T) {
	rec := BuildGoalRecord("123-abcdef", "alice", "reduce churn", "", "999-zzzzzz", "agent", "2026-05-12T12:00:00Z")
	wantKeys := []string{"id", "author", "statement", "parent", "scope", "ts"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d (%+v)", len(rec.Fields), len(wantKeys), rec.Fields)
	}
	for i, k := range wantKeys {
		if rec.Fields[i].Key != k {
			t.Errorf("Fields[%d].Key=%q, want %q", i, rec.Fields[i].Key, k)
		}
	}
	if rec.Get("by") != "" {
		t.Errorf("expected by absent, got %q", rec.Get("by"))
	}
}

func TestBuildGoalRecord_OmitsParentWhenEmpty(t *testing.T) {
	rec := BuildGoalRecord("123-abcdef", "alice", "reduce churn", "2026-06-01", "", "agent", "2026-05-12T12:00:00Z")
	wantKeys := []string{"id", "author", "statement", "by", "scope", "ts"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d (%+v)", len(rec.Fields), len(wantKeys), rec.Fields)
	}
	for i, k := range wantKeys {
		if rec.Fields[i].Key != k {
			t.Errorf("Fields[%d].Key=%q, want %q", i, rec.Fields[i].Key, k)
		}
	}
}

func TestBuildGoalRecord_OmitsBothWhenEmpty(t *testing.T) {
	rec := BuildGoalRecord("123-abcdef", "alice", "reduce churn", "", "", "agent", "2026-05-12T12:00:00Z")
	wantKeys := []string{"id", "author", "statement", "scope", "ts"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d (%+v)", len(rec.Fields), len(wantKeys), rec.Fields)
	}
}

// ---- BuildCompleteRecord ----------------------------------------------------

func TestBuildCompleteRecord_FieldOrder(t *testing.T) {
	rec := BuildCompleteRecord("123-abcdef", "alice", "shipped — churn down 12%", "2026-05-12T12:34:00Z")
	if rec.Type != "goal-complete" {
		t.Errorf("Type=%q, want goal-complete", rec.Type)
	}
	wantKeys := []string{"id", "by", "outcome", "ts"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d", len(rec.Fields), len(wantKeys))
	}
	for i, k := range wantKeys {
		if rec.Fields[i].Key != k {
			t.Errorf("Fields[%d].Key=%q, want %q", i, rec.Fields[i].Key, k)
		}
	}
	if rec.Get("outcome") != "shipped — churn down 12%" {
		t.Errorf("outcome=%q, want %q", rec.Get("outcome"), "shipped — churn down 12%")
	}
}

// ---- BuildAbandonRecord -----------------------------------------------------

func TestBuildAbandonRecord_FieldOrder(t *testing.T) {
	rec := BuildAbandonRecord("123-abcdef", "alice", "deprioritised", "2026-05-12T12:34:00Z")
	if rec.Type != "goal-abandon" {
		t.Errorf("Type=%q, want goal-abandon", rec.Type)
	}
	wantKeys := []string{"id", "by", "reason", "ts"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d", len(rec.Fields), len(wantKeys))
	}
	for i, k := range wantKeys {
		if rec.Fields[i].Key != k {
			t.Errorf("Fields[%d].Key=%q, want %q", i, rec.Fields[i].Key, k)
		}
	}
	if rec.Get("reason") != "deprioritised" {
		t.Errorf("reason=%q, want %q", rec.Get("reason"), "deprioritised")
	}
}

// ---- WriteActive ------------------------------------------------------------

func TestWriteActive_RoundtripParses(t *testing.T) {
	root := t.TempDir()
	rec := BuildGoalRecord("123-abcdef", "alice", "reduce churn", "2026-06-01", "", "agent", "2026-05-12T12:00:00Z")
	if err := WriteActive(root, "123-abcdef", rec); err != nil {
		t.Fatalf("WriteActive: %v", err)
	}
	path := filepath.Join(root, "live", "goals", "active", "123-abcdef.gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if records[0].Type != "goal" {
		t.Errorf("Type=%q, want goal", records[0].Type)
	}
	if records[0].Get("id") != "123-abcdef" || records[0].Get("author") != "alice" {
		t.Errorf("loaded fields wrong: %+v", records[0].Fields)
	}
	if records[0].Get("statement") != "reduce churn" {
		t.Errorf("statement=%q, want reduce churn", records[0].Get("statement"))
	}
}

// ---- LoadAnyState -----------------------------------------------------------

// seedGoalFile writes a goal file into the given state dir, optionally
// with additional audit records appended.
func seedGoalFile(t *testing.T, root, id, dir, author, ts string, extra ...gdl.Record) {
	t.Helper()
	full := filepath.Join(root, "live", "goals", dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rec := BuildGoalRecord(id, author, "do the thing", "2026-12-31", "", "agent", ts)
	lines := []string{gdl.RenderLine(rec)}
	for _, r := range extra {
		lines = append(lines, gdl.RenderLine(r))
	}
	if err := os.WriteFile(filepath.Join(full, id+".gdl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestLoadAnyState_Active(t *testing.T) {
	root := t.TempDir()
	seedGoalFile(t, root, "111-aaaaaa", "active", "alice", "2026-05-12T12:00:00Z")
	got, err := LoadAnyState(root, "111-aaaaaa")
	if err != nil {
		t.Fatalf("LoadAnyState: %v", err)
	}
	if got.State != StateActive {
		t.Errorf("State=%q, want %q", got.State, StateActive)
	}
	if got.ID != "111-aaaaaa" || got.Author != "alice" || got.Statement != "do the thing" {
		t.Errorf("loaded goal wrong: %+v", got)
	}
	if got.By != "2026-12-31" {
		t.Errorf("By=%q, want 2026-12-31", got.By)
	}
	if got.Scope != "agent" {
		t.Errorf("Scope=%q, want agent", got.Scope)
	}
}

func TestLoadAnyState_Completed(t *testing.T) {
	root := t.TempDir()
	seedGoalFile(t, root, "222-bbbbbb", "completed", "alice", "2026-05-12T12:00:00Z",
		BuildCompleteRecord("222-bbbbbb", "alice", "done", "2026-05-12T13:00:00Z"))
	got, err := LoadAnyState(root, "222-bbbbbb")
	if err != nil {
		t.Fatalf("LoadAnyState: %v", err)
	}
	if got.State != StateCompleted {
		t.Errorf("State=%q, want %q", got.State, StateCompleted)
	}
	if got.Outcome != "done" {
		t.Errorf("Outcome=%q, want done", got.Outcome)
	}
	if got.CompletedBy != "alice" || got.CompletedAt != "2026-05-12T13:00:00Z" {
		t.Errorf("complete audit fields wrong: by=%q at=%q", got.CompletedBy, got.CompletedAt)
	}
}

func TestLoadAnyState_Abandoned(t *testing.T) {
	root := t.TempDir()
	seedGoalFile(t, root, "333-cccccc", "abandoned", "alice", "2026-05-12T12:00:00Z",
		BuildAbandonRecord("333-cccccc", "alice", "deprioritised", "2026-05-12T13:00:00Z"))
	got, err := LoadAnyState(root, "333-cccccc")
	if err != nil {
		t.Fatalf("LoadAnyState: %v", err)
	}
	if got.State != StateAbandoned {
		t.Errorf("State=%q, want %q", got.State, StateAbandoned)
	}
	if got.Reason != "deprioritised" {
		t.Errorf("Reason=%q, want deprioritised", got.Reason)
	}
	if got.AbandonedBy != "alice" {
		t.Errorf("AbandonedBy=%q, want alice", got.AbandonedBy)
	}
}

func TestLoadAnyState_NotFound(t *testing.T) {
	root := t.TempDir()
	_, err := LoadAnyState(root, "nope-000000")
	var got *rufioerr.NoSuchGoalError
	if !errors.As(err, &got) {
		t.Fatalf("want *NoSuchGoalError, got %T (%v)", err, err)
	}
	if got.ID != "nope-000000" {
		t.Errorf("ID=%q, want nope-000000", got.ID)
	}
}

// ---- MoveToCompleted --------------------------------------------------------

func TestMoveToCompleted_HappyPath(t *testing.T) {
	root := t.TempDir()
	seedGoalFile(t, root, "555-eeeeee", "active", "alice", "2026-05-12T12:00:00Z")
	err := MoveToCompleted(root, "555-eeeeee", "alice", "shipped", "2026-05-12T12:34:56Z")
	if err != nil {
		t.Fatalf("MoveToCompleted: %v", err)
	}

	// Active file gone.
	activePath := filepath.Join(root, "live", "goals", "active", "555-eeeeee.gdl")
	if _, err := os.Stat(activePath); !os.IsNotExist(err) {
		t.Errorf("active file still exists (err=%v)", err)
	}

	// Completed file has @goal + @goal-complete.
	completedPath := filepath.Join(root, "live", "goals", "completed", "555-eeeeee.gdl")
	bs, err := os.ReadFile(completedPath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", completedPath, err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].Type != "goal" {
		t.Errorf("records[0].Type=%q, want goal", records[0].Type)
	}
	if records[1].Type != "goal-complete" {
		t.Errorf("records[1].Type=%q, want goal-complete", records[1].Type)
	}
	if records[1].Get("outcome") != "shipped" {
		t.Errorf("outcome=%q, want shipped", records[1].Get("outcome"))
	}
	if records[1].Get("by") != "alice" {
		t.Errorf("by=%q, want alice", records[1].Get("by"))
	}
}

func TestMoveToCompleted_AlreadyCompleted_RaceReturnsNoSuchGoal(t *testing.T) {
	root := t.TempDir()
	// Seed: active still present (simulating racing reader), but dest
	// already exists from a prior winner. Link should EEXIST.
	seedGoalFile(t, root, "race-111", "active", "alice", "2026-05-12T12:00:00Z")
	seedGoalFile(t, root, "race-111", "completed", "alice", "2026-05-12T12:00:00Z",
		BuildCompleteRecord("race-111", "alice", "already done", "2026-05-12T12:30:00Z"))

	err := MoveToCompleted(root, "race-111", "alice", "second try", "2026-05-12T12:35:00Z")
	var got *rufioerr.NoSuchGoalError
	if !errors.As(err, &got) {
		t.Fatalf("want *NoSuchGoalError, got %T (%v)", err, err)
	}
	if got.ID != "race-111" {
		t.Errorf("ID=%q, want race-111", got.ID)
	}
}

func TestMoveToCompleted_AlreadyAbandoned_RaceReturnsNoSuchGoal(t *testing.T) {
	root := t.TempDir()
	// Same shape as the prior race but the prior winner moved → abandoned.
	// A subsequent complete must surface NoSuchGoal: when LoadAnyState is
	// the gate, the orchestrator will see State=Abandoned and reject; if
	// the active file is somehow still present (mid-crash), the moveTo
	// goes through but at least the link should NOT silently overwrite
	// the abandoned file. We don't want the moveTo helper to surface
	// success in that case either — test that the helper returns
	// NoSuchGoal when active is missing (the normal post-abandon state).
	seedGoalFile(t, root, "race-222", "abandoned", "alice", "2026-05-12T12:00:00Z",
		BuildAbandonRecord("race-222", "alice", "gave up", "2026-05-12T12:30:00Z"))
	// No active file.
	err := MoveToCompleted(root, "race-222", "alice", "should fail", "2026-05-12T12:35:00Z")
	var got *rufioerr.NoSuchGoalError
	if !errors.As(err, &got) {
		t.Fatalf("want *NoSuchGoalError, got %T (%v)", err, err)
	}
	if got.ID != "race-222" {
		t.Errorf("ID=%q, want race-222", got.ID)
	}
}

// ---- MoveToAbandoned --------------------------------------------------------

func TestMoveToAbandoned_HappyPath(t *testing.T) {
	root := t.TempDir()
	seedGoalFile(t, root, "888-hhhhhh", "active", "alice", "2026-05-12T12:00:00Z")
	if err := MoveToAbandoned(root, "888-hhhhhh", "alice", "deprioritised", "2026-05-12T12:34:56Z"); err != nil {
		t.Fatalf("MoveToAbandoned: %v", err)
	}

	activePath := filepath.Join(root, "live", "goals", "active", "888-hhhhhh.gdl")
	if _, err := os.Stat(activePath); !os.IsNotExist(err) {
		t.Errorf("active file still exists")
	}

	abandonedPath := filepath.Join(root, "live", "goals", "abandoned", "888-hhhhhh.gdl")
	bs, err := os.ReadFile(abandonedPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[1].Type != "goal-abandon" {
		t.Errorf("records[1].Type=%q, want goal-abandon", records[1].Type)
	}
	if records[1].Get("reason") != "deprioritised" {
		t.Errorf("reason=%q, want deprioritised", records[1].Get("reason"))
	}
}

func TestMoveToAbandoned_AlreadyAbandoned(t *testing.T) {
	root := t.TempDir()
	seedGoalFile(t, root, "999-iiiiii", "active", "alice", "2026-05-12T12:00:00Z")
	if err := MoveToAbandoned(root, "999-iiiiii", "alice", "first reason", "2026-05-12T12:00:00Z"); err != nil {
		t.Fatalf("first MoveToAbandoned: %v", err)
	}
	err := MoveToAbandoned(root, "999-iiiiii", "alice", "second try", "2026-05-12T12:01:00Z")
	var got *rufioerr.NoSuchGoalError
	if !errors.As(err, &got) {
		t.Fatalf("want *NoSuchGoalError, got %T (%v)", err, err)
	}
}

// ---- ReadAll ----------------------------------------------------------------

// seedGoalWithTS seeds a goal with a specific ts and (optional) audit
// records, so we can exercise sort order across states.
func seedGoalWithTS(t *testing.T, root, id, dir, ts string, extra ...gdl.Record) {
	t.Helper()
	full := filepath.Join(root, "live", "goals", dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rec := BuildGoalRecord(id, "alice", "stmt-"+id, "", "", "agent", ts)
	lines := []string{gdl.RenderLine(rec)}
	for _, r := range extra {
		lines = append(lines, gdl.RenderLine(r))
	}
	if err := os.WriteFile(filepath.Join(full, id+".gdl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestReadAll_SortOrder(t *testing.T) {
	root := t.TempDir()
	// Two active with different ts; one completed; one abandoned.
	seedGoalWithTS(t, root, "a-1", "active", "2026-05-12T10:00:00Z")
	seedGoalWithTS(t, root, "a-2", "active", "2026-05-12T11:00:00Z")
	seedGoalWithTS(t, root, "c-1", "completed", "2026-05-12T09:00:00Z",
		BuildCompleteRecord("c-1", "alice", "done", "2026-05-12T09:30:00Z"))
	seedGoalWithTS(t, root, "x-1", "abandoned", "2026-05-12T08:00:00Z",
		BuildAbandonRecord("x-1", "alice", "deprioritised", "2026-05-12T08:30:00Z"))

	got, err := ReadAll(root)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d goals, want 4", len(got))
	}
	// Active (ts desc) → completed → abandoned.
	wantOrder := []string{"a-2", "a-1", "c-1", "x-1"}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("got[%d].ID=%q, want %q (state=%s ts=%s)", i, got[i].ID, want, got[i].State, got[i].TS)
		}
	}
	// Verify the completed/abandoned records still carry audit fields.
	if got[2].Outcome != "done" {
		t.Errorf("got[2].Outcome=%q, want done", got[2].Outcome)
	}
	if got[3].Reason != "deprioritised" {
		t.Errorf("got[3].Reason=%q, want deprioritised", got[3].Reason)
	}
}

func TestReadAll_Empty(t *testing.T) {
	root := t.TempDir()
	got, err := ReadAll(root)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d goals, want 0", len(got))
	}
}
