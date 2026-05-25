// live_tabs_test.go — PR-G3: tests for the live channels/goals/memory
// tab loaders + the lineage drill-down id carry + the watcher-fold
// exactly-once drain for the pane Msgs.
//
// Determinism: every test either writes synthetic on-disk records via
// the REAL lib writers under t.TempDir() and reads them back through the
// loader (NO wall-clock, NO fsnotify — the loader is a pure fn of `root`
// + an injected `now`, exactly like project_test.go / live_substrate_
// test.go), OR drives the App via PINNED injected tea.Msgs (the G1/G2
// determinism contract). The memory tab's relative-time is deterministic
// because `now` is injected (a fixed instant), never time.Now().
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/lib/channels"
	"github.com/d-mcmillan/rufio/internal/lib/gdl"
	"github.com/d-mcmillan/rufio/internal/lib/goal"
	"github.com/d-mcmillan/rufio/internal/lib/observation"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// tabsNow is the deterministic clock the memory loader is given (mirrors
// live_substrate_test.go's liveNow / project_test.go's fixedNow). Pinned
// so the "ago" column ("2m"/"1h") is golden-stable.
var tabsNow = time.Date(2026, 5, 15, 14, 10, 0, 0, time.UTC)

// writeSay writes a @say message into a channel's messages/ dir via the
// real channels lib so the on-disk shape is the lib's exactly.
func writeSay(t *testing.T, root, chID, msgID, by, content, ts string) {
	t.Helper()
	rec := channels.BuildSayRecord(msgID, chID, by, content, ts)
	if err := channels.WriteMessage(root, chID, msgID, rec); err != nil {
		t.Fatalf("channels.WriteMessage: %v", err)
	}
}

// writeObservation writes a @observation into learned/<subject>/<id>.gdlm
// via the real observation lib (the SAME path G0 walkLearned recurses).
func writeObservation(t *testing.T, root, id, author, subject, predicate, object, ts string) {
	t.Helper()
	rec := observation.BuildObservationRecord(observation.ObservationInput{
		ID: id, Author: author, Subject: subject, Predicate: predicate,
		Object: object, Scope: "fleet", Confidence: 1.0, TS: ts,
	})
	if err := observation.Write(root, subject, id, rec); err != nil {
		t.Fatalf("observation.Write: %v", err)
	}
}

