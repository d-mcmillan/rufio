// live_app_test.go — PR-G1: the App live-substrate lifecycle + the
// three state-aware cold-start states, all driven by PINNED injected
// Msgs (substrateLoadedMsg / DaemonOnlineMsg / the watcher Msgs). NO
// live fsnotify, NO wall-clock, NO real time.Now() — the locked
// determinism contract. These are the headless proof of the three
// states the controller eyeballs before damon's live gate.
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/lib/autopromote"
	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// pinnedThread is the deterministic substrate fixture injected into the
// state tests + the Msg-injected golden: a compact customer:5821 arc
// (operator opens → claude-code hypothesises → cursor observes →
// claude-code decides with a 2-confirmer quorum). It is shaped EXACTLY
// like projectThread's output (Quorum.Yes set, Total left 0 — the OPEN-2
// resolution is asserted to be applied by the render layer). Distinct
// from SubstrateThread (the gate fixture) so the golden documents the
// injected data inline.
func pinnedThread() []ThreadMsg {
	return []ThreadMsg{
		{Who: "operator", Role: "focus", Time: "14:02:09", Kind: kindOp,
			Text: "investigate customer:5821 churn risk"},
		{Who: "claude-code", Role: "hypothesis", Time: "14:02:11", Kind: kindPlan,
			Text: "14-day silence, customer mentioned cancel — churn signals"},
		{Who: "cursor", Role: "observation", Time: "14:02:12", Kind: kindReply,
			Text: "customer:5821 prefers email contact"},
		{Who: "claude-code", Role: roleDecision, Time: "14:02:46", Kind: kindPlan,
			Text:   "decision: offer downgrade, not churn-save discount",
			Quorum: &Quorum{Yes: []string{"cursor", "data-analyst"}, Total: 0},
			Last:   true},
	}
}

// inject pushes a substrateLoadedMsg with rows + a DaemonOnlineMsg with
// online into a windowed App and returns it (the deterministic seam —
// this is exactly what the live binary's loadSubstrateCmd / poll feed,
// but pinned). selected mirrors the freshest-row default.
func inject(t *testing.T, w, h int, rows []ThreadMsg, online bool) App {
	t.Helper()
	styles.SetProfile(termenv.Ascii)
	a, err := NewApp("/tmp/fake-root")
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	m, _ := a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	app := m.(App)
	m, _ = app.Update(substrateLoadedMsg{rows: rows})
	app = m.(App)
	// PR-G2: the mesh is live too — inject the pinned 4-node arc mesh
	// (operator hub + claude-code/cursor/data-analyst, the same agents
	// pinnedThread() references) so the populated/offline state renders
	// the real mesh, not the operator-only cold-start. NO live fsnotify /
	// wall-clock — the G1 determinism pattern, extended to the mesh.
	m, _ = app.Update(meshLoadedMsg{mesh: pinnedMesh()})
	app = m.(App)
	m, _ = app.Update(DaemonOnlineMsg{Online: online})
	app = m.(App)
	app.selected = lastRowIndex(app.substrate)
	return app
}

