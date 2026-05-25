package attention

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/fslock"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
)

func TestValidateIntent_RejectsEmpty(t *testing.T) {
	for _, raw := range []string{"", "   ", "\t\n"} {
		err := ValidateIntent(raw)
		var want *rufioerr.InvalidContentError
		if !errors.As(err, &want) {
			t.Fatalf("ValidateIntent(%q): want *InvalidContentError, got %T (%v)", raw, err, err)
		}
		if want.Field != "intent" {
			t.Errorf("Field=%q, want %q", want.Field, "intent")
		}
	}
}

func TestValidateIntent_AcceptsNonEmpty(t *testing.T) {
	if err := ValidateIntent("debugging the auth flow"); err != nil {
		t.Fatalf("ValidateIntent: unexpected error %v", err)
	}
}

func TestValidateEntities_RejectsEmptySlice(t *testing.T) {
	err := ValidateEntities(nil)
	var want *rufioerr.InvalidEntitiesError
	if !errors.As(err, &want) {
		t.Fatalf("want *InvalidEntitiesError, got %T (%v)", err, err)
	}
	if want.Token != "" {
		t.Errorf("Token=%q, want empty (signals missing-flag shape)", want.Token)
	}
}

func TestValidateEntities_RejectsMalformedToken(t *testing.T) {
	cases := []string{
		"customer",         // no colon segment
		"5cust:42",         // leading digit
		"CUSTOMER:42",      // uppercase head
		"customer:",        // empty colon segment
		":42",              // empty head
		"customer:foo bar", // whitespace
		"",                 // empty token (e.g., trailing comma)
	}
	for _, tok := range cases {
		err := ValidateEntities([]string{"customer:5821", tok})
		var got *rufioerr.InvalidEntitiesError
		if !errors.As(err, &got) {
			t.Errorf("ValidateEntities([_, %q]): want *InvalidEntitiesError, got %T (%v)", tok, err, err)
			continue
		}
		if got.Token != tok {
			t.Errorf("ValidateEntities([_, %q]): Token=%q, want %q", tok, got.Token, tok)
		}
	}
}

func TestValidateEntities_AcceptsValidTokens(t *testing.T) {
	good := []string{
		"customer:5821",
		"agent:foo-bar",
		"order:abc_def",
		"customer:5821:order:9", // multi-segment
		"a:1",                   // minimal valid
	}
	for _, tok := range good {
		if err := ValidateEntities([]string{tok}); err != nil {
			t.Errorf("ValidateEntities([%q]): unexpected %v", tok, err)
		}
	}
}

func TestValidateTopics_NilAndEmptyAreOK(t *testing.T) {
	if err := ValidateTopics(nil); err != nil {
		t.Errorf("ValidateTopics(nil): %v", err)
	}
}

func TestValidateTopics_RejectsMalformed(t *testing.T) {
	cases := []string{
		"", // empty token
		"-leading-dash",
		"UPPER",
		"has space",
		"has,comma",
	}
	for _, tok := range cases {
		err := ValidateTopics([]string{"auth", tok})
		var got *rufioerr.InvalidTopicsError
		if !errors.As(err, &got) {
			t.Errorf("ValidateTopics([_, %q]): want *InvalidTopicsError, got %T (%v)", tok, err, err)
			continue
		}
		if got.Token != tok {
			t.Errorf("ValidateTopics([_, %q]): Token=%q, want %q", tok, got.Token, tok)
		}
	}
}

func TestValidateTopics_AcceptsValidTokens(t *testing.T) {
	good := []string{"auth", "auth-flow", "auth_flow", "a", "abc123", "x-1_y"}
	if err := ValidateTopics(good); err != nil {
		t.Errorf("ValidateTopics(%v): unexpected %v", good, err)
	}
}

func TestBuildRecord_RendersWithFieldOrder(t *testing.T) {
	rec := BuildRecord("agent-a", "debugging auth", "fleet", []string{"customer:5821", "order:7"}, []string{"auth", "p1"}, "2026-05-11T12:00:00.000000000Z")
	if rec.Type != "attention" {
		t.Fatalf("Type=%q, want attention", rec.Type)
	}
	gotKeys := make([]string, 0, len(rec.Fields))
	for _, f := range rec.Fields {
		gotKeys = append(gotKeys, f.Key)
	}
	// Field order updated #125: scope sits after intent, mirroring the
	// thought record's id/author/type/subject/content/scope ordering.
	want := []string{"agent", "intent", "scope", "entities", "topics", "ts"}
	if !equalStrings(gotKeys, want) {
		t.Errorf("Field order = %v, want %v", gotKeys, want)
	}
	if rec.Get("agent") != "agent-a" {
		t.Error("agent mismatch")
	}
	if rec.Get("intent") != "debugging auth" {
		t.Error("intent mismatch")
	}
	if rec.Get("scope") != "fleet" {
		t.Errorf("scope=%q, want fleet", rec.Get("scope"))
	}
	if rec.Get("entities") != "customer:5821,order:7" {
		t.Error("entities mismatch")
	}
	if rec.Get("topics") != "auth,p1" {
		t.Error("topics mismatch")
	}
	if rec.Get("ts") != "2026-05-11T12:00:00.000000000Z" {
		t.Error("ts mismatch")
	}
}

