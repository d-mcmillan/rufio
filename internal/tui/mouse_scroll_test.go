package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/d-mcmillan/rufio/internal/tui/styles"
	"github.com/muesli/termenv"
)

// ─────────────────────────────────────────────────────────────────────
// #137 mouse-wheel substrate-feed scrollback — TDD spec.
//
// `rufio tui` now captures the wheel (tea.WithMouseCellMotion in
// cli/tui.go) so the wheel scrolls the feed IN-PANE instead of leaking
// into the terminal's native alt-screen scrollback (the "gap on
// scroll"). The wheel reuses the SAME #134 offset/clamp seam as
// PgUp/PgDn — only the step differs (scrollWheelStep, a small per-notch
// nudge, vs the near-screenful scrollPage). Like PgUp/PgDn (and unlike
// Home/End, which stay composer readline motion) the wheel scrolls in
// BOTH nav and compose mode. Every NON-wheel mouse event is a safe
// no-op: the model is returned unchanged, no panic, nothing swallowed.
// ─────────────────────────────────────────────────────────────────────

// wheelMsg builds the tea.MouseMsg the v1.3.10 runtime delivers for a
// wheel notch: a press-action event whose Button is WheelUp/WheelDown
// (confirmed against bubbletea@v1.3.10/mouse.go — MouseMsg is a
// MouseEvent alias with Action MouseAction / Button MouseButton; the
// wheel buttons are MouseButtonWheelUp / MouseButtonWheelDown and a
// notch arrives as MouseActionPress).
func wheelMsg(up bool) tea.MouseMsg {
	btn := tea.MouseButtonWheelDown
	if up {
		btn = tea.MouseButtonWheelUp
	}
	return tea.MouseMsg{Action: tea.MouseActionPress, Button: btn}
}

