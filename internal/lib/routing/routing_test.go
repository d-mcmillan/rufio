package routing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
)

func TestReadAttentions_MissingDirReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	got, err := ReadAttentions(root)
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len=%d", len(got))
	}
}

func TestReadAttentions_ParsesMultipleAgents(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "live", "attention")
	os.MkdirAll(dir, 0o755)

	a := gdl.Record{Type: "attention", Fields: []gdl.RecordField{
		{Key: "agent", Value: "agent-a"},
		{Key: "intent", Value: "debugging"},
		{Key: "entities", Value: "customer:5821,order:42"},
		{Key: "topics", Value: "churn,p1"},
		{Key: "ts", Value: "ts"},
	}}
	os.WriteFile(filepath.Join(dir, "agent-a.gdl"), []byte(gdl.RenderLine(a)+"\n"), 0o644)

	b := gdl.Record{Type: "attention", Fields: []gdl.RecordField{
		{Key: "agent", Value: "agent-b"},
		{Key: "intent", Value: "reviewing"},
		{Key: "entities", Value: "customer:5821"},
		{Key: "ts", Value: "ts"},
	}}
	os.WriteFile(filepath.Join(dir, "agent-b.gdl"), []byte(gdl.RenderLine(b)+"\n"), 0o644)

	got, err := ReadAttentions(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	if len(got["agent-a"].Entities) != 2 {
		t.Errorf("agent-a entities len=%d, want 2", len(got["agent-a"].Entities))
	}
	if got["agent-a"].Entities[0] != "customer:5821" {
		t.Errorf("agent-a entities[0]=%q", got["agent-a"].Entities[0])
	}
	if len(got["agent-b"].Topics) != 0 {
		t.Errorf("agent-b topics should be empty: %v", got["agent-b"].Topics)
	}
}

func TestMatchRecipients_EntityMatch(t *testing.T) {
	attentions := map[string]Attention{
		"agent-a": {Agent: "agent-a", Entities: []string{"customer:5821"}},
		"agent-b": {Agent: "agent-b", Entities: []string{"order:99"}},
	}
	thought := ThoughtForRouting{ID: "1-a", Author: "agent-c", Subject: "customer:5821"}
	got := MatchRecipients(thought, attentions)
	if len(got) != 1 || got[0] != "agent-a" {
		t.Errorf("got=%v, want [agent-a]", got)
	}
}

func TestMatchRecipients_TopicMatch(t *testing.T) {
	attentions := map[string]Attention{
		"agent-a": {Agent: "agent-a", Topics: []string{"churn", "p1"}},
		"agent-b": {Agent: "agent-b", Topics: []string{"unrelated"}},
	}
	thought := ThoughtForRouting{
		ID: "1-a", Author: "agent-c", Subject: "other:1",
		Topics: []string{"churn", "support"},
	}
	got := MatchRecipients(thought, attentions)
	if len(got) != 1 || got[0] != "agent-a" {
		t.Errorf("got=%v, want [agent-a]", got)
	}
}

func TestMatchRecipients_EntityOrTopic_EitherMatches(t *testing.T) {
	attentions := map[string]Attention{
		"agent-a": {Agent: "agent-a", Entities: []string{"customer:5821"}, Topics: []string{"unrelated"}},
		"agent-b": {Agent: "agent-b", Entities: []string{"order:99"}, Topics: []string{"churn"}},
	}
	thought := ThoughtForRouting{
		ID: "1-a", Author: "agent-c", Subject: "customer:5821",
		Topics: []string{"churn"},
	}
	got := MatchRecipients(thought, attentions)
	if len(got) != 2 {
		t.Errorf("len=%d want 2", len(got))
	}
	if got[0] != "agent-a" || got[1] != "agent-b" {
		t.Errorf("got=%v, want sorted [agent-a, agent-b]", got)
	}
}

func TestMatchRecipients_ExcludesAuthor(t *testing.T) {
	attentions := map[string]Attention{
		"agent-a": {Agent: "agent-a", Entities: []string{"customer:5821"}},
		"agent-b": {Agent: "agent-b", Entities: []string{"customer:5821"}},
	}
	thought := ThoughtForRouting{ID: "1-a", Author: "agent-a", Subject: "customer:5821"}
	got := MatchRecipients(thought, attentions)
	if len(got) != 1 || got[0] != "agent-b" {
		t.Errorf("got=%v, want only agent-b (author excluded)", got)
	}
}

func TestMatchRecipients_NoMatch_ReturnsEmpty(t *testing.T) {
	attentions := map[string]Attention{
		"agent-a": {Agent: "agent-a", Entities: []string{"order:1"}, Topics: []string{"unrelated"}},
	}
	thought := ThoughtForRouting{
		ID: "1-a", Author: "agent-c", Subject: "customer:5821",
		Topics: []string{"churn"},
	}
	got := MatchRecipients(thought, attentions)
	if len(got) != 0 {
		t.Errorf("got=%v, want empty", got)
	}
}

