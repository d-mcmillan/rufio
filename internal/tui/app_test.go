// app_test.go — unit + golden tests for the v8 App shell.
//
// RE-SCOPE (2026-05-15, PR-D): asserts the Rufio-domain nav
// (`substrate · fleet · channels · goals · memory`), the deleted footer
// attribution, the dropped `rules`/`agents`/`stream` labels, per-tab
// fixture content, substrate row selection + the lineage drill-down, the
// help overlay, and the (preserved) PR-C border-integrity invariants for
// every tab + overlay. Content assertions run under the Ascii termenv
// profile; one TrueColor test asserts a 24-bit escape.
//
// Golden bootstrap convention matches golden_test.go / chat_test.go:
//
//	TEATEST_UPDATE=1 go test ./internal/tui/... -run TestApp.*Golden
//
// then commit the test/golden/tui-v8-*.txt files.
package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// drive applies a window size then the given key strings to a fresh App,
// returning the final App. Single-rune strings are sent as KeyRunes;
// named keys ("tab", "shift+tab", "up", "down", "enter", "esc") are sent
// as their tea.KeyType so the Update dispatcher sees the right m.String().
//
// PR-G1: the substrate chat is now LIVE — NewApp("/tmp/fake-root")
// hydrates an EMPTY substrate (the fake root has nothing on disk), which
// is the fresh/empty cold-start, not the populated thread the structural
// gates + goldens verify. Per the locked determinism contract ("ALL
// tests inject deterministic substrate state via App.Update(<pinned
// Msg>) — NEVER live fsnotify / wall-clock"), drive() injects the PINNED
// SubstrateThread fixture via a substrateLoadedMsg BEFORE the keys. This
// is the single documented injected fixture for the gates + the
// substrate golden (fixtures.go is byte-unchanged — SubstrateThread now
// doubles as the deterministic gate/golden injection data). Cold-start
// tests use driveColdStart (no injection).
// drive applies a window size + the pinned SubstrateThread injection,
// then drops to NAV mode (G-interact: the App now starts in COMPOSE
// mode — the composer's primary affordance — so nav keys require nav
// mode per the documented modal model; `esc` toggles compose→nav). Every
// test using drive() exercises NAV behaviour (1-5/tab/jk/enter→lineage/
// esc/?/quit), so the harness enters nav once up-front — the documented
// regression contract ("nav mode still works"). Compose-mode behaviour
// is covered by the dedicated driveCompose/G-interact tests.
func drive(t *testing.T, w, h int, keys ...string) App {
	t.Helper()
	return driveInjectedMode(t, w, h, SubstrateThread, false, keys...)
}

// driveCompose is drive() but STAYS in compose mode (the App's default)
// — used by the G-interact composer/Enter-routing tests so keystrokes go
// to the live buffer.
func driveCompose(t *testing.T, w, h int, keys ...string) App {
	t.Helper()
	return driveInjectedMode(t, w, h, SubstrateThread, true, keys...)
}

// driveColdStart is drive() WITHOUT the substrate injection: the App
// renders the fresh/empty cold-start (sawThought == false) — used by the
// PR-G1 cold-start state tests.
func driveColdStart(t *testing.T, w, h int, keys ...string) App {
	t.Helper()
	return driveInjected(t, w, h, nil, keys...)
}

// pinnedMesh is the deterministic mesh gate fixture (PR-G2: the mesh is
// LIVE now — NewApp on the fake root hydrates an OPERATOR-ONLY mesh, not
// the 4-node arc the structural gates + the substrate/fleet goldens
// verify). Per the locked determinism contract ("ALL tests inject
// deterministic mesh state via App.Update(<pinned Msg>) — NO live
// fsnotify / wall-clock"), it is the EXACT meshState loadMesh would
// produce for the canonical customer:5821 arc: the synthesized operator
// hub (◉, central) + the three attention-bearing agents (claude-code /
// cursor / data-analyst) at the same 9×36 cells the PR-D fixture pinned
// (MeshNodes) so the gates + goldens stay byte-stable across the
// fixture→live swap, and the same 5 derived edges (operator↔each agent +
// the two claude-code→confirmer quorum links — deriveMeshEdges's index
// pairs). It mirrors SubstrateThread's role as the chat gate fixture.
func pinnedMesh() meshState {
	nodes := make([]MeshNode, len(MeshNodes))
	copy(nodes, MeshNodes)
	edges := deriveMeshEdges()
	return meshState{Nodes: nodes, Edges: edges}
}

// pinnedTabs is the deterministic channels/goals/memory tab gate fixture
// (PR-G3: the tabs are LIVE now — NewApp on the fake root hydrates EMPTY
// tabs, not the customer:5821 arc the structural gates + the channels/
// goals/memory goldens verify). Per the locked determinism contract
// ("ALL tests inject deterministic tab state via App.Update(<pinned
// Msg>) — NO live fsnotify / wall-clock"), it is the EXACT tabState
// loadTabs would produce for the canonical arc. The fixtures.go values
// (ChannelThreads / GoalCards / MemoryEntries) ARE that canonical arc and
// are field-identical to projectChannels/projectGoals/loadMemory output
// (the projection produces these exact structs), so reusing them keeps
// the gates + goldens byte-stable across the fixture→live swap. It
// mirrors pinnedMesh's role for the mesh and SubstrateThread's role for
// the chat. The memory Ago column is the fixture's pinned relative-time
// (no time.Now — the determinism contract; loadMemory's `now` is the
// production clock, but the gate injects this fixed set instead).
func pinnedTabs() tabState {
	chans := make([]Channel, len(ChannelThreads))
	copy(chans, ChannelThreads)
	goals := make([]GoalCard, len(GoalCards))
	copy(goals, GoalCards)
	mem := make([]MemoryEntry, len(MemoryEntries))
	copy(mem, MemoryEntries)
	return tabState{Channels: chans, Goals: goals, Memory: mem}
}

// driveInjected is driveInjectedMode in NAV mode (the pre-G-interact
// default — every legacy nav/golden/gate test exercises nav behaviour;
// G-interact moved the App's startup mode to compose, so the nav harness
// drops to nav once up-front, the documented regression contract).
func driveInjected(t *testing.T, w, h int, rows []ThreadMsg, keys ...string) App {
	t.Helper()
	return driveInjectedMode(t, w, h, rows, false, keys...)
}