// clickMsg is a representative NON-wheel mouse event (a left-button
// press) — the no-op path: it must NOT touch scrollOffset, must not
// panic, and must not disturb any other state.
func clickMsg() tea.MouseMsg {
	return tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

// TestWheelUpScrollsOlderByStep: from the live tail a wheel-up notch
// moves the render window toward older content by EXACTLY
// scrollWheelStep, and the rendered frame visibly changes (older lines
// scroll into view).
func TestWheelUpScrollsOlderByStep(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	app := driveInjected(t, 120, 40, scrolledThread(60))
	if app.scrollOffset != 0 {
		t.Fatalf("precondition: start at live tail (offset 0), got %d", app.scrollOffset)
	}
	liveFrame := stripSGR(app.View())

	m, _ := app.Update(wheelMsg(true))
	app = m.(App)
	if app.scrollOffset != scrollWheelStep {
		t.Errorf("wheel-up from live must increase offset by exactly scrollWheelStep=%d, got %d",
			scrollWheelStep, app.scrollOffset)
	}
	if stripSGR(app.View()) == liveFrame {
		t.Errorf("wheel-up must visibly scroll the feed (older content), frame byte-identical to live")
	}

	// A second notch advances by another step (it accumulates like PgUp).
	m, _ = app.Update(wheelMsg(true))
	app = m.(App)
	if app.scrollOffset != 2*scrollWheelStep {
		t.Errorf("two wheel-up notches must be 2*scrollWheelStep=%d, got %d", 2*scrollWheelStep, app.scrollOffset)
	}
}

// TestWheelDownScrollsTowardLiveAndFloorsAtZero: wheel-down decreases
// the offset by scrollWheelStep and never goes below 0 (can't scroll
// past live), mirroring PgDn's floor.
func TestWheelDownScrollsTowardLiveAndFloorsAtZero(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	app := driveInjected(t, 120, 40, scrolledThread(60))

	// Scroll up a few notches first so there is room to come back down.
	for i := 0; i < 4; i++ {
		m, _ := app.Update(wheelMsg(true))
		app = m.(App)
	}
	up := app.scrollOffset
	if up != 4*scrollWheelStep {
		t.Fatalf("precondition: 4 wheel-up notches => 4*step=%d, got %d", 4*scrollWheelStep, up)
	}

	m, _ := app.Update(wheelMsg(false))
	app = m.(App)
	if app.scrollOffset != up-scrollWheelStep {
		t.Errorf("wheel-down must decrease by exactly scrollWheelStep: was %d want %d, got %d",
			up, up-scrollWheelStep, app.scrollOffset)
	}

	// Many more wheel-down notches must FLOOR at 0 (live), never negative.
	for i := 0; i < 50; i++ {
		m, _ = app.Update(wheelMsg(false))
		app = m.(App)
	}
	if app.scrollOffset != 0 {
		t.Errorf("wheel-down past live must floor at 0 (live tail), got %d", app.scrollOffset)
	}
}

// TestWheelUpClampsAtOldest: repeated wheel-up can never scroll past the
// absolute-oldest line — the offset is bounded by the SAME clamp
// PgUp/Home use (chatScrollMax / windowLines), the single source of
// truth. After enough notches the rendered window pins at the oldest and
// the offset stops growing past the real max.
func TestWheelUpClampsAtOldest(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	app := driveInjected(t, 120, 40, scrolledThread(60))

	// The real upper bound for THIS frame (the value windowLines clamps
	// to every paint — the single clamp seam #134 established).
	maxOff := app.chatScrollMax()
	if maxOff <= 0 {
		t.Fatalf("precondition: feed must overflow so there is a positive max offset, got %d", maxOff)
	}

	// Spin the wheel up far past the max.
	var prev string
	for i := 0; i < 400; i++ {
		m, _ := app.Update(wheelMsg(true))
		app = m.(App)
	}
	prev = stripSGR(app.View())

	// The rendered window is pinned at the oldest: another notch does NOT
	// change the visible frame (windowLines re-clamps — like PgUp at the
	// top), and Home (the established absolute-oldest jump) renders the
	// SAME oldest window, proving the wheel reuses the same clamp.
	m, _ := app.Update(wheelMsg(true))
	app = m.(App)
	if stripSGR(app.View()) != prev {
		t.Errorf("wheel-up at the oldest window must not move further (clamped like PgUp)")
	}
	ref := driveInjected(t, 120, 40, scrolledThread(60))
	mh, _ := ref.Update(keyMsg("home"))
	ref = mh.(App)
	if stripSGR(app.View()) != stripSGR(ref.View()) {
		t.Errorf("wheel-up clamped window must equal the Home (absolute-oldest) window — same clamp seam")
	}
}

// TestWheelScrollsInComposeWithoutDisturbingBuffer: the wheel scrolls
// the feed in COMPOSE mode too (it is not text-motion, so unlike
// Home/End it is safe in compose — mirrors PgUp/PgDn), and it must NOT
// disturb the composer buffer text.
func TestWheelScrollsInComposeWithoutDisturbingBuffer(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	com := driveInjectedMode(t, 120, 40, scrolledThread(60), true)
	if !com.composeMode {
		t.Fatalf("precondition: app must be in compose mode")
	}
	// Type some buffer text the wheel must not perturb.
	for _, r := range "hello world" {
		m, _ := com.Update(keyMsg(string(r)))
		com = m.(App)
	}
	wantBuf := com.composeTA.Value()
	if wantBuf == "" {
		t.Fatalf("precondition: composer buffer should hold typed text")
	}

	m, _ := com.Update(wheelMsg(true))
	com = m.(App)
	if com.scrollOffset != scrollWheelStep {
		t.Errorf("compose wheel-up must scroll older by scrollWheelStep=%d without mode-hop, got %d",
			scrollWheelStep, com.scrollOffset)
	}
	if !com.composeMode {
		t.Errorf("wheel must NOT exit compose mode")
	}
	if got := com.composeTA.Value(); got != wantBuf {
		t.Errorf("wheel must NOT disturb the composer buffer: was %q now %q", wantBuf, got)
	}

	// Wheel-down in compose works too (mirrors PgDn).
	m, _ = com.Update(wheelMsg(false))
	com = m.(App)
	if com.scrollOffset != 0 {
		t.Errorf("compose wheel-down must return toward live (0 here), got %d", com.scrollOffset)
	}
	if got := com.composeTA.Value(); got != wantBuf {
		t.Errorf("wheel-down must NOT disturb the composer buffer: was %q now %q", wantBuf, got)
	}
}

// TestNonWheelMouseEventIsNoOp: a NON-wheel mouse event (a left click,
// representative of clicks/motion/drag/other buttons) is a safe no-op —
// scrollOffset unchanged, no panic, the rest of the model byte-stable
// (the rendered frame is identical before and after). This guards that
// the new tea.MouseMsg case does not swallow or corrupt other handling.
func TestNonWheelMouseEventIsNoOp(t *testing.T) {
	styles.SetProfile(termenv.Ascii)

	// NAV mode.
	nav := driveInjected(t, 120, 40, scrolledThread(60))
	beforeOff := nav.scrollOffset
	beforeFrame := stripSGR(nav.View())
	m, _ := nav.Update(clickMsg())
	nav = m.(App)
	if nav.scrollOffset != beforeOff {
		t.Errorf("non-wheel mouse event must NOT change scrollOffset: was %d now %d", beforeOff, nav.scrollOffset)
	}
	if got := stripSGR(nav.View()); got != beforeFrame {
		t.Errorf("non-wheel mouse event must be a pure no-op (frame unchanged)")
	}

	// COMPOSE mode: also a no-op AND the composer buffer is untouched.
	com := driveInjectedMode(t, 120, 40, scrolledThread(60), true)
	for _, r := range "draft text" {
		mm, _ := com.Update(keyMsg(string(r)))
		com = mm.(App)
	}
	wantBuf := com.composeTA.Value()
	cOff := com.scrollOffset
	mm, _ := com.Update(clickMsg())
	com = mm.(App)
	if com.scrollOffset != cOff {
		t.Errorf("compose non-wheel mouse event must NOT change scrollOffset: was %d now %d", cOff, com.scrollOffset)
	}
	if !com.composeMode {
		t.Errorf("non-wheel mouse event must NOT exit compose mode")
	}
	if got := com.composeTA.Value(); got != wantBuf {
		t.Errorf("non-wheel mouse event must NOT disturb the composer buffer: was %q now %q", wantBuf, got)
	}
}

// ─────────────────────────────────────────────────────────────────────
// #189 mouse-fragment leak guard — TDD spec.
//
// SGR mouse sequences (\x1b[<35;100;20M) are normally captured as
// tea.MouseMsg (#137, tea.WithMouseCellMotion in cli/tui.go). But a
// fragmented Read() boundary can split the sequence so the bubbletea
// parser emits the prefix as KeyMsg{Alt:true, Runes:['[']} and the
// tail as KeyMsg{Type:KeyRunes, Runes:[<,3,5,;,...,M]}. Without the
// #189 guard those land in the composer textarea, which sanitises
// \x1b away but inserts the printable bytes — the "occasional
// escape-sequence garbage in compose" the operator reported.
//
// The guard (isMouseFragmentLeak) drops both shapes BEFORE handleKey
// so neither the nav keymap nor the textarea sees them. These tests
// pin the routing: a fragment-shaped KeyMsg must NOT mutate the
// composer buffer, must NOT crash, and must not disturb scrollOffset
// or composeMode. Real keystrokes (`<3`, `;)`, `M`, lone `[`, single
// printable runes) MUST still reach the buffer — the patterns are
// specific enough to avoid false positives.
// ─────────────────────────────────────────────────────────────────────

// TestMouseFragmentPrefixDoesNotLeakIntoCompose: the CSI-prefix orphan
// (Alt+`[`) that a chunked SGR mouse read leaves behind must NOT be
// inserted into the composer buffer. Before #189 this was textarea's
// default-rune-insert fallback → a literal `[` appeared.
func TestMouseFragmentPrefixDoesNotLeakIntoCompose(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	com := driveInjectedMode(t, 120, 40, scrolledThread(60), true)
	if !com.composeMode {
		t.Fatalf("precondition: app must be in compose mode")
	}
	// Type a known buffer so we can detect any drift.
	for _, r := range "hello" {
		m, _ := com.Update(keyMsg(string(r)))
		com = m.(App)
	}
	wantBuf := com.composeTA.Value()
	wantOff := com.scrollOffset

	// Deliver the fragmented mouse-prefix: KeyMsg{Alt:true, Runes:['[']}.
	frag := tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune{'['}}
	m, _ := com.Update(frag)
	com = m.(App)
	if got := com.composeTA.Value(); got != wantBuf {
		t.Errorf("mouse-prefix fragment must NOT insert into composer buffer: was %q now %q", wantBuf, got)
	}
	if com.scrollOffset != wantOff {
		t.Errorf("mouse-prefix fragment must NOT touch scrollOffset: was %d now %d", wantOff, com.scrollOffset)
	}
	if !com.composeMode {
		t.Errorf("mouse-prefix fragment must NOT exit compose mode")
	}

	// Same shape for the SS3-prefix orphan (Alt+`O`).
	fragO := tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune{'O'}}
	m, _ = com.Update(fragO)
	com = m.(App)
	if got := com.composeTA.Value(); got != wantBuf {
		t.Errorf("SS3-prefix fragment must NOT insert into composer buffer: was %q now %q", wantBuf, got)
	}
}

