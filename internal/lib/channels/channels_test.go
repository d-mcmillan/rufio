package channels

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

func TestGenerateID_HasChPrefix(t *testing.T) {
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	if !strings.HasPrefix(id, "ch-") {
		t.Errorf("GenerateID returned %q, missing ch- prefix", id)
	}
	re := regexp.MustCompile(`^ch-[0-9]+-[a-z0-9]{6}$`)
	if !re.MatchString(id) {
		t.Errorf("GenerateID returned %q, expected match for ^ch-[0-9]+-[a-z0-9]{6}$", id)
	}
}

func TestBuildMetaRecord_FieldOrder(t *testing.T) {
	rec := BuildMetaRecord("ch-123-abcdef", "alice", "bob", "customer:5821", "discuss churn", "2026-05-12T12:00:00Z")
	if rec.Type != "channel" {
		t.Errorf("Type=%q, want channel", rec.Type)
	}
	wantKeys := []string{"id", "opener", "target", "topic", "intent", "created-at"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d", len(rec.Fields), len(wantKeys))
	}
	for i, k := range wantKeys {
		if rec.Fields[i].Key != k {
			t.Errorf("Fields[%d].Key=%q, want %q", i, rec.Fields[i].Key, k)
		}
	}
	if rec.Get("id") != "ch-123-abcdef" {
		t.Errorf("id=%q, want ch-123-abcdef", rec.Get("id"))
	}
	if rec.Get("created-at") != "2026-05-12T12:00:00Z" {
		t.Errorf("created-at=%q, want 2026-05-12T12:00:00Z", rec.Get("created-at"))
	}
}

func TestWriteMeta_FileExistsAndParses(t *testing.T) {
	root := t.TempDir()
	chID := "ch-123-abcdef"
	rec := BuildMetaRecord(chID, "alice", "bob", "customer:5821", "discuss", "2026-05-12T12:00:00Z")
	if err := WriteMeta(root, chID, rec); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	path := filepath.Join(root, "live", "channels", "active", chID, "meta.gdl")
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
	if records[0].Type != "channel" {
		t.Errorf("Type=%q, want channel", records[0].Type)
	}
	if records[0].Get("opener") != "alice" || records[0].Get("target") != "bob" {
		t.Errorf("opener/target wrong: %+v", records[0].Fields)
	}
}

func TestWriteMeta_CreatesParentDir(t *testing.T) {
	root := t.TempDir()
	chID := "ch-456-zzzzzz"
	// Parent dir doesn't exist yet.
	parent := filepath.Join(root, "live", "channels", "active", chID)
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Fatalf("parent dir already exists or unexpected err: %v", err)
	}
	rec := BuildMetaRecord(chID, "alice", "bob", "topic", "intent", "2026-05-12T12:00:00Z")
	if err := WriteMeta(root, chID, rec); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "meta.gdl")); err != nil {
		t.Errorf("meta.gdl not created: %v", err)
	}
}

// ---------- PR #16 helpers + tests ----------

// seedActiveMeta writes a minimal @channel meta.gdl under
// live/channels/active/<chID>/. Test scaffolding only.
func seedActiveMeta(t *testing.T, root, chID, opener, target string, extra ...gdl.Record) {
	t.Helper()
	rec := BuildMetaRecord(chID, opener, target, "topic", "intent", "2026-05-12T12:00:00Z")
	if err := WriteMeta(root, chID, rec); err != nil {
		t.Fatalf("seed WriteMeta: %v", err)
	}
	if len(extra) > 0 {
		path := filepath.Join(root, "live", "channels", "active", chID, "meta.gdl")
		bs, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("seed read: %v", err)
		}
		buf := string(bs)
		for _, r := range extra {
			buf += gdl.RenderLine(r) + "\n"
		}
		if err := os.WriteFile(path, []byte(buf), 0o644); err != nil {
			t.Fatalf("seed write: %v", err)
		}
	}
}