// driveInjectedMode is the shared driver: fresh App → window size → (if
// rows != nil) inject a pinned substrateLoadedMsg AND a pinned
// meshLoadedMsg/tabsLoadedMsg (the deterministic, fsnotify-free seams) →
// (if !compose) drop to NAV mode → keys. rows == nil leaves the App at
// its empty cold-start. compose==true keeps the App's default COMPOSE
// mode so keystrokes reach the live composer buffer (the G-interact
// tests). The nav-mode drop is a single `esc` BEFORE the test keys so
// the legacy 1-5/tab/jk/enter/?/quit assertions are unchanged.
func driveInjectedMode(t *testing.T, w, h int, rows []ThreadMsg, compose bool, keys ...string) App {
	t.Helper()
	a, err := NewApp("/tmp/fake-root")
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	m, _ := a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	app := m.(App)
	if rows != nil {
		m, _ = app.Update(substrateLoadedMsg{rows: rows})
		app = m.(App)
		// PR-G2: the "populated gate" path also injects the pinned mesh
		// so the structural gates + the substrate/fleet goldens see the
		// 4-node arc (not the operator-only cold-start mesh). Cold-start
		// drivers pass rows == nil and skip BOTH injections (the
		// operator-only mesh + the empty thread are the cold-start state).
		m, _ = app.Update(meshLoadedMsg{mesh: pinnedMesh()})
		app = m.(App)
		// PR-G3: the "populated gate" path also injects the pinned tab
		// state so the structural gates + the channels/goals/memory
		// goldens see the canonical customer:5821 arc (not the empty
		// cold-start tabs NewApp hydrates from the fake root). Cold-start
		// drivers pass rows == nil and skip ALL injections (empty thread
		// + operator-only mesh + empty tabs are the cold-start state).
		// EXACT mirror of the pinnedMesh injection above.
		m, _ = app.Update(tabsLoadedMsg{tabs: pinnedTabs()})
		app = m.(App)
		// Mirror NewApp's default selection (freshest row) so the
		// selection-marker / drill-down tests behave as before the live
		// cutover (substrateLoadedMsg only re-clamps, it doesn't reset
		// to last — the constructor did that off the disk load).
		app.selected = lastRowIndex(app.substrate)
	}
	if !compose {
		// G-interact: the App starts in COMPOSE mode (the composer's
		// primary affordance). The legacy nav/golden/gate tests assert
		// NAV behaviour, so drop to nav once (the documented compose→nav
		// `esc` toggle) BEFORE the test keys — nav keymap then behaves
		// exactly as pre-G-interact.
		m, _ = app.Update(keyMsg("esc"))
		app = m.(App)
	}
	for _, k := range keys {
		m, _ = app.Update(keyMsg(k))
		app = m.(App)
	}
	return app
}

// keyMsg maps a test key string to the tea.KeyMsg the runtime would
// deliver (so m.String() matches app.go's switch).
func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

// TestNewApp asserts the constructor succeeds, defaults to substrate,
// and (PR-G1) hydrates an EMPTY live substrate for a root with nothing
// on disk — the fresh/empty cold-start: substrate empty, sawThought
// false, selected clamped to 0. The populated-thread behaviour is
// covered via the injected substrateLoadedMsg in the gates/goldens.
func TestNewApp(t *testing.T) {
	a, err := NewApp("/some/root")
	if err != nil {
		t.Fatalf("NewApp returned error: %v", err)
	}
	if a.view != viewSubstrate {
		t.Errorf("default view = %q, want %q", a.view, viewSubstrate)
	}
	if a.root != "/some/root" {
		t.Errorf("root = %q, want /some/root", a.root)
	}
	// PR-G1: a nonexistent root has no substrate on disk → empty thread,
	// the fresh/empty cold-start. selected clamps to 0; sawThought false.
	if len(a.substrate) != 0 {
		t.Errorf("fresh root → substrate len = %d, want 0 (empty cold-start)", len(a.substrate))
	}
	if a.sawThought {
		t.Errorf("fresh root → sawThought = true, want false (no thoughts on disk)")
	}
	if a.selected != 0 {
		t.Errorf("empty substrate → selected = %d, want 0 (clamped)", a.selected)
	}
	if a.me != operatorFallbackID {
		t.Errorf("no identity → me = %q, want %q (NoIdentityError fallback)", a.me, operatorFallbackID)
	}
	if a.overlay != overlayNone {
		t.Errorf("default overlay = %q, want none", a.overlay)
	}
}

// TestNewAppInjectedThreadSelection asserts that after the pinned
// substrate is injected (the gate/golden path) selection defaults to
// the freshest (last) row — the drill-down target — exactly as the
// pre-live constructor did off the fixture.
func TestNewAppInjectedThreadSelection(t *testing.T) {
	a := drive(t, 120, 40) // drive injects SubstrateThread
	if a.selected != len(SubstrateThread)-1 {
		t.Errorf("injected-thread selected = %d, want last row %d",
			a.selected, len(SubstrateThread)-1)
	}
	if !a.sawThought {
		t.Errorf("injected thread → sawThought must be true")
	}
}

// TestAppInitArmsCadences asserts Init() arms the five PR-F animation
// cadences (was: returned nil pre-PR-F; PR-F is the layer that wires
// the tea.Tick loop). The detailed batch shape is covered in
// messages_test.go (TestInitArmsAllCadences); here we just assert Init
// is no longer a no-op now that the animation layer is live.
func TestAppInitArmsCadences(t *testing.T) {
	a, _ := NewApp("/r")
	if cmd := a.Init(); cmd == nil {
		t.Errorf("Init() must arm the animation cadences (PR-F), got nil")
	}
}

// TestAppWindowSize asserts a WindowSizeMsg stores width/height.
func TestAppWindowSize(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40)
	if a.width != 120 || a.height != 40 {
		t.Errorf("after WindowSizeMsg{120,40}: width=%d height=%d, want 120/40", a.width, a.height)
	}
}

// TestAppQuitKey pins the G-interact QUIT CONTRACT (the load-bearing
// constraint of the focus/modal model):
//
//   - ctrl+c ALWAYS quits — every mode (the universal quit, the
//     documented obvious alternative to `q`).
//   - `q` quits in NAV mode (and on every non-substrate view).
//   - `q` in COMPOSE mode on the substrate is a LITERAL char (the single
//     deliberate, documented nav/quit exception — ctrl+c is the
//     always-available alternative; esc-then-q also quits).
func TestAppQuitKey(t *testing.T) {
	styles.SetProfile(termenv.Ascii)

	// ctrl+c quits from the App's default (compose) mode.
	a, _ := NewApp("/r")
	_, cmd := a.Update(keyMsg("ctrl+c"))
	if cmd == nil || mustMsg(t, cmd) == nil {
		t.Fatal("ctrl+c must ALWAYS quit (compose mode)")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c should produce tea.QuitMsg, got %T", cmd())
	}

	// `q` in compose mode (substrate, the default) is a LITERAL — it
	// does NOT quit; it appends to the buffer.
	a2, _ := NewApp("/r")
	m, qcmd := a2.Update(keyMsg("q"))
	if qcmd != nil {
		if _, ok := qcmd().(tea.QuitMsg); ok {
			t.Errorf("`q` in COMPOSE mode must NOT quit (documented exception) — it is a literal")
		}
	}
	if got := m.(App).composeText(); got != "q" {
		t.Errorf("`q` in compose mode must append to the buffer, got composeText()=%q", got)
	}

	// `q` in NAV mode quits (drop to nav via esc first).
	a3, _ := NewApp("/r")
	m2, _ := a3.Update(keyMsg("esc")) // compose → nav
	_, ncmd := m2.(App).Update(keyMsg("q"))
	if ncmd == nil {
		t.Fatal("`q` in NAV mode must quit")
	}
	if _, ok := ncmd().(tea.QuitMsg); !ok {
		t.Errorf("`q` in NAV mode should produce tea.QuitMsg, got %T", ncmd())
	}
}

// mustMsg runs a tea.Cmd and returns its Msg (nil-safe for the assertion).
func mustMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	return cmd()
}

// TestAppNavNumericKeys asserts 1-5 switch to the five Rufio tabs in
// canonical order.
func TestAppNavNumericKeys(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	cases := []struct {
		key  string
		want AppView
	}{
		{"1", viewSubstrate}, {"2", viewFleet}, {"3", viewChannels},
		{"4", viewGoals}, {"5", viewMemory},
	}
	for _, c := range cases {
		a := drive(t, 120, 40, c.key)
		if a.view != c.want {
			t.Errorf("key %s: view = %q, want %q", c.key, a.view, c.want)
		}
	}
}