// TestMouseFragmentTailDoesNotLeakIntoCompose: the SGR-tail leftover
// (`<35;100;20M`) that follows a chunked mouse prefix must NOT be
// inserted into the composer buffer. Before #189 textarea sanitised the
// \x1b away (already gone) but inserted the printable `<35;100;20M`.
func TestMouseFragmentTailDoesNotLeakIntoCompose(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	com := driveInjectedMode(t, 120, 40, scrolledThread(60), true)
	for _, r := range "hi" {
		m, _ := com.Update(keyMsg(string(r)))
		com = m.(App)
	}
	wantBuf := com.composeTA.Value()

	// Canonical SGR-tail shapes covering press / release / wheel rows.
	for _, tail := range []string{
		"<35;100;20M",
		"<0;42;7M",
		"<64;1;1m",
		"<65;200;50M",
	} {
		frag := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tail)}
		m, _ := com.Update(frag)
		com = m.(App)
		if got := com.composeTA.Value(); got != wantBuf {
			t.Errorf("SGR-tail fragment %q must NOT insert into composer buffer: was %q now %q", tail, wantBuf, got)
		}
	}
}

// TestX10MouseFragmentDoesNotLeakIntoCompose: the X10 mouse-tail
// (`M` + 3 printable bytes) must NOT be inserted into the composer
// buffer either. Same fragmented-Read failure mode but for legacy X10
// mouse mode.
func TestX10MouseFragmentDoesNotLeakIntoCompose(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	com := driveInjectedMode(t, 120, 40, scrolledThread(60), true)
	for _, r := range "ok" {
		m, _ := com.Update(keyMsg(string(r)))
		com = m.(App)
	}
	wantBuf := com.composeTA.Value()

	// X10: `M` + button + col + row, each byte +32. Pick safely-printable
	// trios.
	for _, tail := range []string{"M! \"", "M@AB", "Mabc"} {
		frag := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tail)}
		m, _ := com.Update(frag)
		com = m.(App)
		if got := com.composeTA.Value(); got != wantBuf {
			t.Errorf("X10 fragment %q must NOT insert into composer buffer: was %q now %q", tail, wantBuf, got)
		}
	}
}

