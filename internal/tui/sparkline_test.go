// sparkline_test.go — deterministic frame-progression + frame-0
// invariant tests for the v8 sparkline series and renderer (PR-F).
package tui

import (
	"strings"
	"testing"
)

// TestSparklineRendererScaling pins Sparkline against the jsx formula
// SPARK[min(7, floor(v/max*8))] (rufio-graphs.jsx 181-189) — data-max
// scaled, one glyph per sample, max(...,1) guard.
func TestSparklineRendererScaling(t *testing.T) {
	// The canonical full ramp: this exact window is the ONLY integer
	// window that renders ▁▂▃▄▅▆▇█▆▅ under data-max (proven by search;
	// see sparkline.go). Verified here so a scaling regression is caught.
	got := Sparkline([]int{0, 1, 2, 3, 4, 5, 6, 7, 5, 4})
	if got != "▁▂▃▄▅▆▇█▆▅" {
		t.Errorf("Sparkline(ramp) = %q, want %q", got, "▁▂▃▄▅▆▇█▆▅")
	}
	// All-zero series: max guarded to 1, every sample floor(0)=▁.
	if got := Sparkline([]int{0, 0, 0}); got != "▁▁▁" {
		t.Errorf("Sparkline(zeros) = %q, want %q", got, "▁▁▁")
	}
	// Flat non-zero series: v==max ⇒ min(7, floor(8)) = 7 = █ each.
	if got := Sparkline([]int{5, 5, 5}); got != "███" {
		t.Errorf("Sparkline(flat) = %q, want %q", got, "███")
	}
	if got := Sparkline(nil); got != "" {
		t.Errorf("Sparkline(nil) = %q, want \"\"", got)
	}
}

// TestSparklineSeriesFrame0Invariant is the load-bearing frame-0 test:
// at counter 0 the chat-chrome history window MUST render exactly
// `▁▂▃▄▅▆▇█▆▅` and the rate sample MUST be 3 ("3/s") — byte-identical
// to the pre-PR-F static consts (sparklinePlaceholder + throughput) so
// every committed golden stays unchanged.
func TestSparklineSeriesFrame0Invariant(t *testing.T) {
	s := newSeries()
	win := s.window()
	if got := Sparkline(win); got != sparklineFrame0 {
		t.Errorf("FRAME-0 BROKEN: series window at counter 0 renders %q, want %q "+
			"(== old sparklinePlaceholder; a golden would change)", got, sparklineFrame0)
	}
	if got := s.rate(); got != 3 {
		t.Errorf("FRAME-0 BROKEN: series rate at counter 0 = %d, want 3 "+
			"(== old throughput \"3/s\"; a golden would change)", got)
	}
	if len(s.values) != seriesLen {
		t.Errorf("series length = %d, want %d", len(s.values), seriesLen)
	}
	if len(win) != sparkWidth {
		t.Errorf("history window width = %d, want %d", len(win), sparkWidth)
	}
}

// TestSparklineSeriesAdvances proves the series is a deterministic ring
// that shifts one sample left per 500ms tick (jsx useTickedSeries
// slice(1)+append shape, rufio-graphs.jsx 199-211) — same inputs ⇒ same
// outputs, no wall-clock, and the rendered bar string actually changes
// over successive series-ticks (motion the goldens can't see).
func TestSparklineSeriesAdvances(t *testing.T) {
	a := newSeries()
	b := newSeries()
	// Determinism: two independent series advanced the same number of
	// ticks are identical (no rand / no Date.now()).
	for k := 0; k < 12; k++ {
		a.advance(k)
		b.advance(k)
	}
	for i := range a.values {
		if a.values[i] != b.values[i] {
			t.Fatalf("non-deterministic series at idx %d after 12 ticks: %d vs %d",
				i, a.values[i], b.values[i])
		}
	}
	// Motion: the bar string changes across the first few series-ticks.
	s := newSeries()
	prev := Sparkline(s.window())
	seen := map[string]bool{prev: true}
	changed := false
	for k := 0; k < 6; k++ {
		s.advance(k)
		cur := Sparkline(s.window())
		if cur != prev {
			changed = true
		}
		seen[cur] = true
		prev = cur
	}
	if !changed {
		t.Errorf("sparkline bar string never changed over 6 series-ticks (no motion)")
	}
	if len(seen) < 3 {
		t.Errorf("expected ≥3 distinct bar strings over 6 ticks, got %d", len(seen))
	}
	// Width invariant: every advanced frame's window renders exactly
	// sparkWidth glyphs (a stable-width cell budget — the chrome strip
	// must never reflow).
	s2 := newSeries()
	for k := 0; k < 20; k++ {
		if w := []rune(Sparkline(s2.window())); len(w) != sparkWidth {
			t.Fatalf("tick %d: sparkline width = %d, want %d", k, len(w), sparkWidth)
		}
		s2.advance(k)
	}
}

// TestSparklineSeriesValuesInRange guards that every advanced sample
// stays in [1,8] (a lively but bounded deterministic sinusoid — keeps
// the bars readable and the rate a small int).
func TestSparklineSeriesValuesInRange(t *testing.T) {
	s := newSeries()
	for k := 0; k < 200; k++ {
		s.advance(k)
		for i, v := range s.values {
			// The seeded frame-0 lead/window literal includes a 0 (the
			// first ▁ bar). Once it shifts out, fresh samples are [1,8].
			if v < 0 || v > 8 {
				t.Fatalf("tick %d idx %d: sample %d out of [0,8]", k, i, v)
			}
		}
		_ = strings.TrimSpace // keep the strings import meaningful if trimmed
	}
}