// TestAppNavCycle asserts tab cycles forward and shift+tab back, with
// wraparound.
func TestAppNavCycle(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	// From substrate, tab → fleet → channels → goals → memory → substrate.
	want := []AppView{viewFleet, viewChannels, viewGoals, viewMemory, viewSubstrate}
	a := drive(t, 120, 40)
	for i, w := range want {
		m, _ := a.Update(keyMsg("tab"))
		a = m.(App)
		if a.view != w {
			t.Fatalf("tab #%d: view = %q, want %q", i+1, a.view, w)
		}
	}
	// shift+tab from substrate wraps back to memory.
	m, _ := a.Update(keyMsg("shift+tab"))
	a = m.(App)
	if a.view != viewMemory {
		t.Errorf("shift+tab from substrate should wrap to memory, got %q", a.view)
	}
}

// TestAppTabContentDistinct asserts each tab's View() shows its OWN
// distinct fixture content (not a placeholder, not another tab's data).
//
// PR-E: rendered at 200×55 — the substrate screen is now a TWO-panel
// composition (a flexible chat panel + a fixed mesh rail), so at 120
// the chat panel is only ≈72 cols and the long operator-row body is
// ellipsized by the row fit policy. A roomy terminal lets the full
// phrase render so the assertion stays strong rather than weakened to a
// truncated substring. The fleet tab's content is now the MESH per the
// PR-E contract (substrate rail AND fleet tab are the same mesh), so
// `data-analyst` here is the mesh node LABEL, still distinct from the
// other tabs' fixtures.
func TestAppTabContentDistinct(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	cases := []struct {
		key  string
		want string // a string unique to that tab's fixture
	}{
		{"1", "investigate customer:5821 churn risk"}, // substrate thread
		{"2", "data-analyst"},                         // fleet = mesh (node label)
		{"3", "ch-1747-x1"},                           // channels list
		{"4", "overlaps"},                             // goals overlap line
		{"5", "usage-trend"},                          // memory observation
	}
	for _, c := range cases {
		a := drive(t, 200, 55, c.key)
		out := a.View()
		if !strings.Contains(out, c.want) {
			t.Errorf("tab key %s missing distinct content %q in:\n%s", c.key, c.want, out)
		}
		// No "not in scope" placeholder text anywhere.
		if strings.Contains(out, "not in") && strings.Contains(out, "scope") {
			t.Errorf("tab key %s rendered a placeholder, not real content:\n%s", c.key, out)
		}
	}
}

// TestAppNavSwitchAwayAndBack asserts switching away from substrate and
// back restores the substrate chat content (selection state preserved).
// Rendered at 200×55 so the two-panel chat column is wide enough for the
// full operator-row body (see TestAppTabContentDistinct's PR-E note).
func TestAppNavSwitchAwayAndBack(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 200, 55, "2", "1")
	if a.view != viewSubstrate {
		t.Fatalf("after keys 2,1: view = %q, want substrate", a.view)
	}
	out := a.View()
	if !strings.Contains(out, "investigate customer:5821 churn risk") {
		t.Errorf("substrate view should render SubstrateThread content, got:\n%s", out)
	}
}

// TestAppFooterNoAttribution asserts the footer has the Rufio keybinds
// and does NOT contain the deleted "built with Bubble Tea" attribution.
func TestAppFooterNoAttribution(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40)
	out := a.View()
	for _, bad := range []string{"Bubble Tea", "built with", "Lip Gloss", "landscape mesh", "quiet tabs"} {
		if strings.Contains(out, bad) {
			t.Errorf("footer attribution %q must be DELETED (re-scope §1), found in:\n%s", bad, out)
		}
	}
	// Rufio footer keybinds present.
	for _, want := range []string{"substrate", "fleet", "channels", "goals", "memory", "cmd", "help"} {
		if !strings.Contains(out, want) {
			t.Errorf("footer missing Rufio keybind label %q in:\n%s", want, out)
		}
	}
}

// TestAppTabStripRufioLabels asserts the tab strip shows the Rufio tab
// set and NOT the dropped prototype labels.
func TestAppTabStripRufioLabels(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40)
	out := a.View()
	for _, want := range []string{"fleet", "channels", "memory"} {
		if !strings.Contains(out, want) {
			t.Errorf("tab strip missing Rufio tab %q in:\n%s", want, out)
		}
	}
	for _, bad := range []string{"rules", "agents", "stream"} {
		if strings.Contains(out, bad) {
			t.Errorf("tab strip / footer must NOT contain dropped label %q, found in:\n%s", bad, out)
		}
	}
}

// TestAppSubstrateSelection asserts ↓ moves the selection and the
// rendered substrate view shows the selection marker.
func TestAppSubstrateSelection(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	// Start: selected = last row. ↑ a few times moves it up.
	a := drive(t, 120, 40, "up", "up")
	if a.selected != len(SubstrateThread)-3 {
		t.Errorf("after 2× up: selected = %d, want %d", a.selected, len(SubstrateThread)-3)
	}
	// ↓ moves back down.
	m, _ := a.Update(keyMsg("down"))
	a = m.(App)
	if a.selected != len(SubstrateThread)-2 {
		t.Errorf("after down: selected = %d, want %d", a.selected, len(SubstrateThread)-2)
	}
	if !strings.Contains(a.View(), selectionMarker) {
		t.Errorf("substrate view should render the selection marker %q", selectionMarker)
	}
	// Selection clamps at the top.
	for i := 0; i < 20; i++ {
		m, _ = a.Update(keyMsg("up"))
		a = m.(App)
	}
	if a.selected != 0 {
		t.Errorf("selection should clamp at 0, got %d", a.selected)
	}
	// And at the bottom.
	for i := 0; i < 20; i++ {
		m, _ = a.Update(keyMsg("down"))
		a = m.(App)
	}
	if a.selected != len(SubstrateThread)-1 {
		t.Errorf("selection should clamp at last row %d, got %d", len(SubstrateThread)-1, a.selected)
	}
}

// TestAppLineageDrillDown asserts `enter` on the decision row opens the
// lineage drill-down (reasoning chain visible), esc closes it, and
// `enter` on a NON-decision row does nothing.
func TestAppLineageDrillDown(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	// Default selection is the last row = the decision row. enter opens it.
	a := drive(t, 120, 40, "enter")
	if a.overlay != overlayLineage {
		t.Fatalf("enter on decision row should open lineage overlay, overlay=%q", a.overlay)
	}
	out := a.View()
	for _, want := range []string{"Reasoning chain:", "approve downgrade", "Context bundle:", "refund-policy.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("lineage overlay missing %q in:\n%s", want, out)
		}
	}
	// esc closes it.
	m, _ := a.Update(keyMsg("esc"))
	a = m.(App)
	if a.overlay != overlayNone {
		t.Errorf("esc should close the lineage overlay, overlay=%q", a.overlay)
	}
	if strings.Contains(a.View(), "Reasoning chain:") {
		t.Errorf("after esc the drill-down must be gone:\n%s", a.View())
	}

	// enter on a NON-decision row (the operator row, index 0) is a no-op.
	b := drive(t, 120, 40)
	for i := 0; i < len(SubstrateThread); i++ {
		m, _ := b.Update(keyMsg("up"))
		b = m.(App)
	}
	if b.selected != 0 {
		t.Fatalf("setup: selected should be 0 (op row), got %d", b.selected)
	}
	m, _ = b.Update(keyMsg("enter"))
	b = m.(App)
	if b.overlay != overlayNone {
		t.Errorf("enter on a non-decision row must be a no-op, overlay=%q", b.overlay)
	}
}