// TestRealKeystrokesStillReachComposer: the fragment filter must NOT
// false-positive on real typing. Common shapes that LOOK mouse-fragment-
// ish (lone `[`, lone `M`, short `<3`, `;)`) must still appear in the
// buffer. The filter shapes are deliberately narrower than these so they
// fall through to the textarea unchanged.
func TestRealKeystrokesStillReachComposer(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	com := driveInjectedMode(t, 120, 40, scrolledThread(60), true)

	// Each entry: rune(s) to deliver as a single KeyRunes (no Alt),
	// and what must end up in the buffer after that delivery.
	cases := []struct {
		typed string
		want  string
	}{
		{typed: "[", want: "["},     // lone bracket
		{typed: "M", want: "[M"},    // lone M after the [
		{typed: "<3", want: "[M<3"}, // short <digit (not a mouse tail — no `;` no terminator)
		{typed: ";)", want: "[M<3;)"},
	}
	for _, c := range cases {
		frag := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(c.typed)}
		m, _ := com.Update(frag)
		com = m.(App)
		if got := com.composeTA.Value(); got != c.want {
			t.Errorf("real keystroke %q must reach buffer: want %q got %q", c.typed, c.want, got)
		}
	}
}

// TestWheelWorksInNavModeSubstrate: #189 Bug 2 — scroll wheel must
// function in NAV mode on the substrate view (composeMode == false), not
// only in compose. The user reviewing history after `esc` should still
// be able to wheel-scroll without re-entering compose.
//
// This bundles the gates the existing TestWheelUpScrollsOlderByStep
// covers (driveInjected = NAV mode), but states the contract explicitly:
// composeMode is false AND scroll wheel mutates scrollOffset by the
// scrollWheelStep, in both directions, with the same floor as PgDn.
func TestWheelWorksInNavModeSubstrate(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	nav := driveInjected(t, 120, 40, scrolledThread(60))
	if nav.composeMode {
		t.Fatalf("precondition: driveInjected must start in nav mode (composeMode false)")
	}
	if nav.view != viewSubstrate {
		t.Fatalf("precondition: driveInjected must start on substrate view, got %v", nav.view)
	}
	if nav.scrollOffset != 0 {
		t.Fatalf("precondition: start at live tail (offset 0), got %d", nav.scrollOffset)
	}

	// Wheel-up in NAV must scroll older by exactly scrollWheelStep.
	m, _ := nav.Update(wheelMsg(true))
	nav = m.(App)
	if nav.scrollOffset != scrollWheelStep {
		t.Errorf("nav wheel-up must scroll by scrollWheelStep=%d, got %d", scrollWheelStep, nav.scrollOffset)
	}
	if nav.composeMode {
		t.Errorf("wheel must NOT toggle composeMode in nav (was false, now true)")
	}

	// Wheel-down in NAV must reduce by scrollWheelStep and floor at 0.
	m, _ = nav.Update(wheelMsg(false))
	nav = m.(App)
	if nav.scrollOffset != 0 {
		t.Errorf("nav wheel-down must return to live (0), got %d", nav.scrollOffset)
	}
	// Past-live wheel-down must floor at 0 (mirrors PgDn floor).
	for i := 0; i < 20; i++ {
		m, _ = nav.Update(wheelMsg(false))
		nav = m.(App)
	}
	if nav.scrollOffset != 0 {
		t.Errorf("nav wheel-down past live must floor at 0, got %d", nav.scrollOffset)
	}
}

