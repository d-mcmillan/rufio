package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/d-mcmillan/rufio/internal/tui/styles"
	"github.com/muesli/termenv"
)

// ─────────────────────────────────────────────────────────────────────
// #134 substrate-feed scrollback — TDD spec.
//
// Scrollback is a PURE-VIEW feature: a scrollOffset on the App + an
// offset-aware window helper + keys + a zero-delta affordance. The
// default (offset 0) render path MUST stay byte-identical (the goldens
// are frame-0, no keys driven), and topTruncate's 2-arg contract MUST
// be preserved (the existing panel-height-invariant test calls it
// directly and stays green untouched).
// ─────────────────────────────────────────────────────────────────────

// numbered builds an N-line string "L0\nL1\n…" so window math is exact.
func numbered(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "L" + itoa(i)
	}
	return strings.Join(lines, "\n")
}

// TestWindowLinesOffsetZeroEqualsTopTruncate: at offset 0 windowLines is
// EXACTLY the old topTruncate (the live-tail anchor — byte-identical).
func TestWindowLinesOffsetZeroEqualsTopTruncate(t *testing.T) {
	for _, n := range []int{0, 1, 5, 20, 100} {
		for _, maxRows := range []int{1, 3, 12, 50, 200} {
			s := numbered(n)
			if got, want := windowLines(s, maxRows, 0), topTruncate(s, maxRows); got != want {
				t.Errorf("windowLines(n=%d,max=%d,0)=%q != topTruncate=%q", n, maxRows, got, want)
			}
		}
	}
}

// TestTopTruncateStillLastAnchored: topTruncate keeps the LAST maxRows
// lines (the contract the existing invariant test depends on) — it must
// not regress when delegated to windowLines.
func TestTopTruncateStillLastAnchored(t *testing.T) {
	s := numbered(20)
	got := topTruncate(s, 5)
	want := "L15\nL16\nL17\nL18\nL19"
	if got != want {
		t.Errorf("topTruncate last-anchored broken: got %q want %q", got, want)
	}
	// n <= maxRows returns the input unchanged.
	if got := topTruncate(s, 50); got != s {
		t.Errorf("topTruncate(n<=max) must return input unchanged, got %q", got)
	}
}

// TestWindowLinesShiftsWindow: a positive offset scrolls the window UP
// (toward older lines) by exactly that many lines; the window stays
// maxRows tall.
func TestWindowLinesShiftsWindow(t *testing.T) {
	s := numbered(20) // L0..L19
	// offset 0 = live tail.
	if got, want := windowLines(s, 5, 0), "L15\nL16\nL17\nL18\nL19"; got != want {
		t.Errorf("offset 0: got %q want %q", got, want)
	}
	// offset 3 = scrolled up 3 lines.
	if got, want := windowLines(s, 5, 3), "L12\nL13\nL14\nL15\nL16"; got != want {
		t.Errorf("offset 3: got %q want %q", got, want)
	}
	// max offset = n-maxRows = 15 → the oldest window L0..L4.
	if got, want := windowLines(s, 5, 15), "L0\nL1\nL2\nL3\nL4"; got != want {
		t.Errorf("max offset: got %q want %q", got, want)
	}
}

// TestWindowLinesClampsBothEnds: a negative offset clamps to 0 (live
// tail) and an over-large offset clamps to n-maxRows (oldest window).
func TestWindowLinesClampsBothEnds(t *testing.T) {
	s := numbered(20)
	if got, want := windowLines(s, 5, -7), windowLines(s, 5, 0); got != want {
		t.Errorf("negative offset must clamp to 0: got %q want %q", got, want)
	}
	if got, want := windowLines(s, 5, 9999), windowLines(s, 5, 15); got != want {
		t.Errorf("over-large offset must clamp to n-maxRows: got %q want %q", got, want)
	}
}

// TestWindowLinesShortInputReturnsUnchanged: when the content already
// fits (n <= maxRows) the input is returned verbatim regardless of
// offset (nothing to scroll).
func TestWindowLinesShortInputReturnsUnchanged(t *testing.T) {
	s := numbered(4)
	for _, off := range []int{0, 1, 5, -3, 100} {
		if got := windowLines(s, 10, off); got != s {
			t.Errorf("short input (n<=max) offset %d must be unchanged, got %q", off, got)
		}
	}
}