// Helpers
func seedAttention(t *testing.T, root, agent string, entities, topics []string) {
	t.Helper()
	dir := filepath.Join(root, "live", "attention")
	os.MkdirAll(dir, 0o755)
	fields := []gdl.RecordField{
		{Key: "agent", Value: agent},
		{Key: "intent", Value: "test"},
	}
	if len(entities) > 0 {
		fields = append(fields, gdl.RecordField{Key: "entities", Value: strings.Join(entities, ",")})
	}
	if len(topics) > 0 {
		fields = append(fields, gdl.RecordField{Key: "topics", Value: strings.Join(topics, ",")})
	}
	fields = append(fields, gdl.RecordField{Key: "ts", Value: "ts"})
	rec := gdl.Record{Type: "attention", Fields: fields}
	os.WriteFile(filepath.Join(dir, agent+".gdl"), []byte(gdl.RenderLine(rec)+"\n"), 0o644)
}

func seedThoughtFile(t *testing.T, root, author, id, subject string, topics []string) string {
	t.Helper()
	dir := filepath.Join(root, "live", "outbox", author)
	os.MkdirAll(dir, 0o755)
	fields := []gdl.RecordField{
		{Key: "id", Value: id},
		{Key: "author", Value: author},
		{Key: "type", Value: "hypothesis"},
		{Key: "subject", Value: subject},
		{Key: "content", Value: "test"},
		{Key: "scope", Value: "fleet"},
	}
	if len(topics) > 0 {
		fields = append(fields, gdl.RecordField{Key: "topics", Value: strings.Join(topics, ",")})
	}
	fields = append(fields, gdl.RecordField{Key: "ts", Value: "2026-05-12T12:00:00Z"})
	fields = append(fields, gdl.RecordField{Key: "ttl", Value: "0"})
	rec := gdl.Record{Type: "thought", Fields: fields}
	path := filepath.Join(dir, id+".gdl")
	os.WriteFile(path, []byte(gdl.RenderLine(rec)+"\n"), 0o644)
	return path
}

func TestRouteThought_WritesToMatchingInbox(t *testing.T) {
	root := t.TempDir()
	seedAttention(t, root, "agent-b", []string{"customer:5821"}, nil)
	path := seedThoughtFile(t, root, "agent-a", "1-aaaaaa", "customer:5821", nil)

	if err := RouteThought(root, path); err != nil {
		t.Fatalf("RouteThought: %v", err)
	}

	inboxFile := filepath.Join(root, "live", "inbox", "agent-b", "1-aaaaaa.gdl")
	bs, err := os.ReadFile(inboxFile)
	if err != nil {
		t.Fatalf("inbox file missing: %v", err)
	}
	content := string(bs)
	if !strings.Contains(content, "@thought|") {
		t.Errorf("inbox missing @thought line:\n%s", content)
	}
	if !strings.Contains(content, "@route|to:agent-b") {
		t.Errorf("inbox missing @route line:\n%s", content)
	}
	if !strings.Contains(content, "from:agent-a") {
		t.Errorf("inbox @route missing from:agent-a:\n%s", content)
	}
}

func TestRouteThought_SkipsAuthor(t *testing.T) {
	root := t.TempDir()
	// Author has attention matching subject — must NOT route to self.
	seedAttention(t, root, "agent-a", []string{"customer:5821"}, nil)
	path := seedThoughtFile(t, root, "agent-a", "1-aaaaaa", "customer:5821", nil)

	if err := RouteThought(root, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "inbox", "agent-a", "1-aaaaaa.gdl")); err == nil {
		t.Errorf("inbox written to author's own dir — should be skipped")
	}
}

func TestRouteThought_NoMatch_NoInboxes(t *testing.T) {
	root := t.TempDir()
	seedAttention(t, root, "agent-b", []string{"order:99"}, nil)
	path := seedThoughtFile(t, root, "agent-a", "1-aaaaaa", "customer:5821", nil)

	if err := RouteThought(root, path); err != nil {
		t.Fatal(err)
	}
	// No inbox dir for agent-b should be created.
	if _, err := os.Stat(filepath.Join(root, "live", "inbox", "agent-b")); err == nil {
		t.Errorf("inbox dir created for non-matching attention")
	}
}