// TestIsMouseFragmentLeakUnitMatrix exhaustively pins the helper's
// contract: each fragment shape returns true; each real-keystroke shape
// returns false. Cheap and direct so a regression here is caught with
// one assertion line per case.
func TestIsMouseFragmentLeakUnitMatrix(t *testing.T) {
	type tc struct {
		name string
		msg  tea.KeyMsg
		want bool
	}
	cases := []tc{
		// Fragment shapes — must return true.
		{"alt+[ (CSI prefix)", tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune{'['}}, true},
		{"alt+O (SS3 prefix)", tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune{'O'}}, true},
		{"SGR press tail", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<35;100;20M")}, true},
		{"SGR release tail (m)", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<0;1;1m")}, true},
		{"X10 tail (4 runes)", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("M@AB")}, true},

		// Real keystrokes — must return false.
		{"lone [", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}}, false},
		{"lone M", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}}, false},
		{"<3 (short, no ;)", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<3")}, false},
		{"<3;)", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<3;)")}, false},
		{"alt+B (word motion)", tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune{'b'}}, false},
		// Alt+letter coverage beyond Alt+B: the compose keymap binds these
		// elsewhere (Alt+f = word-forward, Alt+backspace = delete word) so
		// the fragment filter MUST let them through unchanged.
		{"alt+f (word motion)", tea.KeyMsg{Type: tea.KeyRunes, Alt: true, Runes: []rune{'f'}}, false},
		{"alt+backspace (delete word)", tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}, false},
		{"plain word `hello`", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")}, false},
		{"X10-ish but 5 runes", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("M@ABC")}, false},
		// `<` alone, no digits, no terminator — not a tail. (3 runes <
		// the min length 4, so length gate catches this; explicit guard.)
		{"<;m short", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<;m")}, false},
	}
	for _, c := range cases {
		got := isMouseFragmentLeak(c.msg)
		if got != c.want {
			t.Errorf("%s: isMouseFragmentLeak got %v want %v (msg=%+v)", c.name, got, c.want, c.msg)
		}
	}
}