// TestState_Populated is cold-start state (a): live thread + a decision
// with a confirm tally → the chat shows the projected thread, the
// decision row shows the OPEN-2 dot row (Total = the auto-promote
// constant), and the v8 frame (panels/borders/composer/mesh-rail) is
// intact.
func TestState_Populated(t *testing.T) {
	app := inject(t, 120, 40, pinnedThread(), true)
	out := app.View()

	for _, want := range []string{
		"◆ #substrate",                                   // chrome strip
		"FOCUS", "HYPOTHESIS", "OBSERVATION", "DECISION", // live thread roles (op row = focus type → FOCUS tag)
		"investigate customer:5821 churn risk",
		"›", "⏎ send", // composer + hint (real, kept)
		"◆ MESH",  // mesh rail (fixture, G2)
		"ROUTING", // routing strip
	} {
		if !strings.Contains(out, want) {
			t.Errorf("populated state missing %q in:\n%s", want, out)
		}
	}
	// OPEN-2: the decision row dot counter is `2/<constant>` — assert
	// the denominator is autopromote.MinDistinctConfirmers, NOT a
	// literal. ●● = cursor+data-analyst voted, ○ = the third slot.
	wantDots := "2/" + itoa(autopromote.MinDistinctConfirmers)
	if !strings.Contains(out, wantDots) {
		t.Errorf("populated state missing OPEN-2 quorum %q (Total must be the "+
			"auto-promote constant) in:\n%s", wantDots, out)
	}
	// Two panels intact (chat + mesh rail).
	_, spans := detectPanels(t, out)
	if len(spans) != 2 {
		t.Errorf("populated state: want 2 panels, got %d", len(spans))
	}
	// NO fake typing line in the live path (OPEN-4 locked).
	if strings.Contains(out, "typing ···") || strings.Contains(out, "data-analyst typing") {
		t.Errorf("live substrate path must NOT render a fake typing indicator:\n%s", out)
	}
	// "N minds" is the REAL distinct-author count (operator/claude-code/
	// cursor = 3 distinct authors in pinnedThread).
	if !strings.Contains(out, "3 minds") {
		t.Errorf("populated state chrome must show real distinct-author count `3 minds`:\n%s", out)
	}
	// Online → NO daemon-offline whisper.
	if strings.Contains(out, substrateOfflineNote) {
		t.Errorf("daemon online → must NOT show %q:\n%s", substrateOfflineNote, out)
	}
}

// TestState_DaemonOfflineWithHistory is cold-start state (b): SAME data,
// daemon DOWN → history still renders fully + the quiet offline whisper
// is present + NO modal block (the console stays usable; LOCKED
// 2026-05-16 never gated on the daemon).
func TestState_DaemonOfflineWithHistory(t *testing.T) {
	app := inject(t, 120, 40, pinnedThread(), false)
	out := app.View()

	// History still renders normally.
	for _, want := range []string{"FOCUS", "HYPOTHESIS", "DECISION", "investigate customer:5821"} {
		if !strings.Contains(out, want) {
			t.Errorf("daemon-offline state must STILL render history (%q) — the "+
				"console is never gated on the daemon. Missing in:\n%s", want, out)
		}
	}
	// The quiet offline whisper is present.
	if !strings.Contains(out, substrateOfflineNote) {
		t.Errorf("daemon-offline state missing the quiet `%s` whisper in:\n%s",
			substrateOfflineNote, out)
	}
	// No modal block: the two panels + composer are intact (a modal
	// would replace the body).
	_, spans := detectPanels(t, out)
	if len(spans) != 2 {
		t.Errorf("daemon-offline must NOT be a modal block — want 2 intact panels, got %d", len(spans))
	}
	if !strings.Contains(out, "⏎ send") {
		t.Errorf("daemon-offline: composer must still render (console usable):\n%s", out)
	}
}