func TestRouteThought_Idempotent(t *testing.T) {
	root := t.TempDir()
	seedAttention(t, root, "agent-b", []string{"customer:5821"}, nil)
	path := seedThoughtFile(t, root, "agent-a", "1-aaaaaa", "customer:5821", nil)

	if err := RouteThought(root, path); err != nil {
		t.Fatal(err)
	}
	if err := RouteThought(root, path); err != nil {
		t.Fatal(err)
	}

	// Verify the inbox file exists and has exactly one @route line
	// (idempotent — second call should skip).
	bs, _ := os.ReadFile(filepath.Join(root, "live", "inbox", "agent-b", "1-aaaaaa.gdl"))
	if c := strings.Count(string(bs), "@route|"); c != 1 {
		t.Errorf("expected exactly 1 @route line, got %d:\n%s", c, bs)
	}
}

func TestRouteThought_MultipleRecipients(t *testing.T) {
	root := t.TempDir()
	for _, a := range []string{"agent-b", "agent-c", "agent-d"} {
		seedAttention(t, root, a, []string{"customer:5821"}, nil)
	}
	path := seedThoughtFile(t, root, "agent-a", "1-aaaaaa", "customer:5821", nil)

	if err := RouteThought(root, path); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"agent-b", "agent-c", "agent-d"} {
		if _, err := os.Stat(filepath.Join(root, "live", "inbox", a, "1-aaaaaa.gdl")); err != nil {
			t.Errorf("missing inbox file for %s: %v", a, err)
		}
	}
}

// seedSummonFile writes a @summon record (with optional extra fields) to
// live/summons/pending/<id>.gdl and returns the path. Mirrors the shape
// produced by the `rufio summon` command.
func seedSummonFile(t *testing.T, root, id, from, to string, includeTo bool) string {
	t.Helper()
	dir := filepath.Join(root, "live", "summons", "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fields := []gdl.RecordField{
		{Key: "id", Value: id},
		{Key: "from", Value: from},
	}
	if includeTo {
		fields = append(fields, gdl.RecordField{Key: "to", Value: to})
	}
	fields = append(fields,
		gdl.RecordField{Key: "topic", Value: "customer:5821"},
		gdl.RecordField{Key: "intent", Value: "discuss"},
		gdl.RecordField{Key: "ts", Value: "2026-05-12T12:00:00Z"},
		gdl.RecordField{Key: "ttl", Value: "86400"},
	)
	rec := gdl.Record{Type: "summon", Fields: fields}
	path := filepath.Join(dir, id+".gdl")
	if err := os.WriteFile(path, []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatalf("write summon: %v", err)
	}
	return path
}

func TestRouteSummon_HappyPath_DeliversToTargetInbox(t *testing.T) {
	root := t.TempDir()
	path := seedSummonFile(t, root, "1-summon", "agent-a", "agent-b", true)

	if err := RouteSummon(root, path); err != nil {
		t.Fatalf("RouteSummon: %v", err)
	}

	inboxFile := filepath.Join(root, "live", "inbox", "agent-b", "1-summon.gdl")
	bs, err := os.ReadFile(inboxFile)
	if err != nil {
		t.Fatalf("inbox file missing: %v", err)
	}
	content := string(bs)
	if !strings.Contains(content, "@summon|") {
		t.Errorf("inbox missing @summon line:\n%s", content)
	}
	if !strings.Contains(content, "@route|to:agent-b") {
		t.Errorf("inbox missing @route|to:agent-b:\n%s", content)
	}
	if !strings.Contains(content, "from:agent-a") {
		t.Errorf("inbox @route missing from:agent-a:\n%s", content)
	}
}

func TestRouteSummon_Idempotent_SkipsExistingInbox(t *testing.T) {
	root := t.TempDir()
	path := seedSummonFile(t, root, "1-summon", "agent-a", "agent-b", true)

	if err := RouteSummon(root, path); err != nil {
		t.Fatalf("first RouteSummon: %v", err)
	}
	inboxFile := filepath.Join(root, "live", "inbox", "agent-b", "1-summon.gdl")
	first, err := os.ReadFile(inboxFile)
	if err != nil {
		t.Fatalf("inbox file missing after first call: %v", err)
	}
	firstStat, err := os.Stat(inboxFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// Second call must be a no-op — content + mtime unchanged.
	if err := RouteSummon(root, path); err != nil {
		t.Fatalf("second RouteSummon: %v", err)
	}
	second, err := os.ReadFile(inboxFile)
	if err != nil {
		t.Fatalf("inbox file missing after second call: %v", err)
	}
	secondStat, err := os.Stat(inboxFile)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("inbox file content changed on second call:\nbefore=%s\nafter=%s", first, second)
	}
	if !firstStat.ModTime().Equal(secondStat.ModTime()) {
		t.Errorf("inbox file mtime changed: %v → %v", firstStat.ModTime(), secondStat.ModTime())
	}
	// And exactly one @route line.
	if c := strings.Count(string(second), "@route|"); c != 1 {
		t.Errorf("expected exactly 1 @route line, got %d:\n%s", c, second)
	}
}