// TestWheelReusesSingleClampSeam pins the single-source-of-truth
// contract: the wheel and PgUp/PgDn move scrollOffset through the SAME
// scrollBy arithmetic + floor — only the step constant differs (wheel =
// scrollWheelStep, keys = scrollPage). It proves this WITHOUT needing
// scrollPage to be a multiple of the wheel step: (a) the wheel offset
// progression is exactly additive in scrollWheelStep (the identical
// accumulation scrollBy gives PgUp's scrollPage), and (b) wheel-down
// cancels wheel-up and floors at 0 EXACTLY as PgDn cancels PgUp — same
// seam, same lower bound, scaled only by the step.
func TestWheelReusesSingleClampSeam(t *testing.T) {
	styles.SetProfile(termenv.Ascii)

	// (a) Pure additive accumulation: k wheel-up notches land at exactly
	// k*scrollWheelStep — the same arithmetic shape PgUp gets from the
	// shared scrollBy (one PgUp == +scrollPage), proving the wheel feeds
	// the same offset seam, not a parallel implementation.
	w := driveInjected(t, 120, 40, scrolledThread(400))
	for k := 1; k <= 6; k++ {
		m, _ := w.Update(wheelMsg(true))
		w = m.(App)
		if w.scrollOffset != k*scrollWheelStep {
			t.Fatalf("wheel offset must be exactly k*scrollWheelStep after k notches: k=%d want %d got %d",
				k, k*scrollWheelStep, w.scrollOffset)
		}
	}

	// (b) Wheel-down cancels wheel-up and floors at 0 — byte-identical
	// behaviour to PgDn cancelling PgUp (the shared scrollBy floor). Drive
	// the SAME up/down sequence through the wheel and through PgUp/PgDn
	// scaled by their respective step counts and require identical end
	// offsets AND identical rendered frames.
	wheel := driveInjected(t, 120, 40, scrolledThread(400))
	keys := driveInjected(t, 120, 40, scrolledThread(400))

	ratio := scrollPage / scrollWheelStep
	if ratio < 1 {
		ratio = 1
	}
	// Up: `ratio*3` wheel notches ≈ 3 PgUps' worth of travel; then back
	// down further than we went (proves the shared 0-floor, not negative).
	for i := 0; i < ratio*3; i++ {
		m, _ := wheel.Update(wheelMsg(true))
		wheel = m.(App)
	}
	for i := 0; i < 3; i++ {
		m, _ := keys.Update(keyMsg("pgup"))
		keys = m.(App)
	}
	// Both now sit at +3 "pages" of travel via the SAME scrollBy. Wheel
	// offset == 3*ratio*scrollWheelStep; with ratio=scrollPage/step that
	// is ~3*scrollPage == the 3-PgUp offset (exact when step | page,
	// otherwise within one step — the seam is identical, only granularity
	// differs). The load-bearing assertion is the floor below.
	for i := 0; i < ratio*10; i++ {
		m, _ := wheel.Update(wheelMsg(false))
		wheel = m.(App)
	}
	for i := 0; i < 10; i++ {
		m, _ := keys.Update(keyMsg("pgdown"))
		keys = m.(App)
	}
	if wheel.scrollOffset != 0 {
		t.Errorf("wheel-down past live must floor at 0 via the shared scrollBy seam, got %d", wheel.scrollOffset)
	}
	if keys.scrollOffset != 0 {
		t.Errorf("PgDn past live must floor at 0 (same seam), got %d", keys.scrollOffset)
	}
	if sw, sk := stripSGR(wheel.View()), stripSGR(keys.View()); sw != sk {
		t.Errorf("wheel and key paths floored at 0 must render the IDENTICAL live frame (one seam):\n--- wheel ---\n%s\n--- keys ---\n%s", sw, sk)
	}
}