// TestAppHelpOverlay asserts `?` opens the help overlay and esc / any
// key closes it.
func TestAppHelpOverlay(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40, "?")
	if a.overlay != overlayHelp {
		t.Fatalf("? should open the help overlay, overlay=%q", a.overlay)
	}
	out := a.View()
	if !strings.Contains(out, helpTitle) {
		t.Errorf("help overlay missing title %q in:\n%s", helpTitle, out)
	}
	if !strings.Contains(out, "switch tab") {
		t.Errorf("help overlay missing keymap content in:\n%s", out)
	}
	// esc closes it.
	m, _ := a.Update(keyMsg("esc"))
	a = m.(App)
	if a.overlay != overlayNone {
		t.Errorf("esc should close the help overlay, overlay=%q", a.overlay)
	}
	// Re-open, then ANY key closes it (PR-C dismissal style).
	a = drive(t, 120, 40, "?")
	m, _ = a.Update(keyMsg("x"))
	a = m.(App)
	if a.overlay != overlayNone {
		t.Errorf("any key should close the help overlay, overlay=%q", a.overlay)
	}
}

// TestAppCmdStubNoCrash asserts `:` is an accepted no-op stub (command
// palette is a later PR) and does not change state or crash.
func TestAppCmdStubNoCrash(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40, ":")
	if a.overlay != overlayNone || a.view != viewSubstrate {
		t.Errorf("`:` should be a no-op stub, got overlay=%q view=%q", a.overlay, a.view)
	}
	_ = a.View() // must not panic
}

// TestAppEscNoOverlayNoOp asserts esc with no overlay open is a no-op
// (does not change the view).
func TestAppEscNoOverlayNoOp(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40, "2", "esc")
	if a.view != viewFleet {
		t.Errorf("esc with no overlay should be a no-op, view=%q want fleet", a.view)
	}
}

// TestAppViewVerticalFill asserts the rendered screen fills the terminal
// height for every tab (the old-TUI "panes don't fill" fix).
func TestAppViewVerticalFill(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	for _, key := range []string{"1", "2", "3", "4", "5"} {
		a := drive(t, 120, 40, key)
		got := lipgloss.Height(a.View())
		if got < 36 || got > 44 {
			t.Errorf("tab %s View() height = %d lines, want ≈40 (fill-bug regression)", key, got)
		}
	}
}

// TestFooterModalityHintAndHeightInvariant is the #68 contract: the
// footer surfaces the Esc→nav / i→compose modality affordance WITHOUT
// the `?` overlay (so the modal model is discoverable), AND the footer
// stays EXACTLY ONE line at every width (the footerH=1 / bodyH = h −
// headerH − footerH invariant the whole layout depends on — a wrapped
// footer would silently steal a body row at narrow widths).
func TestFooterModalityHintAndHeightInvariant(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40)

	// (1) The hint is present at a comfortable width: the footer carries
	// the esc→nav and i→compose affordances (plain text under Ascii).
	footer := a.renderFooter(120)
	if lipgloss.Height(footer) != 1 {
		t.Fatalf("footer must be exactly 1 line at width 120, got %d: %q",
			lipgloss.Height(footer), footer)
	}
	for _, want := range []string{"esc", "nav", "i", "compose"} {
		if !strings.Contains(footer, want) {
			t.Errorf("#68: footer missing modality affordance %q: %q", want, footer)
		}
	}
	// The pre-existing binds must still be there (the hint is APPENDED,
	// not a replacement).
	for _, want := range []string{"substrate", "fleet", "memory", "help"} {
		if !strings.Contains(footer, want) {
			t.Errorf("#68: footer dropped an existing bind %q: %q", want, footer)
		}
	}

	// (2) footerH == 1 at EVERY width incl. pathologically narrow ones —
	// gutter()/clampLine must hard-truncate the longer footer, never
	// wrap it (a wrapped footer breaks bodyH = h − headerH − footerH).
	for _, w := range []int{1, 5, 10, 20, 40, 56, 70, 80, 120, 200} {
		f := a.renderFooter(w)
		if h := lipgloss.Height(f); h != 1 {
			t.Errorf("#68: footer height = %d at width %d, want 1 (footerH invariant): %q", h, w, f)
		}
		if gw := lipgloss.Width(f); gw > w && w >= 1 {
			t.Errorf("#68: footer width %d exceeds terminal %d (overflow): %q", gw, w, f)
		}
	}

	// (3) The bodyH math is intact: in the composed View() the body
	// region is exactly h − headerH − footerH and the footer is the
	// LAST screen line (not pushed off / wrapped).
	for _, sz := range [][2]int{{120, 40}, {70, 30}, {56, 24}} {
		v := drive(t, sz[0], sz[1]).View()
		vl := strings.Split(v, "\n")
		if got := len(vl); got != sz[1] {
			t.Errorf("#68: View() at %dx%d produced %d lines, want exactly %d (footer must not add a row)",
				sz[0], sz[1], got, sz[1])
		}
	}
}

// TestAppTinyTerminal asserts a tiny terminal does not panic and the
// view is non-empty for every tab + overlay.
func TestAppTinyTerminal(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	for _, keys := range [][]string{
		{"1"}, {"2"}, {"3"}, {"4"}, {"5"}, {"?"}, {"enter"},
	} {
		a := drive(t, 20, 8, keys...)
		out := a.View() // must not panic
		if strings.TrimSpace(out) == "" {
			t.Errorf("tiny-terminal View() returned empty for keys %v", keys)
		}
	}
}

// panelSpan is one bordered panel's geometry on a rendered screen: the
// top/bottom border row indices and the left/right border COLUMNS (rune
// indices). PR-E: the substrate screen is now a TWO-panel composition,
// so the border-integrity invariants must hold PER PANEL, not for a
// single full-width box.
type panelSpan struct {
	top, bot int // border row indices (the ╭…╮ and ╰…╯ rows)
	l, r     int // left/right border columns (rune indices)
}

// detectPanels finds every rounded panel on a View() (Ascii profile) by
// pairing the ╭/╮ on the top border row with the ╰/╯ on the matching
// bottom row. Side-by-side panels (the substrate chat panel + mesh rail)
// share the SAME top/bottom rows but have distinct column spans, so this
// returns one panelSpan per ╭…╮ pair. Used by the per-panel
// border-integrity invariants so BOTH panels are checked.
func detectPanels(t *testing.T, out string) ([]string, []panelSpan) {
	t.Helper()
	lines := strings.Split(out, "\n")
	top, bot := -1, -1
	for i, ln := range lines {
		if strings.ContainsRune(ln, '╭') {
			top = i
		}
		if strings.ContainsRune(ln, '╰') {
			bot = i
			break
		}
	}
	if top < 0 || bot < 0 || bot <= top+1 {
		t.Fatalf("could not locate any panel border (top=%d bot=%d) in:\n%s", top, bot, out)
	}
	topR := []rune(lines[top])
	botR := []rune(lines[bot])
	var spans []panelSpan
	l := -1
	for i, ch := range topR {
		switch ch {
		case '╭':
			l = i
		case '╮':
			if l < 0 {
				t.Fatalf("top border ╮ at col %d with no opening ╭: %q", i, lines[top])
			}
			if i >= len(botR) || botR[l] != '╰' || botR[i] != '╯' {
				t.Fatalf("panel [%d,%d] bottom rule not closed: %q", l, i, lines[bot])
			}
			spans = append(spans, panelSpan{top: top, bot: bot, l: l, r: i})
			l = -1
		}
	}
	if len(spans) == 0 {
		t.Fatalf("no ╭…╮ panel pair on top border: %q", lines[top])
	}
	return lines, spans
}