func TestRouteSummon_SkipsNonSummonFile(t *testing.T) {
	root := t.TempDir()
	// A @thought-only file landing in the summons/pending dir (e.g. an
	// errant write) — RouteSummon must not deliver it.
	dir := filepath.Join(root, "live", "summons", "pending")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-bogus"},
		{Key: "author", Value: "agent-a"},
		{Key: "type", Value: "hypothesis"},
		{Key: "subject", Value: "customer:5821"},
		{Key: "content", Value: "wrong type"},
		{Key: "scope", Value: "fleet"},
		{Key: "ts", Value: "2026-05-12T12:00:00Z"},
		{Key: "ttl", Value: "0"},
	}}
	path := filepath.Join(dir, "1-bogus.gdl")
	if err := os.WriteFile(path, []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RouteSummon(root, path); err != nil {
		t.Fatalf("RouteSummon: %v", err)
	}
	// No inbox should be written.
	if _, err := os.Stat(filepath.Join(root, "live", "inbox")); err == nil {
		t.Errorf("inbox tree created for non-summon file")
	}
}

func TestRouteSummon_MissingTargetField_SkipsSilently(t *testing.T) {
	root := t.TempDir()
	path := seedSummonFile(t, root, "1-summon", "agent-a", "", false)

	if err := RouteSummon(root, path); err != nil {
		t.Fatalf("RouteSummon: %v", err)
	}
	// No inbox should be written.
	if _, err := os.Stat(filepath.Join(root, "live", "inbox")); err == nil {
		t.Errorf("inbox tree created for summon missing `to:` field")
	}
}

// seedChannelMeta writes a minimal active meta.gdl for chID with the
// given opener/target plus any extra audit records (e.g. @channel-leave,
// @channel-close). Test scaffolding only.
func seedChannelMeta(t *testing.T, root, chID, opener, target string, extra ...gdl.Record) {
	t.Helper()
	dir := filepath.Join(root, "live", "channels", "active", chID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seedChannelMeta mkdir: %v", err)
	}
	rec := gdl.Record{Type: "channel", Fields: []gdl.RecordField{
		{Key: "id", Value: chID},
		{Key: "opener", Value: opener},
		{Key: "target", Value: target},
		{Key: "topic", Value: "customer:5821"},
		{Key: "intent", Value: "discuss churn"},
		{Key: "created-at", Value: "2026-05-12T12:00:00Z"},
	}}
	buf := gdl.RenderLine(rec) + "\n"
	for _, r := range extra {
		buf += gdl.RenderLine(r) + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.gdl"), []byte(buf), 0o644); err != nil {
		t.Fatalf("seedChannelMeta write: %v", err)
	}
}

// seedClosedChannelMeta writes a meta.gdl directly under closed/<chID>/.
func seedClosedChannelMeta(t *testing.T, root, chID, opener, target string, extra ...gdl.Record) {
	t.Helper()
	dir := filepath.Join(root, "live", "channels", "closed", chID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seedClosedChannelMeta mkdir: %v", err)
	}
	rec := gdl.Record{Type: "channel", Fields: []gdl.RecordField{
		{Key: "id", Value: chID},
		{Key: "opener", Value: opener},
		{Key: "target", Value: target},
		{Key: "topic", Value: "customer:5821"},
		{Key: "intent", Value: "discuss churn"},
		{Key: "created-at", Value: "2026-05-12T12:00:00Z"},
	}}
	buf := gdl.RenderLine(rec) + "\n"
	for _, r := range extra {
		buf += gdl.RenderLine(r) + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.gdl"), []byte(buf), 0o644); err != nil {
		t.Fatalf("seedClosedChannelMeta write: %v", err)
	}
}

// seedChannelMessage writes a @channel-message record under
// live/channels/active/<chID>/messages/<msgID>.gdl and returns the
// path. Issue #107: on-disk Type is "channel-message" (the canonical
// taxonomy used by recall.AllTypes). The legacy "say" token is still
// tolerated by the router for backward compatibility — see
// TestRouteChannelMessage_LegacySayType_StillRoutes for the proof.
func seedChannelMessage(t *testing.T, root, chID, msgID, by, content string) string {
	t.Helper()
	dir := filepath.Join(root, "live", "channels", "active", chID, "messages")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seedChannelMessage mkdir: %v", err)
	}
	rec := gdl.Record{Type: "channel-message", Fields: []gdl.RecordField{
		{Key: "id", Value: msgID},
		{Key: "channel", Value: chID},
		{Key: "by", Value: by},
		{Key: "content", Value: content},
		{Key: "ts", Value: "2026-05-12T12:00:00Z"},
	}}
	path := filepath.Join(dir, msgID+".gdl")
	if err := os.WriteFile(path, []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatalf("seedChannelMessage write: %v", err)
	}
	return path
}