// TestBundleF_OfflineSuppressesLyingIndicators is the v1.0.6.3 regression
// guard. When the daemon is offline the TUI MUST NOT render the four
// "live"-implying indicators (F1-F4) — they contradict the truthful
// `· daemon offline ·` whisper. Asserts the negative-space contract:
// none of the lying surface markers (the header `syncing` label, the
// substrate chrome ` live` badge, the substrate `N/s` rate, the
// pre-PR-F sparkline frame, the mesh-panel ` live` badge) appear in
// the rendered output when daemonOnline is false.
//
// Companion: TestBundleF_OnlineRestoresIndicators asserts the SAME
// markers ARE present when the daemon is online — proving the gating
// flips both ways (no silent permanent removal).
func TestBundleF_OfflineSuppressesLyingIndicators(t *testing.T) {
	app := inject(t, 120, 40, pinnedThread(), false)
	out := app.View()

	// F1 — top-left `syncing` label.
	if strings.Contains(out, "syncing") {
		t.Errorf("F1: daemon-offline must NOT render `syncing` indicator in top header:\n%s", out)
	}
	// F2/F3 — `live` badges in the substrate chrome (` ◜ live`) AND the
	// mesh-panel header (` ⠁ live`). With daemon offline both right-side
	// segments collapse, so the badge text MUST NOT appear in any panel
	// header. Each badge is followed by trailing spaces and a panel
	// border `│`, so `live ` followed (eventually) by `│` is the
	// badge-specific anchor — distinct from the F5 hint string
	// `for live updates` (no border on the hint row).
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " live") && strings.Contains(line, "│") {
			t.Errorf("F2/F3: daemon-offline must NOT render the `live` badge in a panel header — found in line:\n%s\n\nFull output:\n%s", line, out)
			break
		}
	}
	// F4 — substrate `N/s` rate. The series rate frame-0 is "3/s"; any
	// `\d+/s` token is a bug here.
	for _, rate := range []string{"0/s", "1/s", "2/s", "3/s", "4/s", "5/s", "6/s", "7/s", "8/s"} {
		if strings.Contains(out, rate) {
			t.Errorf("F4: daemon-offline must NOT render fictional rate %q:\n%s", rate, out)
		}
	}
	// F4 — pre-PR-F sparkline frame ▁▂▃▄▅▆▇█▆▅ MUST be suppressed.
	if strings.Contains(out, sparklineFrame0) {
		t.Errorf("F4: daemon-offline must NOT render the sparkline frame %q:\n%s",
			sparklineFrame0, out)
	}
	// F5 — the new bottom hint MUST be present so the offline state is
	// taught, not just hidden.
	if !strings.Contains(out, "daemon offline — run `rufio dev` for live updates") {
		t.Errorf("F5: daemon-offline must render the bottom teaching hint in:\n%s", out)
	}
}

// TestBundleF_OnlineRestoresIndicators is the positive-space companion
// to TestBundleF_OfflineSuppressesLyingIndicators. When the daemon IS
// online the F1 syncing label, F2/F3 `live` badges, AND the F4
// sparkline + `N/s` rate MUST render, and the F5 teaching hint MUST
// be absent — proves the gating flips both ways.
//
// v1.0.6.3 (Bundle F): the F4 sparkline + `N/s` rate are now wired to
// real substrate event rate (events_per_sec sampled at 2 Hz from
// ThoughtMsg / ConfirmMsg / AttentionMsg arrivals). At zero injected
// events the seeded series renders its initial frame-0 window (the
// initial `▁▂▃▄▅▆▇█▆▅` + rate `3`) — that proves both indicators are
// drawn; the actual values shift to observed rates once events flow.
func TestBundleF_OnlineRestoresIndicators(t *testing.T) {
	app := inject(t, 120, 40, pinnedThread(), true)
	out := app.View()

	// F1 — `syncing` label IS present.
	if !strings.Contains(out, "syncing") {
		t.Errorf("F1: daemon-online MUST render the `syncing` indicator in:\n%s", out)
	}
	// F2/F3 — the ` live` badge is present in panel headers. We look
	// for ` live` co-located with a panel border `│` on the SAME line
	// (the badge sits at the right edge of the chat-chrome / mesh-
	// header strip, just inside the border). Animation-frame-agnostic.
	foundLiveBadge := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " live") && strings.Contains(line, "│") {
			foundLiveBadge = true
			break
		}
	}
	if !foundLiveBadge {
		t.Errorf("F2/F3: daemon-online MUST render the `live` badge in a panel header:\n%s", out)
	}
	// F4 — sparkline glyphs + `N/s` rate render the seeded frame-0 ring
	// at startup (before any events have flowed). The seeded window is
	// `▁▂▃▄▅▆▇█▆▅` and the seeded rate sample is `3`.
	if !strings.Contains(out, sparklineFrame0) {
		t.Errorf("F4: daemon-online MUST render the sparkline frame %q in:\n%s",
			sparklineFrame0, out)
	}
	if !strings.Contains(out, "3/s") {
		t.Errorf("F4: daemon-online MUST render the seeded frame-0 rate `3/s` in:\n%s", out)
	}
	// F5 — the bottom teaching hint MUST be ABSENT when the daemon is up
	// (it is a teaching moment for the offline-only case; never a
	// permanent footer affordance).
	if strings.Contains(out, "daemon offline — run `rufio dev` for live updates") {
		t.Errorf("F5: daemon-online must NOT render the offline teaching hint:\n%s", out)
	}
}

