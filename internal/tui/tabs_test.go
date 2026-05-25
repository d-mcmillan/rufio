// tabs_test.go — unit tests for the per-tab fixture renderers + the
// lineage drill-down overlay (PR-D §3/§4).
//
// Asserts each tab renders its OWN distinct customer:5821-arc fixture in
// the v8 borderless language, the channels tab default-selects the first
// channel and shows its @say transcript, the goals overlap line is
// present, and the lineage overlay renders the decision header / context
// bundle / numbered reasoning chain. Content assertions run under the
// Ascii profile (escapes stripped); a TrueColor case asserts a Rufio
// agent color escape.
package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

func TestFleetTabContent(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	out := renderFleetTab(116)
	for _, want := range []string{
		"claude-code", "cursor", "data-analyst",
		"churn investigation", "usage analysis", "customer:5821", "14:02:46",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fleet tab missing %q in:\n%s", want, out)
		}
	}
	// Distinctly NOT the channel/goal/memory data.
	for _, bad := range []string{"ch-1747-x1", "usage-trend", "overlaps"} {
		if strings.Contains(out, bad) {
			t.Errorf("fleet tab leaked other-tab content %q:\n%s", bad, out)
		}
	}
}

func TestChannelsTabContent(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	// PR-G3: the renderer is data-source-agnostic now (takes its slice
	// as a param). Pass the ChannelThreads fixture explicitly so this
	// renderer-shape gate still verifies the v8 visual language on the
	// canonical fixture (the live wiring is covered by the live_tabs /
	// live golden tests; this stays a pure renderer test).
	out := renderChannelsTab(ChannelThreads, 116, 0)
	for _, want := range []string{
		"ch-1747-x1", "claude-code", "data-analyst", "customer:5821",
		"14-day silence, mentioned cancel",
		"team usage 12→3 in 30d. contraction, not churn",
		"got it — proposing downgrade", "14:03:34",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("channels tab missing %q in:\n%s", want, out)
		}
	}
}

func TestChannelsTabDefaultSelectsFirst(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	// Out-of-range / negative selection clamps to the first channel.
	for _, sel := range []int{-1, 0, 99} {
		out := renderChannelsTab(ChannelThreads, 116, sel)
		if !strings.Contains(out, "ch-1747-x1") {
			t.Errorf("sel=%d should still show the (only/first) channel transcript:\n%s", sel, out)
		}
	}
}

func TestGoalsTabContent(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	out := renderGoalsTab(GoalCards, 116)
	for _, want := range []string{
		"resolve customer:5821 churn risk",
		"improve customer:5821 onboarding re-engagement",
		"[active]", "claude-code", "cursor",
		"overlaps cursor — shared entity customer:5821",
		"overlaps claude-code — shared entity customer:5821",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("goals tab missing %q in:\n%s", want, out)
		}
	}
}

func TestMemoryTabContent(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	out := renderMemoryTab(MemoryEntries, 116)
	for _, want := range []string{
		"customer:5821", "prefers", "email",
		"usage-trend", "contraction",
		"tier", "standard", "initial-import",
		"cursor", "data-analyst", "2m", "1m", "2h",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("memory tab missing %q in:\n%s", want, out)
		}
	}
}

func TestLineageOverlayContent(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	d := SubstrateThread[5].Lineage
	if d == nil {
		t.Fatal("fixture invariant: decision row must carry a Lineage payload")
	}
	out := renderLineageOverlay(d, 120, 36)
	for _, want := range []string{
		"Decision: offer downgrade, not churn-save discount",
		"by ", "claude-code", "14:02:46",
		"Subject: ", "customer:5821", "#1747-d29",
		"Context bundle:", "given/refund-policy.md@v1 (sha a3f8…)", "learned/customer:5821",
		"Reasoning chain:",
		"customer requested downgrade, not cancellation",
		"usage contraction confirmed by data-analyst (12→3/30d)",
		"policy: downgrade offers < $500 auto-approve",
		"decision: approve downgrade offer, no churn-save discount",
		"press esc to close",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lineage overlay missing %q in:\n%s", want, out)
		}
	}
	// Numbered chain: "1." … "4." present.
	for _, n := range []string{"1", "2", "3", "4"} {
		if !strings.Contains(out, n+". ") {
			t.Errorf("lineage overlay missing numbered step %q. in:\n%s", n, out)
		}
	}
}

// longDecisionStatement is a ~280-char real-world decision body (the
// shape the live demo's decision carries): one prose line far wider than
// any terminal. The fixture goldens only ever use short statements, so
// this is the FIRST input that exercises the unbounded-Panel defect.
const longDecisionStatement = "approve the customer:5821 downgrade to the standard tier without applying any churn-save discount because usage telemetry confirms a sustained contraction from twelve to three active seats over the trailing thirty days and the refund policy auto-approves downgrade offers under five hundred dollars so no human escalation is required here"

// boxExtent returns the rune-width of the bordered region in a placed
// overlay render: the widest line trimmed of lipgloss.Place's all-space
// centering gutters (Place legitimately pads the canvas out to the full
// terminal width with spaces — exactly as the stable golden does — so a
// raw line-width measure would always be the terminal width and prove
// nothing about whether the BOX is bounded).
func boxExtent(out string) int {
	max := 0
	for _, ln := range strings.Split(out, "\n") {
		trimmed := strings.Trim(ln, " ")
		if w := lipgloss.Width(trimmed); w > max {
			max = w
		}
	}
	return max
}