// seedClosedMeta writes meta.gdl directly under closed/<chID>/.
func seedClosedMeta(t *testing.T, root, chID, opener, target string, extra ...gdl.Record) {
	t.Helper()
	dir := filepath.Join(root, "live", "channels", "closed", chID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seedClosed mkdir: %v", err)
	}
	rec := BuildMetaRecord(chID, opener, target, "topic", "intent", "2026-05-12T12:00:00Z")
	buf := gdl.RenderLine(rec) + "\n"
	for _, r := range extra {
		buf += gdl.RenderLine(r) + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.gdl"), []byte(buf), 0o644); err != nil {
		t.Fatalf("seedClosed write: %v", err)
	}
}

func TestLoadMeta_Active_HappyPath(t *testing.T) {
	root := t.TempDir()
	chID := "ch-1-aaaaaa"
	seedActiveMeta(t, root, chID, "alice", "bob")
	ch, err := LoadMeta(root, chID)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if ch.ID != chID || ch.Opener != "alice" || ch.Target != "bob" {
		t.Errorf("Channel header wrong: %+v", ch)
	}
	if ch.Closed {
		t.Errorf("Closed=true for active channel")
	}
	if len(ch.Left) != 0 {
		t.Errorf("Left=%v, want empty", ch.Left)
	}
}

func TestLoadMeta_Closed_HappyPath(t *testing.T) {
	root := t.TempDir()
	chID := "ch-2-bbbbbb"
	closeRec := BuildCloseRecord("alice", "2026-05-12T13:00:00Z")
	seedClosedMeta(t, root, chID, "alice", "bob", closeRec)
	ch, err := LoadMeta(root, chID)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if !ch.Closed {
		t.Errorf("Closed=false, want true")
	}
	if ch.ClosedBy != "alice" {
		t.Errorf("ClosedBy=%q, want alice", ch.ClosedBy)
	}
	if ch.ClosedAt != "2026-05-12T13:00:00Z" {
		t.Errorf("ClosedAt=%q, want 2026-05-12T13:00:00Z", ch.ClosedAt)
	}
}

func TestLoadMeta_NotFound(t *testing.T) {
	root := t.TempDir()
	_, err := LoadMeta(root, "ch-missing-aaaaaa")
	var got *rufioerr.NoSuchChannelError
	if !errors.As(err, &got) {
		t.Fatalf("want *NoSuchChannelError, got %T (%v)", err, err)
	}
	if got.ID != "ch-missing-aaaaaa" {
		t.Errorf("err.ID=%q, want ch-missing-aaaaaa", got.ID)
	}
}

func TestLoadMeta_WithLeaves_PopulatesLeftMap(t *testing.T) {
	root := t.TempDir()
	chID := "ch-3-cccccc"
	leaveA := BuildLeaveRecord("alice", "2026-05-12T13:00:00Z")
	leaveB := BuildLeaveRecord("bob", "2026-05-12T13:05:00Z")
	seedActiveMeta(t, root, chID, "alice", "bob", leaveA, leaveB)
	ch, err := LoadMeta(root, chID)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if len(ch.Left) != 2 {
		t.Fatalf("Left=%v, want 2 entries", ch.Left)
	}
	if ch.Left["alice"] != "2026-05-12T13:00:00Z" || ch.Left["bob"] != "2026-05-12T13:05:00Z" {
		t.Errorf("Left timestamps wrong: %+v", ch.Left)
	}
}

func TestLoadMeta_WithClose_SetsClosedFields(t *testing.T) {
	root := t.TempDir()
	chID := "ch-4-dddddd"
	closeRec := BuildCloseRecord("alice", "2026-05-12T14:00:00Z")
	seedActiveMeta(t, root, chID, "alice", "bob", closeRec)
	ch, err := LoadMeta(root, chID)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if !ch.Closed {
		t.Errorf("Closed=false, want true")
	}
	if ch.ClosedBy != "alice" || ch.ClosedAt != "2026-05-12T14:00:00Z" {
		t.Errorf("ClosedBy=%q ClosedAt=%q", ch.ClosedBy, ch.ClosedAt)
	}
}

func TestIsCurrentMember_OpenerYes(t *testing.T) {
	ch := Channel{Opener: "alice", Target: "bob"}
	if !ch.IsCurrentMember("alice") {
		t.Errorf("opener should be member")
	}
}

func TestIsCurrentMember_TargetYes(t *testing.T) {
	ch := Channel{Opener: "alice", Target: "bob"}
	if !ch.IsCurrentMember("bob") {
		t.Errorf("target should be member")
	}
}