// panelInterior returns the interior rows of one panelSpan with its
// left/right `│` border cells stripped, asserting the border is intact
// at exactly cols l and r on every interior row (the Defect-1 contract,
// now per panel).
func panelInterior(t *testing.T, lines []string, p panelSpan) []string {
	t.Helper()
	interior := make([]string, 0, p.bot-p.top-1)
	for ri := p.top + 1; ri < p.bot; ri++ {
		rr := []rune(lines[ri])
		if p.l >= len(rr) || p.r >= len(rr) || rr[p.l] != '│' || rr[p.r] != '│' {
			t.Fatalf("panel [%d,%d] border broken on row %d: %q", p.l, p.r, ri, lines[ri])
		}
		interior = append(interior, string(rr[p.l+1:p.r]))
	}
	return interior
}

// TestAppPanelBorderIntactEveryTab is the Defect-1 invariant for EVERY
// PANEL of every tab: each rounded panel's border is unbroken on every
// interior row and every interior row is exactly that panel's inner
// width (so nothing overflowed and wrapped flush to the border). PR-E:
// the substrate screen is TWO panels (chat panel + fixed mesh rail), so
// the invariant is asserted per panel — both the flexible chat panel
// AND the mesh rail must hold. Overlays are a centered floating box
// (different geometry) — TestAppOverlayBoxIntegrity / keys_test.go.
func TestAppPanelBorderIntactEveryTab(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	const w = 120
	cases := []struct {
		keys       []string
		wantPanels int // substrate = 2 panels, the rest = 1
	}{
		{[]string{"1"}, 2}, // substrate: chat panel + mesh rail
		{[]string{"2"}, 1}, // fleet: single mesh panel
		{[]string{"3"}, 1}, // channels
		{[]string{"4"}, 1}, // goals
		{[]string{"5"}, 1}, // memory
	}
	for _, c := range cases {
		a := drive(t, w, 40, c.keys...)
		out := a.View()
		lines, spans := detectPanels(t, out)
		if len(spans) != c.wantPanels {
			t.Errorf("keys %v: found %d panels, want %d (substrate=2 / others=1)",
				c.keys, len(spans), c.wantPanels)
		}
		if c.wantPanels == 2 {
			// The fixed mesh rail must be exactly meshRailOuter wide.
			rail := spans[len(spans)-1]
			if gw := rail.r - rail.l + 1; gw != meshRailOuter {
				t.Errorf("keys %v: mesh rail outer width = %d, want meshRailOuter %d",
					c.keys, gw, meshRailOuter)
			}
		}
		for pi, p := range spans {
			interior := panelInterior(t, lines, p)
			innerWidth := p.r - p.l - 1
			for i, body := range interior {
				if gw := lipgloss.Width(body); gw != innerWidth {
					t.Errorf("keys %v panel %d interior row %d width = %d, want %d (border-intact); row=%q",
						c.keys, pi, i, gw, innerWidth, body)
				}
			}
			if len(interior) < 10 {
				t.Errorf("keys %v panel %d: only %d interior rows at height 40 — not filling",
					c.keys, pi, len(interior))
			}
		}
	}
}

// TestAppOverlayBoxIntegrity is the border-integrity invariant for the
// help + lineage overlays: each is a centered rounded box composited
// over the screen. The box border must be closed (top ╭…╮ and bottom
// ╰…╯ of equal width), every box row must carry its left+right `│`
// border at the SAME columns, no box-interior line may exceed the box's
// inner width, and no screen line may exceed the terminal width. This
// keeps the invariant strong for overlays without assuming the (full-
// width) tab-panel geometry.
func TestAppOverlayBoxIntegrity(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	const w = 120
	for _, keys := range [][]string{{"?"}, {"enter"}} {
		a := drive(t, w, 40, keys...)
		out := a.View()
		lines := strings.Split(out, "\n")

		// No screen line exceeds the terminal width.
		for i, ln := range lines {
			if gw := lipgloss.Width(ln); gw > w {
				t.Errorf("keys %v screen line %d width=%d exceeds terminal %d: %q", keys, i, gw, w, ln)
			}
		}

		// Locate the box: first line with ╭ and the matching ╰.
		topIdx, botIdx := -1, -1
		for i, ln := range lines {
			if strings.Contains(ln, "╭") {
				topIdx = i
			}
			if strings.Contains(ln, "╰") {
				botIdx = i
				break
			}
		}
		if topIdx < 0 || botIdx <= topIdx {
			t.Fatalf("keys %v: could not locate overlay box border in:\n%s", keys, out)
		}
		topRunes := []rune(lines[topIdx])
		botRunes := []rune(lines[botIdx])
		// The box's left/right border columns are the ╭ / ╮ positions.
		var l, r int = -1, -1
		for i, ch := range topRunes {
			if ch == '╭' {
				l = i
			}
			if ch == '╮' {
				r = i
			}
		}
		if l < 0 || r <= l {
			t.Fatalf("keys %v: malformed box top rule: %q", keys, lines[topIdx])
		}
		// Bottom rule must close at the SAME columns.
		if l >= len(botRunes) || r >= len(botRunes) || botRunes[l] != '╰' || botRunes[r] != '╯' {
			t.Errorf("keys %v: box bottom rule not closed at cols %d/%d: %q", keys, l, r, lines[botIdx])
		}
		// Every interior box row carries `│` at exactly cols l and r, and
		// the content strictly between them never collides with them.
		for i := topIdx + 1; i < botIdx; i++ {
			rr := []rune(lines[i])
			if l >= len(rr) || r >= len(rr) || rr[l] != '│' || rr[r] != '│' {
				t.Errorf("keys %v: overlay box border broken on row %d: %q", keys, i, lines[i])
				continue
			}
			inner := string(rr[l+1 : r])
			if iw := lipgloss.Width(inner); iw != r-l-1 {
				t.Errorf("keys %v: overlay box interior row %d width=%d, want %d: %q",
					keys, i, iw, r-l-1, inner)
			}
		}
	}
}

// TestAppNoContentFlushToBorder is the Defect-2 invariant for the
// substrate CHAT panel: every NON-BLANK, non-hairline interior row
// begins with at least chatPanelHPad spaces (nothing jammed against the
// panel `│`). PR-E: the substrate screen is two panels; this asserts
// the chat panel (the first detected span — the mesh rail is verified
// separately by TestAppMeshRailBorderIntact).
func TestAppNoContentFlushToBorder(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40)
	out := a.View()
	lines, spans := detectPanels(t, out)
	interior := panelInterior(t, lines, spans[0]) // chat panel
	wantPad := strings.Repeat(" ", chatPanelHPad)
	for i, body := range interior {
		if strings.TrimSpace(body) == "" {
			continue
		}
		if strings.Trim(strings.TrimSpace(body), "─ ") == "" {
			continue
		}
		if !strings.HasPrefix(body, wantPad) {
			t.Errorf("interior row %d not h-padded (Defect 2): want >=%d leading spaces, got %q",
				i, chatPanelHPad, body)
		}
	}
}