// TestLineageOverlayLongStatementBounded is the #132 root-cause-#1/#3
// regression: a decision with a very long Statement must NOT render a
// full-terminal-width band — the bordered box is bounded to
// lineageMaxBoxW — AND the long statement must WRAP (multiple lines, no
// mid-prose loss) rather than being forced off-screen on one line.
func TestLineageOverlayLongStatementBounded(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	const w = 120
	d := &DecisionLineage{
		ID:        "1747-d29",
		Author:    "claude-code",
		Subject:   "customer:5821",
		Statement: longDecisionStatement,
		Time:      "14:02:46",
		Bundle:    []string{"given/refund-policy.md@v1 (sha a3f8…)"},
		Chain:     []string{"customer requested downgrade, not cancellation"},
	}
	out := renderLineageOverlay(d, w, 36)

	// (1) Every composed line fits the terminal (no full-width break).
	for i, ln := range strings.Split(out, "\n") {
		if gw := lipgloss.Width(ln); gw > w {
			t.Errorf("line %d width %d exceeds terminal width %d (unbounded box): %q", i, gw, w, ln)
		}
	}
	// (2) The bordered box is bounded to the cap — far under the 120-col
	// terminal. The pre-#132 code produced a ~290-col box here.
	if ext := boxExtent(out); ext > lineageMaxBoxW {
		t.Errorf("bordered box extent = %d exceeds lineageMaxBoxW %d — overlay box is not bounded", ext, lineageMaxBoxW)
	}

	// (3) The statement wrapped onto multiple lines (it cannot fit a
	// bounded ~60-col content box on one line) with NO mid-prose loss:
	// this ~280-char body fits within wrapBody's 6-line cap, so EVERY
	// word must survive somewhere in the escape-stripped render.
	for _, word := range strings.Fields(longDecisionStatement) {
		if !strings.Contains(out, word) {
			t.Errorf("long statement word %q lost (not wrapped / truncated away):\n%s", word, out)
		}
	}
	// The body must span several lines — count the bordered content
	// lines that carry statement prose (between the "Decision:" line and
	// the "by " line). A single forced-off-screen line would be 1.
	stmtLines := 0
	seenDecision := false
	for _, ln := range strings.Split(out, "\n") {
		s := strings.TrimSpace(strings.Trim(ln, " │"))
		if strings.HasPrefix(s, "Decision:") {
			seenDecision = true
		}
		if !seenDecision {
			continue
		}
		if strings.HasPrefix(s, "by ") {
			break
		}
		if s != "" {
			stmtLines++
		}
	}
	if stmtLines < 4 {
		t.Errorf("expected the long statement to wrap onto several bounded lines, got %d:\n%s", stmtLines, out)
	}
}

// TestLineageOverlayEmptySections is the #132 root-cause-#2 regression: a
// decision with NO context-bundle refs and NO reasoning chain must show
// an explicit "(none)" sentinel under each header, never a bare empty
// section (degrade gracefully, never blank — the read-only convention).
func TestLineageOverlayEmptySections(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	d := &DecisionLineage{
		ID:        "1747-d29",
		Author:    "claude-code",
		Subject:   "customer:5821",
		Statement: "offer downgrade, not churn-save discount",
		Time:      "14:02:46",
		Bundle:    nil,
		Chain:     nil,
	}
	out := renderLineageOverlay(d, 120, 36)

	if !strings.Contains(out, "Context bundle:") || !strings.Contains(out, "Reasoning chain:") {
		t.Fatalf("overlay missing section headers:\n%s", out)
	}
	// Exactly two "(none)" sentinels — one per empty section.
	if n := strings.Count(out, "(none)"); n != 2 {
		t.Errorf("expected 2 \"(none)\" sentinels (empty bundle + empty chain), got %d:\n%s", n, out)
	}
	// The "(none)" must follow each header (not appear before either).
	ctxIdx := strings.Index(out, "Context bundle:")
	chainIdx := strings.Index(out, "Reasoning chain:")
	firstNone := strings.Index(out, "(none)")
	if firstNone < ctxIdx || firstNone > chainIdx {
		t.Errorf("Context bundle (none) sentinel not located under its header:\n%s", out)
	}
}

// TestTabTrueColorAgentEscape asserts the fleet tab carries the
// data-analyst agent color escape under TrueColor (data-analyst → Good
// #a8e6a3 → rgb(168,230,163) → ESC[38;2;168;230;163m).
func TestTabTrueColorAgentEscape(t *testing.T) {
	styles.SetProfile(termenv.TrueColor)
	defer styles.SetProfile(termenv.Ascii)
	out := renderFleetTab(116)
	const wantGoodFg = "38;2;168;230;163"
	if !strings.Contains(out, wantGoodFg) {
		t.Errorf("fleet tab missing data-analyst 24-bit fg %q (Good): %q", wantGoodFg, out)
	}
}

// TestTabsFitNarrowWidth asserts every tab renderer truncates rather
// than overflows at a narrow width (no panel-border break upstream).
func TestTabsFitNarrowWidth(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	const narrow = 24
	render := []func() string{
		func() string { return renderFleetTab(narrow) },
		func() string { return renderChannelsTab(ChannelThreads, narrow, 0) },
		func() string { return renderGoalsTab(GoalCards, narrow) },
		func() string { return renderMemoryTab(MemoryEntries, narrow) },
	}
	for i, r := range render {
		out := r()
		for _, ln := range strings.Split(out, "\n") {
			if w := lipgloss.Width(ln); w > narrow {
				t.Errorf("renderer #%d line exceeds narrow width %d (got %d): %q", i, narrow, w, ln)
			}
		}
	}
}
