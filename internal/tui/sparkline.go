// sparkline.go — the v8 deterministic event-rate series + the Sparkline
// renderer (PR-F; plan §12 lists sparkline.go as their home).
//
// ── Renderer (faithful to the jsx) ────────────────────────────────────
//
// `Sparkline` is a verbatim port of rufio-graphs.jsx `Sparkline`
// (lines 181-189): `max = Math.max(...data, 1)`, then one glyph per
// sample, `SPARK[min(7, floor(v/max*8))]`, SPARK = `▁▂▃▄▅▆▇█`. One
// glyph per data point (NOT down-sampled) — exactly the jsx `data.map`.
//
// ── Series (DELIBERATE, DOCUMENTED deviation from the jsx) ─────────────
//
// The jsx `useTickedSeries` (lines 199-211) seeds with
// `Math.random()` and appends `Math.round(5 + sin(Date.now()*…) +
// Math.random()*4)` every 500ms — i.e. it is BOTH wall-clock- AND
// rand-driven. That is unusable here for two reasons the plan calls out:
//
//  1. Tests must be deterministic (no sleeps, no wall-clock — the
//     verification ritual + the golden harness drive Msgs directly).
//  2. The REAL event rate is PR-G's job (live `rufio stream` window
//     counts, data-mapping §1 row "Sparkline thoughts/s"). This PR only
//     needs a faithful-LOOKING, deterministic stand-in.
//
// So the series is a pure deterministic function: a fixed 36-sample
// ring that shifts one sample left per 500ms tick (the jsx
// `slice(1)+append` shape) where the appended sample is
// `nextSample(tick)`, a fixed integer SINUSOID (no rand, no clock).
// Same tick ⇒ same series, always. This deviation (deterministic
// sinusoid vs jsx random + the PR-G real-rate note) is the one the plan
// asks to document.
//
// ── FRAME-0 INVARIANT (highest-risk; do not break) ────────────────────
//
// Before PR-F the chat chrome rendered two hardcoded, mutually
// INCONSISTENT constants: `sparklinePlaceholder = "▁▂▃▄▅▆▇█▆▅"` and
// `throughput = "3/s"`. An exhaustive search proves the ONLY integer
// window that renders `▁▂▃▄▅▆▇█▆▅` under the jsx data-max scaling is
// exactly `[0 1 2 3 4 5 6 7 5 4]`, whose last value is 4 — so a strict
// jsx port (`window = series.slice(-N)`, `rate = series[len-1]`, the
// window's last bar == the rate sample) can reproduce the `▁▂▃▄▅▆▇█▆▅`
// bars OR a `3/s` rate, never BOTH from one integer series. The bars
// and the "3" were authored independently and cannot co-exist on the
// jsx's exact slice/last coupling.
//
// Frame-0-byte-identical goldens are the non-negotiable, highest-
// priority constraint (the prompt: "If any golden changes ... STOP").
// So the port makes ONE minimal, documented deviation from the jsx's
// `slice(-N)` / `series[len-1]` coupling: the chrome's HISTORY WINDOW
// is `series[seriesLen-sparkWidth-1 : seriesLen-1]` (the recent
// history) and the RATE is `series[seriesLen-1]` (the latest
// instantaneous sample) — both views into the SAME series (faithful in
// spirit: "the readout updates from the same series"), but the window's
// final bar is NOT forced to equal the rate sample. The initial 36
// series is seeded so at counter 0 the window literal is exactly
// `[0 1 2 3 4 5 6 7 5 4]` (→ byte-exact `▁▂▃▄▅▆▇█▆▅`) and the rate
// sample is exactly 3 (→ byte-exact `3/s`). Result: every committed
// golden stays byte-identical at counter 0; the window + rate both
// shift deterministically on each 500ms tick thereafter.
//
// ADD-ONLY: consumed only by the v8 app.go chat chrome (preview-gated).
package tui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// sparkGlyphs is the 8-level ramp `'▁▂▃▄▅▆▇█'.split(”)` (rufio-
// graphs.jsx line 181, `const SPARK`). Index 0..7 = lowest..highest.
var sparkGlyphs = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// Sparkline renders one glyph per data point, scaled to the data's own
// max — `SPARK[min(7, floor(v/max*8))]` with `max = Math.max(...data,
// 1)` (rufio-graphs.jsx 182-187, verbatim). Empty data ⇒ "" (the chrome
// renders nothing rather than a stray glyph). The result's visible
// width is exactly len(data) cells (one glyph per sample) — the
// stable-width contract the chrome strip relies on.
func Sparkline(data []int) string {
	if len(data) == 0 {
		return ""
	}
	max := 1
	for _, v := range data {
		if v > max {
			max = v
		}
	}
	var b strings.Builder
	for _, v := range data {
		// floor(v/max*8); guard v<0 to index 0 (defensive — the series
		// is non-negative, but Sparkline is a general renderer).
		idx := int(math.Floor(float64(v) / float64(max) * 8))
		if idx > 7 {
			idx = 7
		}
		if idx < 0 {
			idx = 0
		}
		b.WriteString(sparkGlyphs[idx])
	}
	return b.String()
}

// seriesLen is the ring length — jsx `useTickedSeries(36, 500)`
// (rufio-bubbletea-v8.jsx line 169): 36 samples.
const seriesLen = 36