// TestAppReplyRailNotFlush is the Defect-3 invariant: the substrate
// reply rows' `│` rail must NOT be adjacent to the panel `│` border.
func TestAppReplyRailNotFlush(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40)
	out := a.View()
	if strings.Contains(out, "││") {
		t.Errorf("Defect 3 regression: panel border adjacent to reply rail (`││`) in:\n%s", out)
	}
	lines, spans := detectPanels(t, out)
	interior := panelInterior(t, lines, spans[0]) // chat panel
	var sawReply bool
	for _, body := range interior {
		if strings.Contains(body, "OBSERVATION") || strings.Contains(body, "CONFIRM") {
			sawReply = true
			if !strings.HasPrefix(body, strings.Repeat(" ", chatPanelHPad)+"│") {
				t.Errorf("reply row rail not separated from panel border by h-pad: %q", body)
			}
		}
	}
	if !sawReply {
		t.Fatal("no reply row found in panel interior — fixture/layout regression")
	}
}

// TestAppSubstrateTwoPanelComposition is the PR-E structural-contract
// invariant: the substrate screen is NO outer border, a border-less
// header/footer on bg with a bodyGutter, and TWO bordered panels side
// by side (a flexible chat panel + a fixed mesh rail) separated by a
// panelGap. Verifies (a) no outer border, (c) two panels, (d) the rail
// is exactly meshRailOuter wide, and the gap between them.
func TestAppSubstrateTwoPanelComposition(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40)
	out := a.View()
	lines := strings.Split(out, "\n")

	// (a) NO outer border: the first/last screen lines are the header /
	// footer text, not a ╭…╮ / ╰…╯ rule, and no line starts at col 0
	// with a border glyph.
	if strings.ContainsAny(lines[0], "╭╮╰╯") {
		t.Errorf("(a) header row must not be an outer border: %q", lines[0])
	}
	for i, ln := range lines {
		r := []rune(ln)
		if len(r) > 0 && (r[0] == '╭' || r[0] == '│' || r[0] == '╰') {
			t.Errorf("(a) line %d starts with a border glyph at col 0 — outer border / no gutter: %q", i, ln)
		}
	}

	// (c)/(d) exactly two panels; the second is the fixed mesh rail.
	allLines, spans := detectPanels(t, out)
	if len(spans) != 2 {
		t.Fatalf("(c) substrate must be TWO panels, found %d", len(spans))
	}
	chat, rail := spans[0], spans[1]
	if gw := rail.r - rail.l + 1; gw != meshRailOuter {
		t.Errorf("(d) mesh rail outer width = %d, want meshRailOuter %d", gw, meshRailOuter)
	}
	// The chat panel is left of the rail with a panelGap between them.
	if got := rail.l - chat.r - 1; got != panelGap {
		t.Errorf("inter-panel gap = %d cols, want panelGap %d", got, panelGap)
	}

	// The mesh rail itself must show the MESH header + ROUTING strip.
	railInterior := panelInterior(t, allLines, rail)
	joined := strings.Join(railInterior, "\n")
	for _, want := range []string{"MESH", "nodes", "links", "ROUTING", "quorum"} {
		if !strings.Contains(joined, want) {
			t.Errorf("mesh rail missing %q in:\n%s", want, joined)
		}
	}
}

// TestMeshRailSplitShrinkNotDrop is the #67-P5 contract (maintainer
// decision: SHRINK the rail at narrow widths, NEVER fully drop it until
// a much smaller floor). meshRailSplit is the single geometry source for
// the chat-outer/rail-outer/single-panel decision.
//
//   - WIDE/DEFAULT (innerW with room for the full rail): the rail is
//     EXACTLY meshRailOuter and chatOuter == innerW − panelGap −
//     meshRailOuter (byte-identical to the pre-P5 split — the wide
//     composition must not move a single cell).
//   - NARROW that PREVIOUSLY dropped the rail (old: chatOuter < 24 ⇒
//     single-panel): the rail is now KEPT and COMPRESSED — railOuter
//     strictly between the floor and meshRailOuter, two panels still.
//   - Below the (much smaller) FLOOR: only THEN does it fall to
//     single-panel — and that floor terminal width is far below the old
//     ~72 drop point.
func TestMeshRailSplitShrinkNotDrop(t *testing.T) {
	// Helper: the OLD split (verbatim pre-P5 logic) — used to find the
	// widths that previously dropped the rail.
	oldDropped := func(innerW int) bool {
		return innerW-panelGap-meshRailOuter < minChatOuter
	}

	// (1) WIDE: byte-identical split. innerW for a 120-wide terminal.
	const wideInner = 120 - 2*bodyGutter // 116
	chatW, railW, singleW := meshRailSplit(wideInner)
	if singleW {
		t.Fatalf("wide innerW=%d must NOT be single-panel", wideInner)
	}
	if railW != meshRailOuter {
		t.Errorf("wide rail outer = %d, want meshRailOuter %d (wide must be byte-identical)",
			railW, meshRailOuter)
	}
	if want := wideInner - panelGap - meshRailOuter; chatW != want {
		t.Errorf("wide chatOuter = %d, want %d (byte-identical pre-P5 split)", chatW, want)
	}
	if oldDropped(wideInner) {
		t.Fatalf("test precondition broken: wide innerW=%d should not be an old-drop width", wideInner)
	}

	// (2) NARROW that the OLD logic dropped: the rail is now KEPT +
	// COMPRESSED. Scan every innerW the old split would have dropped but
	// where the new floor still seats a (compressed) rail.
	sawCompressed := false
	for innerW := 1; innerW <= wideInner; innerW++ {
		c, r, single := meshRailSplit(innerW)
		if single {
			// Single-panel: the new contract signals it as chatOuter ==
			// innerW and railOuter == 0.
			if c != innerW || r != 0 {
				t.Errorf("innerW=%d single-panel must be (innerW,0), got chat=%d rail=%d", innerW, c, r)
			}
			continue
		}
		// Two-panel: rail must be within [floor, meshRailOuter], chat at
		// least the readable minimum, and the three widths must fit.
		if r > meshRailOuter {
			t.Errorf("innerW=%d rail outer %d exceeds meshRailOuter %d (rail must never GROW)",
				innerW, r, meshRailOuter)
		}
		if r < meshRailFloorOuter {
			t.Errorf("innerW=%d rail outer %d below the floor %d", innerW, r, meshRailFloorOuter)
		}
		if c < minChatOuter {
			t.Errorf("innerW=%d chat outer %d below minChatOuter %d", innerW, c, minChatOuter)
		}
		if c+panelGap+r > innerW {
			t.Errorf("innerW=%d split overflows: chat %d + gap %d + rail %d > %d", innerW, c, panelGap, r, innerW)
		}
		if oldDropped(innerW) && r < meshRailOuter {
			sawCompressed = true // a width the old logic dropped, now a compressed rail
		}
	}
	if !sawCompressed {
		t.Error("P5 regression: NO width that the old split dropped now keeps a compressed rail")
	}

	// (3) The new single-panel FLOOR is far below the old ~72-col drop.
	// Old terminal drop point: width < 72 (innerW < 68). The new floor
	// must seat a two-panel rail well below that.
	const oldDropTerminalW = 72
	newFloorInner := -1
	for innerW := 1; innerW <= wideInner; innerW++ {
		if _, _, single := meshRailSplit(innerW); !single {
			newFloorInner = innerW
			break
		}
	}
	if newFloorInner < 0 {
		t.Fatal("meshRailSplit never seats a two-panel rail at any width")
	}
	newFloorTerminalW := newFloorInner + 2*bodyGutter
	if newFloorTerminalW >= oldDropTerminalW {
		t.Errorf("new two-panel floor terminal width = %d, must be MUCH smaller than the old drop %d",
			newFloorTerminalW, oldDropTerminalW)
	}

	// (4) chatPanelOuterWidth still honours its single-source contract:
	// it returns innerW exactly when meshRailSplit says single-panel,
	// and the chat outer otherwise (chatScrollMax mirror depends on it).
	for _, innerW := range []int{16, newFloorInner, 64, wideInner} {
		c, _, single := meshRailSplit(innerW)
		got := chatPanelOuterWidth(innerW)
		if single && got != innerW {
			t.Errorf("innerW=%d single-panel: chatPanelOuterWidth=%d, want innerW %d", innerW, got, innerW)
		}
		if !single && got != c {
			t.Errorf("innerW=%d two-panel: chatPanelOuterWidth=%d, want chatOuter %d", innerW, got, c)
		}
	}
}