func TestIsCurrentMember_ThirdPartyNo(t *testing.T) {
	ch := Channel{Opener: "alice", Target: "bob"}
	if ch.IsCurrentMember("eve") {
		t.Errorf("third-party should not be member")
	}
}

func TestIsCurrentMember_LeftMemberNo(t *testing.T) {
	ch := Channel{Opener: "alice", Target: "bob", Left: map[string]string{"alice": "2026-05-12T13:00:00Z"}}
	if ch.IsCurrentMember("alice") {
		t.Errorf("left member should not be member")
	}
	if !ch.IsCurrentMember("bob") {
		t.Errorf("remaining member should still be member")
	}
}

func TestIsCurrentMember_ClosedChannelNo(t *testing.T) {
	ch := Channel{Opener: "alice", Target: "bob", Closed: true}
	if ch.IsCurrentMember("alice") {
		t.Errorf("closed-channel opener should not be member")
	}
	if ch.IsCurrentMember("bob") {
		t.Errorf("closed-channel target should not be member")
	}
}

func TestCurrentMembers_BothActive_ReturnsBoth(t *testing.T) {
	ch := Channel{Opener: "alice", Target: "bob"}
	got := ch.CurrentMembers()
	if len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Errorf("CurrentMembers=%v, want [alice bob]", got)
	}
}

func TestCurrentMembers_AfterOneLeaves_ReturnsOne(t *testing.T) {
	ch := Channel{Opener: "alice", Target: "bob", Left: map[string]string{"alice": "2026-05-12T13:00:00Z"}}
	got := ch.CurrentMembers()
	if len(got) != 1 || got[0] != "bob" {
		t.Errorf("CurrentMembers=%v, want [bob]", got)
	}
}

func TestCurrentMembers_Closed_ReturnsEmpty(t *testing.T) {
	ch := Channel{Opener: "alice", Target: "bob", Closed: true}
	got := ch.CurrentMembers()
	if len(got) != 0 {
		t.Errorf("CurrentMembers=%v, want empty", got)
	}
}

func TestOther_ReturnsCounterpart(t *testing.T) {
	ch := Channel{Opener: "alice", Target: "bob"}
	if got := ch.Other("alice"); got != "bob" {
		t.Errorf("Other(alice)=%q, want bob", got)
	}
	if got := ch.Other("bob"); got != "alice" {
		t.Errorf("Other(bob)=%q, want alice", got)
	}
}

func TestOther_NonMember_ReturnsEmpty(t *testing.T) {
	ch := Channel{Opener: "alice", Target: "bob"}
	if got := ch.Other("eve"); got != "" {
		t.Errorf("Other(eve)=%q, want empty", got)
	}
	// Left member → not current → no counterpart either.
	ch2 := Channel{Opener: "alice", Target: "bob", Left: map[string]string{"alice": "ts"}}
	if got := ch2.Other("alice"); got != "" {
		t.Errorf("Other(alice) after leave=%q, want empty", got)
	}
}

func TestBuildSayRecord_FieldOrder(t *testing.T) {
	rec := BuildSayRecord("123-aaaaaa", "ch-1-bbbbbb", "alice", "hello", "2026-05-12T15:00:00Z")
	// Issue #107: Type is "channel-message" to align with recall.AllTypes
	// — the CLI verb name (`rufio say`) is unchanged but the on-disk
	// record's Type token shifted so listen's default filter accepts
	// channel-message events. See BuildSayRecord doc.
	if rec.Type != "channel-message" {
		t.Errorf("Type=%q, want channel-message", rec.Type)
	}
	wantKeys := []string{"id", "channel", "by", "content", "ts"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d", len(rec.Fields), len(wantKeys))
	}
	for i, k := range wantKeys {
		if rec.Fields[i].Key != k {
			t.Errorf("Fields[%d].Key=%q, want %q", i, rec.Fields[i].Key, k)
		}
	}
}