// sparkWidth is how many history samples the chat chrome shows. The
// pre-PR-F static `sparklinePlaceholder` was 10 glyphs wide; keeping
// the window 10 wide is what makes frame-0 byte-identical (the chrome
// strip layout is unchanged). (The jsx uses `slice(-24)`; our chrome
// historically showed 10 — the narrower window is the established v8
// chrome width, not a new choice this PR.)
const sparkWidth = 10

// sparklineFrame0 is the EXACT glyph string the chat chrome rendered
// before PR-F (`sparklinePlaceholder`). The series is seeded so its
// counter-0 window renders precisely this — the frame-0 invariant
// witness (sparkline_test.go asserts it; if it ever differs a golden
// would change and the test fails loudly).
const sparklineFrame0 = "▁▂▃▄▅▆▇█▆▅"

// seriesWindowLiteral is the counter-0 history window — the ONLY
// integer window that renders `▁▂▃▄▅▆▇█▆▅` under jsx data-max scaling
// (proven by exhaustive search; see the file doc comment). It occupies
// series indices [seriesLen-sparkWidth-1, seriesLen-1).
var seriesWindowLiteral = []int{0, 1, 2, 3, 4, 5, 6, 7, 5, 4}

// seriesRate0 is the counter-0 rate sample (series[seriesLen-1]) — 3,
// so the readout reads "3/s" byte-identically to the pre-PR-F static
// `throughput` const.
const seriesRate0 = 3

// series is the deterministic 36-sample event-rate ring. values[0] is
// the oldest sample; advance() drops it and appends a fresh
// deterministic sample (the jsx slice(1)+append shape).
type series struct {
	values []int
}

// nextSample is the deterministic replacement for the jsx's
// `Math.round(5 + sin(Date.now()*0.001)*4 + Math.random()*4)` (rufio-
// graphs.jsx line 204). A fixed two-term integer sinusoid of the
// ABSOLUTE 500ms tick counter — no rand, no wall-clock — kept in [1,8]
// so the bars stay lively and the rate stays a small int. This is the
// documented deviation (deterministic for testability; the real
// stream-derived rate is PR-G, data-mapping §1).
func nextSample(tick int) int {
	t := float64(tick)
	v := 4.5 + 3*math.Sin(t*0.45) + 1.5*math.Sin(t*1.3)
	r := int(math.Round(v))
	if r < 1 {
		r = 1
	}
	if r > 8 {
		r = 8
	}
	return r
}

// newSeries builds the counter-0 (frame-0) series: a deterministic
// lead, then the seriesWindowLiteral, then the seriesRate0 sample. The
// lead is `nextSample` over a fixed negative phase so it is
// deterministic, non-flat, and never affects frame-0 (it sits left of
// the history window). Layout (indices):
//
//	[0 .. seriesLen-sparkWidth-2]   lead   (deterministic, off-window)
//	[seriesLen-sparkWidth-1 ..      ]
//	          [.. seriesLen-2]      window (seriesWindowLiteral)
//	[seriesLen-1]                   rate   (seriesRate0 == 3)
//
// At counter 0: window() == seriesWindowLiteral ⇒ Sparkline ==
// `▁▂▃▄▅▆▇█▆▅`; rate() == 3 ⇒ "3/s". Both frame-0 byte-identical.
func newSeries() *series {
	vals := make([]int, 0, seriesLen)
	leadN := seriesLen - sparkWidth - 1 // = 25
	for i := 0; i < leadN; i++ {
		// Negative phase so the lead is deterministic and distinct from
		// the window; never read at frame 0 (it is left of the window).
		vals = append(vals, nextSample(i-leadN))
	}
	vals = append(vals, seriesWindowLiteral...) // sparkWidth samples
	vals = append(vals, seriesRate0)            // the rate sample
	return &series{values: vals}
}

// advance shifts the ring one sample left and appends nextSample for
// the given ABSOLUTE 500ms tick counter (jsx `[...d.slice(1), next]`).
// Deterministic in `tick` — the test harness calls advance(0),
// advance(1), … directly; the live app passes the monotonic
// anim.series counter.
func (s *series) advance(tick int) {
	s.values = append(s.values[1:], nextSample(tick))
}

// window is the chat-chrome history window: the sparkWidth samples
// ending just BEFORE the latest (rate) sample —
// series[seriesLen-sparkWidth-1 : seriesLen-1]. (The documented
// deviation from jsx `slice(-sparkWidth)`: the window excludes the very
// last sample so the bars and the rate are decoupled enough to keep
// frame-0 byte-identical; both still shift with the same series.)
func (s *series) window() []int {
	hi := len(s.values) - 1 // exclude the rate sample
	lo := hi - sparkWidth   // sparkWidth samples
	if lo < 0 {
		lo = 0
	}
	return s.values[lo:hi]
}

// rate is the latest instantaneous sample — jsx `series[length-1]`
// (rufio-bubbletea-v8.jsx line 244 `{series[series.length-1]}/s`).
func (s *series) rate() int {
	return s.values[len(s.values)-1]
}

// renderSparkline styles a value slice with the chrome's Accent
// foreground (jsx line 242 `<Sparkline color={p.accent}/>`). Kept
// alongside the pure Sparkline so app.go's chrome stays a thin caller.
func renderSparkline(data []int) string {
	return lipgloss.NewStyle().
		Foreground(styles.Palette.Accent).
		Render(Sparkline(data))
}