// TestAppNarrowRailShrinksNotDropped is the #67-P5 END-TO-END contract:
// at a terminal width that the OLD layout dropped the rail entirely
// (single-panel), App.View() now renders TWO panels — a compressed mesh
// rail whose node identity is still legible (the agent ids read), the
// border intact on both panels, and the footer/body height math sane.
func TestAppNarrowRailShrinksNotDropped(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	// width 70 → innerW 66 → OLD: 66−2−42 = 22 < 24 ⇒ single-panel
	// (rail dropped). NEW: rail kept, compressed.
	const w, h = 70, 40
	if !(70-2*bodyGutter-panelGap-meshRailOuter < minChatOuter) {
		t.Fatalf("test precondition: width %d must be an OLD rail-drop width", w)
	}
	a := drive(t, w, h)
	out := a.View()
	lines, spans := detectPanels(t, out)
	if len(spans) != 2 {
		t.Fatalf("P5: width %d must now render TWO panels (rail shrunk-not-dropped), got %d:\n%s",
			w, len(spans), out)
	}
	chat, rail := spans[0], spans[1]
	railW := rail.r - rail.l + 1
	if railW >= meshRailOuter {
		t.Errorf("P5: narrow rail outer %d must be COMPRESSED (< meshRailOuter %d)", railW, meshRailOuter)
	}
	if railW < meshRailFloorOuter {
		t.Errorf("P5: narrow rail outer %d below the floor %d", railW, meshRailFloorOuter)
	}
	// Border intact: every interior row of BOTH panels is exactly that
	// panel's inner width (no overflow against the border).
	for pi, p := range []panelSpan{chat, rail} {
		for i, body := range panelInterior(t, lines, p) {
			if gw := lipgloss.Width(body); gw != p.r-p.l-1 {
				t.Errorf("P5 panel %d interior row %d width %d != inner %d (border broke at narrow width): %q",
					pi, i, gw, p.r-p.l-1, body)
			}
		}
	}
	// Node identity still legible in the compressed rail: the mesh node
	// ids must still read (labels may elide but the agents must be
	// identifiable). pinnedMesh = operator hub + claude-code/cursor/
	// data-analyst.
	railText := strings.Join(panelInterior(t, lines, rail), "\n")
	for _, want := range []string{"MESH", "ROUTING", "operator"} {
		if !strings.Contains(railText, want) {
			t.Errorf("P5: compressed rail lost %q (node identity must stay legible):\n%s", want, railText)
		}
	}
	// footerH must still be 1 and the body-height math intact at this
	// narrow width (coordinated with #68/P3).
	flines := strings.Split(out, "\n")
	footer := flines[len(flines)-1]
	if strings.TrimSpace(footer) == "" && len(flines) > 1 {
		footer = flines[len(flines)-2]
	}
	if lipgloss.Width(footer) > w {
		t.Errorf("P5: footer width %d exceeds terminal %d at narrow width: %q", lipgloss.Width(footer), w, footer)
	}
}

// TestAppMeshRailBorderIntact is the per-panel Defect-1 invariant for
// the mesh rail specifically (the contract requires border-integrity
// invariants for BOTH panels). Every rail interior row is exactly
// meshRailInner wide with its `│` intact.
func TestAppMeshRailBorderIntact(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	for _, sz := range [][2]int{{120, 40}, {200, 55}} {
		a := drive(t, sz[0], sz[1])
		out := a.View()
		lines, spans := detectPanels(t, out)
		if len(spans) != 2 {
			t.Fatalf("%dx%d: expected 2 substrate panels, got %d", sz[0], sz[1], len(spans))
		}
		rail := spans[1]
		interior := panelInterior(t, lines, rail)
		for i, body := range interior {
			if gw := lipgloss.Width(body); gw != meshRailInner {
				t.Errorf("%dx%d mesh rail interior row %d width = %d, want meshRailInner %d: %q",
					sz[0], sz[1], i, gw, meshRailInner, body)
			}
		}
	}
}

// TestAppRoutingRuleAlignsWithComposer is the deliberate v8 cross-column
// TestRenderRoutingStripNarrowClamp is the #67-P3 contract: on a WIDE
// inner width the full `ROUTING  N linked · quorum X/Y · append-only`
// renders verbatim (byte-identical — the suffix fits); on a NARROW
// inner width (the mesh RAIL, contentWidth 36) the ` · append-only`
// suffix degrades to a CLEAN drop rather than the ugly mid-word `…`
// hard-clamp (`… · ap…`). The core `ROUTING  N linked · quorum X/Y`
// must stay legible at the narrow width, and the rule line (the
// full-width `─` divider) is unchanged at every width.
func TestRenderRoutingStripNarrowClamp(t *testing.T) {
	styles.SetProfile(termenv.Ascii) // plain text — no escapes to strip
	mesh := pinnedMesh()             // 4 nodes (operator hub + 3 agents)
	sub := SubstrateThread           // carries the 2/3 decision quorum

	const suffix = " · append-only"

	// WIDE: the full string fits → byte-identical, suffix present, the
	// rule is exactly innerWidth `─`.
	wide := renderRoutingStrip(112, mesh, sub)
	wLines := strings.Split(wide, "\n")
	if len(wLines) != 2 {
		t.Fatalf("routing strip must be rule+row (2 lines), got %d: %q", len(wLines), wide)
	}
	if strings.Trim(wLines[0], "─") != "" || lipgloss.Width(wLines[0]) != 112 {
		t.Errorf("wide rule line must be exactly 112 `─`, got %q", wLines[0])
	}
	if !strings.Contains(wLines[1], "ROUTING") || !strings.Contains(wLines[1], "linked") ||
		!strings.Contains(wLines[1], "quorum") || !strings.Contains(wLines[1], suffix) {
		t.Errorf("wide routing row must carry the FULL `ROUTING … · append-only`: %q", wLines[1])
	}
	if strings.Contains(wLines[1], ellipsis) {
		t.Errorf("wide routing row must NOT be `…`-clamped (the suffix fits): %q", wLines[1])
	}

	// NARROW: the mesh-rail contentWidth (meshRailInner − 2·hpad = 36).
	// The ` · append-only` suffix can no longer fit; it must be DROPPED
	// cleanly — the row must NOT end in a mid-word `…` truncation, and
	// the core `ROUTING … quorum X/Y` must still read.
	const railInner = meshRailInner - 2*chatPanelHPad // 36
	narrow := renderRoutingStrip(railInner, mesh, sub)
	nLines := strings.Split(narrow, "\n")
	if len(nLines) != 2 {
		t.Fatalf("narrow routing strip must be rule+row, got %d: %q", len(nLines), narrow)
	}
	if strings.Trim(nLines[0], "─") != "" || lipgloss.Width(nLines[0]) != railInner {
		t.Errorf("narrow rule line must be exactly %d `─` (rule unchanged at any width), got %q",
			railInner, nLines[0])
	}
	row := nLines[1]
	if lipgloss.Width(row) > railInner {
		t.Errorf("narrow routing row width %d exceeds rail inner %d: %q",
			lipgloss.Width(row), railInner, row)
	}
	if !strings.Contains(row, "ROUTING") {
		t.Errorf("narrow routing row lost the ROUTING label: %q", row)
	}
	// The core count + quorum must survive the degrade (the legible
	// minimum). The fixture's decision quorum is 2/3.
	if !strings.Contains(row, "linked") || !strings.Contains(row, "quorum 2/3") {
		t.Errorf("narrow routing row must keep the legible core `… linked · quorum 2/3`: %q", row)
	}
	// Clean drop, not ugly clamp: the suffix is gone AND the row does
	// not end in the dangling `…` mid-`append-only` hard-truncation the
	// old clampLine produced (`quorum 2/3 · ap…`).
	if strings.Contains(row, suffix) {
		t.Errorf("narrow routing row must DROP ` · append-only` (does not fit): %q", row)
	}
	if strings.HasSuffix(strings.TrimRight(row, " "), ellipsis) {
		t.Errorf("narrow routing row must degrade by a CLEAN suffix-drop, not a mid-word `…` clamp: %q", row)
	}
}