// TestState_FreshEmpty is cold-start state (c): NO thoughts at all → the
// normal v8 frame (panels/borders/chrome/composer intact) with a single
// quiet centered setup hint — NOT a blank void, NOT a crash, NOT a modal.
func TestState_FreshEmpty(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	app := driveColdStart(t, 120, 40) // NO substrate injected → empty
	out := app.View()

	if app.sawThought {
		t.Fatalf("fresh/empty: sawThought must be false")
	}
	// The single quiet setup hint is shown.
	if !strings.Contains(out, substrateEmptyHint) {
		t.Errorf("fresh/empty must show the quiet setup hint %q in:\n%s",
			substrateEmptyHint, out)
	}
	// The normal v8 frame is intact (NOT a void / crash): two panels,
	// chrome strip, composer.
	_, spans := detectPanels(t, out)
	if len(spans) != 2 {
		t.Errorf("fresh/empty must render the normal v8 frame — want 2 panels, got %d", len(spans))
	}
	for _, want := range []string{"◆ #substrate", "›", "⏎ send", "◆ MESH"} {
		if !strings.Contains(out, want) {
			t.Errorf("fresh/empty frame missing %q (must be the normal frame, not a void):\n%s", want, out)
		}
	}
	// 0 minds (no authors yet) — driven by real data, not a literal.
	if !strings.Contains(out, "0 minds") {
		t.Errorf("fresh/empty chrome must show `0 minds`:\n%s", out)
	}
	// No fixture thread leaked into the empty render.
	if strings.Contains(out, "investigate customer:5821 churn risk") {
		t.Errorf("fresh/empty must NOT render any fixture thread content:\n%s", out)
	}
}

// TestState_LiveUpdateFold proves a live ThoughtMsg/ConfirmMsg re-arms
// the watcher drain (the load-bearing correctness property) and that a
// substrateLoadedMsg folds new rows + flips sawThought without re-arming
// the watcher (the drain-invariant exception).
func TestState_LiveUpdateFold(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a, _ := NewApp("/tmp/fake-root")
	m, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app := m.(App)

	// Wire a fake watcher drain so the re-arm is observable.
	armed := 0
	fakeCmd := func() tea.Msg { armed++; return nil }
	app.watcherCmd = tea.Cmd(fakeCmd)

	// A ThoughtMsg must return a cmd that includes the watcher re-arm
	// (exactly once) batched with the substrate re-read.
	_, cmd := app.Update(ThoughtMsg{Agent: "claude-code"})
	if cmd == nil {
		t.Fatalf("ThoughtMsg must return a re-arm cmd (drain must not stop)")
	}
	// A ConfirmMsg likewise re-arms.
	_, cmd2 := app.Update(ConfirmMsg{ThoughtID: "1-d"})
	if cmd2 == nil {
		t.Fatalf("ConfirmMsg must return a re-arm cmd")
	}

	// substrateLoadedMsg folds rows but MUST NOT re-arm the watcher
	// (it is produced by the one-shot loadSubstrateCmd / a test inject,
	// not the drain — re-arming here would double-drain).
	before := app.sawThought
	m2, cmd3 := app.Update(substrateLoadedMsg{rows: pinnedThread()})
	app = m2.(App)
	if cmd3 != nil {
		t.Errorf("substrateLoadedMsg MUST NOT re-arm the watcher (drain-"+
			"invariant: double-re-arm) — got non-nil cmd %T", cmd3)
	}
	if before {
		t.Fatalf("setup: sawThought should start false")
	}
	if !app.sawThought {
		t.Errorf("substrateLoadedMsg with rows must set sawThought=true")
	}
	if len(app.substrate) != len(pinnedThread()) {
		t.Errorf("substrateLoadedMsg must fold the rows, got %d", len(app.substrate))
	}

	// WatcherClosedMsg drops the cmd (no infinite closed-channel read).
	m3, _ := app.Update(WatcherClosedMsg{})
	app = m3.(App)
	if app.watcherCmd != nil {
		t.Errorf("WatcherClosedMsg must drop watcherCmd, got non-nil")
	}
}

