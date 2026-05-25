package summon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

// ---- ValidateTopic ----------------------------------------------------------

func TestValidateTopic_Empty(t *testing.T) {
	err := ValidateTopic("")
	var got *rufioerr.InvalidTopicError
	if !errors.As(err, &got) {
		t.Fatalf("want *InvalidTopicError, got %T (%v)", err, err)
	}
	if got.Topic != "" {
		t.Errorf("Topic=%q, want empty", got.Topic)
	}
}

func TestValidateTopic_SingleSegment(t *testing.T) {
	for _, s := range []string{"churn-strategy", "topic", "a", "a-b-c"} {
		if err := ValidateTopic(s); err != nil {
			t.Errorf("ValidateTopic(%q): unexpected %v", s, err)
		}
	}
}

func TestValidateTopic_EntityForm(t *testing.T) {
	for _, s := range []string{"customer:5821", "agent:foo-bar", "order:abc_def", "a:1", "x:1:y:2"} {
		if err := ValidateTopic(s); err != nil {
			t.Errorf("ValidateTopic(%q): unexpected %v", s, err)
		}
	}
}

func TestValidateTopic_Malformed_LeadingDigit(t *testing.T) {
	for _, s := range []string{"1foo", "9-bar", "1:foo"} {
		err := ValidateTopic(s)
		var got *rufioerr.InvalidTopicError
		if !errors.As(err, &got) {
			t.Errorf("ValidateTopic(%q): want *InvalidTopicError, got %T (%v)", s, err, err)
			continue
		}
		if got.Topic != s {
			t.Errorf("Topic=%q, want %q", got.Topic, s)
		}
	}
}

func TestValidateTopic_Malformed_Uppercase(t *testing.T) {
	for _, s := range []string{"Foo:bar", "FOO", "foo bar", "foo:", ":bar"} {
		err := ValidateTopic(s)
		var got *rufioerr.InvalidTopicError
		if !errors.As(err, &got) {
			t.Errorf("ValidateTopic(%q): want *InvalidTopicError, got %T (%v)", s, err, err)
		}
	}
}

// ---- ValidateIntent ---------------------------------------------------------

func TestValidateIntent_Empty(t *testing.T) {
	err := ValidateIntent("")
	var got *rufioerr.InvalidContentError
	if !errors.As(err, &got) {
		t.Fatalf("want *InvalidContentError, got %T (%v)", err, err)
	}
	if got.Field != "intent" {
		t.Errorf("Field=%q, want intent", got.Field)
	}
}

