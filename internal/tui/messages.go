// messages.go — the v8 PR-F animation cadence messages + self-
// rescheduling tea.Tick commands (plan §12 lists messages.go as the
// tea.Msg home: "ThoughtMsg, QuorumMsg, TickMsg, …"; PR-F adds the five
// animation TickMsgs — the data/Thought/Quorum msgs are PR-G).
//
// Five INDEPENDENT cadences, exact periods from handoff §8.4 + the jsx:
//
//	spinTickMsg    80ms  — one tick drives ALL three spinners (dots/arc/
//	                       bouncing); jsx single `setInterval(…,80)`
//	                       (rufio-bubbletea-v8.jsx 166 / rufio-graphs 163).
//	meshTickMsg    90ms  — mesh particle flow + node pulse rings; jsx
//	                       MeshGrid `setInterval(…,90)` (rufio-graphs 126).
//	typingTickMsg  220ms — typing-dots 3-state cycle (handoff §8.4).
//	seriesTickMsg  500ms — sparkline series shift (jsx `useTickedSeries(
//	                       36, 500)`, rufio-bubbletea-v8.jsx 169).
//	caretTickMsg   500ms  — caret blink TOGGLE. The jsx `r-blink 1s
//	                       steps(1)` with `50% { opacity: 0 }` (rufio-
//	                       styles.css 454) is a 1000ms PERIOD that is ON
//	                       for 500ms then OFF for 500ms. handoff §9 maps
//	                       this exactly: "toggle its visibility on a
//	                       500ms tick". So the tea.Tick fires every
//	                       500ms (the toggle/half-period) and the caret
//	                       is ON when anim.caret is EVEN, OFF when ODD —
//	                       a full ON+OFF blink CYCLE is 1000ms, 50% duty
//	                       (the "1000ms" in handoff §8.4 is the cycle
//	                       period, not the tick interval). It is a
//	                       SEPARATE cadence/counter from seriesTickMsg
//	                       even though both are 500ms — independent
//	                       phases, independent Msgs (the plan wants one
//	                       Msg + one counter per cadence).
//
// Each Msg is a distinct empty struct so Update's type switch routes it
// to exactly one counter; each tick cmd RE-SCHEDULES itself (returns the
// same Msg again after its period) so the cadence is perpetual without a
// central clock. tea.Tick is the ONLY place a wall-clock duration
// appears — the live program loop fires it; TESTS deliver the Msgs
// directly (deterministic, no sleeps), per the plan.
//
// ADD-ONLY: these Msgs are produced/consumed only by the v8 App
// (preview-gated); the old internal/tui path is untouched until PR-G.
package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The five animation cadence periods. Verbatim from handoff §8.4 / the
// jsx setInterval / keyframe durations cited in the file doc comment.
const (
	spinPeriod   = 80 * time.Millisecond  // spinners (dots/arc/bouncing)
	meshPeriod   = 90 * time.Millisecond  // mesh particles + pulse rings
	typingPeriod = 220 * time.Millisecond // typing-dots 3-state
	seriesPeriod = 500 * time.Millisecond // sparkline series shift
	// caretPeriod is the 500ms blink TOGGLE interval (NOT 1000ms): the
	// caret flips ON↔OFF every 500ms so a full ON(500ms)+OFF(500ms)
	// cycle is 1000ms at 50% duty — the faithful `r-blink 1s steps(1)`
	// + handoff §9 "toggle on a 500ms tick". Distinct cadence/counter
	// from seriesPeriod despite the equal duration.
	caretPeriod = 500 * time.Millisecond
)

// Per-cadence empty Msg structs. Empty (no payload) because the cadence
// itself is the signal — Update advances that cadence's counter and
// re-arms. Distinct types so the Update type switch is unambiguous.
type (
	spinTickMsg   struct{} // 80ms  → anim.spin++
	meshTickMsg   struct{} // 90ms  → anim.mesh++
	typingTickMsg struct{} // 220ms → anim.typing++
	seriesTickMsg struct{} // 500ms → anim.series++
	caretTickMsg  struct{} // 500ms toggle → anim.caret++ (even=ON, odd=OFF)
)

// tickEvery returns a tea.Cmd that emits msg once after d. Update
// returns tickEvery(period, sameMsg) again on receipt, so each cadence
// re-arms itself perpetually (the standard Bubble Tea self-tick
// pattern). The closure ignores the tea.Tick time argument — the
// counter, not the wall-clock instant, is the animation state (keeps
// the model a pure function of tick COUNT, which is what makes the
// frame-progression tests deterministic).
func tickEvery(d time.Duration, msg tea.Msg) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return msg })
}

// animCmds is the full set of cadence arming commands — one per period.
// Init() batches these; each re-arms itself in Update. Centralised so
// Init and the re-arm sites cannot drift apart.
func animCmds() tea.Cmd {
	return tea.Batch(
		tickEvery(spinPeriod, spinTickMsg{}),
		tickEvery(meshPeriod, meshTickMsg{}),
		tickEvery(typingPeriod, typingTickMsg{}),
		tickEvery(seriesPeriod, seriesTickMsg{}),
		tickEvery(caretPeriod, caretTickMsg{}),
	)
}