// TestKnownFalsePositive_SGRPasteShape pins — as INTENTIONAL — the one
// SGR false-positive surface the shape filter cannot disambiguate from a
// real mouse-event tail: a 4+ rune payload of the exact form
// `<\d+;\d+;\d+[Mm]` (e.g. `<1;2;3M` or `<0;0;0m`) pasted into compose
// will be dropped.
//
// The trade-off is deliberate. The filter EXISTS to stop intermittent
// SGR mouse fragments from leaking into the textarea after a chunk-
// boundary split (#189). Any check tight enough to admit `<1;2;3M` as
// "real typing" would have to admit a genuine fragment too, because
// shape is all we have at this layer — the runtime has already routed
// the bytes as a KeyMsg, so there is no MouseMsg context to lean on.
//
// We pin "fragment-shape wins" for short SGR-looking payloads. Single-
// rune keystrokes, math snippets like `<3` / `;)`, and prose without the
// full `<digits;digits;digits[Mm]` shape still reach the composer
// unchanged (see TestRealKeystrokesStillReachComposer +
// TestIsMouseFragmentLeakUnitMatrix). A user pasting a literal SGR-
// shaped string is the rare end of the trade.
func TestKnownFalsePositive_SGRPasteShape(t *testing.T) {
	// All three of these are real-typing/paste shapes a user might
	// produce. All three match the SGR-tail filter and so are dropped.
	// This test is here to ANNOUNCE THE LIMITATION, not to assert it is
	// the desired UX: if/when we have richer routing context we should
	// revisit. Until then: shape-filter wins.
	for _, payload := range []string{"<1;2;3M", "<0;0;0m", "<12;34;56M"} {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(payload)}
		if !isMouseFragmentLeak(msg) {
			t.Errorf("SGR-shape paste %q: pinned as a known false-positive (must return true). If this test starts failing the filter loosened — confirm intentional, then update this pin.", payload)
		}
	}
}

// TestKnownFalsePositive_X10Paste pins — as a documented limitation —
// the X10 false-positive surface: a 4-rune `M`+3-printable-bytes burst
// matching common English words ("Mary", "Mike") or short alphanumeric
// pastes ("M123", "MABC") arriving as a single KeyRunes event will be
// dropped.
//
// Why we keep the X10 branch despite this: modern terminals essentially
// never emit X10 mouse reports (they emit SGR — see the SGR tail check
// above), but bubbletea's parser can still surface them on legacy paths,
// and a leaked X10 tail in compose produces the same "occasional
// escape-sequence garbage" #189 reported. The shape is unavoidably
// ambiguous: `M` + 3 printable bytes is too narrow to disambiguate from
// short 4-char pastes without context the runtime has already discarded.
//
// The TODO on the X10 branch in app.go marks this for removal if we
// ever go SGR-only. Until then this pin documents that "Mary" / "Mike"
// / "M123" / "MABC" pasted as a single 4-rune burst will not reach the
// composer.
func TestKnownFalsePositive_X10Paste(t *testing.T) {
	for _, payload := range []string{"Mary", "Mike", "M123", "MABC"} {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(payload)}
		if !isMouseFragmentLeak(msg) {
			t.Errorf("X10-shape paste %q: pinned as a known false-positive (must return true). If this test starts failing the X10 branch tightened or was removed — confirm intentional, then update this pin.", payload)
		}
	}
}