// TestState_QuitStopsWatcher proves the quit path calls watcherStop
// BEFORE tea.Quit (no goroutine leak past program exit).
func TestState_QuitStopsWatcher(t *testing.T) {
	a, _ := NewApp("/tmp/fake-root")
	stopped := false
	a.watcherStop = func() { stopped = true }
	// ctrl+c is the universal quit (every mode — the G-interact QUIT
	// CONTRACT); it must still tear the watcher down BEFORE tea.Quit.
	_, cmd := a.Update(keyMsg("ctrl+c"))
	if !stopped {
		t.Errorf("ctrl+c must call watcherStop() before tea.Quit (goroutine leak)")
	}
	if cmd == nil {
		t.Fatalf("ctrl+c must still return tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c must produce tea.QuitMsg, got %T", cmd())
	}
	// And `q` in NAV mode also tears down + quits (the normal nav quit).
	a2, _ := NewApp("/tmp/fake-root")
	stopped2 := false
	a2.watcherStop = func() { stopped2 = true }
	m, _ := a2.Update(keyMsg("esc")) // compose → nav
	_, cmd2 := m.(App).Update(keyMsg("q"))
	if !stopped2 {
		t.Errorf("`q` in NAV mode must call watcherStop() before tea.Quit")
	}
	if cmd2 == nil {
		t.Fatalf("`q` in NAV mode must return tea.Quit")
	}
}

// TestGoldenLiveSubstrateInjected is a deterministic Msg-injected
// substrate golden (DISTINCT from TestAppGoldenSubstrate, which uses the
// SubstrateThread gate fixture): it pins pinnedThread() + daemon-online
// via injected Msgs and snapshots the live render. The injected fixture
// is documented inline in pinnedThread(); regenerate with
// TEATEST_UPDATE=1. This is the spec's "Msg-injected fixed-data render".
func TestGoldenLiveSubstrateInjected(t *testing.T) {
	app := inject(t, 120, 40, pinnedThread(), true)
	goldenFromView(t, "tui-v8-live-substrate.txt", app.View())
}

// TestGoldenLiveSubstrateColdStart is the deterministic fresh/empty
// cold-start golden (the normal v8 frame + the single setup hint).
func TestGoldenLiveSubstrateColdStart(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	app := driveColdStart(t, 120, 40)
	goldenFromView(t, "tui-v8-cold-start.txt", app.View())
}