func TestRouteChannelMessage_HappyPath_DeliversToOtherMember(t *testing.T) {
	root := t.TempDir()
	chID := "ch-1-aaaaaa"
	seedChannelMeta(t, root, chID, "agent-a", "agent-b")
	msgPath := seedChannelMessage(t, root, chID, "msg-1-aaaaaa", "agent-a", "hi bob")

	if err := RouteChannelMessage(root, msgPath); err != nil {
		t.Fatalf("RouteChannelMessage: %v", err)
	}

	inboxFile := filepath.Join(root, "live", "inbox", "agent-b", "msg-1-aaaaaa.gdl")
	bs, err := os.ReadFile(inboxFile)
	if err != nil {
		t.Fatalf("inbox file missing: %v", err)
	}
	content := string(bs)
	// Issue #107: on-disk Type is "channel-message" (CLI verb still `say`).
	if !strings.Contains(content, "@channel-message|") {
		t.Errorf("inbox missing @channel-message line:\n%s", content)
	}
	if !strings.Contains(content, "@route|to:agent-b|from:agent-a") {
		t.Errorf("inbox missing @route|to:agent-b|from:agent-a line:\n%s", content)
	}
}

func TestRouteChannelMessage_DoesNotEchoToSender(t *testing.T) {
	root := t.TempDir()
	chID := "ch-1-aaaaaa"
	seedChannelMeta(t, root, chID, "agent-a", "agent-b")
	msgPath := seedChannelMessage(t, root, chID, "msg-1-aaaaaa", "agent-a", "hi bob")

	if err := RouteChannelMessage(root, msgPath); err != nil {
		t.Fatalf("RouteChannelMessage: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "live", "inbox", "agent-a")); err == nil {
		t.Errorf("sender's own inbox dir was created — must not echo to sender")
	}
}

func TestRouteChannelMessage_LeftMemberNotDelivered(t *testing.T) {
	root := t.TempDir()
	chID := "ch-1-aaaaaa"
	// agent-b leaves the channel before agent-a says anything.
	leaveB := gdl.Record{Type: "channel-leave", Fields: []gdl.RecordField{
		{Key: "by", Value: "agent-b"},
		{Key: "ts", Value: "2026-05-12T12:30:00Z"},
	}}
	seedChannelMeta(t, root, chID, "agent-a", "agent-b", leaveB)
	msgPath := seedChannelMessage(t, root, chID, "msg-2-bbbbbb", "agent-a", "hello?")

	if err := RouteChannelMessage(root, msgPath); err != nil {
		t.Fatalf("RouteChannelMessage: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "live", "inbox", "agent-b")); err == nil {
		t.Errorf("inbox dir created for left member — must skip")
	}
	if _, err := os.Stat(filepath.Join(root, "live", "inbox", "agent-a")); err == nil {
		t.Errorf("sender's inbox dir created — must not echo to sender")
	}
}

func TestRouteChannelMessage_ClosedChannel_LogsAndReturnsNil(t *testing.T) {
	root := t.TempDir()
	chID := "ch-1-aaaaaa"
	// Seed the meta as closed (close record appended in active/).
	closeRec := gdl.Record{Type: "channel-close", Fields: []gdl.RecordField{
		{Key: "by", Value: "agent-a"},
		{Key: "ts", Value: "2026-05-12T13:00:00Z"},
	}}
	seedChannelMeta(t, root, chID, "agent-a", "agent-b", closeRec)
	// Message file landed before close took effect (rare race).
	msgPath := seedChannelMessage(t, root, chID, "msg-1-aaaaaa", "agent-a", "ignored")

	if err := RouteChannelMessage(root, msgPath); err != nil {
		t.Fatalf("RouteChannelMessage: unexpected error %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "inbox")); err == nil {
		t.Errorf("inbox tree created for closed channel — must skip")
	}
}

func TestRouteChannelMessage_NoSuchChannel_LogsAndReturnsNil(t *testing.T) {
	root := t.TempDir()
	chID := "ch-orphan-xxxxxx"
	// Write the message file but DON'T seed the channel meta (channel
	// doesn't exist in either active/ or closed/).
	dir := filepath.Join(root, "live", "channels", "active", chID, "messages")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := gdl.Record{Type: "channel-message", Fields: []gdl.RecordField{
		{Key: "id", Value: "msg-orphan"},
		{Key: "channel", Value: chID},
		{Key: "by", Value: "agent-a"},
		{Key: "content", Value: "into the void"},
		{Key: "ts", Value: "2026-05-12T12:00:00Z"},
	}}
	msgPath := filepath.Join(dir, "msg-orphan.gdl")
	if err := os.WriteFile(msgPath, []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Remove the meta.gdl that was never written? It was never written —
	// only the messages/ subdir exists. So LoadMeta will fail with
	// NoSuchChannelError. But wait — the active/<chID>/ dir exists. We
	// need to also ensure meta.gdl is absent. seedChannelMeta wasn't
	// called, so it is. Confirm:
	if _, err := os.Stat(filepath.Join(root, "live", "channels", "active", chID, "meta.gdl")); err == nil {
		t.Fatal("test setup error: meta.gdl shouldn't exist")
	}

	if err := RouteChannelMessage(root, msgPath); err != nil {
		t.Fatalf("RouteChannelMessage: unexpected error %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "inbox")); err == nil {
		t.Errorf("inbox tree created for orphaned message — must skip")
	}
}

func TestRouteChannelMessage_NonSayFile_ReturnsNil(t *testing.T) {
	root := t.TempDir()
	chID := "ch-1-aaaaaa"
	seedChannelMeta(t, root, chID, "agent-a", "agent-b")
	// Write a @thought record where a @channel-message is expected.
	dir := filepath.Join(root, "live", "channels", "active", chID, "messages")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := gdl.Record{Type: "thought", Fields: []gdl.RecordField{
		{Key: "id", Value: "1-bogus"},
		{Key: "author", Value: "agent-a"},
		{Key: "type", Value: "hypothesis"},
		{Key: "subject", Value: "customer:5821"},
		{Key: "content", Value: "wrong type"},
		{Key: "scope", Value: "fleet"},
		{Key: "ts", Value: "2026-05-12T12:00:00Z"},
		{Key: "ttl", Value: "0"},
	}}
	msgPath := filepath.Join(dir, "1-bogus.gdl")
	if err := os.WriteFile(msgPath, []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RouteChannelMessage(root, msgPath); err != nil {
		t.Fatalf("RouteChannelMessage: unexpected error %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "inbox")); err == nil {
		t.Errorf("inbox tree created for non-channel-message file — must skip")
	}
}

func TestRouteChannelMessage_ClosedByDir_ReturnsNil(t *testing.T) {
	// Variant: meta moved to closed/ (the normal close path). We get
	// closedByDir=true from LoadMeta, so meta.Closed=true and we skip.
	root := t.TempDir()
	chID := "ch-closed-dir"
	seedClosedChannelMeta(t, root, chID, "agent-a", "agent-b")
	// Message file lingers in active/ — orphaned by the channel-close
	// rename in real life this shouldn't happen (close moves the whole
	// dir) but be defensive.
	dir := filepath.Join(root, "live", "channels", "active", chID, "messages")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := gdl.Record{Type: "channel-message", Fields: []gdl.RecordField{
		{Key: "id", Value: "msg-orphan"},
		{Key: "channel", Value: chID},
		{Key: "by", Value: "agent-a"},
		{Key: "content", Value: "ignored"},
		{Key: "ts", Value: "2026-05-12T12:00:00Z"},
	}}
	msgPath := filepath.Join(dir, "msg-orphan.gdl")
	if err := os.WriteFile(msgPath, []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RouteChannelMessage(root, msgPath); err != nil {
		t.Fatalf("RouteChannelMessage: unexpected error %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "live", "inbox")); err == nil {
		t.Errorf("inbox tree created for closed-by-dir channel — must skip")
	}
}

// TestRouteChannelMessage_LegacySayType_StillRoutes was removed
// 2026-05-23 (v1.0.6) per the 2026-06-01 deadline on the legacy @say
// tolerance. Channels TTL = 24h and v1.0.1 shipped 2026-05-19; all
// stale @say records have long since aged out. The router now requires
// Type="channel-message" exactly; the dual-token tolerance is gone.

// ---- RouteGoalOverlap -------------------------------------------------------

// seedGoalFile writes a single-record @goal file to live/goals/active/<id>.gdl
// with the given author + statement. Mirrors the on-disk shape produced by
// the `rufio goal` write path (goal.WriteActive).
func seedGoalFile(t *testing.T, root, author, id, statement string) string {
	t.Helper()
	dir := filepath.Join(root, "live", "goals", "active")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("seedGoalFile mkdir: %v", err)
	}
	rec := goal.BuildGoalRecord(id, author, statement, "", "", "agent", "2026-05-12T00:00:00Z")
	body := gdl.RenderLine(rec) + "\n"
	path := filepath.Join(dir, id+".gdl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("seedGoalFile write: %v", err)
	}
	return path
}

func TestRouteGoalOverlap_HappyPath_DeliversToBothInboxes(t *testing.T) {
	root := t.TempDir()
	// Pre-existing peer goal mentioning customer:5821.
	seedGoalFile(t, root, "agent-b", "goal-b-1", "ship customer:5821 dashboard")
	// New goal by agent-a — overlaps on customer:5821.
	newPath := seedGoalFile(t, root, "agent-a", "goal-a-1", "investigate customer:5821 spike")

	if err := RouteGoalOverlap(root, newPath); err != nil {
		t.Fatalf("RouteGoalOverlap: %v", err)
	}

	// Both inboxes get the same filename: <source>-overlap-<target>.gdl.
	peerInbox := filepath.Join(root, "live", "inbox", "agent-b", "goal-a-1-overlap-goal-b-1.gdl")
	selfInbox := filepath.Join(root, "live", "inbox", "agent-a", "goal-a-1-overlap-goal-b-1.gdl")

	peerBytes, err := os.ReadFile(peerInbox)
	if err != nil {
		t.Fatalf("peer inbox missing: %v", err)
	}
	selfBytes, err := os.ReadFile(selfInbox)
	if err != nil {
		t.Fatalf("self inbox missing: %v", err)
	}

	peer := string(peerBytes)
	self := string(selfBytes)

	// Each file holds one @goal-overlap (one shared entity in this test).
	if c := strings.Count(peer, "@goal-overlap|"); c != 1 {
		t.Errorf("peer file: want 1 @goal-overlap record, got %d:\n%s", c, peer)
	}
	if c := strings.Count(self, "@goal-overlap|"); c != 1 {
		t.Errorf("self file: want 1 @goal-overlap record, got %d:\n%s", c, self)
	}

	// Peer file's `to:` is agent-b; self file's `to:` is agent-a (the
	// recipient field reflects whose inbox the file lives in).
	if !strings.Contains(peer, "to:agent-b") {
		t.Errorf("peer file missing to:agent-b:\n%s", peer)
	}
	if !strings.Contains(self, "to:agent-a") {
		t.Errorf("self file missing to:agent-a:\n%s", self)
	}
	// Both have from:agent-a (the new-goal author triggered the scan).
	for _, content := range []string{peer, self} {
		if !strings.Contains(content, "from:agent-a") {
			t.Errorf("file missing from:agent-a:\n%s", content)
		}
		// `:` in customer:5821 escapes to customer\:5821 on render.
		if !strings.Contains(content, `entity:customer\:5821`) {
			t.Errorf("file missing escaped entity customer\\:5821:\n%s", content)
		}
		if !strings.Contains(content, "source-goal:goal-a-1") {
			t.Errorf("file missing source-goal:goal-a-1:\n%s", content)
		}
		if !strings.Contains(content, "target-goal:goal-b-1") {
			t.Errorf("file missing target-goal:goal-b-1:\n%s", content)
		}
	}
}

func TestRouteGoalOverlap_NoOverlap_NoDelivery(t *testing.T) {
	root := t.TempDir()
	// Peer goal mentions a different entity.
	seedGoalFile(t, root, "agent-b", "goal-b-1", "build order:42 export")
	newPath := seedGoalFile(t, root, "agent-a", "goal-a-1", "investigate customer:5821 spike")

	if err := RouteGoalOverlap(root, newPath); err != nil {
		t.Fatalf("RouteGoalOverlap: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "live", "inbox")); err == nil {
		t.Errorf("inbox tree created when no overlap — must skip")
	}
}

func TestRouteGoalOverlap_SelfOverlap_Suppressed(t *testing.T) {
	root := t.TempDir()
	// Two goals from the same author sharing customer:5821 — D18.2.
	seedGoalFile(t, root, "agent-a", "goal-a-1", "fix customer:5821 churn")
	newPath := seedGoalFile(t, root, "agent-a", "goal-a-2", "ship customer:5821 dashboard")

	if err := RouteGoalOverlap(root, newPath); err != nil {
		t.Fatalf("RouteGoalOverlap: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "live", "inbox")); err == nil {
		t.Errorf("inbox tree created for same-author overlap — must skip (D18.2)")
	}
}

func TestRouteGoalOverlap_MultiEntityGrouping(t *testing.T) {
	root := t.TempDir()
	// Peer goal mentions BOTH customer:5821 AND vendor:acme.
	seedGoalFile(t, root, "agent-b", "goal-b-1",
		"renew vendor:acme contract and stabilise customer:5821 churn")
	// New goal mentions both too.
	newPath := seedGoalFile(t, root, "agent-a", "goal-a-1",
		"coordinate customer:5821 with vendor:acme deal")

	if err := RouteGoalOverlap(root, newPath); err != nil {
		t.Fatalf("RouteGoalOverlap: %v", err)
	}

	// Exactly ONE file per recipient with TWO @goal-overlap records inside (D18.8).
	for _, recipient := range []string{"agent-a", "agent-b"} {
		path := filepath.Join(root, "live", "inbox", recipient, "goal-a-1-overlap-goal-b-1.gdl")
		bs, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("recipient %s inbox missing: %v", recipient, err)
		}
		content := string(bs)
		if c := strings.Count(content, "@goal-overlap|"); c != 2 {
			t.Errorf("recipient %s: want 2 @goal-overlap records, got %d:\n%s", recipient, c, content)
		}
		if !strings.Contains(content, `entity:customer\:5821`) {
			t.Errorf("recipient %s: missing entity:customer\\:5821:\n%s", recipient, content)
		}
		if !strings.Contains(content, "entity:vendor") {
			t.Errorf("recipient %s: missing entity:vendor:acme:\n%s", recipient, content)
		}
	}
	// And exactly one file per recipient (not multiple).
	for _, recipient := range []string{"agent-a", "agent-b"} {
		entries, err := os.ReadDir(filepath.Join(root, "live", "inbox", recipient))
		if err != nil {
			t.Fatalf("readdir %s: %v", recipient, err)
		}
		if len(entries) != 1 {
			t.Errorf("recipient %s: want 1 inbox file, got %d", recipient, len(entries))
		}
	}
}

func TestRouteGoalOverlap_MultiAgentFanout(t *testing.T) {
	root := t.TempDir()
	// Two peer agents with active goals mentioning customer:5821.
	seedGoalFile(t, root, "agent-b", "goal-b-1", "fix customer:5821 churn")
	seedGoalFile(t, root, "agent-c", "goal-c-1", "support customer:5821 onboarding")
	newPath := seedGoalFile(t, root, "agent-a", "goal-a-1", "investigate customer:5821 spike")

	if err := RouteGoalOverlap(root, newPath); err != nil {
		t.Fatalf("RouteGoalOverlap: %v", err)
	}

	// 4 inbox files total: 1 each at agent-b, agent-c, and 2 at agent-a
	// (one per (source,target) pair).
	wantPaths := []string{
		filepath.Join(root, "live", "inbox", "agent-b", "goal-a-1-overlap-goal-b-1.gdl"),
		filepath.Join(root, "live", "inbox", "agent-c", "goal-a-1-overlap-goal-c-1.gdl"),
		filepath.Join(root, "live", "inbox", "agent-a", "goal-a-1-overlap-goal-b-1.gdl"),
		filepath.Join(root, "live", "inbox", "agent-a", "goal-a-1-overlap-goal-c-1.gdl"),
	}
	for _, p := range wantPaths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected inbox file missing: %s (%v)", p, err)
		}
	}
	// Verify agent-a's inbox has exactly 2 files (one per peer).
	entries, err := os.ReadDir(filepath.Join(root, "live", "inbox", "agent-a"))
	if err != nil {
		t.Fatalf("readdir agent-a: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("agent-a inbox: want 2 files, got %d", len(entries))
	}
}

func TestRouteGoalOverlap_Idempotent_SecondCallSkipped(t *testing.T) {
	root := t.TempDir()
	seedGoalFile(t, root, "agent-b", "goal-b-1", "ship customer:5821 dashboard")
	newPath := seedGoalFile(t, root, "agent-a", "goal-a-1", "investigate customer:5821 spike")

	if err := RouteGoalOverlap(root, newPath); err != nil {
		t.Fatalf("first RouteGoalOverlap: %v", err)
	}
	peerInbox := filepath.Join(root, "live", "inbox", "agent-b", "goal-a-1-overlap-goal-b-1.gdl")
	firstBytes, err := os.ReadFile(peerInbox)
	if err != nil {
		t.Fatalf("peer inbox missing after first call: %v", err)
	}
	firstStat, err := os.Stat(peerInbox)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// Second call must be a no-op — content + mtime unchanged.
	if err := RouteGoalOverlap(root, newPath); err != nil {
		t.Fatalf("second RouteGoalOverlap: %v", err)
	}
	secondBytes, err := os.ReadFile(peerInbox)
	if err != nil {
		t.Fatalf("peer inbox missing after second call: %v", err)
	}
	secondStat, err := os.Stat(peerInbox)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Errorf("inbox content changed on second call:\nbefore=%s\nafter=%s", firstBytes, secondBytes)
	}
	if !firstStat.ModTime().Equal(secondStat.ModTime()) {
		t.Errorf("inbox mtime changed: %v → %v", firstStat.ModTime(), secondStat.ModTime())
	}
	if c := strings.Count(string(secondBytes), "@goal-overlap|"); c != 1 {
		t.Errorf("expected exactly 1 @goal-overlap line, got %d:\n%s", c, secondBytes)
	}
}