func TestBuildRecord_OmitsTopicsKeyWhenEmpty(t *testing.T) {
	rec := BuildRecord("agent-a", "x", "fleet", []string{"customer:5821"}, nil, "2026-05-11T12:00:00.000000000Z")
	for _, f := range rec.Fields {
		if f.Key == "topics" {
			t.Fatalf("expected no topics field when nil, got %+v", f)
		}
	}
}

func TestBuildRecord_OmitsTopicsKeyWhenZeroLengthSlice(t *testing.T) {
	rec := BuildRecord("agent-a", "x", "fleet", []string{"customer:5821"}, []string{}, "2026-05-11T12:00:00.000000000Z")
	for _, f := range rec.Fields {
		if f.Key == "topics" {
			t.Fatalf("expected no topics field when empty slice, got %+v", f)
		}
	}
}

func TestWrite_RoundTripsThroughParser(t *testing.T) {
	root := t.TempDir()
	rec := BuildRecord(
		"agent-a",
		"debugging auth",
		"fleet",
		[]string{"customer:5821", "order:7"},
		[]string{"auth", "p1"},
		"2026-05-11T12:00:00.123456789Z",
	)
	if err := Write(root, "agent-a", rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	bs, err := os.ReadFile(filepath.Join(root, "live", "attention", "agent-a.gdl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Parse back: the rendered line must round-trip through ParseDocument
	// so downstream consumers (the attention read-side in PR #21) recover
	// the original field values regardless of how gdl escapes.
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v\nfile: %q", err, string(bs))
	}
	if len(records) != 1 {
		t.Fatalf("expected exactly 1 record, got %d", len(records))
	}
	got := records[0]
	if got.Type != "attention" {
		t.Errorf("Type=%q, want attention", got.Type)
	}
	if got.Get("agent") != "agent-a" {
		t.Errorf("agent=%q", got.Get("agent"))
	}
	if got.Get("intent") != "debugging auth" {
		t.Errorf("intent=%q", got.Get("intent"))
	}
	if got.Get("entities") != "customer:5821,order:7" {
		t.Errorf("entities=%q", got.Get("entities"))
	}
	if got.Get("topics") != "auth,p1" {
		t.Errorf("topics=%q", got.Get("topics"))
	}
	if got.Get("ts") != "2026-05-11T12:00:00.123456789Z" {
		t.Errorf("ts=%q", got.Get("ts"))
	}
}

func TestWrite_OverwritesPriorRecord(t *testing.T) {
	root := t.TempDir()
	first := BuildRecord("agent-a", "old intent", "fleet", []string{"customer:1"}, nil, "2026-05-11T12:00:00.000000000Z")
	if err := Write(root, "agent-a", first); err != nil {
		t.Fatal(err)
	}

	second := BuildRecord("agent-a", "new intent", "fleet", []string{"customer:2"}, []string{"hot"}, "2026-05-11T12:00:01.000000000Z")
	if err := Write(root, "agent-a", second); err != nil {
		t.Fatal(err)
	}

	bs, _ := os.ReadFile(filepath.Join(root, "live", "attention", "agent-a.gdl"))
	got := string(bs)
	if !strings.Contains(got, "new intent") {
		t.Errorf("file missing new intent: %q", got)
	}
	if strings.Contains(got, "old intent") {
		t.Errorf("file still contains old intent (not overwritten): %q", got)
	}
	// Exactly one @attention line — overwrite, not append.
	if c := strings.Count(got, "@attention"); c != 1 {
		t.Errorf("expected exactly 1 @attention line, got %d in %q", c, got)
	}
}

func TestWrite_NoTempFileLeftBehind(t *testing.T) {
	root := t.TempDir()
	rec := BuildRecord("agent-a", "x", "fleet", []string{"customer:1"}, nil, "2026-05-11T12:00:00.000000000Z")
	if err := Write(root, "agent-a", rec); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "live", "attention", "*.tmp"))
	if len(matches) != 0 {
		t.Errorf("found leftover .tmp files: %v", matches)
	}
}

func TestWrite_LockContentionReturnsLockBusyError(t *testing.T) {
	root := t.TempDir()
	// Pre-create the lock dir to simulate another writer holding it.
	lockDir := filepath.Join(root, ".rufio", "locks", "attention-agent-a.lock")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rec := BuildRecord("agent-a", "x", "fleet", []string{"customer:1"}, nil, "2026-05-11T12:00:00.000000000Z")
	// Short timeout so this test doesn't take 5s.
	err := WriteWithTimeout(root, "agent-a", rec, 50*time.Millisecond)
	var got *fslock.LockBusyError
	if !errors.As(err, &got) {
		t.Fatalf("want *LockBusyError, got %T (%v)", err, err)
	}
}

// helper
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