func TestBuildLeaveRecord_FieldOrder(t *testing.T) {
	rec := BuildLeaveRecord("alice", "2026-05-12T15:00:00Z")
	if rec.Type != "channel-leave" {
		t.Errorf("Type=%q, want channel-leave", rec.Type)
	}
	wantKeys := []string{"by", "ts"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d", len(rec.Fields), len(wantKeys))
	}
	for i, k := range wantKeys {
		if rec.Fields[i].Key != k {
			t.Errorf("Fields[%d].Key=%q, want %q", i, rec.Fields[i].Key, k)
		}
	}
}

func TestBuildCloseRecord_FieldOrder(t *testing.T) {
	rec := BuildCloseRecord("alice", "2026-05-12T15:00:00Z")
	if rec.Type != "channel-close" {
		t.Errorf("Type=%q, want channel-close", rec.Type)
	}
	wantKeys := []string{"by", "ts"}
	if len(rec.Fields) != len(wantKeys) {
		t.Fatalf("len(Fields)=%d, want %d", len(rec.Fields), len(wantKeys))
	}
	for i, k := range wantKeys {
		if rec.Fields[i].Key != k {
			t.Errorf("Fields[%d].Key=%q, want %q", i, rec.Fields[i].Key, k)
		}
	}
}

func TestGenerateMessageID_FormatMatches(t *testing.T) {
	id, err := GenerateMessageID()
	if err != nil {
		t.Fatalf("GenerateMessageID: %v", err)
	}
	re := regexp.MustCompile(`^[0-9]+-[a-z0-9]{6}$`)
	if !re.MatchString(id) {
		t.Errorf("GenerateMessageID=%q, want match ^[0-9]+-[a-z0-9]{6}$", id)
	}
	if strings.HasPrefix(id, "ch-") {
		t.Errorf("GenerateMessageID=%q should NOT have ch- prefix", id)
	}
}

