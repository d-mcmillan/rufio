package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- Match ----

func TestMatch_TypesFilter(t *testing.T) {
	ev := Event{Type: "thought"}
	if !Match(ev, FilterParams{}) {
		t.Fatal("empty filter should pass any type")
	}
	if !Match(ev, FilterParams{Types: []string{"thought"}}) {
		t.Fatal("matching type should pass")
	}
	if Match(ev, FilterParams{Types: []string{"observation"}}) {
		t.Fatal("non-matching type should fail")
	}
	if !Match(ev, FilterParams{Types: []string{"observation", "thought"}}) {
		t.Fatal("type-in-set should pass")
	}
}

func TestMatch_ScopeBroaderPasses(t *testing.T) {
	// scope=deployment is broader than filter=agent → always visible
	ev := Event{Type: "thought", Scope: "deployment", Author: "other"}
	if !Match(ev, FilterParams{Scope: "agent", CurrentAgent: "me"}) {
		t.Fatal("broader-scoped record should pass tighter filter")
	}
	ev2 := Event{Type: "thought", Scope: "fleet", Author: "other"}
	if !Match(ev2, FilterParams{Scope: "agent", CurrentAgent: "me"}) {
		t.Fatal("fleet-scoped record should pass agent filter")
	}
}

func TestMatch_ScopeSameAuthorRequired(t *testing.T) {
	// Same-scope: only the current agent's own record is visible.
	mine := Event{Type: "thought", Scope: "agent", Author: "me"}
	theirs := Event{Type: "thought", Scope: "agent", Author: "other"}
	if !Match(mine, FilterParams{Scope: "agent", CurrentAgent: "me"}) {
		t.Fatal("same-scope, same-author should pass")
	}
	if Match(theirs, FilterParams{Scope: "agent", CurrentAgent: "me"}) {
		t.Fatal("same-scope, different-author should be excluded")
	}
}

func TestMatch_GivenBypassesScope(t *testing.T) {
	// given records always pass scope filter (D9.3-style project-wide visibility).
	ev := Event{Type: "given", Scope: "", Path: "given/policy.md"}
	if !Match(ev, FilterParams{Scope: "agent", CurrentAgent: "me"}) {
		t.Fatal("given record should bypass scope filter")
	}
}

// TestMatch_PrivacyFilter_OtherAgentScopeAgentExcluded covers the #139
// followup privacy gap: with no explicit --scope but a known CurrentAgent,
// another agent's scope:agent record must NOT pass — it's their private
// thought, not ours. (Before the broader catch-up walk landed, the
// daemon-routed inbox already excluded these by structure; now that the
// walk reaches live/outbox/<other-agent>/ directly, the predicate has to
// enforce it.)
func TestMatch_PrivacyFilter_OtherAgentScopeAgentExcluded(t *testing.T) {
	theirs := Event{Type: "thought", Scope: "agent", Author: "agent-b"}
	if Match(theirs, FilterParams{CurrentAgent: "agent-a"}) {
		t.Fatal("other agent's scope:agent record must be excluded when CurrentAgent is set and no explicit scope filter")
	}
}

// TestMatch_PrivacyFilter_OwnScopeAgentStillVisible — corollary: my own
// scope:agent records still flow. The privacy rule is "other agents'
// private records hidden", not "all private records hidden".
func TestMatch_PrivacyFilter_OwnScopeAgentStillVisible(t *testing.T) {
	mine := Event{Type: "thought", Scope: "agent", Author: "agent-a"}
	if !Match(mine, FilterParams{CurrentAgent: "agent-a"}) {
		t.Fatal("own scope:agent record must still pass when CurrentAgent is set and no explicit scope filter")
	}
}

// TestMatch_PrivacyFilter_NoCurrentAgent_NoFilter — regression guard for
// the firehose path (admin/test callers that supply no CurrentAgent).
// With CurrentAgent == "", the privacy rule is OFF: pre-existing semantic
// preserved so `rufio stream` outside any project / without identity keeps
// emitting every record.
func TestMatch_PrivacyFilter_NoCurrentAgent_NoFilter(t *testing.T) {
	theirs := Event{Type: "thought", Scope: "agent", Author: "agent-b"}
	if !Match(theirs, FilterParams{}) {
		t.Fatal("without CurrentAgent the privacy rule must be off — firehose semantics preserved")
	}
}