// scrolledThread is a deterministic thread long enough that the feed
// overflows at the gate size so scroll keys have an effect.
func scrolledThread(n int) []ThreadMsg {
	rows := make([]ThreadMsg, n)
	for i := range rows {
		rows[i] = ThreadMsg{
			Who:  "claude-code",
			Role: "thought",
			Time: "12:00:0" + itoa(i%10),
			Kind: kindReply,
			Text: "event number " + itoa(i),
		}
	}
	rows[n-1].Last = true
	return rows
}

// TestSubstrateFoldReclampsScrollOffset: when a fresh (shorter)
// substrateLoadedMsg folds in, an out-of-range scrollOffset is clamped
// to the new max (mirrors the existing `selected` re-clamp) so the view
// can never point past the new content.
func TestSubstrateFoldReclampsScrollOffset(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	// driveInjected drops to NAV mode (where Home scrolls to oldest;
	// in compose Home is composer cursor motion — the #134
	// reconciliation, see TestComposeHomeEndStayComposerMotion).
	app := driveInjected(t, 120, 40, scrolledThread(60))
	// Scroll way up (Home → max).
	m, _ := app.Update(keyMsg("home"))
	app = m.(App)
	if app.scrollOffset <= 0 {
		t.Fatalf("Home must scroll up (offset>0), got %d", app.scrollOffset)
	}
	big := app.scrollOffset
	// A much shorter re-read folds in: the offset MUST be re-clamped
	// down (it cannot exceed the new max).
	m, _ = app.Update(substrateLoadedMsg{rows: scrolledThread(8)})
	app = m.(App)
	if app.scrollOffset >= big {
		t.Errorf("fold must re-clamp scrollOffset to the new (smaller) max: was %d still %d", big, app.scrollOffset)
	}
	if app.scrollOffset < 0 {
		t.Errorf("scrollOffset must never go negative, got %d", app.scrollOffset)
	}
}

// TestChromeByteIdenticalAtOffsetZero: the chat-chrome strip is
// byte-identical at offset 0 (no affordance) and the affordance text
// appears only when scrolled. This is the golden-stability guard at the
// chrome level (the goldens are the screen-level guard).
func TestChromeByteIdenticalAtOffsetZero(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	a := drive(t, 120, 40)          // nav, empty
	base := a.renderChatChrome(100) // offset 0 by construction
	if strings.Contains(base, "scrolled") {
		t.Fatalf("offset 0 chrome must NOT show the scrolled affordance:\n%s", base)
	}
	a.scrollOffset = 5
	scrolled := a.renderChatChrome(100)
	if scrolled == base {
		t.Errorf("offset>0 chrome must differ (show the affordance), got identical")
	}
	if !strings.Contains(scrolled, "scrolled") {
		t.Errorf("offset>0 chrome must contain the scrolled affordance, got:\n%s", scrolled)
	}
	// The affordance must NOT add a vertical line — the chrome strip is
	// still exactly the same number of lines (strip + bottom-rule).
	if lipgloss.Height(scrolled) != lipgloss.Height(base) {
		t.Errorf("affordance must not consume an extra chrome line: base %d lines, scrolled %d lines",
			lipgloss.Height(base), lipgloss.Height(scrolled))
	}
}

// TestEndKeyResetsScrollToLive: End (jump-to-live) zeroes the offset
// from any scrolled state, and PgDn never drives it negative.
func TestEndKeyResetsScrollToLive(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	app := driveInjected(t, 120, 40, scrolledThread(60))
	m, _ := app.Update(keyMsg("home")) // scroll all the way up
	app = m.(App)
	if app.scrollOffset == 0 {
		t.Fatalf("Home must move offset off live; got 0")
	}
	m, _ = app.Update(keyMsg("end"))
	app = m.(App)
	if app.scrollOffset != 0 {
		t.Errorf("End must reset scrollOffset to 0 (live tail), got %d", app.scrollOffset)
	}
	// PgDn at the live tail stays at 0 (never negative).
	m, _ = app.Update(keyMsg("pgdown"))
	app = m.(App)
	if app.scrollOffset != 0 {
		t.Errorf("PgDn at live tail must stay 0, got %d", app.scrollOffset)
	}
}