func TestWriteMessage_CreatesMessagesDir(t *testing.T) {
	root := t.TempDir()
	chID := "ch-5-eeeeee"
	seedActiveMeta(t, root, chID, "alice", "bob")
	msgsDir := filepath.Join(root, "live", "channels", "active", chID, "messages")
	if _, err := os.Stat(msgsDir); !os.IsNotExist(err) {
		t.Fatalf("messages/ should not exist before first WriteMessage, got err=%v", err)
	}
	msgID := "100-aaaaaa"
	rec := BuildSayRecord(msgID, chID, "alice", "hi", "2026-05-12T15:00:00Z")
	if err := WriteMessage(root, chID, msgID, rec); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	if _, err := os.Stat(msgsDir); err != nil {
		t.Errorf("messages/ not created after WriteMessage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(msgsDir, msgID+".gdl")); err != nil {
		t.Errorf("message file not created: %v", err)
	}
}

func TestWriteMessage_Roundtrip(t *testing.T) {
	root := t.TempDir()
	chID := "ch-6-ffffff"
	seedActiveMeta(t, root, chID, "alice", "bob")
	msgID := "101-bbbbbb"
	rec := BuildSayRecord(msgID, chID, "alice", "hello bob", "2026-05-12T15:00:00Z")
	if err := WriteMessage(root, chID, msgID, rec); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	path := filepath.Join(root, "live", "channels", "active", chID, "messages", msgID+".gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(records) != 1 || records[0].Type != "channel-message" {
		t.Fatalf("got %d records, first.Type=%q, want 1 channel-message", len(records), records[0].Type)
	}
	if records[0].Get("by") != "alice" || records[0].Get("content") != "hello bob" || records[0].Get("channel") != chID {
		t.Errorf("roundtrip wrong: %+v", records[0].Fields)
	}
}

// G/#R28: --content on `rufio say` accepted multi-line free-text.
// Writing it to live/channels/active/<ch>/messages/<msg>.gdl wedged
// `rufio listen` and channel readback with malformed-line errors.
func TestSay_MultilineContent_DoesNotPoisonSubstrate(t *testing.T) {
	root := t.TempDir()
	chID := "ch-9-zzzzzz"
	seedActiveMeta(t, root, chID, "alice", "bob")
	msgID := "200-cccccc"
	multiline := "hello bob\nhope you're well\n- one\n- two"
	rec := BuildSayRecord(msgID, chID, "alice", multiline, "ts")
	if err := WriteMessage(root, chID, msgID, rec); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	path := filepath.Join(root, "live", "channels", "active", chID, "messages", msgID+".gdl")
	bs, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	body := strings.TrimRight(string(bs), "\n")
	if strings.ContainsAny(body, "\n\r") {
		t.Fatalf("channel-message file contains raw newline/CR (poisoned): %q", string(bs))
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument errored after multi-line say: %v\nfile: %q", err, string(bs))
	}
	if got := records[0].Get("content"); got != multiline {
		t.Errorf("say content round-trip mismatch:\n got=%q\nwant=%q", got, multiline)
	}
}

func TestAppendLeave_AppendsRecord(t *testing.T) {
	root := t.TempDir()
	chID := "ch-7-gggggg"
	seedActiveMeta(t, root, chID, "alice", "bob")
	if err := AppendLeave(root, chID, "bob", "2026-05-12T16:00:00Z"); err != nil {
		t.Fatalf("AppendLeave: %v", err)
	}
	bs, err := os.ReadFile(filepath.Join(root, "live", "channels", "active", chID, "meta.gdl"))
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
	if records[1].Type != "channel-leave" || records[1].Get("by") != "bob" {
		t.Errorf("appended record wrong: %+v", records[1])
	}
}

func TestAppendLeave_Idempotent(t *testing.T) {
	root := t.TempDir()
	chID := "ch-8-hhhhhh"
	seedActiveMeta(t, root, chID, "alice", "bob")
	if err := AppendLeave(root, chID, "bob", "2026-05-12T16:00:00Z"); err != nil {
		t.Fatalf("AppendLeave #1: %v", err)
	}
	if err := AppendLeave(root, chID, "bob", "2026-05-12T16:01:00Z"); err != nil {
		t.Fatalf("AppendLeave #2: %v", err)
	}
	bs, err := os.ReadFile(filepath.Join(root, "live", "channels", "active", chID, "meta.gdl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2 (idempotent)", len(records))
	}
}

func TestAppendLeave_NoSuchChannel(t *testing.T) {
	root := t.TempDir()
	err := AppendLeave(root, "ch-missing-zzzzzz", "alice", "2026-05-12T16:00:00Z")
	var got *rufioerr.NoSuchChannelError
	if !errors.As(err, &got) {
		t.Fatalf("want *NoSuchChannelError, got %T (%v)", err, err)
	}
}

func TestAppendClose_AppendsAndRenames(t *testing.T) {
	root := t.TempDir()
	chID := "ch-9-iiiiii"
	seedActiveMeta(t, root, chID, "alice", "bob")
	if err := AppendClose(root, chID, "alice", "2026-05-12T17:00:00Z"); err != nil {
		t.Fatalf("AppendClose: %v", err)
	}
	// active dir should be gone
	if _, err := os.Stat(filepath.Join(root, "live", "channels", "active", chID)); !os.IsNotExist(err) {
		t.Errorf("active/<ch>/ still exists after close (err=%v)", err)
	}
	// closed dir should contain meta.gdl with the @channel-close record
	bs, err := os.ReadFile(filepath.Join(root, "live", "channels", "closed", chID, "meta.gdl"))
	if err != nil {
		t.Fatalf("ReadFile closed meta: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[1].Type != "channel-close" || records[1].Get("by") != "alice" || records[1].Get("ts") != "2026-05-12T17:00:00Z" {
		t.Errorf("close record wrong: %+v", records[1])
	}
}

func TestAppendClose_Idempotent_AlreadyInClosedDir(t *testing.T) {
	root := t.TempDir()
	chID := "ch-10-jjjjjj"
	seedActiveMeta(t, root, chID, "alice", "bob")
	if err := AppendClose(root, chID, "alice", "2026-05-12T17:00:00Z"); err != nil {
		t.Fatalf("AppendClose #1: %v", err)
	}
	if err := AppendClose(root, chID, "alice", "2026-05-12T17:01:00Z"); err != nil {
		t.Fatalf("AppendClose #2 (should be no-op): %v", err)
	}
	// closed meta should still have exactly 2 records (no duplicate close)
	bs, err := os.ReadFile(filepath.Join(root, "live", "channels", "closed", chID, "meta.gdl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	records, err := gdl.ParseDocument(string(bs))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("got %d records, want 2 (no duplicate close)", len(records))
	}
}