// TestMatch_PrivacyFilter_BroaderScopeUnaffected — scope:fleet records
// from another agent remain visible even when the privacy rule is on.
// Only scope:agent is treated as private.
func TestMatch_PrivacyFilter_BroaderScopeUnaffected(t *testing.T) {
	fleet := Event{Type: "thought", Scope: "fleet", Author: "agent-b"}
	if !Match(fleet, FilterParams{CurrentAgent: "agent-a"}) {
		t.Fatal("other agent's scope:fleet record must still pass — only scope:agent is private")
	}
	deployment := Event{Type: "thought", Scope: "deployment", Author: "agent-b"}
	if !Match(deployment, FilterParams{CurrentAgent: "agent-a"}) {
		t.Fatal("other agent's scope:deployment record must still pass")
	}
	unscoped := Event{Type: "thought", Scope: "", Author: "agent-b"}
	if !Match(unscoped, FilterParams{CurrentAgent: "agent-a"}) {
		t.Fatal("other agent's no-scope record must still pass — privacy filter targets scope:agent only")
	}
}

// TestEmitCatchUp_PrivacyFilter_OtherAgentScopeAgentExcluded — the
// integration version: seed agent-b's outbox with one scope:agent and one
// scope:fleet thought. agent-a's catch-up must see the fleet record but
// not the private one. This is the exact shape that leaked in the #139
// smoke run.
func TestEmitCatchUp_PrivacyFilter_OtherAgentScopeAgentExcluded(t *testing.T) {
	root := t.TempDir()
	outboxB := filepath.Join(root, "live", "outbox", "agent-b")
	if err := os.MkdirAll(outboxB, 0o755); err != nil {
		t.Fatal(err)
	}
	// agent-b's private scope:agent thought — must be filtered out.
	priv := "@thought|ts:2026-05-20T00:00:00Z|author:agent-b|subject:test:1|content:agent-b private|scope:agent\n"
	if err := os.WriteFile(filepath.Join(outboxB, "priv.gdl"), []byte(priv), 0o644); err != nil {
		t.Fatal(err)
	}
	// agent-b's broadcast scope:fleet thought — must pass.
	pub := "@thought|ts:2026-05-20T00:00:01Z|author:agent-b|subject:test:2|content:agent-b public|scope:fleet\n"
	if err := os.WriteFile(filepath.Join(outboxB, "pub.gdl"), []byte(pub), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := EmitCatchUp(&buf, root, []string{"live/outbox"}, FilterParams{CurrentAgent: "agent-a"})
	if err != nil {
		t.Fatalf("EmitCatchUp: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "agent-b private") {
		t.Fatalf("agent-b's scope:agent record leaked into agent-a's catch-up: %q", out)
	}
	if !strings.Contains(out, "agent-b public") {
		t.Fatalf("agent-b's scope:fleet record was dropped — privacy filter is too broad: %q", out)
	}
}

// TestEmitCatchUp_PrivacyFilter_OwnScopeAgentStillVisible — agent-a's own
// scope:agent record must still appear in their catch-up. Same-author +
// same-scope is "their own private state" — they're supposed to see it.
func TestEmitCatchUp_PrivacyFilter_OwnScopeAgentStillVisible(t *testing.T) {
	root := t.TempDir()
	outboxA := filepath.Join(root, "live", "outbox", "agent-a")
	if err := os.MkdirAll(outboxA, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "@thought|ts:2026-05-20T00:00:00Z|author:agent-a|subject:test:1|content:agent-a own private|scope:agent\n"
	if err := os.WriteFile(filepath.Join(outboxA, "own.gdl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := EmitCatchUp(&buf, root, []string{"live/outbox"}, FilterParams{CurrentAgent: "agent-a"})
	if err != nil {
		t.Fatalf("EmitCatchUp: %v", err)
	}
	if !strings.Contains(buf.String(), "agent-a own private") {
		t.Fatalf("agent-a's own scope:agent record was dropped — privacy filter must not hide own records: %q", buf.String())
	}
}

// TestEmitCatchUp_NoCurrentAgent_NoPrivacyFilter — admin/test firehose
// path. Without a CurrentAgent, the broader walk emits every record
// including other agents' scope:agent (existing semantic preserved for
// the unfiltered consumer).
func TestEmitCatchUp_NoCurrentAgent_NoPrivacyFilter(t *testing.T) {
	root := t.TempDir()
	outboxB := filepath.Join(root, "live", "outbox", "agent-b")
	if err := os.MkdirAll(outboxB, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "@thought|ts:2026-05-20T00:00:00Z|author:agent-b|subject:test:1|content:agent-b private|scope:agent\n"
	if err := os.WriteFile(filepath.Join(outboxB, "priv.gdl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := EmitCatchUp(&buf, root, []string{"live/outbox"}, FilterParams{})
	if err != nil {
		t.Fatalf("EmitCatchUp: %v", err)
	}
	if !strings.Contains(buf.String(), "agent-b private") {
		t.Fatalf("firehose mode (no CurrentAgent) must emit every record, including other agents' scope:agent: %q", buf.String())
	}
}

// ---- FileToEvents ----

func TestFileToEvents_ParsesGDLFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thought.gdl")
	body := "@thought|ts:2026-05-12T00:00:00Z|author:me|subject:agent::me|predicate:thinks|object:something|content:hello|scope:agent\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	evs, err := FileToEvents(path, "live/outbox/me/thought.gdl")
	if err != nil {
		t.Fatalf("FileToEvents: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	ev := evs[0]
	if ev.Type != "thought" {
		t.Errorf("Type = %q, want thought", ev.Type)
	}
	if ev.TS != "2026-05-12T00:00:00Z" {
		t.Errorf("TS = %q", ev.TS)
	}
	if ev.Author != "me" {
		t.Errorf("Author = %q", ev.Author)
	}
	if ev.Subject != "agent::me" {
		t.Errorf("Subject = %q", ev.Subject)
	}
	if ev.Predicate != "thinks" {
		t.Errorf("Predicate = %q", ev.Predicate)
	}
	if ev.Object != "something" {
		t.Errorf("Object = %q", ev.Object)
	}
	if ev.Content != "hello" {
		t.Errorf("Content = %q", ev.Content)
	}
	if ev.Scope != "agent" {
		t.Errorf("Scope = %q", ev.Scope)
	}
	if ev.Path != "live/outbox/me/thought.gdl" {
		t.Errorf("Path = %q", ev.Path)
	}
	if !strings.HasPrefix(ev.Raw, "@thought|") {
		t.Errorf("Raw should be rendered GDL line, got %q", ev.Raw)
	}
}

func TestFileToEvents_SkipsNonGDLExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("@thought|ts:x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evs, err := FileToEvents(path, "note.txt")
	if err != nil {
		t.Fatalf("FileToEvents: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("expected 0 events for non-gdl file, got %d", len(evs))
	}
}

func TestFileToEvents_MissingFileReturnsError(t *testing.T) {
	_, err := FileToEvents("/nonexistent/path/file.gdl", "file.gdl")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// ---- EmitCatchUp ----

func TestEmitCatchUp_WalksAndEmits(t *testing.T) {
	root := t.TempDir()
	// Pre-seed live/inbox/me/thought.gdl
	inbox := filepath.Join(root, "live", "inbox", "me")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "@thought|ts:2026-05-12T00:00:00Z|author:other|subject:agent::me|content:hi|scope:agent\n"
	if err := os.WriteFile(filepath.Join(inbox, "t1.gdl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := EmitCatchUp(&buf, root, []string{"live/inbox/me"}, FilterParams{})
	if err != nil {
		t.Fatalf("EmitCatchUp: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %q", len(lines), buf.String())
	}
	var ev Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if ev.Type != "thought" {
		t.Errorf("Type = %q", ev.Type)
	}
	if ev.Path != "live/inbox/me/t1.gdl" {
		t.Errorf("Path = %q (want POSIX-form relative)", ev.Path)
	}
}

func TestEmitCatchUp_MissingDirSkipsCleanly(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	err := EmitCatchUp(&buf, root, []string{"live/inbox/me", "live/promoted"}, FilterParams{})
	if err != nil {
		t.Fatalf("EmitCatchUp should skip missing dirs, got: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected empty output for missing dirs, got %q", buf.String())
	}
}

func TestEmitCatchUp_FiltersByType(t *testing.T) {
	root := t.TempDir()
	inbox := filepath.Join(root, "live", "inbox", "me")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	thoughtBody := "@thought|ts:2026-05-12T00:00:00Z|author:other|content:hi\n"
	obsBody := "@observation|ts:2026-05-12T00:00:00Z|author:other|subject:x|predicate:is|object:y\n"
	if err := os.WriteFile(filepath.Join(inbox, "t1.gdl"), []byte(thoughtBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "o1.gdlm"), []byte(obsBody), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := EmitCatchUp(&buf, root, []string{"live/inbox/me"}, FilterParams{Types: []string{"thought"}})
	if err != nil {
		t.Fatalf("EmitCatchUp: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line after type filter, got %d: %q", len(lines), buf.String())
	}
	var ev Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if ev.Type != "thought" {
		t.Errorf("Type = %q, want thought", ev.Type)
	}
}

// TestEmitCatchUp_WalksNestedSubdirs covers the channel-messages nesting
// shape (live/channels/active/<ch-id>/messages/<id>.gdl) that the listen
// catch-up walk now traverses. filepath.WalkDir already recurses, but
// this locks the contract so a future refactor (e.g. to glob-based walk)
// can't silently drop nested records — issue #139 was triggered by the
// runListen-side walk not REACHING these dirs, but the lib walker must
// keep recursing once it does.
func TestEmitCatchUp_WalksNestedSubdirs(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "live", "channels", "active", "ch-1", "messages")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "@channel-message|id:m1|channel:ch-1|by:agent-b|content:hi|ts:2026-05-12T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(nested, "m1.gdl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := EmitCatchUp(&buf, root, []string{"live/channels/active"}, FilterParams{})
	if err != nil {
		t.Fatalf("EmitCatchUp: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("expected 1 channel-message line from nested dir, got %d: %q", len(lines), buf.String())
	}
	var ev Event
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if ev.Type != "channel-message" {
		t.Errorf("Type = %q, want channel-message", ev.Type)
	}
	if ev.Path != "live/channels/active/ch-1/messages/m1.gdl" {
		t.Errorf("Path = %q (want POSIX-form nested path)", ev.Path)
	}
}

// TestEmitCatchUp_MultipleDirs verifies the walker handles a heterogeneous
// dir set (the listen catch-up shape from issue #139's fix: inbox +
// outbox + channels/active + summons/pending), emitting records from each
// in a single pass. Missing dirs in the set are skipped cleanly.
func TestEmitCatchUp_MultipleDirs(t *testing.T) {
	root := t.TempDir()
	outboxA := filepath.Join(root, "live", "outbox", "agent-a")
	chanDir := filepath.Join(root, "live", "channels", "active", "ch-x")
	chanMsgs := filepath.Join(chanDir, "messages")
	pendSum := filepath.Join(root, "live", "summons", "pending")
	for _, d := range []string{outboxA, chanMsgs, pendSum} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outboxA, "t1.gdl"),
		[]byte("@thought|id:t1|ts:2026-05-12T00:00:01Z|author:agent-a|subject:order:1|content:hi|scope:fleet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// channel-privacy fix: the channel-membership predicate needs a
	// real meta.gdl to look up. agent-a is a member (opener) so the
	// channel-message lands in the visible output.
	if err := os.WriteFile(filepath.Join(chanDir, "meta.gdl"),
		[]byte("@channel|id:ch-x|opener:agent-a|target:agent-b|topic:test|intent:test|ts:2026-05-12T00:00:00Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chanMsgs, "m1.gdl"),
		[]byte("@channel-message|id:m1|channel:ch-x|by:agent-b|content:bye|ts:2026-05-12T00:00:02Z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendSum, "s1.gdl"),
		[]byte("@summon|id:s1|from:agent-b|to:agent-a|topic:test:c|intent:hi|ts:2026-05-12T00:00:03Z|ttl:86400\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	dirs := []string{
		"live/inbox/agent-a", // missing — must skip cleanly
		"live/outbox",
		"live/channels/active",
		"live/summons/pending",
	}
	// Filter by --types so the channel meta.gdl (record type @channel,
	// emitted because the walker reads every .gdl under the dir tree)
	// doesn't pollute the count assertion. The point of this test is
	// "all three substrate types reach a single EmitCatchUp pass";
	// the meta record is incidental.
	if err := EmitCatchUp(&buf, root, dirs, FilterParams{
		Types:        []string{"thought", "channel-message", "summon"},
		CurrentAgent: "agent-a",
	}); err != nil {
		t.Fatalf("EmitCatchUp: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines across 3 dirs (1 thought + 1 channel-message + 1 summon), got %d: %q", len(lines), buf.String())
	}
	types := map[string]bool{}
	for _, l := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatalf("invalid JSON %q: %v", l, err)
		}
		types[ev.Type] = true
	}
	for _, want := range []string{"thought", "channel-message", "summon"} {
		if !types[want] {
			t.Errorf("missing %q in output: %q", want, buf.String())
		}
	}
}

// ---- WatchAndEmit ----

// safeBuffer is a goroutine-safe bytes.Buffer wrapper for tests.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestWatchAndEmit_ContextCancelReturns(t *testing.T) {
	root := t.TempDir()
	inbox := filepath.Join(root, "live", "inbox", "me")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var buf safeBuffer
	done := make(chan error, 1)
	go func() {
		done <- WatchAndEmit(ctx, &buf, root, []string{"live/inbox/me"}, FilterParams{})
	}()
	// Give the watcher a moment to register, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WatchAndEmit returned error on context cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchAndEmit did not return after context cancel")
	}
}

func TestWatchAndEmit_FileCreateEmits(t *testing.T) {
	root := t.TempDir()
	inbox := filepath.Join(root, "live", "inbox", "me")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var buf safeBuffer
	done := make(chan error, 1)
	go func() {
		done <- WatchAndEmit(ctx, &buf, root, []string{"live/inbox/me"}, FilterParams{})
	}()

	// Deterministic deflake (tasks #100/#102/#106). Two-sided fix:
	//
	//  1. Writer side — distinct-file retry loop. stream.WatchAndEmit acts
	//     ONLY on fsnotify.Create events (stream.go: WK2-STREAM-2). A
	//     same-path os.WriteFile retry produces a WRITE event that the
	//     daemon drops — the OLD loop here had effectively NO retry, just
	//     the original single-Create race, which under heavy parallel -race
	//     load loses to the watcher-registration window. Fix: write a fresh
	//     filename each iteration so every attempt is a genuine Create. The
	//     content is identical, so any one match satisfies the assertion.
	//     Same shape as test/integration/listen_test.go (#106).
	//
	//  2. Consumer side — drain-and-match across all emitted lines. The
	//     daemon may emit multiple JSONL lines (one per Create that lands
	//     after registration completes). The symmetric stream-side
	//     equivalent of internal/tui/watch_test.go's "discard transient
	//     msgs until target" is "scan ALL lines, accept the FIRST that
	//     parses + matches" — transient noise from concurrent fs activity
	//     (e.g. unrelated inbox writes from sibling subtests in the
	//     package's parallel runs) goes to stderr in the daemon, not into
	//     buf, so this scan is naturally noise-tolerant.
	//
	// No fixed pre-sleep is relied on for synchronization — a later
	// iteration is guaranteed caught once fsnotify registers. The deadline
	// is only a bounded backstop against a real watcher regression.
	const body = "@thought|ts:2026-05-12T00:00:00Z|author:other|subject:agent::me|content:hi|scope:agent\n"
	deadline := time.Now().Add(8 * time.Second)
	retry := time.NewTicker(100 * time.Millisecond)
	defer retry.Stop()
	seq := 0
	writeFresh := func() {
		seq++
		// Distinct filename each iteration => a genuine Create event.
		p := filepath.Join(inbox, fmt.Sprintf("t%d.gdl", seq))
		_ = os.WriteFile(p, []byte(body), 0o644)
	}
	writeFresh() // first attempt promptly — no fixed pre-sleep.

	// Consumer-side match helper: scan every JSONL line in buf, return the
	// first valid Event whose _type is "thought". Mirrors the
	// "drain-and-match" discipline of awaitWatcherMsg (internal/tui/
	// watch_test.go) adapted to a stdout-buffer consumer.
	findThought := func() (Event, string, bool) {
		raw := buf.String()
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimRight(line, "\n")
			if line == "" {
				continue
			}
			var ev Event
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				continue // partial/in-flight line; keep scanning.
			}
			if ev.Type == "thought" {
				return ev, line, true
			}
		}
		return Event{}, "", false
	}

	var (
		got     Event
		rawLine string
		ok      bool
	)
	timeout := time.After(time.Until(deadline))
loop:
	for {
		if got, rawLine, ok = findThought(); ok {
			break
		}
		select {
		case <-timeout:
			break loop
		case <-retry.C:
			writeFresh()
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WatchAndEmit didn't return after cancel")
	}

	if !ok {
		t.Fatalf("expected thought event in output within deadline, got %q", buf.String())
	}
	// Verify the matched line is valid JSON (already parsed in findThought,
	// but assert the round-trip explicitly to keep the original test's
	// contract).
	var roundTrip Event
	if err := json.Unmarshal([]byte(rawLine), &roundTrip); err != nil {
		t.Fatalf("invalid JSON: %v (line=%q)", err, rawLine)
	}
	// Path is now per-iteration; assert the POSIX-form prefix + .gdl family
	// rather than the old hardcoded "t1.gdl" (which assumed exactly one
	// write — no longer true under the distinct-file retry loop).
	const wantPrefix = "live/inbox/me/t"
	if !strings.HasPrefix(got.Path, wantPrefix) || !strings.HasSuffix(got.Path, ".gdl") {
		t.Errorf("Path = %q (want POSIX-form relative under %q*.gdl)", got.Path, wantPrefix)
	}
}