func TestValidateIntent_WhitespaceOnly(t *testing.T) {
	for _, raw := range []string{" ", "\t", "\n", "  \t \n "} {
		err := ValidateIntent(raw)
		var got *rufioerr.InvalidContentError
		if !errors.As(err, &got) {
			t.Errorf("ValidateIntent(%q): want *InvalidContentError, got %T (%v)", raw, err, err)
			continue
		}
		if got.Field != "intent" {
			t.Errorf("Field=%q, want intent", got.Field)
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

// ---- BuildSummonRecord ------------------------------------------------------

func TestBuildSummonRecord_FieldOrder(t *testing.T) {
	rec := BuildSummonRecord("123-abcdef", "alice", "bob", "customer:5821", "discuss churn", "2026-05-12T12:00:00Z", 86400)
	if rec.Type != "summon" {
		t.Errorf("Type=%q, want summon", rec.Type)
	}
	wantKeys := []string{"id", "from", "to", "topic", "intent", "ts", "ttl"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d", len(rec.Fields), len(wantKeys))
	}
	for i, k := range wantKeys {
		if rec.Fields[i].Key != k {
			t.Errorf("Fields[%d].Key=%q, want %q", i, rec.Fields[i].Key, k)
		}
	}
	// Spot check rendered line begins with the expected prefix.
	line := gdl.RenderLine(rec)
	wantPrefix := "@summon|id:123-abcdef|from:alice|to:bob|topic:customer\\:5821|intent:discuss churn|ts:2026-05-12T12\\:00\\:00Z|ttl:86400"
	if line != wantPrefix {
		t.Errorf("RenderLine:\n got=%q\nwant=%q", line, wantPrefix)
	}
}

// ---- BuildAcceptRecord ------------------------------------------------------

func TestBuildAcceptRecord_FieldOrder(t *testing.T) {
	rec := BuildAcceptRecord("123-abcdef", "bob", "ch-456-ghijkl", "2026-05-12T12:34:00Z")
	if rec.Type != "accept" {
		t.Errorf("Type=%q, want accept", rec.Type)
	}
	wantKeys := []string{"id", "by", "channel", "ts"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d", len(rec.Fields), len(wantKeys))
	}
	for i, k := range wantKeys {
		if rec.Fields[i].Key != k {
			t.Errorf("Fields[%d].Key=%q, want %q", i, rec.Fields[i].Key, k)
		}
	}
	if rec.Get("id") != "123-abcdef" || rec.Get("by") != "bob" ||
		rec.Get("channel") != "ch-456-ghijkl" || rec.Get("ts") != "2026-05-12T12:34:00Z" {
		t.Errorf("field values wrong: %+v", rec.Fields)
	}
}

// ---- BuildDeclineRecord -----------------------------------------------------

func TestBuildDeclineRecord_FieldOrder(t *testing.T) {
	rec := BuildDeclineRecord("123-abcdef", "bob", "not interested", "2026-05-12T12:34:00Z")
	if rec.Type != "decline" {
		t.Errorf("Type=%q, want decline", rec.Type)
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
	if rec.Get("reason") != "not interested" {
		t.Errorf("reason=%q, want %q", rec.Get("reason"), "not interested")
	}
}

// ---- WritePending -----------------------------------------------------------

func TestWritePending_FileExistsAndParses(t *testing.T) {
	root := t.TempDir()
	rec := BuildSummonRecord("123-abcdef", "alice", "bob", "churn-strategy", "let's talk", "2026-05-12T12:00:00Z", 86400)
	if err := WritePending(root, "123-abcdef", rec); err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	path := filepath.Join(root, "live", "summons", "pending", "123-abcdef.gdl")
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
	if records[0].Type != "summon" {
		t.Errorf("Type=%q, want summon", records[0].Type)
	}
	if records[0].Get("id") != "123-abcdef" {
		t.Errorf("id=%q, want 123-abcdef", records[0].Get("id"))
	}
}

// ---- LoadAnyState -----------------------------------------------------------

func seedPendingFile(t *testing.T, root, id, dir string, extra ...gdl.Record) {
	t.Helper()
	full := filepath.Join(root, "live", "summons", dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rec := BuildSummonRecord(id, "alice", "bob", "customer:5821", "intent text", "2026-05-12T12:00:00Z", 86400)
	var lines []string
	lines = append(lines, gdl.RenderLine(rec))
	for _, r := range extra {
		lines = append(lines, gdl.RenderLine(r))
	}
	if err := os.WriteFile(filepath.Join(full, id+".gdl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestLoadAnyState_Pending(t *testing.T) {
	root := t.TempDir()
	seedPendingFile(t, root, "111-aaaaaa", "pending")
	got, err := LoadAnyState(root, "111-aaaaaa")
	if err != nil {
		t.Fatalf("LoadAnyState: %v", err)
	}
	if got.State != StatePending {
		t.Errorf("State=%q, want %q", got.State, StatePending)
	}
	if got.ID != "111-aaaaaa" || got.From != "alice" || got.To != "bob" {
		t.Errorf("loaded summon wrong: %+v", got)
	}
	if got.Topic != "customer:5821" {
		t.Errorf("Topic=%q, want customer:5821", got.Topic)
	}
	if got.TTL != 86400 {
		t.Errorf("TTL=%d, want 86400", got.TTL)
	}
}

func TestLoadAnyState_Accepted(t *testing.T) {
	root := t.TempDir()
	seedPendingFile(t, root, "222-bbbbbb", "accepted",
		BuildAcceptRecord("222-bbbbbb", "bob", "ch-1-aaaaaa", "2026-05-12T12:01:00Z"))
	got, err := LoadAnyState(root, "222-bbbbbb")
	if err != nil {
		t.Fatalf("LoadAnyState: %v", err)
	}
	if got.State != StateAccepted {
		t.Errorf("State=%q, want %q", got.State, StateAccepted)
	}
}

func TestLoadAnyState_Declined(t *testing.T) {
	root := t.TempDir()
	seedPendingFile(t, root, "333-cccccc", "declined",
		BuildDeclineRecord("333-cccccc", "bob", "busy", "2026-05-12T12:01:00Z"))
	got, err := LoadAnyState(root, "333-cccccc")
	if err != nil {
		t.Fatalf("LoadAnyState: %v", err)
	}
	if got.State != StateDeclined {
		t.Errorf("State=%q, want %q", got.State, StateDeclined)
	}
}

func TestLoadAnyState_Expired(t *testing.T) {
	root := t.TempDir()
	seedPendingFile(t, root, "444-dddddd", "expired")
	got, err := LoadAnyState(root, "444-dddddd")
	if err != nil {
		t.Fatalf("LoadAnyState: %v", err)
	}
	if got.State != StateExpired {
		t.Errorf("State=%q, want %q", got.State, StateExpired)
	}
}

func TestLoadAnyState_NotFound(t *testing.T) {
	root := t.TempDir()
	_, err := LoadAnyState(root, "nope-000000")
	var got *rufioerr.NoSuchSummonError
	if !errors.As(err, &got) {
		t.Fatalf("want *NoSuchSummonError, got %T (%v)", err, err)
	}
	if got.ID != "nope-000000" {
		t.Errorf("ID=%q, want nope-000000", got.ID)
	}
}

// ---- MoveToAccepted ---------------------------------------------------------

func TestMoveToAccepted_HappyPath(t *testing.T) {
	root := t.TempDir()
	seedPendingFile(t, root, "555-eeeeee", "pending")
	err := MoveToAccepted(root, "555-eeeeee", "bob", "ch-666-ffffff", "2026-05-12T12:34:56Z")
	if err != nil {
		t.Fatalf("MoveToAccepted: %v", err)
	}

	// Pending file gone.
	pendingPath := filepath.Join(root, "live", "summons", "pending", "555-eeeeee.gdl")
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Errorf("pending file still exists (err=%v)", err)
	}

	// Accepted file has @summon + @accept.
	acceptedPath := filepath.Join(root, "live", "summons", "accepted", "555-eeeeee.gdl")
	bs, err := os.ReadFile(acceptedPath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", acceptedPath, err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].Type != "summon" {
		t.Errorf("records[0].Type=%q, want summon", records[0].Type)
	}
	if records[1].Type != "accept" {
		t.Errorf("records[1].Type=%q, want accept", records[1].Type)
	}
	if records[1].Get("channel") != "ch-666-ffffff" {
		t.Errorf("channel=%q, want ch-666-ffffff", records[1].Get("channel"))
	}
	if records[1].Get("by") != "bob" {
		t.Errorf("by=%q, want bob", records[1].Get("by"))
	}
}

func TestMoveToAccepted_IdempotentRace(t *testing.T) {
	root := t.TempDir()
	seedPendingFile(t, root, "777-gggggg", "pending")
	if err := MoveToAccepted(root, "777-gggggg", "bob", "ch-1-aaaaaa", "2026-05-12T12:00:00Z"); err != nil {
		t.Fatalf("first MoveToAccepted: %v", err)
	}
	err := MoveToAccepted(root, "777-gggggg", "bob", "ch-2-bbbbbb", "2026-05-12T12:01:00Z")
	var got *rufioerr.NoSuchSummonError
	if !errors.As(err, &got) {
		t.Fatalf("second MoveToAccepted: want *NoSuchSummonError, got %T (%v)", err, err)
	}
	if got.ID != "777-gggggg" {
		t.Errorf("ID=%q, want 777-gggggg", got.ID)
	}
}

// ---- MoveToDeclined ---------------------------------------------------------

func TestMoveToDeclined_HappyPath(t *testing.T) {
	root := t.TempDir()
	seedPendingFile(t, root, "888-hhhhhh", "pending")
	if err := MoveToDeclined(root, "888-hhhhhh", "bob", "not now", "2026-05-12T12:34:56Z"); err != nil {
		t.Fatalf("MoveToDeclined: %v", err)
	}

	pendingPath := filepath.Join(root, "live", "summons", "pending", "888-hhhhhh.gdl")
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Errorf("pending file still exists")
	}

	declinedPath := filepath.Join(root, "live", "summons", "declined", "888-hhhhhh.gdl")
	bs, err := os.ReadFile(declinedPath)
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
	if records[1].Type != "decline" {
		t.Errorf("records[1].Type=%q, want decline", records[1].Type)
	}
	if records[1].Get("reason") != "not now" {
		t.Errorf("reason=%q, want \"not now\"", records[1].Get("reason"))
	}
}

func TestMoveToDeclined_IdempotentRace(t *testing.T) {
	root := t.TempDir()
	seedPendingFile(t, root, "999-iiiiii", "pending")
	if err := MoveToDeclined(root, "999-iiiiii", "bob", "busy", "2026-05-12T12:00:00Z"); err != nil {
		t.Fatalf("first MoveToDeclined: %v", err)
	}
	err := MoveToDeclined(root, "999-iiiiii", "bob", "busy", "2026-05-12T12:01:00Z")
	var got *rufioerr.NoSuchSummonError
	if !errors.As(err, &got) {
		t.Fatalf("want *NoSuchSummonError, got %T (%v)", err, err)
	}
}

// ---- ReadAll ----------------------------------------------------------------

func seedSummonWithTS(t *testing.T, root, id, dir, ts string, extra ...gdl.Record) {
	t.Helper()
	full := filepath.Join(root, "live", "summons", dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rec := BuildSummonRecord(id, "alice", "bob", "topic-x", "intent", ts, 86400)
	lines := []string{gdl.RenderLine(rec)}
	for _, r := range extra {
		lines = append(lines, gdl.RenderLine(r))
	}
	if err := os.WriteFile(filepath.Join(full, id+".gdl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// ---- SweepExpired -----------------------------------------------------------

// writeRawPending writes a raw @summon line (no validation) into
// live/summons/pending/<id>.gdl. Used by sweep tests so we can inject
// malformed ts/ttl values without going through BuildSummonRecord.
func writeRawPending(t *testing.T, root, id, line string) {
	t.Helper()
	dir := filepath.Join(root, "live", "summons", "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".gdl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// seedPendingWithTSTTL seeds a valid @summon at the given ts/ttl.
func seedPendingWithTSTTL(t *testing.T, root, id, ts string, ttl int) {
	t.Helper()
	dir := filepath.Join(root, "live", "summons", "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rec := BuildSummonRecord(id, "alice", "bob", "customer:5821", "intent text", ts, ttl)
	if err := os.WriteFile(filepath.Join(dir, id+".gdl"), []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestSweepExpired_PastTTLMovesToExpired(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	// ts = now - 90000s (25 hours ago), ttl = 86400 (24h) → expired.
	ts := now.Add(-90000 * time.Second).Format(time.RFC3339Nano)
	seedPendingWithTSTTL(t, root, "sweep-expired-1", ts, 86400)

	var buf bytes.Buffer
	moved, err := SweepExpired(root, func() time.Time { return now }, &buf)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if moved != 1 {
		t.Fatalf("moved=%d, want 1 (log: %s)", moved, buf.String())
	}

	pendingPath := filepath.Join(root, "live", "summons", "pending", "sweep-expired-1.gdl")
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Errorf("pending file still exists (err=%v)", err)
	}
	expiredPath := filepath.Join(root, "live", "summons", "expired", "sweep-expired-1.gdl")
	bs, err := os.ReadFile(expiredPath)
	if err != nil {
		t.Fatalf("ReadFile expired: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(records) != 1 || records[0].Type != "summon" {
		t.Errorf("got %d records (first type %q), want 1 @summon", len(records), records[0].Type)
	}
	if records[0].Get("id") != "sweep-expired-1" {
		t.Errorf("id=%q, want sweep-expired-1", records[0].Get("id"))
	}
}

func TestSweepExpired_NotYetExpired_Skips(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	// ts = now - 1000s, ttl = 86400 → still fresh.
	ts := now.Add(-1000 * time.Second).Format(time.RFC3339Nano)
	seedPendingWithTSTTL(t, root, "sweep-fresh-1", ts, 86400)

	moved, err := SweepExpired(root, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if moved != 0 {
		t.Errorf("moved=%d, want 0", moved)
	}
	pendingPath := filepath.Join(root, "live", "summons", "pending", "sweep-fresh-1.gdl")
	if _, err := os.Stat(pendingPath); err != nil {
		t.Errorf("pending file missing: %v", err)
	}
	expiredPath := filepath.Join(root, "live", "summons", "expired", "sweep-fresh-1.gdl")
	if _, err := os.Stat(expiredPath); !os.IsNotExist(err) {
		t.Errorf("expired file unexpectedly exists (err=%v)", err)
	}
}

func TestSweepExpired_TTLZero_Skips(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	// ts is ancient but ttl=0 → never expires (defensive).
	ts := now.Add(-365 * 24 * time.Hour).Format(time.RFC3339Nano)
	seedPendingWithTSTTL(t, root, "sweep-ttl0-1", ts, 0)

	moved, err := SweepExpired(root, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if moved != 0 {
		t.Errorf("moved=%d, want 0", moved)
	}
	pendingPath := filepath.Join(root, "live", "summons", "pending", "sweep-ttl0-1.gdl")
	if _, err := os.Stat(pendingPath); err != nil {
		t.Errorf("pending file missing: %v", err)
	}
}

func TestSweepExpired_Idempotent(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	ts := now.Add(-90000 * time.Second).Format(time.RFC3339Nano)
	seedPendingWithTSTTL(t, root, "sweep-idem-1", ts, 86400)

	moved, err := SweepExpired(root, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatalf("first SweepExpired: %v", err)
	}
	if moved != 1 {
		t.Fatalf("first moved=%d, want 1", moved)
	}
	expiredPath := filepath.Join(root, "live", "summons", "expired", "sweep-idem-1.gdl")
	firstBytes, err := os.ReadFile(expiredPath)
	if err != nil {
		t.Fatalf("ReadFile first: %v", err)
	}

	// Second call: nothing left to move, expired file untouched.
	moved2, err := SweepExpired(root, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatalf("second SweepExpired: %v", err)
	}
	if moved2 != 0 {
		t.Errorf("second moved=%d, want 0", moved2)
	}
	secondBytes, err := os.ReadFile(expiredPath)
	if err != nil {
		t.Fatalf("ReadFile second: %v", err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Errorf("expired file mutated across idempotent sweeps")
	}
}

func TestSweepExpired_MalformedTS_LogsAndContinues(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	// One malformed (bad ts) — should log + skip; one valid + expired.
	writeRawPending(t, root, "bad-ts-1",
		`@summon|id:bad-ts-1|from:alice|to:bob|topic:t|intent:i|ts:foo|ttl:86400`)
	tsValid := now.Add(-90000 * time.Second).Format(time.RFC3339Nano)
	seedPendingWithTSTTL(t, root, "good-1", tsValid, 86400)

	var buf bytes.Buffer
	moved, err := SweepExpired(root, func() time.Time { return now }, &buf)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if moved != 1 {
		t.Errorf("moved=%d, want 1", moved)
	}
	logText := buf.String()
	if !strings.Contains(logText, "bad-ts-1") || !strings.Contains(logText, "malformed ts") {
		t.Errorf("log missing malformed-ts message: %q", logText)
	}

	// Malformed file stays in pending — defensive: don't delete data we
	// can't classify.
	if _, err := os.Stat(filepath.Join(root, "live", "summons", "pending", "bad-ts-1.gdl")); err != nil {
		t.Errorf("malformed pending file missing: %v", err)
	}
	// Valid expired file moved.
	if _, err := os.Stat(filepath.Join(root, "live", "summons", "expired", "good-1.gdl")); err != nil {
		t.Errorf("valid expired file missing: %v", err)
	}
}

func TestSweepExpired_MalformedTTL_LogsAndContinues(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	ts := now.Add(-90000 * time.Second).Format(time.RFC3339Nano)
	writeRawPending(t, root, "bad-ttl-1",
		`@summon|id:bad-ttl-1|from:alice|to:bob|topic:t|intent:i|ts:`+ts+`|ttl:notanint`)
	seedPendingWithTSTTL(t, root, "good-2", ts, 86400)

	var buf bytes.Buffer
	moved, err := SweepExpired(root, func() time.Time { return now }, &buf)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if moved != 1 {
		t.Errorf("moved=%d, want 1", moved)
	}
	logText := buf.String()
	if !strings.Contains(logText, "bad-ttl-1") || !strings.Contains(logText, "malformed ttl") {
		t.Errorf("log missing malformed-ttl message: %q", logText)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "summons", "pending", "bad-ttl-1.gdl")); err != nil {
		t.Errorf("malformed pending file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "summons", "expired", "good-2.gdl")); err != nil {
		t.Errorf("valid expired file missing: %v", err)
	}
}

func TestSweepExpired_MissingPendingDir_ReturnsZeroNil(t *testing.T) {
	root := t.TempDir() // no live/summons/pending dir created
	now := time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)
	moved, err := SweepExpired(root, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if moved != 0 {
		t.Errorf("moved=%d, want 0", moved)
	}
}

func TestReadAll_SortOrder(t *testing.T) {
	root := t.TempDir()
	// Two pending with different ts; one accepted; one declined; one expired.
	seedSummonWithTS(t, root, "p-1", "pending", "2026-05-12T10:00:00Z")
	seedSummonWithTS(t, root, "p-2", "pending", "2026-05-12T11:00:00Z")
	seedSummonWithTS(t, root, "a-1", "accepted", "2026-05-12T09:00:00Z")
	seedSummonWithTS(t, root, "d-1", "declined", "2026-05-12T08:00:00Z")
	seedSummonWithTS(t, root, "e-1", "expired", "2026-05-12T07:00:00Z")

	got, err := ReadAll(root)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d summons, want 5", len(got))
	}
	wantOrder := []string{"p-2", "p-1", "a-1", "d-1", "e-1"}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("got[%d].ID=%q, want %q (state=%s ts=%s)", i, got[i].ID, want, got[i].State, got[i].TS)
		}
	}
}