// TestPgUpScrollsInBothModes: PgUp scrolls older in BOTH compose and nav
// mode (the operator should not have to mode-hop to scroll the live
// debate), and plain up/k is UNCHANGED (does not scroll the feed).
func TestPgUpScrollsInBothModes(t *testing.T) {
	styles.SetProfile(termenv.Ascii)

	// NAV mode.
	navApp := driveInjected(t, 120, 40, scrolledThread(60))
	m, _ := navApp.Update(keyMsg("pgup"))
	navApp = m.(App)
	if navApp.scrollOffset <= 0 {
		t.Errorf("nav PgUp must scroll older (offset>0), got %d", navApp.scrollOffset)
	}

	// COMPOSE mode (the App's default — driveCompose stays in compose).
	comApp := driveInjectedMode(t, 120, 40, scrolledThread(60), true)
	if !comApp.composeMode {
		t.Fatalf("precondition: app must be in compose mode")
	}
	m, _ = comApp.Update(keyMsg("pgup"))
	comApp = m.(App)
	if comApp.scrollOffset <= 0 {
		t.Errorf("compose PgUp must scroll older (offset>0) without mode-hop, got %d", comApp.scrollOffset)
	}

	// Plain up/k is UNCHANGED: in nav it moves `selected`, never the
	// feed scroll.
	navApp2 := driveInjected(t, 120, 40, scrolledThread(60))
	before := navApp2.scrollOffset
	m, _ = navApp2.Update(keyMsg("up"))
	navApp2 = m.(App)
	if navApp2.scrollOffset != before {
		t.Errorf("plain up must NOT scroll the feed (offset unchanged): was %d now %d", before, navApp2.scrollOffset)
	}
}

// wrappingThread is scrolledThread but EVERY event's Text is long enough
// to WRAP to several rendered lines at the gate width. This makes the
// rendered-line count strictly EXCEED len(rows) by a wide margin, so the
// true max offset (renderedLineCount−threadH) FAR exceeds len(a.substrate)
// — a Home bound naively capped at len(a.substrate) would stop SHORT of
// the oldest wrapped line by ~hundreds of rows, the exact subtlety #134's
// Home fix must get right. The OLDEST event (index 0) keeps "number 0" in
// its text so the absolute-oldest rendered line is identifiable.
func wrappingThread(n int) []ThreadMsg {
	rows := scrolledThread(n)
	for i := range rows {
		long := "event number " + itoa(i)
		// ~20 extra words → wraps well past the wrapBody cap at the
		// gate content width, so each event renders to several lines.
		for j := 0; j < 20; j++ {
			long += " filler" + itoa(j)
		}
		rows[i].Text = long
	}
	return rows
}

