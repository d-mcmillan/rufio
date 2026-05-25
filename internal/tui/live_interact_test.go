// live_interact_test.go — G-interact: the headless (a)-(e) controller
// self-check. Drives the v8 App against t.TempDir() REAL roots through
// the SAME tea.KeyMsg path the runtime delivers, proves the correct
// substrate record is written by the right lib as `me`, and proves the
// SUBSEQUENT App render reflects it. NO live fsnotify / NO wall-clock /
// NO real time.Now() assertions (the post-write reload one-shot is
// invoked directly — the deterministic seam — and the on-disk record is
// read back, never a golden-pinned ts). These are the keystone proofs
// the user eyeballs before G4.
package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/lib/confirm"
	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// appOnRoot builds a windowed App on a REAL root in COMPOSE mode (the
// App's default). The composer has focus so keystrokes go to the live
// buffer — exactly the operator's experience.
func appOnRoot(t *testing.T, root string) App {
	t.Helper()
	styles.SetProfile(termenv.Ascii)
	a, err := NewApp(root)
	if err != nil {
		t.Fatalf("NewApp(%s): %v", root, err)
	}
	m, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m.(App)
}

// typeAndSend feeds `text` rune-by-rune into the composer (the real
// KeyRunes path) then presses Enter, applying the returned post-write
// reload cmd's Msg back into the App (the snappy-feedback one-shot — this
// is what the live binary does; here it is deterministic, no fsnotify).
// Returns the folded App.
func typeAndSend(t *testing.T, app App, text string) App {
	t.Helper()
	for _, r := range text {
		var km tea.KeyMsg
		if r == ' ' {
			km = tea.KeyMsg{Type: tea.KeySpace}
		} else {
			km = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		}
		m, _ := app.Update(km)
		app = m.(App)
	}
	m, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = m.(App)
	if cmd != nil {
		// Apply EVERY batched one-shot Msg (loadSubstrate/Tabs/Mesh) so
		// the next render reflects the on-disk write — the live
		// post-write reload, deterministically.
		app = applyCmd(t, app, cmd)
	}
	return app
}

// applyCmd runs a (possibly batched) tea.Cmd and folds each produced Msg
// back into the App. tea.BatchMsg is a []tea.Cmd; recurse so the
// substrate/tabs/mesh reloads all land.
func applyCmd(t *testing.T, app App, cmd tea.Cmd) App {
	t.Helper()
	if cmd == nil {
		return app
	}
	msg := cmd()
	switch mm := msg.(type) {
	case tea.BatchMsg:
		for _, c := range mm {
			app = applyCmd(t, app, c)
		}
	case nil:
		// no-op
	default:
		m, _ := app.Update(msg)
		app = m.(App)
	}
	return app
}

// ── (a) free-text → broadcast operator @thought + re-render shows it ──

func TestInteract_A_FreeTextBroadcast(t *testing.T) {
	root := t.TempDir()
	app := appOnRoot(t, root)
	if !app.composeMode {
		t.Fatalf("the App must start in COMPOSE mode (composer focused)")
	}

	app = typeAndSend(t, app, "downgrade approved, notify the team")

	// (a.1) a real operator @thought is on disk: type=focus, scope=fleet,
	// subject=<resolved fallback `general` — no rows yet>, author=me.
	recs := readGDLFiles(t, filepath.Join(root, "live", "outbox", app.me))
	r := findRec(t, recs, "thought")
	if r.Get("author") != app.me {
		t.Errorf("broadcast author=%q, want %q (the resolved operator)", r.Get("author"), app.me)
	}
	if r.Get("type") != "focus" || r.Get("scope") != "fleet" {
		t.Errorf("broadcast must be type=focus scope=fleet (approved default), got type=%q scope=%q",
			r.Get("type"), r.Get("scope"))
	}
	if r.Get("subject") != opSubjectFallback {
		t.Errorf("no rows ⇒ subject must be the documented fallback %q, got %q",
			opSubjectFallback, r.Get("subject"))
	}
	if r.Get("content") != "downgrade approved, notify the team" {
		t.Errorf("broadcast content=%q", r.Get("content"))
	}

	// (a.2) the NEXT render shows it — as an operator (kindOp) row, and
	// the in-pane ✓ note is surfaced in the chrome strip.
	out := stripSGR(app.View())
	if !strings.Contains(out, "downgrade approved, notify the team") {
		t.Errorf("post-write render must show the new operator @thought:\n%s", out)
	}
	if !strings.Contains(out, "broadcast ✓") {
		t.Errorf("post-write render must surface the in-pane ✓ note:\n%s", out)
	}
	if app.composeText() != "" {
		t.Errorf("buffer must clear on a successful send, got %q", app.composeText())
	}
	// The new row is authored by `me` → it projects as a kindOp row.
	var sawOp bool
	for _, m := range app.substrate {
		if m.Kind == kindOp && strings.Contains(m.Text, "downgrade approved") {
			sawOp = true
		}
	}
	if !sawOp {
		t.Errorf("the operator-authored row must project as a kindOp row, substrate=%+v", app.substrate)
	}
}

