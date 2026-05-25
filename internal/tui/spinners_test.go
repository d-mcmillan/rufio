// spinners_test.go — exhaustive frame-progression tests for the v8
// SPINNERS table and the pure spinnerFrame helper (PR-F).
//
// These are deterministic (no wall-clock, no tea runtime) and prove the
// frame-0 invariant for the three wired sets: at counter 0 the selected
// frame is byte-identical to the static const the screen rendered before
// PR-F (⠋ / ◜ / ⠁), so every committed golden stays unchanged.
package tui

import "testing"

// TestSpinnersTableVerbatim pins the six SPINNERS frame sets exactly as
// rufio-graphs.jsx lines 152-159 — all six (the plan §12 lists all six
// even though only dots/arc/bouncing are wired this PR).
func TestSpinnersTableVerbatim(t *testing.T) {
	want := map[string][]string{
		"dots":     {"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		"arc":      {"◜", "◠", "◝", "◞", "◡", "◟"},
		"bouncing": {"⠁", "⠂", "⠄", "⠂"},
		"bar":      {"▎", "▍", "▌", "▋", "▊", "▉", "█", "▉", "▊", "▋", "▌", "▍"},
		"pulse":    {"·", "∙", "•", "●", "•", "∙"},
		"triangle": {"◢", "◣", "◤", "◥"},
	}
	got := map[string][]string{
		"dots":     spinnerDots,
		"arc":      spinnerArc,
		"bouncing": spinnerBouncing,
		"bar":      spinnerBar,
		"pulse":    spinnerPulse,
		"triangle": spinnerTriangle,
	}
	for name, w := range want {
		g := got[name]
		if len(g) != len(w) {
			t.Fatalf("SPINNERS[%s] length = %d, want %d", name, len(g), len(w))
		}
		for i := range w {
			if g[i] != w[i] {
				t.Errorf("SPINNERS[%s][%d] = %q, want %q", name, i, g[i], w[i])
			}
		}
	}
}

// TestSpinnerFrameWrap proves spinnerFrame(set, counter) = set[counter %
// len(set)] for every wired set across >1 full cycle, AND that the
// counter-0 frame is the exact static const the pre-PR-F screen showed
// (the frame-0 invariant — goldens stay byte-identical).
func TestSpinnerFrameWrap(t *testing.T) {
	cases := []struct {
		name   string
		set    []string
		frame0 string // the pre-PR-F static const
	}{
		{"dots", spinnerDots, "⠋"},         // syncSpinnerFrame
		{"arc", spinnerArc, "◜"},           // liveArcFrame
		{"bouncing", spinnerBouncing, "⠁"}, // bouncingSpinnerFrame
	}
	for _, c := range cases {
		if got := spinnerFrame(c.set, 0); got != c.frame0 {
			t.Errorf("FRAME-0 INVARIANT BROKEN: spinnerFrame(%s, 0) = %q, want static const %q",
				c.name, got, c.frame0)
		}
		// Two full cycles + a partial, asserting exact wrap.
		for k := 0; k < 2*len(c.set)+3; k++ {
			want := c.set[k%len(c.set)]
			if got := spinnerFrame(c.set, k); got != want {
				t.Errorf("spinnerFrame(%s, %d) = %q, want %q", c.name, k, got, want)
			}
		}
	}
}

// TestSpinnerFrameNegativeAndEmpty guards the defensive paths: a
// negative counter never panics (Go % can be negative) and an empty set
// returns "" rather than indexing out of range.
func TestSpinnerFrameNegativeAndEmpty(t *testing.T) {
	if got := spinnerFrame(nil, 5); got != "" {
		t.Errorf("spinnerFrame(nil, 5) = %q, want \"\"", got)
	}
	if got := spinnerFrame([]string{}, 0); got != "" {
		t.Errorf("spinnerFrame(empty, 0) = %q, want \"\"", got)
	}
	// Negative counter must not panic and must land in range.
	got := spinnerFrame(spinnerDots, -1)
	if got == "" {
		t.Errorf("spinnerFrame(dots, -1) = %q, want a valid frame", got)
	}
}