// TestHomeThenPgDnPagesBackFromOldest is the #134 Minor regression: after
// Home the STORED scrollOffset must be the real clamped maximum (NOT the
// 1<<30 sentinel), so a subsequent PgDn actually pages the rendered
// window back toward live by exactly scrollPage lines. It also proves
// Home reaches the ABSOLUTE oldest line even when events wrap (the true
// max = renderedLineCount-threadH, which EXCEEDS len(a.substrate) here),
// so the bound must not be naively capped at len(a.substrate).
func TestHomeThenPgDnPagesBackFromOldest(t *testing.T) {
	styles.SetProfile(termenv.Ascii)

	rows := wrappingThread(40)
	app := driveInjected(t, 120, 40, rows)

	// Home → jump to the absolute oldest.
	m, _ := app.Update(keyMsg("home"))
	homeApp := m.(App)
	homeOffset := homeApp.scrollOffset

	// (a) Home must reach the absolute-oldest line: the oldest event's
	// text ("number 0") is visible in the rendered frame.
	homeFrame := stripSGR(homeApp.View())
	if !strings.Contains(homeFrame, "number 0") {
		t.Fatalf("Home must reveal the absolute-oldest event (\"number 0\"), frame:\n%s", homeFrame)
	}

	// The wrapping bound proof: the true max offset (what Home must
	// store) EXCEEDS len(a.substrate) because event 0 wraps to many
	// rendered lines. A fix that naively stored len(a.substrate) would
	// fail this — and would also stop Home short of the oldest line.
	if homeOffset <= len(homeApp.substrate) {
		t.Fatalf("Home offset must exceed len(substrate)=%d under wrapping (true max = renderedLineCount-threadH); got %d — bound is short, oldest line unreachable",
			len(homeApp.substrate), homeOffset)
	}

	// (b) PgDn after Home must page the window back toward live by
	// EXACTLY scrollPage lines. With the sentinel bug the stored offset
	// stays ~1<<30 so PgDn's -=scrollPage is invisible for ~1e8 presses;
	// the rendered frame would be byte-identical to the post-Home frame.
	m, _ = homeApp.Update(keyMsg("pgdown"))
	pgdnApp := m.(App)

	if pgdnApp.scrollOffset != homeOffset-scrollPage {
		t.Errorf("PgDn after Home must move toward live by exactly scrollPage: post-Home offset=%d, want post-PgDn=%d, got=%d",
			homeOffset, homeOffset-scrollPage, pgdnApp.scrollOffset)
	}

	pgdnFrame := stripSGR(pgdnApp.View())
	if pgdnFrame == homeFrame {
		t.Errorf("PgDn after Home must visibly move the rendered window (older scrolls off the top); frame is byte-identical to post-Home — Home left scroll stuck (#134 sentinel bug)")
	}

	// And the post-PgDn window is EXACTLY the window at (homeOffset -
	// scrollPage): render a reference App with that offset set directly
	// and require byte-identical frames (proves the move is precisely
	// scrollPage rendered lines, not merely "different").
	ref := homeApp
	ref.scrollOffset = homeOffset - scrollPage
	if refFrame := stripSGR(ref.View()); pgdnFrame != refFrame {
		t.Errorf("post-PgDn frame must equal the window at offset (homeOffset-scrollPage):\n--- got ---\n%s\n--- want ---\n%s", pgdnFrame, refFrame)
	}
}

// TestComposeHomeEndStayComposerMotion locks the #134 reconciliation
// (reported deviation): in COMPOSE mode Home/End remain the composer's
// LOAD-BEARING readline line-motion (TestComposerEdit_Readline
// "home/end motion") — they must NOT scroll the feed, or that existing
// contract would be weakened. PgUp/PgDn carry the full scroll range in
// compose; Home/End jump-to-extremes are NAV-mode only (where they were
// previously unbound, so that is purely additive). This proves the
// resolution holds at the App.Update seam.
func TestComposeHomeEndStayComposerMotion(t *testing.T) {
	styles.SetProfile(termenv.Ascii)

	// COMPOSE: Home/End must NOT scroll the feed (they reach the
	// textarea as cursor motion — the existing readline contract).
	com := driveInjectedMode(t, 120, 40, scrolledThread(60), true)
	if !com.composeMode {
		t.Fatalf("precondition: compose mode")
	}
	m, _ := com.Update(keyMsg("home"))
	com = m.(App)
	if com.scrollOffset != 0 {
		t.Errorf("compose Home must NOT scroll (composer cursor motion), offset=%d", com.scrollOffset)
	}
	m, _ = com.Update(keyMsg("end"))
	com = m.(App)
	if com.scrollOffset != 0 {
		t.Errorf("compose End must NOT scroll (composer cursor motion), offset=%d", com.scrollOffset)
	}

	// NAV: Home/End DO scroll (previously unbound there — additive).
	nav := driveInjected(t, 120, 40, scrolledThread(60))
	m, _ = nav.Update(keyMsg("home"))
	nav = m.(App)
	if nav.scrollOffset <= 0 {
		t.Errorf("nav Home must scroll to oldest (offset>0), got %d", nav.scrollOffset)
	}
	m, _ = nav.Update(keyMsg("end"))
	nav = m.(App)
	if nav.scrollOffset != 0 {
		t.Errorf("nav End must reset to live (offset 0), got %d", nav.scrollOffset)
	}
}