// (a.subject) the broadcast subject resolves to the SELECTED row's
// subject when one is selected (the approved context-aware default).
func TestInteract_A_BroadcastSubjectFromSelectedRow(t *testing.T) {
	root := t.TempDir()
	// Seed an agent @thought about customer:5821 so the live read gives
	// the App a row whose subject is customer:5821.
	if err := emitThought(root, "claude-code", "customer:5821", "14-day silence — churn risk"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	app := appOnRoot(t, root) // hydrates the row + the parallel subject carry
	if len(app.substrate) == 0 {
		t.Fatalf("seeded thought should project a row")
	}
	app.selected = lastRowIndex(app.substrate)

	app = typeAndSend(t, app, "escalate this")

	// The operator @thought's subject must be the focused entity
	// (customer:5821 — the selected row's subject), NOT the fallback.
	var found bool
	for _, r := range readGDLFiles(t, filepath.Join(root, "live", "outbox", app.me)) {
		if r.Type == "thought" && r.Get("content") == "escalate this" {
			found = true
			if r.Get("subject") != "customer:5821" {
				t.Errorf("broadcast subject must resolve to the selected row's "+
					"subject customer:5821, got %q", r.Get("subject"))
			}
		}
	}
	if !found {
		t.Fatalf("the operator @thought `escalate this` was not written")
	}
}

// ── (b) /confirm (no arg) on the selected DECISION row → quorum advances

func TestInteract_B_SlashConfirmSelectedDecisionAdvancesQuorum(t *testing.T) {
	root := t.TempDir()
	// A real DECISION @thought by claude-code + TWO existing confirmers
	// (cursor, data-analyst) so the tally is 2/3 — one short of the
	// auto-promote threshold (autopromote.MinDistinctConfirmers). seeded
	// as a type=decision thought (not the operator focus broadcast) so
	// projectThread emits a roleDecision row that carries a Quorum.
	decID := seedDecision(t, root, "claude-code", "customer:5821", "offer downgrade, not churn-save")
	for _, who := range []string{"cursor", "data-analyst"} {
		if err := emitConfirm(root, who, decID, ""); err != nil {
			t.Fatalf("seed confirm by %s: %v", who, err)
		}
	}
	pre, _ := confirm.ReadAll(root, decID)
	if len(pre.Confirms) != 2 {
		t.Fatalf("setup: want 2 pre-confirmers, got %v", pre.Confirms)
	}

	app := appOnRoot(t, root)
	// Select the decision row (it is the only @thought row).
	for i := range app.substrate {
		if app.substrate[i].Role == roleDecision {
			app.selected = i
		}
	}
	if app.currentRowID() != decID {
		t.Fatalf("selected row id=%q, want the decision %q (the G3 carry)", app.currentRowID(), decID)
	}

	// /confirm with NO argument → acts on the selected decision row.
	app = typeAndSend(t, app, "/confirm")

	// (b.1) a real @confirm by `me` on that thought-id is on disk →
	// 3 distinct confirmers now (crossing the auto-promote threshold).
	post, _ := confirm.ReadAll(root, decID)
	var sawMe bool
	for _, c := range post.Confirms {
		if c == app.me {
			sawMe = true
		}
	}
	if !sawMe {
		t.Errorf("/confirm must append an @confirm by %q on %s; tally=%v", app.me, decID, post.Confirms)
	}
	if len(post.Confirms) != 3 {
		t.Errorf("after the operator confirm there must be 3 distinct confirmers "+
			"(2/3 → 3/3 — auto-promote crossing), got %d: %v", len(post.Confirms), post.Confirms)
	}

	// (b.2) the re-render shows the quorum advanced (the dots/denominator
	// reflect the new tally — the demo centerpiece).
	out := stripSGR(app.View())
	if !strings.Contains(out, "confirmed ✓") {
		t.Errorf("post-confirm render must surface the in-pane ✓ note:\n%s", out)
	}
	// The decision row's quorum Yes now includes me + the two seeds.
	var q *Quorum
	for i := range app.substrate {
		if app.substrate[i].Role == roleDecision {
			q = app.substrate[i].Quorum
		}
	}
	if q == nil {
		t.Fatalf("the decision row must carry a Quorum after confirms")
	}
	if len(q.Yes) != 3 {
		t.Errorf("the re-rendered decision quorum must show 3 confirmers (advanced "+
			"from 2), got Yes=%v Total=%d", q.Yes, q.Total)
	}
}

// ── (c) every other verb writes the right record via the right lib ────

func TestInteract_C_AllVerbsWriteCorrectRecordAsMe(t *testing.T) {
	t.Run("/refute <id>", func(t *testing.T) {
		root := t.TempDir()
		id := seedThought(t, root, "claude-code", "customer:5821", "a decision")
		app := appOnRoot(t, root)
		app = typeAndSend(t, app, "/refute "+id+" contradicts the prior preference")
		tally, _ := confirm.ReadAll(root, id)
		if len(tally.Refutes) == 0 || tally.Refutes[0] != app.me {
			t.Errorf("/refute <id> <reason> must append an @refute by %q, tally.Refutes=%v",
				app.me, tally.Refutes)
		}
	})

	t.Run("@agent → channel say or summon", func(t *testing.T) {
		root := t.TempDir()
		app := appOnRoot(t, root)
		app = typeAndSend(t, app, "@claude-code can you look at customer:5821")
		// No channel exists → the honest summon→accept handshake: a
		// pending @summon from me to claude-code is written.
		recs := readGDLFiles(t, filepath.Join(root, "live", "summons", "pending"))
		r := findRec(t, recs, "summon")
		if r.Get("from") != app.me || r.Get("to") != "claude-code" {
			t.Errorf("@agent with no channel must summon (from=%q to=%q, want %q→claude-code)",
				r.Get("from"), r.Get("to"), app.me)
		}
		out := stripSGR(app.View())
		if !strings.Contains(out, "summoned") {
			t.Errorf("@agent (no channel) must surface the summon note in-pane:\n%s", out)
		}
	})

	t.Run("@agent → say into a reusable channel", func(t *testing.T) {
		root := t.TempDir()
		chID := seedActiveChannel(t, root, "operator", "claude-code", "customer:5821")
		app := appOnRoot(t, root)
		app = typeAndSend(t, app, "@claude-code status?")
		recs := readGDLFiles(t, filepath.Join(root, "live", "channels", "active", chID, "messages"))
		// Issue #107: on-disk Type is "channel-message" (CLI verb still `say`).
		r := findRec(t, recs, "channel-message")
		if r.Get("by") != app.me || r.Get("content") != "status?" {
			t.Errorf("@agent with a reusable channel must say (by=%q content=%q)",
				r.Get("by"), r.Get("content"))
		}
	})

	t.Run("/goal", func(t *testing.T) {
		root := t.TempDir()
		app := appOnRoot(t, root)
		app = typeAndSend(t, app, "/goal stabilise customer:5821 before EOW")
		r := findRec(t, readGDLFiles(t, filepath.Join(root, "live", "goals", "active")), "goal")
		if r.Get("author") != app.me || r.Get("statement") != "stabilise customer:5821 before EOW" {
			t.Errorf("/goal must write an active @goal by %q, got author=%q statement=%q",
				app.me, r.Get("author"), r.Get("statement"))
		}
	})

	t.Run("/observe s p o", func(t *testing.T) {
		root := t.TempDir()
		app := appOnRoot(t, root)
		app = typeAndSend(t, app, "/observe customer:5821 prefers email-contact")
		r := findRec(t, readGDLFiles(t, filepath.Join(root, "learned")), "observation")
		if r.Get("author") != app.me || r.Get("subject") != "customer:5821" ||
			r.Get("predicate") != "prefers" || r.Get("object") != "email-contact" {
			t.Errorf("/observe must write an @observation triple by %q, got s=%q p=%q o=%q author=%q",
				app.me, r.Get("subject"), r.Get("predicate"), r.Get("object"), r.Get("author"))
		}
	})

	t.Run("/attend", func(t *testing.T) {
		root := t.TempDir()
		app := appOnRoot(t, root)
		app = typeAndSend(t, app, "/attend steering the churn arc | customer:5821")
		r := findRec(t, readGDLFiles(t, filepath.Join(root, "live", "attention")), "attention")
		if r.Get("agent") != app.me {
			t.Errorf("/attend must write attention for %q, got agent=%q", app.me, r.Get("agent"))
		}
	})

	t.Run("/summon", func(t *testing.T) {
		root := t.TempDir()
		app := appOnRoot(t, root)
		app = typeAndSend(t, app, "/summon claude-code customer:5821 discuss the downgrade")
		r := findRec(t, readGDLFiles(t, filepath.Join(root, "live", "summons", "pending")), "summon")
		if r.Get("from") != app.me || r.Get("to") != "claude-code" || r.Get("topic") != "customer:5821" {
			t.Errorf("/summon must write a pending @summon from %q, got from=%q to=%q topic=%q",
				app.me, r.Get("from"), r.Get("to"), r.Get("topic"))
		}
	})

	t.Run("/say", func(t *testing.T) {
		root := t.TempDir()
		chID := seedActiveChannel(t, root, "operator", "claude-code", "customer:5821")
		app := appOnRoot(t, root)
		app = typeAndSend(t, app, "/say "+chID+" here is an update")
		// Issue #107: on-disk Type is "channel-message" (CLI/TUI verb still `say`).
		r := findRec(t, readGDLFiles(t, filepath.Join(root, "live", "channels", "active", chID, "messages")), "channel-message")
		if r.Get("by") != app.me || r.Get("content") != "here is an update" {
			t.Errorf("/say must write an @channel-message by %q, got by=%q content=%q",
				app.me, r.Get("by"), r.Get("content"))
		}
	})
}

// ── (d) bad input → clean in-pane error, no crash, no exit code ───────

func TestInteract_D_BadInputCleanInPaneErrorNoCrash(t *testing.T) {
	t.Run("/nope unknown verb", func(t *testing.T) {
		root := t.TempDir()
		app := appOnRoot(t, root)
		app = typeAndSend(t, app, "/nope do a thing")
		if app.composeNote == "" || !strings.HasPrefix(app.composeNote, "✗") {
			t.Errorf("an unknown /verb must render a clean ✗ in-pane note, got %q", app.composeNote)
		}
		// Buffer PRESERVED so the operator can fix it; no crash, no write.
		if app.composeText() != "/nope do a thing" {
			t.Errorf("a bad command must preserve the buffer, got %q", app.composeText())
		}
		out := stripSGR(app.View())
		if !strings.Contains(out, "✗") {
			t.Errorf("the in-pane error must be visible in the render:\n%s", out)
		}
	})

	t.Run("/confirm with no selection", func(t *testing.T) {
		root := t.TempDir() // empty — no rows, nothing selected
		app := appOnRoot(t, root)
		app = typeAndSend(t, app, "/confirm")
		if app.composeNote == "" || !strings.HasPrefix(app.composeNote, "✗") {
			t.Errorf("/confirm with no selected row must render a clean ✗ note, got %q",
				app.composeNote)
		}
		// Nothing written.
		if _, err := os.Stat(filepath.Join(root, "live", "confirms")); err == nil {
			t.Errorf("/confirm with no target must NOT produce a write side effect")
		}
	})

	t.Run("empty buffer send is a harmless no-op", func(t *testing.T) {
		root := t.TempDir()
		app := appOnRoot(t, root)
		m, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
		app = m.(App)
		if cmd != nil {
			t.Errorf("an empty send must be a no-op (no reload cmd), got %T", cmd())
		}
		if app.composeNote != "" {
			t.Errorf("an empty send must not set a note, got %q", app.composeNote)
		}
	})
}

// ── (e) REGRESSION: nav still works; quit contract; read-only intact ──

func TestInteract_E_NavRegressionAndQuitContract(t *testing.T) {
	styles.SetProfile(termenv.Ascii)

	// esc toggles compose → nav; the existing nav keymap then works.
	a := drive(t, 120, 40) // drive() drops to nav (the regression harness)
	if a.composeMode {
		t.Fatalf("drive() must be in NAV mode for the legacy regression assertions")
	}
	// 1-5 switch tabs in nav mode.
	for _, c := range []struct {
		key  string
		want AppView
	}{{"1", viewSubstrate}, {"2", viewFleet}, {"3", viewChannels}, {"4", viewGoals}, {"5", viewMemory}} {
		got := drive(t, 120, 40, c.key)
		if got.view != c.want {
			t.Errorf("nav key %s: view=%q want %q", c.key, got.view, c.want)
		}
	}
	// tab / jk / ? still work in nav mode.
	if got := drive(t, 120, 40, "tab"); got.view != viewFleet {
		t.Errorf("nav `tab` must cycle to fleet, got %q", got.view)
	}
	if got := drive(t, 120, 40, "?"); got.overlay != overlayHelp {
		t.Errorf("nav `?` must open help, got overlay=%q", got.overlay)
	}
	// `i` returns nav → compose.
	if got := drive(t, 120, 40, "i"); !got.composeMode {
		t.Errorf("nav `i` must return to COMPOSE mode")
	}

	// The read-only chat/mesh/tabs + composer still render (console
	// usable); the v8 frame is intact (2 panels).
	out := stripSGR(drive(t, 120, 40).View())
	for _, want := range []string{"◆ #substrate", "◆ MESH", "ROUTING", "⏎ send"} {
		if !strings.Contains(out, want) {
			t.Errorf("the read-only v8 frame must stay intact, missing %q:\n%s", want, out)
		}
	}

	// Quit contract: ctrl+c quits in compose; q quits in nav.
	c1, _ := NewApp("/r")
	if _, cmd := c1.Update(keyMsg("ctrl+c")); cmd == nil {
		t.Errorf("ctrl+c must ALWAYS quit (compose mode)")
	}
	c2, _ := NewApp("/r")
	mm, _ := c2.Update(keyMsg("esc")) // → nav
	if _, cmd := mm.(App).Update(keyMsg("q")); cmd == nil {
		t.Errorf("`q` must quit in NAV mode")
	}
}

// TestInteract_E_PostWriteReloadNeverRearmsWatcher proves the snappy
// post-write reload is a one-shot that NEVER re-arms the fsnotify drain
// (the exactly-once invariant — same exception class as substrateLoaded
// Msg/meshLoadedMsg/tabsLoadedMsg). A double re-arm would racily
// double-drain the watcher.
func TestInteract_E_PostWriteReloadNeverRearmsWatcher(t *testing.T) {
	root := t.TempDir()
	app := appOnRoot(t, root)

	// Wire a fake watcher drain so a re-arm would be observable.
	armed := 0
	app.watcherCmd = tea.Cmd(func() tea.Msg { armed++; return nil })

	// Send a broadcast (a successful write → returns the post-write
	// reload cmd). The reload cmd must be the loaders ONLY — never the
	// watcher drain (which would double-drain).
	for _, r := range "hello fleet" {
		m, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		app = m.(App)
	}
	m, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = m.(App)
	if cmd == nil {
		t.Fatalf("a successful send must return the post-write reload cmd")
	}
	// Execute the batched reload; none of its Msgs may be the watcher
	// drain (armed must stay 0 — the reload is loaders only).
	app = applyCmd(t, app, cmd)
	if armed != 0 {
		t.Errorf("the post-write reload must NOT invoke the watcher drain "+
			"(exactly-once invariant), armed=%d", armed)
	}

	// And the substrateLoadedMsg fold itself still never re-arms (the
	// proven drain-invariant exception — unchanged by G-interact).
	_, foldCmd := app.Update(substrateLoadedMsg{rows: pinnedThread()})
	if foldCmd != nil {
		t.Errorf("substrateLoadedMsg MUST NOT re-arm the watcher (drain "+
			"invariant), got %T", foldCmd)
	}
}

// TestInteract_E_ShiftEnterNewlineFixedHeight proves ⇧⏎ appends a
// newline to the buffer (multi-line) WITHOUT growing the composer's
// rendered footprint (the documented fixed-height contract — the
// structural gates depend on it).
func TestInteract_E_ShiftEnterNewlineFixedHeight(t *testing.T) {
	root := t.TempDir()
	app := appOnRoot(t, root)
	base := strings.Count(stripSGR(app.View()), "\n")

	for _, r := range "line one" {
		m, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		app = m.(App)
	}
	m, _ := app.Update(tea.KeyMsg{Type: tea.KeyShiftTab}) // sanity: not newline
	app = m.(App)
	m, _ = app.Update(keyMsg("shift+enter"))
	app = m.(App)
	for _, r := range "line two" {
		m, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		app = m.(App)
	}
	if !strings.Contains(app.composeText(), "\n") {
		t.Errorf("⇧⏎ must append a newline to the buffer (multi-line), got %q", app.composeText())
	}
	if got := strings.Count(stripSGR(app.View()), "\n"); got != base {
		t.Errorf("the composer must NOT grow vertically on a multi-line buffer "+
			"(fixed-height contract): render rows changed %d → %d", base, got)
	}
}