// writeOverlap writes a @goal-overlap into live/inbox/<to>/<src>-overlap-
// <n>.gdl (the format loadInboxOverlap parses, watch_panes.go:274-281).
func writeOverlap(t *testing.T, root, to, from, entity, srcGoal, tgtGoal, ts string) {
	t.Helper()
	dir := filepath.Join(root, "live", "inbox", to)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := gdl.Record{Type: "goal-overlap", Fields: []gdl.RecordField{
		{Key: "to", Value: to},
		{Key: "from", Value: from},
		{Key: "entity", Value: entity},
		{Key: "source-goal", Value: srcGoal},
		{Key: "target-goal", Value: tgtGoal},
		{Key: "ts", Value: ts},
	}}
	name := from + "-overlap-1.gdl"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(gdl.RenderLine(rec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ── loadTabs: the live read path (disk → fixture-shaped structs) ──────

// TestLoadTabs_ChannelsLiveFromDisk proves the channels tab is sourced
// via channels.LoadMeta + the pane walk, mapped to the v8 Channel/
// ChannelSay structs field-identically, transcript ordered by ts, closed
// channels excluded (InitialWalkPanes walks active/ only — matching the
// old TUI).
//
// Operator identity is a channel MEMBER (data-analyst). The v1.0.5
// channel-membership floor in InitialWalkPanes drops channels the
// operator isn't a member of; this test uses a member id to exercise the
// positive case. See TestLoadTabs_ChannelsRequiresMembership_NonMember
// for the negative regression guard.
func TestLoadTabs_ChannelsLiveFromDisk(t *testing.T) {
	root := t.TempDir()
	writeChannelMeta(t, root, "ch-1747-x1", "claude-code", "data-analyst", "customer:5821", "downgrade sync")
	// Write OUT OF ts order on disk to prove the transcript re-orders.
	writeSay(t, root, "ch-1747-x1", "m3", "claude-code", "got it — proposing downgrade", "2026-05-15T14:03:34Z")
	writeSay(t, root, "ch-1747-x1", "m1", "claude-code", "14-day silence, mentioned cancel", "2026-05-15T14:03:01Z")
	writeSay(t, root, "ch-1747-x1", "m2", "data-analyst", "team usage 12→3 in 30d", "2026-05-15T14:03:20Z")

	tabs := loadTabs(root, "data-analyst", tabsNow)
	if len(tabs.Channels) != 1 {
		t.Fatalf("want 1 channel, got %d: %#v", len(tabs.Channels), tabs.Channels)
	}
	ch := tabs.Channels[0]
	if ch.ID != "ch-1747-x1" || ch.Opener != "claude-code" || ch.Target != "data-analyst" || ch.Topic != "customer:5821" {
		t.Errorf("channel header mismatch: %#v", ch)
	}
	// Transcript ordered by ts (m1<m2<m3) regardless of disk write order.
	wantTexts := []string{
		"14-day silence, mentioned cancel",
		"team usage 12→3 in 30d",
		"got it — proposing downgrade",
	}
	if len(ch.Msgs) != 3 {
		t.Fatalf("want 3 says, got %d: %#v", len(ch.Msgs), ch.Msgs)
	}
	for i, want := range wantTexts {
		if ch.Msgs[i].Text != want {
			t.Errorf("say[%d].Text = %q, want %q (ts order)", i, ch.Msgs[i].Text, want)
		}
	}
	// Time is the G0 tsToClock "HH:MM:SS" form (field-identical to the
	// ChannelSay fixture shape).
	if ch.Msgs[0].Time != "14:03:01" {
		t.Errorf("say[0].Time = %q, want 14:03:01 (tsToClock)", ch.Msgs[0].Time)
	}
}

// TestLoadTabs_ChannelsRequiresMembership_NonMember is the regression
// guard for the v1.0.5 TUI channel-privacy fix. Pre-fix, InitialWalkPanes
// + consumePaneEvent walked all active channels and emitted ChannelMsg /
// ChannelMessageMsg regardless of the operator's membership — a
// non-member running `rufio tui` saw every channel's metadata + every
// say. This test asserts the membership floor: a non-member sees zero
// channels.
//
// Lineage: same vuln shape as the listen-surface leak fixed in
// internal/lib/stream/channel_privacy.go (the predicate gates by
// channels.IsEverMember). The TUI bypassed that predicate because it
// reads channel records directly from disk, not via the stream package.
func TestLoadTabs_ChannelsRequiresMembership_NonMember(t *testing.T) {
	root := t.TempDir()
	// Same channel + says as the positive test — same on-disk shape.
	writeChannelMeta(t, root, "ch-1747-x1", "claude-code", "data-analyst", "customer:5821", "downgrade sync")
	writeSay(t, root, "ch-1747-x1", "m1", "claude-code", "14-day silence, mentioned cancel", "2026-05-15T14:03:01Z")
	writeSay(t, root, "ch-1747-x1", "m2", "data-analyst", "team usage 12→3 in 30d", "2026-05-15T14:03:20Z")

	// Operator is a NON-MEMBER (neither opener nor target).
	tabs := loadTabs(root, "carol", tabsNow)
	if len(tabs.Channels) != 0 {
		t.Fatalf("non-member must see zero channels, got %d: %#v", len(tabs.Channels), tabs.Channels)
	}
}

// TestConsumePaneEvent_ChannelMembershipFloor exercises the live-event
// branch of the TUI channel-privacy fix (watch_panes.go:165-181 for the
// @channel meta and :182-200 for @channel-message). Pre-fix and pre-
// regression-guard, the InitialWalkPanes branch was covered by
// TestLoadTabs_ChannelsRequiresMembership_NonMember but consumePaneEvent
// — the path live channel events take after the TUI is already running
// — had no direct test. A future regression that drops the IsEverMember
// check from consumePaneEvent only (leaving InitialWalkPanes correct)
// would slip past the existing guard.
//
// Shape: synthesise an fsnotify Create event for a real meta.gdl and a
// real messages/<id>.gdl on a channel that alice+bob own. Call
// consumePaneEvent twice: once as alice (member, expect emit), once as
// carol (non-member, expect drop).
func TestConsumePaneEvent_ChannelMembershipFloor(t *testing.T) {
	root := t.TempDir()
	chID := "ch-1747-floor"
	writeChannelMeta(t, root, chID, "alice", "bob", "customer:5821", "private")
	writeSay(t, root, chID, "m1", "alice", "confidential", "2026-05-15T14:03:01Z")

	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("fsnotify.NewWatcher: %v", err)
	}
	defer w.Close()

	metaPath := filepath.Join(root, "live", "channels", "active", chID, "meta.gdl")
	msgPath := filepath.Join(root, "live", "channels", "active", chID, "messages", "m1.gdl")

	cases := []struct {
		name     string
		me       string
		path     string
		wantOK   bool
		wantType string // empty = don't care about type when wantOK=false
	}{
		{"member-alice-meta", "alice", metaPath, true, "tui.ChannelMsg"},
		{"member-bob-meta", "bob", metaPath, true, "tui.ChannelMsg"},
		{"non-member-carol-meta", "carol", metaPath, false, ""},
		{"member-alice-message", "alice", msgPath, true, "tui.ChannelMessageMsg"},
		{"non-member-carol-message", "carol", msgPath, false, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := fsnotify.Event{Name: c.path, Op: fsnotify.Create}
			msg, ok := consumePaneEvent(root, c.me, w, ev)
			if ok != c.wantOK {
				t.Errorf("consumePaneEvent ok=%v want %v (msg=%T %+v)", ok, c.wantOK, msg, msg)
			}
			if c.wantOK {
				gotType := fmt.Sprintf("%T", msg)
				if gotType != c.wantType {
					t.Errorf("type mismatch: got %s want %s", gotType, c.wantType)
				}
			}
		})
	}
}

// TestLoadTabs_GoalsLiveAndOverlapFromStructuredRecord proves goals are
// sourced via goal.ReadAll, mapped to GoalCard, ordered by the old TUI's
// precedence (active first), and the overlap line is FORMATTED FROM the
// structured @goal-overlap record keyed by TARGET-goal-id (matching the
// old TUI's keying, tui.go:438).
func TestLoadTabs_GoalsLiveAndOverlapFromStructuredRecord(t *testing.T) {
	root := t.TempDir()
	writeGoalActive(t, root, "g-claude-1", "claude-code", "resolve customer:5821 churn risk")
	writeGoalActive(t, root, "g-cursor-1", "cursor", "improve customer:5821 onboarding re-engagement")
	// Overlap addressed to the inbox owner, keyed on the TARGET goal
	// (g-claude-1) — exactly the old TUI's m.inboxOverlaps[TargetGoalID].
	writeOverlap(t, root, "operator", "cursor", "customer:5821", "g-cursor-1", "g-claude-1", "2026-05-15T14:02:55Z")

	tabs := loadTabs(root, "operator", tabsNow)
	if len(tabs.Goals) != 2 {
		t.Fatalf("want 2 goals, got %d: %#v", len(tabs.Goals), tabs.Goals)
	}
	var claudeCard *GoalCard
	for i := range tabs.Goals {
		if tabs.Goals[i].Statement == "resolve customer:5821 churn risk" {
			claudeCard = &tabs.Goals[i]
		}
	}
	if claudeCard == nil {
		t.Fatalf("claude-code goal card not found: %#v", tabs.Goals)
	}
	if claudeCard.Author != "claude-code" || claudeCard.State != "active" {
		t.Errorf("goal card field mismatch: %#v", *claudeCard)
	}
	// The overlap line is formatted FROM the structured record (NOT a
	// pre-rendered string): keyed by target-goal-id (g-claude-1) so it
	// lands on claude's card; phrasing mirrors the v8 fixture intent.
	wantOverlap := "overlaps cursor — shared entity customer:5821"
	if claudeCard.Overlap != wantOverlap {
		t.Errorf("overlap line = %q, want %q (formatted from the structured "+
			"InboxOverlap, target-goal-keyed)", claudeCard.Overlap, wantOverlap)
	}
	// The OTHER goal (g-cursor-1, the source) has no overlap on its card
	// (the old TUI keyed on target-goal, not source).
	for _, g := range tabs.Goals {
		if g.Statement == "improve customer:5821 onboarding re-engagement" && g.Overlap != "" {
			t.Errorf("source goal must not carry the target-keyed overlap: %q", g.Overlap)
		}
	}
}

// TestLoadTabs_MemoryReusesG0WalkLearnedVerbatim proves the memory tab is
// the G0 walkLearned output VERBATIM (subject/predicate/object/author +
// the deterministic injected-now relative-time), and that loadMemory is
// byte-identical to calling walkLearned directly (G0 reused, not
// reimplemented).
func TestLoadTabs_MemoryReusesG0WalkLearnedVerbatim(t *testing.T) {
	root := t.TempDir()
	// ts chosen against the pinned tabsNow (14:10:00) so the buckets are
	// deterministic: 2m ago, 1h ago.
	writeObservation(t, root, "1-a", "cursor", "customer:5821", "prefers", "email",
		"2026-05-15T14:08:00Z") // 2m before tabsNow
	writeObservation(t, root, "1-b", "data-analyst", "customer:5821", "usage-trend", "contraction",
		"2026-05-15T13:10:00Z") // 1h before tabsNow

	tabs := loadTabs(root, "operator", tabsNow)
	// loadMemory == walkLearned VERBATIM (G0 reused, not reimplemented).
	direct, err := walkLearned(root, tabsNow)
	if err != nil {
		t.Fatalf("walkLearned: %v", err)
	}
	if !reflect.DeepEqual(tabs.Memory, direct) {
		t.Fatalf("loadMemory must equal walkLearned VERBATIM:\n got=%#v\nwant=%#v",
			tabs.Memory, direct)
	}
	if len(tabs.Memory) != 2 {
		t.Fatalf("want 2 observations, got %d: %#v", len(tabs.Memory), tabs.Memory)
	}
	// Sorted by (subject, ts, path) — same subject, so ts ascending:
	// 13:10 (1h) before 14:08 (2m).
	if tabs.Memory[0].Predicate != "usage-trend" || tabs.Memory[0].Ago != "1h" {
		t.Errorf("mem[0] = %#v, want usage-trend / 1h (deterministic injected now)", tabs.Memory[0])
	}
	if tabs.Memory[1].Predicate != "prefers" || tabs.Memory[1].Ago != "2m" {
		t.Errorf("mem[1] = %#v, want prefers / 2m", tabs.Memory[1])
	}
}

// TestLoadTabs_Deterministic: same disk → byte-identical tabState (golden
// stability — the live render is a pure fn of disk state + injected now).
func TestLoadTabs_Deterministic(t *testing.T) {
	root := t.TempDir()
	writeChannelMeta(t, root, "ch-1", "a", "b", "ent", "topic")
	writeSay(t, root, "ch-1", "m1", "a", "hi", "2026-05-15T14:00:00Z")
	writeGoalActive(t, root, "g-1", "a", "do the thing")
	writeObservation(t, root, "o-1", "a", "ent", "is", "ok", "2026-05-15T14:00:00Z")
	x := loadTabs(root, "operator", tabsNow)
	y := loadTabs(root, "operator", tabsNow)
	if !reflect.DeepEqual(x, y) {
		t.Fatalf("loadTabs not deterministic:\n x=%#v\n y=%#v", x, y)
	}
}

// TestLoadTabs_EmptySubstrate: a fresh project (nothing on disk) → empty
// slices, never a panic (the renderers handle empty — tabs.go:91).
func TestLoadTabs_EmptySubstrate(t *testing.T) {
	tabs := loadTabs(t.TempDir(), "operator", tabsNow)
	if len(tabs.Channels) != 0 || len(tabs.Goals) != 0 || len(tabs.Memory) != 0 {
		t.Fatalf("fresh project must yield empty tabs, got %#v", tabs)
	}
}

// ── lineage drill-down id carry ──────────────────────────────────────

// TestSubstrateRowIDs_ParallelToRows proves substrateRowIDs returns the
// @thought id of each event in order, so ids[i] is the id of substrate
// row i (the 1:1 row↔event contract projectThread guarantees).
func TestSubstrateRowIDs_ParallelToRows(t *testing.T) {
	root := t.TempDir()
	writeOutboxThought(t, root, "1747000000000-op0", "operator", "focus",
		"customer:5821", "investigate", "2026-05-15T14:02:09Z")
	writeOutboxThought(t, root, "1747000002000-d29", "claude-code", "decision",
		"customer:5821", "offer downgrade", "2026-05-15T14:02:46Z")

	rows, ids := loadSubstrateWithIDs(root, "operator", liveNow)
	if len(rows) != len(ids) {
		t.Fatalf("rows (%d) and ids (%d) must be parallel", len(rows), len(ids))
	}
	// Find the decision row; its id must be the real thought-id so the
	// drill-down can projectLineage it.
	var decIdx = -1
	for i := range rows {
		if rows[i].Role == roleDecision {
			decIdx = i
		}
	}
	if decIdx < 0 {
		t.Fatalf("no decision row projected: %#v", rows)
	}
	if ids[decIdx] != "1747000002000-d29" {
		t.Errorf("decision-row id = %q, want 1747000002000-d29 (the real "+
			"@thought id so projectLineage resolves)", ids[decIdx])
	}
}

// TestLiveLineageDrillDown_ViaProjectLineageVerbatim is the (d) self-
// check: `enter` on a LIVE decision row resolves the overlay via the G0
// projectLineage(root, <real thought-id>) VERBATIM (decision header +
// context bundle + numbered reasoning chain), and `esc` closes it.
func TestLiveLineageDrillDown_ViaProjectLineageVerbatim(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	root := t.TempDir()

	// A real decision with a @context-bundle + a @reason chain on disk
	// (the SAME fixture shape project_test.go's TestProjectLineage uses —
	// proving G0 projectLineage is reused, not reimplemented).
	decID := "1747000002000-d29"
	author := "claude-code"
	decRec := thought.BuildThoughtRecord(thought.ThoughtInput{
		ID: decID, Author: author, Type: "decision", Subject: "customer:5821",
		Content: "offer downgrade, not churn-save discount", Scope: "fleet",
		TS: "2026-05-15T14:02:46Z", TTL: 0,
	})
	bundleRec := thought.BuildContextBundle(decID, []string{"deadbeefcafe0001"})
	if err := thought.Write(root, author, decID, []gdl.Record{decRec, bundleRec}); err != nil {
		t.Fatalf("thought.Write decision: %v", err)
	}
	refDir := filepath.Join(root, ".rufio", "refs", "given")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatal(err)
	}
	refRec := gdl.Record{Type: "ref", Fields: []gdl.RecordField{
		{Key: "path", Value: "given/refund-policy.md"},
		{Key: "version", Value: "1"},
		{Key: "sha256", Value: "deadbeefcafe0001"},
		{Key: "stage", Value: "live"},
		{Key: "ts", Value: "2026-05-15T13:00:00Z"},
		{Key: "author", Value: "operator"},
	}}
	if err := os.WriteFile(filepath.Join(refDir, "refund-policy.md.gdl"),
		[]byte(gdl.RenderLine(refRec)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reasonDir := filepath.Join(root, "live", "reasoning", author, decID)
	if err := os.MkdirAll(reasonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeR := func(file, id, parent, content, ts string) {
		r := gdl.Record{Type: "reason", Fields: []gdl.RecordField{
			{Key: "id", Value: id}, {Key: "author", Value: author},
			{Key: "content", Value: content}, {Key: "ts", Value: ts},
			{Key: "parent", Value: parent}, {Key: "decision", Value: decID},
		}}
		if err := os.WriteFile(filepath.Join(reasonDir, file),
			[]byte(gdl.RenderLine(r)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeR("r1.gdl", "r1", "", "customer requested downgrade, not cancellation", "2026-05-15T14:02:40Z")
	writeR("r2.gdl", "r2", "r1", "policy: downgrade offers < $500 auto-approve", "2026-05-15T14:02:41Z")

	// Build the App against the REAL root (NewApp hydrates the live
	// substrate + the id carry from disk — NO injection, this exercises
	// the real read path end to end). The single decision row is the
	// freshest → default-selected.
	a, err := NewApp(root)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	m, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app := m.(App)
	if len(app.substrate) == 0 {
		t.Fatalf("live substrate empty — the decision @thought should project a row")
	}
	// Select the decision row explicitly (defensive — it is the only row).
	for i := range app.substrate {
		if app.substrate[i].Role == roleDecision {
			app.selected = i
		}
	}
	// G-interact: drop to NAV mode (the App starts in compose) so `enter`
	// opens the lineage drill-down rather than sending the composer.
	m, _ = app.Update(keyMsg("esc"))
	app = m.(App)
	// esc may have been consumed as the compose→nav toggle; re-select the
	// decision row defensively (esc does not move selection).
	for i := range app.substrate {
		if app.substrate[i].Role == roleDecision {
			app.selected = i
		}
	}
	// enter → the LIVE drill-down via projectLineage.
	m, _ = app.Update(keyMsg("enter"))
	app = m.(App)
	if app.overlay != overlayLineage {
		t.Fatalf("enter on a live decision row must open the lineage overlay "+
			"(via projectLineage); overlay=%q", app.overlay)
	}
	if app.lineage == nil {
		t.Fatalf("the live drill-down payload (a.lineage) must be set from projectLineage")
	}
	out := stripSGR(app.View())
	for _, want := range []string{
		"Decision: offer downgrade, not churn-save discount", // header
		"by claude-code",
		"Context bundle:",
		"given/refund-policy.md@v1 (sha: deadbeef)", // resolved bundle ref (G0 format)
		"Reasoning chain:",
		"customer requested downgrade, not cancellation", // chain step 1
		"policy: downgrade offers < $500 auto-approve",   // chain step 2
		"press esc to close",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("live lineage drill-down missing %q in:\n%s", want, out)
		}
	}
	// esc closes it.
	m, _ = app.Update(keyMsg("esc"))
	app = m.(App)
	if app.overlay != overlayNone {
		t.Errorf("esc must close the lineage drill-down; overlay=%q", app.overlay)
	}
}

// TestLiveLineage_ProjectGoIsByteUnchanged is a guard: this slice MUST
// reuse G0 projectLineage / walkLearned VERBATIM. (The byte-unchanged
// proof is the empty `git diff` on project*.go in the PR; this test
// additionally pins the contract that loadMemory delegates to walkLearned
// and the drill-down delegates to projectLineage, by asserting their
// outputs are identical to calling G0 directly — already covered above
// for memory; here we assert projectLineage's error propagates, mirroring
// G0's contract that the caller decides availability.)
func TestLiveLineage_MissingDecisionDegradesGracefully(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	// A decision row whose id has NO on-disk decision (projectLineage
	// errors) → the drill-down must NOT open (degrade, never crash/blank).
	a, _ := NewApp("/tmp/fake-root")
	m, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app := m.(App)
	app.composeMode = false // G-interact: nav mode so `enter` drills down
	app.substrate = []ThreadMsg{{Who: "claude-code", Role: roleDecision, Kind: kindPlan, Text: "x"}}
	app.substrateIDs = []string{"9-nonexistent"}
	app.selected = 0
	m, _ = app.Update(keyMsg("enter"))
	app = m.(App)
	if app.overlay == overlayLineage {
		t.Errorf("a decision id with no on-disk lineage must NOT open the " +
			"overlay (graceful degrade — projectLineage errored)")
	}
}

// ── watcher-fold exactly-once drain (the load-bearing property) ──────

// TestTabsLoadedMsg_FoldsWithoutRearmingWatcher proves the drain
// invariant for the tab seam: a pane Msg (ChannelMsg/GoalMsg/…) re-arms
// the watcher EXACTLY ONCE batched with the tab loader one-shot, and a
// tabsLoadedMsg folds the tab state but MUST NOT re-arm the watcher (it
// is produced by the one-shot loadTabsCmd / a test inject, NOT the drain
// — re-arming there would double-drain). EXACT mirror of the mesh
// precedent (TestMeshLoadedMsg_FoldsWithoutRearmingWatcher).
func TestTabsLoadedMsg_FoldsWithoutRearmingWatcher(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a, _ := NewApp("/tmp/fake-root")
	m, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app := m.(App)

	armed := 0
	app.watcherCmd = tea.Cmd(func() tea.Msg { armed++; return nil })

	// Each pane Msg → re-arm (exactly once) + a tab re-read one-shot.
	for _, paneMsg := range []tea.Msg{
		ChannelMsg{Channel: channels.Channel{ID: "c1"}},
		ChannelMessageMsg{ChannelID: "c1", Message: ChannelMessage{ID: "m1"}},
		GoalMsg{Goal: goal.Goal{ID: "g1", State: goal.StateActive}},
		InboxMsg{Overlap: InboxOverlap{TargetGoalID: "g1"}},
	} {
		_, cmd := app.Update(paneMsg)
		if cmd == nil {
			t.Fatalf("%T must return a re-arm cmd (drain must not stop)", paneMsg)
		}
		// The returned cmd is a Batch(watcherCmd, loadTabsCmd). Running it
		// must re-arm the watcher EXACTLY ONCE (never zero: stream stops;
		// never twice: racy double-drain). tea.Batch returns a BatchMsg
		// of the child cmds; we execute them and count watcher re-arms.
		before := armed
		runBatch(cmd)
		if armed != before+1 {
			t.Errorf("%T: watcher must re-arm EXACTLY ONCE, got %d (want %d)",
				paneMsg, armed-before, 1)
		}
	}

	// tabsLoadedMsg folds the tabs but MUST NOT re-arm the watcher.
	pinned := pinnedTabs()
	m2, cmd2 := app.Update(tabsLoadedMsg{tabs: pinned})
	app = m2.(App)
	if cmd2 != nil {
		t.Errorf("tabsLoadedMsg MUST NOT re-arm the watcher (drain-invariant: "+
			"double-re-arm) — got non-nil cmd %T", cmd2)
	}
	if !reflect.DeepEqual(app.tabs, pinned) {
		t.Errorf("tabsLoadedMsg must fold the projected tabs into a.tabs")
	}
}

// runBatch executes a (possibly tea.Batch) cmd and recursively runs every
// child cmd it yields, so a fake watcherCmd's side-effect counter
// reflects exactly how many times the drain was armed (the
// double-drain / no-drain detector). A nil cmd is a no-op.
func runBatch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	switch v := msg.(type) {
	case tea.BatchMsg:
		for _, c := range v {
			runBatch(c)
		}
	}
}

// ── the (a)(b)(c) self-check renders, Msg-injected (deterministic) ───

// TestLiveTabs_SelfCheckRenders is the controller-gate self-check for
// surfaces (a) channels, (b) goals, (c) memory — driven by the PINNED
// injected tabsLoadedMsg (NO live fsnotify / wall-clock). It asserts the
// live tab state renders the channel list + transcript, the goal cards +
// the structured overlap line, and the memory subject/predicate/object +
// author + ago.
func TestLiveTabs_SelfCheckRenders(t *testing.T) {
	styles.SetProfile(termenv.Ascii)

	// (a) channels: ≥1 channel + a say transcript.
	chans := []Channel{{
		ID: "ch-1747-x1", Opener: "claude-code", Target: "data-analyst",
		Topic: "customer:5821",
		Msgs: []ChannelSay{
			{By: "claude-code", Text: "14-day silence, mentioned cancel", Time: "14:03:01"},
			{By: "data-analyst", Text: "team usage 12→3 in 30d", Time: "14:03:20"},
		},
	}}
	// (b) goals: ≥2 incl. an overlap (formatted from the structured rec).
	goals := []GoalCard{
		{ID: "claude-code", Author: "claude-code", Statement: "resolve customer:5821 churn risk",
			State: "active", Time: "14:02:50", Overlap: "overlaps cursor — shared entity customer:5821"},
		{ID: "cursor", Author: "cursor", Statement: "improve customer:5821 onboarding",
			State: "active", Time: "14:02:55"},
	}
	// (c) memory: ≥2 learned/ observations.
	mem := []MemoryEntry{
		{Subject: "customer:5821", Predicate: "prefers", Object: "email", Author: "cursor", Ago: "2m"},
		{Subject: "customer:5821", Predicate: "usage-trend", Object: "contraction", Author: "data-analyst", Ago: "1h"},
	}
	live := tabState{Channels: chans, Goals: goals, Memory: mem}

	build := func(tabKey string) string {
		a, _ := NewApp("/tmp/fake-root")
		m, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		app := m.(App)
		m, _ = app.Update(tabsLoadedMsg{tabs: live})
		app = m.(App)
		m, _ = app.Update(keyMsg("esc")) // G-interact: compose → nav
		app = m.(App)
		m, _ = app.Update(keyMsg(tabKey))
		app = m.(App)
		return stripSGR(app.View())
	}

	chOut := build("3")
	for _, want := range []string{
		"◆ channels", "ch-1747-x1", "claude-code", "data-analyst",
		"14-day silence, mentioned cancel", "team usage 12→3 in 30d", "14:03:20",
	} {
		if !strings.Contains(chOut, want) {
			t.Errorf("(a) channels self-check missing %q in:\n%s", want, chOut)
		}
	}

	gOut := build("4")
	for _, want := range []string{
		"◆ goals", "[active]", "resolve customer:5821 churn risk",
		"improve customer:5821 onboarding", "claude-code", "cursor",
		"overlaps cursor — shared entity customer:5821", // the structured overlap line
	} {
		if !strings.Contains(gOut, want) {
			t.Errorf("(b) goals self-check missing %q in:\n%s", want, gOut)
		}
	}

	mOut := build("5")
	for _, want := range []string{
		"◆ memory", "customer:5821", "prefers", "email",
		"usage-trend", "contraction", "cursor", "data-analyst", "2m", "1h",
	} {
		if !strings.Contains(mOut, want) {
			t.Errorf("(c) memory self-check missing %q in:\n%s", want, mOut)
		}
	}
}

// TestLiveTabs_ColdStartEmptyRenders proves the empty cold-start tabs
// (NewApp on a fake root → nothing on disk) render the renderers' empty
// affordance, never a void/crash.
func TestLiveTabs_ColdStartEmptyRenders(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a, _ := NewApp("/tmp/fake-root")
	m, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app := m.(App)
	m, _ = app.Update(keyMsg("esc")) // G-interact: compose → nav
	app = m.(App)
	m, _ = app.Update(keyMsg("3")) // channels tab, empty
	if !strings.Contains(stripSGR(m.(App).View()), "(no channels)") {
		t.Errorf("empty channels cold-start must render the (no channels) affordance")
	}
}

// ── deterministic Msg-injected goldens for the 4 surfaces ────────────

// TestGoldenLiveTabsChannels/Goals/Memory are the deterministic
// Msg-injected per-tab goldens (DISTINCT from the SubstrateThread-gate
// goldens which use pinnedTabs == the fixture values): they pin an inline
// tabState via tabsLoadedMsg and snapshot the live render. Regenerate
// with TEATEST_UPDATE=1.
func TestGoldenLiveTabsChannels(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	app := injectTabs(t, 120, 40, goldenLiveTabs(), "3")
	goldenFromView(t, "tui-v8-live-channels.txt", app.View())
}

func TestGoldenLiveTabsGoals(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	app := injectTabs(t, 120, 40, goldenLiveTabs(), "4")
	goldenFromView(t, "tui-v8-live-goals.txt", app.View())
}

func TestGoldenLiveTabsMemory(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	app := injectTabs(t, 120, 40, goldenLiveTabs(), "5")
	goldenFromView(t, "tui-v8-live-memory.txt", app.View())
}

// goldenLiveTabs is the deterministic inline tab fixture the live-tab
// goldens pin (documented inline, regenerated via TEATEST_UPDATE=1 — the
// G1 pinnedThread() pattern, extended to G3).
func goldenLiveTabs() tabState {
	return tabState{
		Channels: []Channel{{
			ID: "ch-1747-x1", Opener: "claude-code", Target: "data-analyst",
			Topic: "customer:5821",
			Msgs: []ChannelSay{
				{By: "claude-code", Text: "14-day silence, mentioned cancel", Time: "14:03:01"},
				{By: "data-analyst", Text: "team usage 12→3 in 30d. contraction, not churn", Time: "14:03:20"},
				{By: "claude-code", Text: "got it — proposing downgrade", Time: "14:03:34"},
			},
		}},
		Goals: []GoalCard{
			{ID: "claude-code", Author: "claude-code",
				Statement: "resolve customer:5821 churn risk",
				State:     "active", Time: "14:02:50",
				Overlap: "overlaps cursor — shared entity customer:5821"},
			{ID: "cursor", Author: "cursor",
				Statement: "improve customer:5821 onboarding re-engagement",
				State:     "active", Time: "14:02:55"},
		},
		Memory: []MemoryEntry{
			{Subject: "customer:5821", Predicate: "prefers", Object: "email", Author: "cursor", Ago: "2m"},
			{Subject: "customer:5821", Predicate: "usage-trend", Object: "contraction", Author: "data-analyst", Ago: "1m"},
			{Subject: "customer:5821", Predicate: "tier", Object: "standard", Author: "initial-import", Ago: "2h"},
		},
	}
}

// injectTabs builds a windowed App, injects the pinned substrate (the
// SubstrateThread gate fixture, so the substrate/mesh chrome is the
// eyeballed canonical render) AND a pinned tabState, then applies keys —
// the deterministic G1/G2/G3 seam (NO live fsnotify / wall-clock).
func injectTabs(t *testing.T, w, h int, tabs tabState, keys ...string) App {
	t.Helper()
	a, err := NewApp("/tmp/fake-root")
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	m, _ := a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	app := m.(App)
	m, _ = app.Update(substrateLoadedMsg{rows: SubstrateThread})
	app = m.(App)
	m, _ = app.Update(meshLoadedMsg{mesh: pinnedMesh()})
	app = m.(App)
	m, _ = app.Update(tabsLoadedMsg{tabs: tabs})
	app = m.(App)
	// G-interact: the App starts in COMPOSE mode (the composer's primary
	// affordance). These tab self-check tests press nav keys (3/4/5/enter)
	// — drop to NAV mode once (the documented compose→nav `esc` toggle)
	// so the nav keymap behaves exactly as pre-G-interact.
	m, _ = app.Update(keyMsg("esc"))
	app = m.(App)
	for _, k := range keys {
		m, _ = app.Update(keyMsg(k))
		app = m.(App)
	}
	return app
}
