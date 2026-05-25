package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// channel-privacy tests — third-pass discovery (post-v1.0.5 audit).
//
// The listen surface (CLI + MCP + SSE all share stream.Poll/EmitCatchUp/
// WatchAndEmit) walked live/channels/active/*/messages/ and applied
// only scope-based privacy. channel-message records carry no `scope`
// field (channel-membership is the visibility primitive), so
// non-members read everything via `rufio listen --types=channel-message`.
//
// Fix: a channel-membership predicate applied alongside Match in every
// stream entry point. Tests below pin the visibility rules:
//
//   - Non-member listening: zero channel-message events (positive
//     guard: must be empty).
//   - Member listening: sees both own and counterpart's messages.
//   - Anonymous (CurrentAgent=="") listening: firehose preserved, gets
//     everything — this is the local stdio mode contract.
//   - Past member (left or closed): still sees prior messages (audit
//     trail surfaces via channels.IsEverMember which we reuse).
//   - Other record types unaffected (regression guard).

// seedChannelWithMessages scaffolds a 2-party channel between alice
// and bob with two messages on disk. Returns the channel id. Layout
// mirrors what `rufio accept` + `rufio say` produce.
func seedChannelWithMessages(t *testing.T, root string) string {
	t.Helper()
	chID := "ch-1779000000000-test01"
	chanDir := filepath.Join(root, "live", "channels", "active", chID)
	msgDir := filepath.Join(chanDir, "messages")
	if err := os.MkdirAll(msgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := "@channel|id:" + chID + "|opener:alice|target:bob|topic:lunch|intent:planning|ts:2026-05-22T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(chanDir, "meta.gdl"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	msg1 := "@channel-message|id:1779000000001-aaaaaa|channel:" + chID + "|by:alice|content:confidential alice|ts:2026-05-22T00:00:01Z\n"
	if err := os.WriteFile(filepath.Join(msgDir, "1779000000001-aaaaaa.gdl"), []byte(msg1), 0o644); err != nil {
		t.Fatalf("write msg1: %v", err)
	}
	msg2 := "@channel-message|id:1779000000002-bbbbbb|channel:" + chID + "|by:bob|content:confidential bob|ts:2026-05-22T00:00:02Z\n"
	if err := os.WriteFile(filepath.Join(msgDir, "1779000000002-bbbbbb.gdl"), []byte(msg2), 0o644); err != nil {
		t.Fatalf("write msg2: %v", err)
	}
	return chID
}

// collectCatchUp drives EmitCatchUp with the channels directory and
// returns the captured events.
func collectCatchUp(t *testing.T, root, currentAgent string) []Event {
	t.Helper()
	var buf bytes.Buffer
	fp := FilterParams{Types: []string{"channel-message"}, CurrentAgent: currentAgent}
	if err := EmitCatchUp(&buf, root, []string{"live"}, fp); err != nil {
		t.Fatalf("EmitCatchUp: %v", err)
	}
	out := []Event{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

// TestListen_NonMemberCannotSeeChannelMessages — THE bug. Carol is
// not a channel member; her listen MUST return zero channel-message
// events. Pre-fix this fails (carol receives both alice's and bob's
// confidential messages). Post-fix she gets nothing.
func TestListen_NonMemberCannotSeeChannelMessages(t *testing.T) {
	root := t.TempDir()
	_ = seedChannelWithMessages(t, root)

	got := collectCatchUp(t, root, "carol")
	if len(got) != 0 {
		t.Fatalf("carol (non-member) saw %d channel-message events; expected 0\nevents: %#v", len(got), got)
	}
}

// TestListen_MemberSeesOwnChannelMessages — both members must see
// the full channel history. Pin both directions: alice sees her own
// AND bob's message; bob sees his own AND alice's. Regression guard
// against an over-strict predicate that filters by `by:` field.
func TestListen_MemberSeesOwnChannelMessages(t *testing.T) {
	root := t.TempDir()
	_ = seedChannelWithMessages(t, root)

	for _, agent := range []string{"alice", "bob"} {
		got := collectCatchUp(t, root, agent)
		if len(got) != 2 {
			t.Errorf("%s saw %d channel-message events; expected 2\nevents: %#v", agent, len(got), got)
			continue
		}
		seenAlice := false
		seenBob := false
		for _, ev := range got {
			if strings.Contains(ev.Raw, "by:alice") {
				seenAlice = true
			}
			if strings.Contains(ev.Raw, "by:bob") {
				seenBob = true
			}
		}
		if !seenAlice || !seenBob {
			t.Errorf("%s missing messages: seenAlice=%v seenBob=%v", agent, seenAlice, seenBob)
		}
	}
}

// TestListen_AnonymousAuthClientFirehose_PreservedForLocalStdioMode —
// when CurrentAgent is empty (anonymous local stdio mode, admin/test
// callers), the firehose path is preserved. The convention is set in
// stream.Match's existing scope-bypass logic and we must NOT change it
// for the channel predicate either.
func TestListen_AnonymousAuthClientFirehose_PreservedForLocalStdioMode(t *testing.T) {
	root := t.TempDir()
	_ = seedChannelWithMessages(t, root)

	got := collectCatchUp(t, root, "")
	if len(got) != 2 {
		t.Errorf("anonymous listener saw %d channel-message events; expected 2 (firehose)\nevents: %#v", len(got), got)
	}
}

// TestListen_PastMember_SeesPriorMessages — a member who left the
// channel (or whose channel was closed) should still see the audit
// trail. Mirrors channels.IsEverMember semantics (opener OR target,
// regardless of left state). The bob-leaves case below is the
// minimal scenario: bob can still see alice's messages after he left.
func TestListen_PastMember_SeesPriorMessages(t *testing.T) {
	root := t.TempDir()
	chID := seedChannelWithMessages(t, root)
	// Append a @channel-leave record for bob — the on-disk
	// representation of "bob has left the channel" per D16.4.
	metaPath := filepath.Join(root, "live", "channels", "active", chID, "meta.gdl")
	existing, _ := os.ReadFile(metaPath)
	left := "@channel-leave|channel:" + chID + "|by:bob|ts:2026-05-22T00:00:03Z\n"
	if err := os.WriteFile(metaPath, append(existing, []byte(left)...), 0o644); err != nil {
		t.Fatalf("append leave: %v", err)
	}

	got := collectCatchUp(t, root, "bob")
	if len(got) != 2 {
		t.Errorf("bob (past member) saw %d channel-message events; expected 2 (audit trail)\nevents: %#v", len(got), got)
	}
}

// TestListen_OtherTypesUnaffected — regression guard. The channel-
// membership predicate must apply ONLY to channel-message records.
// A non-member listening for thoughts/observations/etc. must NOT be
// dropped by the new filter; only `channel-message` is gated.
func TestListen_OtherTypesUnaffected(t *testing.T) {
	root := t.TempDir()
	// Seed a thought in alice's outbox (scope=fleet so carol can see it).
	outbox := filepath.Join(root, "live", "outbox", "alice")
	if err := os.MkdirAll(outbox, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec := "@thought|id:1779000000010-thought|author:alice|type:hypothesis|subject:test:1|content:public|scope:fleet|ts:2026-05-22T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(outbox, "1779000000010-thought.gdl"), []byte(rec), 0o644); err != nil {
		t.Fatalf("write thought: %v", err)
	}

	var buf bytes.Buffer
	fp := FilterParams{Types: []string{"thought"}, CurrentAgent: "carol"}
	if err := EmitCatchUp(&buf, root, []string{"live"}, fp); err != nil {
		t.Fatalf("EmitCatchUp: %v", err)
	}
	body := strings.TrimSpace(buf.String())
	if !strings.Contains(body, `"content":"public"`) {
		t.Errorf("non-member carol should see fleet-scoped thoughts; got %q", body)
	}
}

// TestListen_WatchAndEmit_NonMemberCannotSeeChannelMessages — the
// live-watch path (fsnotify) also goes through Match. Pre-fix this
// fails identically to the catch-up path. Post-fix the predicate
// applies symmetrically.
func TestListen_WatchAndEmit_NonMemberCannotSeeChannelMessages(t *testing.T) {
	root := t.TempDir()
	chID := "ch-1779000000000-livech"
	chanDir := filepath.Join(root, "live", "channels", "active", chID)
	if err := os.MkdirAll(filepath.Join(chanDir, "messages"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := "@channel|id:" + chID + "|opener:alice|target:bob|topic:lunch|intent:planning|ts:2026-05-22T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(chanDir, "meta.gdl"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		fp := FilterParams{Types: []string{"channel-message"}, CurrentAgent: "carol"}
		done <- WatchAndEmit(ctx, &buf, root, []string{"live"}, fp)
	}()

	// Wait briefly for the watcher to register, then drop a message.
	time.Sleep(150 * time.Millisecond)
	msg := "@channel-message|id:1779000000099-livemsg|channel:" + chID + "|by:alice|content:secret|ts:2026-05-22T00:00:09Z\n"
	if err := os.WriteFile(filepath.Join(chanDir, "messages", "1779000000099-livemsg.gdl"), []byte(msg), 0o644); err != nil {
		t.Fatalf("write live msg: %v", err)
	}
	<-done

	body := strings.TrimSpace(buf.String())
	if body != "" {
		t.Errorf("carol (non-member) saw live channel events via WatchAndEmit:\n%s", body)
	}
}

// TestMetaCache_TransientIOErrorNotCached is the regression guard for
// the v1.0.5 post-review correctness fix. Pre-fix, ANY metaCache.load
// failure inserted a `nil` sentinel that hid every subsequent event
// for that chID — including the case where the channel-meta later
// became readable (mirror sync arrival ordering: messages/<id>.gdl
// landed before meta.gdl was visible; or a brief NFS attribute-cache
// miss). Long-lived `rufio listen` consumers (cursor_emit.go's
// WatchAndEmitFrom shares one cache across the entire watch lifetime)
// would permanently lose channel visibility.
//
// Fix: positive-only caching. Failures (including
// *NoSuchChannelError and any transient IO error) are NOT cached.
// The mirror-sync race makes negative caching unsafe; the bounded
// retry-per-event cost is acceptable per channel_privacy.go's
// metaCache.load doc. See also TestMetaCache_FailuresNotCached for
// the no-failure-cached internals assertion.
//
// Shape: load a chID that has no meta on disk yet → expect (zero,
// false); the load MUST NOT have cached the failure. Then write the
// meta and load again → expect (populated, true).
func TestMetaCache_TransientIOErrorNotCached(t *testing.T) {
	root := t.TempDir()
	chID := "ch-1779000000-tmp01"
	chanDir := filepath.Join(root, "live", "channels", "active", chID)
	if err := os.MkdirAll(filepath.Join(chanDir, "messages"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// meta.gdl is intentionally missing — mimics the "message arrived
	// before meta is visible" mirror-sync race + the NFS attr-cache
	// miss case.

	cache := newMetaCache()
	if _, ok := cache.load(root, chID); ok {
		t.Fatal("expected load to fail with no meta on disk")
	}
	// Channel id exists as a dir but with no meta.gdl. channels.LoadMeta
	// returns *NoSuchChannelError in this state — so it IS expected to
	// be cached. To exercise the transient-not-cached path, blow away
	// the channel dir entirely so the next load gets a different error
	// path. Actually simpler: write the meta now and verify load() picks
	// it up (i.e. the previous miss was NOT cached as a permanent miss).
	meta := "@channel|id:" + chID + "|opener:alice|target:bob|topic:lunch|intent:planning|ts:2026-05-22T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(chanDir, "meta.gdl"), []byte(meta), 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	got, ok := cache.load(root, chID)
	if !ok {
		t.Fatal("expected load to succeed once meta is on disk — failure was incorrectly cached")
	}
	if got.Opener != "alice" || got.Target != "bob" {
		t.Errorf("loaded meta wrong: opener=%q target=%q", got.Opener, got.Target)
	}
}

// TestMetaCache_FailuresNotCached locks in the positive-only-cache
// semantic. A missing/unreadable meta MUST NOT be cached, so a later
// successful load picks up the meta. The hostile-stream "1000 events
// for a non-existent chID" cost is one disk stat each (the channel
// dir doesn't exist → fast NoSuchChannelError) — acceptable given the
// mirror-sync race makes negative caching unsafe.
func TestMetaCache_FailuresNotCached(t *testing.T) {
	root := t.TempDir()
	cache := newMetaCache()
	chID := "ch-1779000000-ghost"
	// First load fails — channel dir doesn't exist.
	if _, ok := cache.load(root, chID); ok {
		t.Fatal("expected load to fail for non-existent channel")
	}
	// Internals: the failure must NOT have been cached.
	cache.mu.Lock()
	_, seen := cache.cells[chID]
	cache.mu.Unlock()
	if seen {
		t.Error("failure was incorrectly cached — mirror-sync race would permanently hide the chID")
	}
}