// liveThreadNoQuorum is a LIVE-shaped substrate (a focus + a hypothesis
// + a decision that carries NO Quorum — the history-on-disk / daemon-
// offline / pre-confirm shape projectThread+loadSubstrate produce, see
// TestLoadSubstrate_HistoryNoConfirms). It deliberately contains a
// roleDecision row WITHOUT a *Quorum so the live `routingQuorum` loop
// finds NO decision-with-quorum — exactly the "no decision-with-quorum
// yet" case V8G2-M2 covers. Distinct from pinnedThread() (whose
// decision DOES carry a Quorum) and from SubstrateThread (the gate
// fixture, whose decision carries a 2/3 Quorum).
func liveThreadNoQuorum() []ThreadMsg {
	return []ThreadMsg{
		{Who: "operator", Role: "focus", Time: "14:02:09", Kind: kindOp,
			Text: "investigate customer:5821 churn risk"},
		{Who: "claude-code", Role: "hypothesis", Time: "14:02:11", Kind: kindPlan,
			Text: "14-day silence, customer mentioned cancel — churn signals"},
		{Who: "claude-code", Role: roleDecision, Time: "14:02:46", Kind: kindPlan,
			Text: "decision: offer downgrade, not churn-save discount",
			Last: true}, // NO Quorum (no confirms yet) — the V8G2-M2 case
	}
}

// TestRoutingQuorum_NoDecisionYet_DoesNotReadFixture is the V8G2-M2
// regression guard: when the LIVE substrate carries no decision-with-
// quorum yet, routingQuorum must NOT reach into the SubstrateThread
// gate fixture for a fake `X/Y` — it must render the live empty-state
// `—` (the function's own already-coded no-data token, matching the
// live path's "degrade, never substitute fixture data" convention used
// elsewhere, e.g. the projectLineage/contextualVote degrade paths and
// the mesh header's live zero counts). Pure-unit on routingQuorum +
// the two render entry points (empty cold-start AND a non-empty live
// thread whose decision has no Quorum).
func TestRoutingQuorum_NoDecisionYet_DoesNotReadFixture(t *testing.T) {
	// SubstrateThread (the gate fixture) carries a decision Quorum of
	// 2/3 — the exact fake the retired fallback used to surface.
	const fixtureFake = "2/3"

	// (a) pure unit: empty live substrate → `—`, NOT the fixture's 2/3.
	if got := routingQuorum(nil); got != "—" {
		t.Errorf("routingQuorum(nil) = %q, want %q (live empty-state, "+
			"must NOT read the SubstrateThread gate fixture)", got, "—")
	}
	// (b) pure unit: a live thread WITH a decision but NO Quorum → `—`.
	if got := routingQuorum(liveThreadNoQuorum()); got != "—" {
		t.Errorf("routingQuorum(no-quorum live thread) = %q, want %q "+
			"(no decision-with-quorum yet → live empty-state, NOT the "+
			"SubstrateThread fixture's %s)", got, "—", fixtureFake)
	}

	// (c) render-level: the empty cold-start ROUTING strip must show
	// `quorum —`, never the fixture-derived `quorum 2/3`.
	styles.SetProfile(termenv.Ascii)
	coldOut := stripSGR(driveColdStart(t, 120, 40).View())
	if strings.Contains(coldOut, "quorum "+fixtureFake) {
		t.Errorf("cold-start ROUTING strip leaked the fixture-derived "+
			"`quorum %s` (V8G2-M2 fallback not retired):\n%s",
			fixtureFake, coldOut)
	}
	if !strings.Contains(coldOut, "quorum —") {
		t.Errorf("cold-start ROUTING strip must render the live empty-"+
			"state `quorum —`:\n%s", coldOut)
	}

	// (d) render-level: a non-empty LIVE thread whose decision has no
	// Quorum must ALSO render `quorum —`, not the fixture's 2/3.
	app := inject(t, 120, 40, liveThreadNoQuorum(), true)
	liveOut := stripSGR(app.View())
	if strings.Contains(liveOut, "quorum "+fixtureFake) {
		t.Errorf("live no-quorum thread leaked the fixture-derived "+
			"`quorum %s`:\n%s", fixtureFake, liveOut)
	}
	if !strings.Contains(liveOut, "quorum —") {
		t.Errorf("live no-quorum thread ROUTING strip must render "+
			"`quorum —`:\n%s", liveOut)
	}
}