// detail (jsx 327-336): the mesh rail's ROUTING hairline must sit on the
// SAME screen row as the chat composer's TOP-rule (same distance from
// the panel bottom). Both bottom blocks are ComposerHeight inner lines
// (the composer block top-rule…padV; the ROUTING block rule + row +
// routingBottomPad, where routingBottomPad = ComposerHeight−2), so the
// rule is at the same row index in both panels regardless of the
// composer's vertical padding. lastRule() finds the LAST full-content-
// width hairline in each panel — for the chat panel that is the
// composer TOP-rule (the only hairlines are chrome-bottom + composer-
// top), so this asserts alignment to the composer's first line.
func TestAppRoutingRuleAlignsWithComposer(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40)
	out := a.View()
	lines, spans := detectPanels(t, out)
	if len(spans) != 2 {
		t.Fatalf("expected 2 substrate panels, got %d", len(spans))
	}
	chat, rail := spans[0], spans[1]

	// The composer top-rule is the LAST full-width hairline inside the
	// chat panel; the ROUTING hairline is the last hairline inside the
	// rail. They must be the same screen row.
	lastRule := func(p panelSpan) int {
		row := -1
		for ri := p.top + 1; ri < p.bot; ri++ {
			rr := []rune(lines[ri])
			if p.r >= len(rr) {
				continue
			}
			body := strings.TrimSpace(string(rr[p.l+1 : p.r]))
			if body != "" && strings.Trim(body, "─") == "" {
				row = ri
			}
		}
		return row
	}
	composerRule := lastRule(chat)
	routingRule := lastRule(rail)
	if composerRule < 0 || routingRule < 0 {
		t.Fatalf("could not locate composer rule (%d) / routing rule (%d)", composerRule, routingRule)
	}
	if composerRule != routingRule {
		t.Errorf("ROUTING rule (row %d) must align with composer top-rule (row %d) across columns (jsx 327-336)",
			routingRule, composerRule)
	}
}

// TestAppHeaderGradientTrueColor asserts the gradient wordmark carries
// 24-bit color escapes under TrueColor and is a genuine per-character
// SWEEP, AND that a Rufio agent's color escape is present in the
// composed substrate screen. claude-code → Accent2 #8ab4f8; termenv's
// TrueColor round-trip yields ESC[38;2;138;179;248m (G 180→179, the
// documented off-by-one) — asserted as the round-tripped value.
func TestAppHeaderGradientTrueColor(t *testing.T) {
	styles.SetProfile(termenv.TrueColor)
	defer styles.SetProfile(termenv.Ascii)

	mark := gradientWordmark(wordmark)
	const wantAccentFg = "38;2;167;139;250" // Accent #a78bfa, clean round-trip
	if !strings.Contains(mark, wantAccentFg) {
		t.Errorf("gradient wordmark missing 24-bit Accent fg %q: %q", wantAccentFg, mark)
	}
	distinct := map[string]struct{}{}
	for _, tok := range strings.Split(mark, "\x1b[") {
		if i := strings.Index(tok, "38;2;"); i >= 0 {
			seq := tok[i:]
			if mIdx := strings.IndexByte(seq, 'm'); mIdx >= 0 {
				distinct[seq[:mIdx]] = struct{}{}
			}
		}
	}
	if len(distinct) < 2 {
		t.Errorf("gradient wordmark is not a per-char sweep: only %d distinct fg(s): %q", len(distinct), mark)
	}

	// The composed substrate screen carries the claude-code agent color
	// (Accent2 #8ab4f8, round-tripped) — a Rufio TrueColor assertion.
	const wantClaudeFg = "38;2;138;179;248"
	out := drive(t, 120, 40).View()
	if !strings.Contains(out, wantClaudeFg) {
		t.Errorf("composed substrate View() missing claude-code 24-bit fg %q (Accent2)", wantClaudeFg)
	}
}

// goldenFor renders a sized App after the given keys and compares (or
// bootstraps under TEATEST_UPDATE=1) against test/golden/<name>.
func goldenFor(t *testing.T, name string, keys ...string) {
	t.Helper()
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40, keys...)
	got := a.View()
	path := filepath.Join("..", "..", "test", "golden", name)
	if os.Getenv("TEATEST_UPDATE") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file %s missing — run TEATEST_UPDATE=1 go test ./internal/tui/... -run TestApp to bootstrap: %v", path, err)
	}
	if string(want) != got {
		t.Errorf("golden mismatch %s:\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// goldenFromView compares an ALREADY-rendered view (the Msg-injected
// pattern — the caller built the App via injected substrateLoadedMsg /
// DaemonOnlineMsg, NO live fsnotify / wall-clock) against
// test/golden/<name>, bootstrapping under TEATEST_UPDATE=1. Distinct
// from goldenFor (which re-drives via keys) so a PR-G1 live golden can
// pin a deterministic injected fixture without a key sequence.
func goldenFromView(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("..", "..", "test", "golden", name)
	if os.Getenv("TEATEST_UPDATE") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file %s missing — run TEATEST_UPDATE=1 go test ./internal/tui/... to bootstrap: %v", path, err)
	}
	if string(want) != got {
		t.Errorf("golden mismatch %s:\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// TestAppGoldenSubstrate is the substrate screen golden (Rufio data, new
// nav, NO attribution) at 120×40 under Ascii. PR-G1: drive() now injects
// the pinned SubstrateThread via substrateLoadedMsg (the deterministic
// fsnotify-free seam) so this regression golden is a fixed-data live
// render.
func TestAppGoldenSubstrate(t *testing.T) {
	goldenFor(t, "tui-v8-screenshot-nomesh.txt", "1")
}

// TestAppGoldenFleet/Channels/Goals/Memory/Lineage are the per-tab +
// drill-down goldens.
func TestAppGoldenFleet(t *testing.T)    { goldenFor(t, "tui-v8-fleet.txt", "2") }
func TestAppGoldenChannels(t *testing.T) { goldenFor(t, "tui-v8-channels.txt", "3") }
func TestAppGoldenGoals(t *testing.T)    { goldenFor(t, "tui-v8-goals.txt", "4") }
func TestAppGoldenMemory(t *testing.T)   { goldenFor(t, "tui-v8-memory.txt", "5") }
func TestAppGoldenLineage(t *testing.T)  { goldenFor(t, "tui-v8-lineage.txt", "enter") }
