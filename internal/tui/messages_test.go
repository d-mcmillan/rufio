// messages_test.go — deterministic tests for the five PR-F animation
// cadences: the per-cadence tea.Msg types, Init() arming one
// self-rescheduling tea.Tick per period, and Update() advancing the
// right counter + re-arming. NO wall-clock, NO sleeps, NO tea runtime —
// the Msgs are delivered directly (the plan: "TESTS drive the Msgs
// directly").
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestInitArmsAllCadences asserts Init() returns the PR-G1 batch:
// tea.Batch(animCmds(), startWatcherCmd, PollDaemonOnline) — three
// top-level children, where animCmds() is itself the nested batch of
// the FIVE self-rescheduling animation cadences (80/90/220/500/1000ms).
// PR-F flipped Init from nil → the cadence batch; PR-G1 wraps it with
// the live watcher + daemon poll (the cadences are unchanged + still 5,
// just one batch-level deeper).
func TestInitArmsAllCadences(t *testing.T) {
	a, _ := NewApp("/r")
	cmd := a.Init()
	if cmd == nil {
		t.Fatalf("Init() must arm the cadences + watcher + poll (PR-G1), got nil")
	}
	// tea.Batch yields a tea.BatchMsg of its child cmds. PR-G1 Init has
	// 3 children: the animCmds() sub-batch, the watcher cmd, the poll.
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init() cmd should yield a tea.BatchMsg, got %T", msg)
	}
	if len(batch) != 3 {
		t.Fatalf("Init() must batch exactly 3 (animCmds, watcher, poll), got %d", len(batch))
	}
	// The animation cadences are still EXACTLY 5 — assert via animCmds()
	// directly (the same sub-batch Init embeds), so the cadence-count
	// guarantee is preserved independent of the PR-G1 wrapping.
	animMsg := animCmds()()
	animBatch, ok := animMsg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("animCmds() should yield a tea.BatchMsg, got %T", animMsg)
	}
	if len(animBatch) != 5 {
		t.Errorf("animCmds() must arm exactly 5 cadences (80/90/220/500/1000ms), got %d", len(animBatch))
	}
}

// TestUpdateAdvancesEachCounterAndRearms drives each cadence Msg
// directly and asserts (a) ONLY that cadence's counter advances and
// (b) Update returns a non-nil cmd (the re-arm) so the cadence keeps
// ticking. The five counters are independent.
func TestUpdateAdvancesEachCounterAndRearms(t *testing.T) {
	a, _ := NewApp("/r")

	type probe struct {
		name string
		msg  tea.Msg
		get  func(App) int
	}
	probes := []probe{
		{"spin", spinTickMsg{}, func(x App) int { return x.anim.spin }},
		{"mesh", meshTickMsg{}, func(x App) int { return x.anim.mesh }},
		{"typing", typingTickMsg{}, func(x App) int { return x.anim.typing }},
		{"series", seriesTickMsg{}, func(x App) int { return x.anim.series }},
		{"caret", caretTickMsg{}, func(x App) int { return x.anim.caret }},
	}

	for _, p := range probes {
		before := a
		m, cmd := a.Update(p.msg)
		got := m.(App)
		if cmd == nil {
			t.Errorf("%s: Update must re-arm (return a non-nil cmd) so the cadence keeps ticking", p.name)
		}
		// The targeted counter advanced by exactly 1.
		if p.get(got) != p.get(before)+1 {
			t.Errorf("%s: counter = %d, want %d (advance by 1)", p.name, p.get(got), p.get(before)+1)
		}
		// No OTHER counter moved (the five cadences are independent).
		for _, q := range probes {
			if q.name == p.name {
				continue
			}
			if q.get(got) != q.get(before) {
				t.Errorf("%s msg moved the %s counter (%d→%d) — cadences must be independent",
					p.name, q.name, q.get(before), q.get(got))
			}
		}
		a = got // accumulate so the next probe sees the prior advance
	}
}

// TestAnimZeroValueIsFrame0 asserts a freshly constructed App has every
// animation counter at 0 (the frame-0 state every golden test renders —
// drive() never delivers a tick Msg). This is the structural guarantee
// behind the byte-identical-goldens invariant.
func TestAnimZeroValueIsFrame0(t *testing.T) {
	a, _ := NewApp("/r")
	if a.anim != (anim{}) {
		t.Errorf("fresh App anim = %+v, want zero value (frame-0)", a.anim)
	}
}
