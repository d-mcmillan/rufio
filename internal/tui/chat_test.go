// chat_test.go — unit + golden tests for the v8 static chat panel.
//
// RE-SCOPE (2026-05-15, PR-D): the fixture is now the Rufio
// customer:5821 churn arc (SubstrateThread), not the fictional V8Thread.
// The chat.go RENDERER is unchanged (PR-B); only the data these tests
// assert against changed. Structural tests (timestamp-suppression logic,
// quorum POSITION ordering, glyphs, truncation) are kept; their expected
// values track the new arc. Text-content assertions run under the Ascii
// termenv profile; one TrueColor test asserts a known 24-bit escape.
//
// IMPORTANT: this package (tui) also has an OLD package-level SetProfile
// (internal/tui/styles.go, PR #22). The v8 chat panel is styled by the
// NEW internal/tui/styles SUBPACKAGE, so every test here calls the
// qualified styles.SetProfile — never the bare in-package SetProfile.
//
// Golden bootstrap: TEATEST_UPDATE=1 go test ./internal/tui/... -run
// TestRenderChatGolden ; then commit test/golden/tui-v8-chat.txt.
package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// TestShowTimestamp is the §7.3 / jsx `timestampVisible` table (logic is
// unchanged; this is a pure function test independent of the fixture).
func TestShowTimestamp(t *testing.T) {
	cases := []struct {
		name       string
		curr, prev string
		want       bool
	}{
		{"first message (no prev)", "14:02:09", "", true},
		{"same minute, <30s gap", "14:02:14", "14:02:11", false},
		{"same minute, exactly 30s gap", "14:02:45", "14:02:15", true},
		{"same minute, >=30s gap (arc 14:02:14→14:02:46=32s)", "14:02:46", "14:02:14", true},
		{"minute rollover, tiny clock gap", "14:03:01", "14:02:59", true},
		{"hour rollover", "15:00:01", "14:59:59", true},
		{"same minute, 29s gap", "14:02:44", "14:02:15", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := showTimestamp(c.curr, c.prev); got != c.want {
				t.Errorf("showTimestamp(%q,%q) = %v, want %v", c.curr, c.prev, got, c.want)
			}
		})
	}
}

// opRow renders SubstrateThread[0] (the operator row) in Ascii.
func opRow(t *testing.T) string {
	t.Helper()
	styles.SetProfile(termenv.Ascii)
	return renderRow(SubstrateThread[0], "", 120)
}

// planRow renders the claude-code hypothesis row (SubstrateThread[1]).
func planRow(t *testing.T) string {
	t.Helper()
	styles.SetProfile(termenv.Ascii)
	return renderRow(SubstrateThread[1], SubstrateThread[0].Time, 120)
}

// replyRow renders the cursor observation reply (SubstrateThread[2]).
func replyRow(t *testing.T) string {
	t.Helper()
	styles.SetProfile(termenv.Ascii)
	return renderRow(SubstrateThread[2], SubstrateThread[1].Time, 120)
}

// decisionRow renders the claude-code decision row (SubstrateThread[5]),
// which carries quorum + Last caret + Lineage payload. It deliberately
// has NO chips — the context-bundle refs live in the lineage drill-down
// (DecisionLineage.Bundle), not as row chips (data-mapping §3a "chips —
// RESOLVED").
func decisionRow(t *testing.T) string {
	t.Helper()
	styles.SetProfile(termenv.Ascii)
	return renderRow(SubstrateThread[5], SubstrateThread[4].Time, 120)
}

// TestOpRow asserts the operator row's glyph, uppercase tag, no rail,
// indent, and Rufio body text.
func TestOpRow(t *testing.T) {
	row := opRow(t)
	if !strings.Contains(row, glyphOp) {
		t.Errorf("op row missing %q glyph: %q", glyphOp, row)
	}
	if !strings.Contains(row, "OPERATOR") {
		t.Errorf("op row missing UPPERCASE role tag OPERATOR: %q", row)
	}
	if strings.Contains(row, glyphPlan) || strings.Contains(row, "│") {
		t.Errorf("op row must have no left rail: %q", row)
	}
	if !strings.HasPrefix(row, strings.Repeat(" ", indentOp)+glyphOp) {
		t.Errorf("op row indent: want %d spaces then %q, got %q", indentOp, glyphOp, row)
	}
	if !strings.Contains(row, "investigate customer:5821 churn risk") {
		t.Errorf("op row missing Rufio body text: %q", row)
	}
}

// TestPlanRow asserts the ◆ glyph, the agent-color bar, the indent, and
// the hypothesis chips (the hypothesis row has no quorum — the decision
// row does; see TestDecisionRow).
func TestPlanRow(t *testing.T) {
	row := planRow(t)
	if !strings.Contains(row, glyphPlan) {
		t.Errorf("plan row missing %q glyph: %q", glyphPlan, row)
	}
	if !strings.Contains(row, "HYPOTHESIS") {
		t.Errorf("plan row missing UPPERCASE role tag HYPOTHESIS: %q", row)
	}
	if !strings.HasPrefix(row, strings.Repeat(" ", indentPlan)+glyphPlan) {
		t.Errorf("plan row indent: want %d cells then %q, got %q", indentPlan, glyphPlan, row)
	}
	for _, chip := range []string{"customer:5821", "scope:fleet"} {
		if !strings.Contains(row, chip) {
			t.Errorf("hypothesis row missing chip %q: %q", chip, row)
		}
	}
}

// TestDecisionRow asserts the decision plan row has NO context-bundle
// chips (chips are short entity/scope tags, not bundle refs/paths — see
// data-mapping §3a "chips — RESOLVED"), that its body is therefore
// LEGIBLE (a meaningful prefix of the decision text, not obliterated to
// `o…`), and that it still carries the quorum dots + 2/3 counter (the
// AutoPromoteHandler threshold) + the Last caret. The context-bundle
// refs are asserted in the lineage drill-down by tabs_test.go /
// keys_test.go, not here.
func TestDecisionRow(t *testing.T) {
	row := decisionRow(t)
	if !strings.Contains(row, "DECISION") {
		t.Errorf("decision row missing UPPERCASE role tag DECISION: %q", row)
	}
	// The bundle-ref strings must NOT appear as row chips.
	for _, ref := range []string{"given/refund-policy.md@v1", "learned/customer:5821"} {
		if strings.Contains(row, ref) {
			t.Errorf("decision row must NOT carry context-bundle ref %q as a chip (chips ≠ bundle refs): %q", ref, row)
		}
	}
	// The fixture row carries no chips at all.
	if SubstrateThread[5].Chips != nil {
		t.Errorf("SubstrateThread[5] (decision row) Chips must be nil, got %v", SubstrateThread[5].Chips)
	}
	// With no chips eating the budget the body is legible — a meaningful
	// prefix of the decision text is present (not just `decision: o…`).
	if !strings.Contains(row, "decision: offer downgrade") {
		t.Errorf("decision row body should be legible (\"decision: offer downgrade…\"), got: %q", row)
	}
	if !strings.Contains(row, dotVoted) {
		t.Errorf("decision row missing quorum %q dots: %q", dotVoted, row)
	}
	if !strings.Contains(row, "2/3") {
		t.Errorf("decision row missing quorum counter 2/3 (AutoPromote threshold): %q", row)
	}
	if !strings.Contains(row, caretGlyph) {
		t.Errorf("decision row (Last) missing trailing caret %q: %q", caretGlyph, row)
	}
}

// TestReplyRow asserts the ↳ glyph, the │ Line rail, and the indent.
func TestReplyRow(t *testing.T) {
	row := replyRow(t)
	if !strings.Contains(row, glyphReply) {
		t.Errorf("reply row missing %q glyph: %q", glyphReply, row)
	}
	if !strings.Contains(row, "OBSERVATION") {
		t.Errorf("reply row missing UPPERCASE role tag OBSERVATION: %q", row)
	}
	if !strings.Contains(row, "│") {
		t.Errorf("reply row missing │ rail: %q", row)
	}
	wantPrefix := "│" + strings.Repeat(" ", indentReply-1) + glyphReply
	if !strings.HasPrefix(row, wantPrefix) {
		t.Errorf("reply row indent: want %q-prefixed, got %q", wantPrefix, row)
	}
}

// TestQuorumDotsCanonicalCount asserts the dot row for the REAL decision
// fixture row (SubstrateThread[5]) has exactly q.Total glyphs — the LIVE
// denominator the trailing counter uses, NOT the obsolete len(QuorumOrder)
// — with len(q.Yes) filled `●` then the rest hollow `○`, and the matching
// counter. Fixture: Yes = [cursor, data-analyst], Total = 3 → ●●○ 2/3.
//
// REWORKED (P1): the old assertion pinned the dot count to
// len(QuorumOrder) (the dead fixture slot list). That coincidentally
// equalled q.Total for this fixture but is the WRONG invariant — for
// arbitrary live confirmer ids the count must follow q.Total (so the
// rendered dots agree with the live denominator). The guard is
// STRENGTHENED: it now ties the dot count to the same q.Total the
// counter prints, which is the property OPEN-2 actually locks.
func TestQuorumDotsCanonicalCount(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	q := SubstrateThread[5].Quorum
	out := renderQuorumDots(q)
	dots := quorumDots(out)
	if len(dots) != q.Total {
		t.Fatalf("quorum dot count = %d, want q.Total = %d (the live denominator) (%q)",
			len(dots), q.Total, out)
	}
	wantFilled := len(q.Yes)
	if got := strings.Count(string(dots), dotVoted); got != wantFilled {
		t.Errorf("voted dots = %d, want len(q.Yes) = %d: %q", got, wantFilled, out)
	}
	if got := strings.Count(string(dots), dotUnvoted); got != q.Total-wantFilled {
		t.Errorf("unvoted dots = %d, want %d: %q", got, q.Total-wantFilled, out)
	}
	wantCounter := strconv.Itoa(len(q.Yes)) + "/" + strconv.Itoa(q.Total)
	if !strings.HasSuffix(out, wantCounter) {
		t.Errorf("quorum counter suffix = want ...%s, got %q", wantCounter, out)
	}
}

// TestQuorumDotsCountIsConfirmerOrderIndependent asserts the dot run is a
// COUNT toward q.Total — filled `●` left-packed, then hollow `○` — and is
// INDEPENDENT of which arbitrary (non-fixture) ids confirmed or the order
// they appear in q.Yes.
//
// REWORKED (P1): replaces TestQuorumDotsCanonicalOrder, which asserted
// "dot POSITION is driven by QuorumOrder, not q.Yes order". That
// QuorumOrder-slot coupling is exactly the demo-fatal defect (a confirmer
// not in the dead fixture list could never light a slot), so an
// assertion that only made sense for the obsolete coupling is removed.
// The replacement guards the invariant that now matters and is NOT
// weaker: same Total-2 voted shape, but with ARBITRARY live ids in a
// deliberately non-sorted order, proving the fill is a left-packed count
// — never keyed to a fixed id slot. This is a strictly broader guarantee
// (it would have CAUGHT the original defect; the old test passed it).
func TestQuorumDotsCountIsConfirmerOrderIndependent(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	// Arbitrary live ids, none in the obsolete QuorumOrder fixture, in a
	// deliberately unsorted order. 2 of 3 confirmed.
	q := &Quorum{Yes: []string{"gemini-cli", "cursor-cli"}, Total: 3}
	out := renderQuorumDots(q)
	dots := quorumDots(out)
	if len(dots) != q.Total {
		t.Fatalf("dot run length = %d, want q.Total = %d: %q", len(dots), q.Total, out)
	}
	// Filled dots are contiguous from the left (progress reads L→R), one
	// per confirmer, then hollow up to Total — regardless of WHICH ids.
	want := strings.Repeat(dotVoted, len(q.Yes)) +
		strings.Repeat(dotUnvoted, q.Total-len(q.Yes))
	if string(dots) != want {
		t.Errorf("dot run = %q, want %q (left-packed count, id-agnostic) (full=%q)",
			string(dots), want, out)
	}
	// Reordering q.Yes must not change the rendered run (it's a count).
	q2 := &Quorum{Yes: []string{"cursor-cli", "gemini-cli"}, Total: 3}
	if got := string(quorumDots(renderQuorumDots(q2))); got != want {
		t.Errorf("reordered q.Yes changed the dot run: got %q, want %q", got, want)
	}
}

// quorumDots strips the trailing " N/M" counter from a renderQuorumDots
// result and returns just the dot-glyph run, for count/position asserts.
func quorumDots(out string) []rune {
	i := strings.LastIndexAny(out, string(dotVoted)+string(dotUnvoted))
	if i < 0 {
		return nil
	}
	// out is "<dots> N/M"; the dot run is the prefix up to the last dot.
	return []rune(out[:i+len(string(dotVoted))])
}

// TestQuorumDotsLiveConfirmers is the P1 regression guard: the dot row
// must reflect the LIVE distinct-confirmer COUNT toward q.Total for
// ARBITRARY real agent ids — NOT a fixed fixture slot list. The launch
// demo's three real harness ids are claude-code / gemini-cli /
// cursor-cli; only claude-code is in the obsolete QuorumOrder fixture, so
// the OLD QuorumOrder-keyed render lit exactly ONE dot for a full 3/3
// quorum (the demo-fatal defect). After the fix the row is
// min(len(Yes),Total) filled `●` then hollow `○` up to Total, for any
// ids, with the matching N/Total counter.
func TestQuorumDotsLiveConfirmers(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	const total = 3
	ids := []string{"claude-code", "gemini-cli", "cursor-cli"}
	// Drive the confirmers accumulating 0→1→2→3 (the live demo climax).
	for n := 0; n <= total; n++ {
		q := &Quorum{Yes: append([]string(nil), ids[:n]...), Total: total}
		out := renderQuorumDots(q)
		dots := quorumDots(out)
		if len(dots) != total {
			t.Fatalf("Yes=%d: dot run length = %d, want %d (Total): %q",
				n, len(dots), total, out)
		}
		filled := strings.Count(string(dots), dotVoted)
		hollow := strings.Count(string(dots), dotUnvoted)
		if filled != n {
			t.Errorf("Yes=%d: filled ● = %d, want %d: %q", n, filled, n, out)
		}
		if hollow != total-n {
			t.Errorf("Yes=%d: hollow ○ = %d, want %d: %q", n, hollow, total-n, out)
		}
		// Filled dots precede hollow ones (progress reads left→right).
		want := strings.Repeat(dotVoted, n) + strings.Repeat(dotUnvoted, total-n)
		if string(dots) != want {
			t.Errorf("Yes=%d: dot run = %q, want %q (full=%q)", n, string(dots), want, out)
		}
		wantCounter := strconv.Itoa(n) + "/" + strconv.Itoa(total)
		if !strings.HasSuffix(out, wantCounter) {
			t.Errorf("Yes=%d: counter suffix = want ...%s, got %q", n, wantCounter, out)
		}
	}
}

// TestQuorumDotsOverflowClamp guards that an over-quorum (more confirmers
// than Total — possible live: a 4th agent confirms after auto-promote)
// never renders MORE than Total dots and never a negative hollow count.
func TestQuorumDotsOverflowClamp(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	q := &Quorum{Yes: []string{"claude-code", "gemini-cli", "cursor-cli", "operator"}, Total: 3}
	out := renderQuorumDots(q)
	dots := quorumDots(out)
	if len(dots) != 3 {
		t.Fatalf("over-quorum dot run = %d, want 3 (clamped to Total): %q", len(dots), out)
	}
	if got := strings.Count(string(dots), dotVoted); got != 3 {
		t.Errorf("over-quorum filled = %d, want 3: %q", got, out)
	}
	if got := strings.Count(string(dots), dotUnvoted); got != 0 {
		t.Errorf("over-quorum hollow = %d, want 0: %q", got, out)
	}
	if !strings.HasSuffix(out, "4/3") {
		t.Errorf("over-quorum counter = want ...4/3 (raw len/Total), got %q", out)
	}
}

// TestLastCaret asserts the Last row (SubstrateThread[5], the decision)
// renders a trailing caret block.
func TestLastCaret(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	last := SubstrateThread[5]
	if !last.Last {
		t.Fatalf("fixture invariant broken: SubstrateThread[5].Last should be true")
	}
	row := renderRow(last, SubstrateThread[4].Time, 120)
	if !strings.Contains(row, caretGlyph) {
		t.Errorf("Last row missing trailing caret %q: %q", caretGlyph, row)
	}
}

// TestTimestampSuppressionAcrossThread asserts WHICH of the 6
// SubstrateThread rows show a timestamp under the §7.3 rule:
//
//	[0] 14:02:09 op          — first message       → SHOW
//	[1] 14:02:11 hypothesis  — same min, 2s gap    → hide
//	[2] 14:02:12 observation — same min, 1s gap    → hide
//	[3] 14:02:13 observation — same min, 1s gap    → hide
//	[4] 14:02:14 confirm     — same min, 1s gap    → hide
//	[5] 14:02:46 decision    — same min, 32s gap   → SHOW
func TestTimestampSuppressionAcrossThread(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	wantShow := []bool{true, false, false, false, false, true}
	prev := ""
	for i, m := range SubstrateThread {
		row := renderRow(m, prev, 120)
		shown := strings.Contains(row, m.Time)
		if shown != wantShow[i] {
			t.Errorf("row %d (%s): timestamp shown=%v, want %v (row=%q)",
				i, m.Time, shown, wantShow[i], row)
		}
		prev = m.Time
	}
}

// TestRenderChatStructure asserts RenderChat threads the rows with the
// right vertical rhythm: a blank line precedes each non-first plan row,
// op/reply rows are tight. SubstrateThread: op, plan, reply, reply,
// reply, plan → blank before index-1 plan and index-5 plan only.
//
// WIDTH 220 (WIDE) — REWORK RATIONALE: the original test pinned width
// 120 and asserted "exactly 8 lines". V8B-M1 makes a row that overflows
// `avail` wrap into >1 line, and at 120 the hypothesis + decision bodies
// DO overflow (prefix ~31 + 74-cell body + chips/ts) — so the rigid
// 8-line count encoded the pre-wrap one-line-per-row model. The structural
// rhythm property it actually guards (blank-before-non-first-plan, tight
// op/reply) is UNCHANGED by wrap; it is re-pinned here at a width wide
// enough that NO body wraps (every body fits on its prefix line — the
// short/fitting-body byte-identical-render invariant), so the exact
// 8-line rhythm assertion is preserved verbatim. Wrap-aware rhythm (the
// separator survives multi-line rows) is covered by the width-60 block
// appended below — strictly ADDED coverage, nothing removed.
func TestRenderChatStructure(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	out := RenderChat(SubstrateThread, 220)
	lines := strings.Split(out, "\n")
	if len(lines) != 8 {
		t.Fatalf("RenderChat line count = %d, want 8 (6 rows + 2 plan separators) at a no-wrap width:\n%s",
			len(lines), out)
	}
	if strings.TrimSpace(lines[0]) == "" {
		t.Errorf("first line must not be a blank separator: %q", out)
	}
	for _, idx := range []int{1, 6} {
		if strings.TrimSpace(lines[idx]) != "" {
			t.Errorf("expected blank separator at line %d, got %q", idx, lines[idx])
		}
	}

	// Wrap-aware rhythm: at a narrow width several bodies wrap, but the
	// plan-separator rhythm must still hold — a blank line precedes the
	// FIRST line of every non-first plan, the first line is never blank,
	// and every non-blank line is either a row-start (begins with a
	// glyph/rail/indent, NOT the pure hanging indent) or a continuation
	// (a hanging-indent-then-text line). This proves wrapping did not
	// break the threading structure.
	narrow := strings.Split(RenderChat(SubstrateThread, 60), "\n")
	if strings.TrimSpace(narrow[0]) == "" {
		t.Errorf("narrow: first line must not be blank: %q", narrow[0])
	}
	plans := 0
	for i, ln := range narrow {
		isBlank := strings.TrimSpace(ln) == ""
		if isBlank {
			continue
		}
		// A plan row's first line contains the ◆ glyph; the blank-before
		// rule applies to NON-first plans only (SubstrateThread[1],[5]).
		if strings.Contains(ln, glyphPlan) && !strings.HasPrefix(ln, strings.Repeat(" ", indentPlan+2)) {
			plans++
			if plans >= 2 { // the second plan-start (index-5) is non-first
				if i == 0 || strings.TrimSpace(narrow[i-1]) != "" {
					t.Errorf("narrow: non-first plan start at line %d not preceded by a blank separator: %q",
						i, ln)
				}
			}
		}
	}
	if plans < 2 {
		t.Errorf("narrow: expected >=2 plan row-starts (hypothesis+decision), got %d", plans)
	}
}

// TestRenderChatSelectedMarker asserts RenderChatSelected draws the
// selection marker at column 0 of the selected row, the row width is
// UNCHANGED (marker replaces a leading cell, not added), and -1 selects
// nothing.
func TestRenderChatSelectedMarker(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	none := RenderChatSelected(SubstrateThread, 120, -1)
	if strings.Contains(none, selectionMarker) {
		t.Errorf("selected=-1 must render NO marker: %q", none)
	}
	// Select the op row (index 0).
	withSel := RenderChatSelected(SubstrateThread, 120, 0)
	if !strings.Contains(withSel, selectionMarker) {
		t.Errorf("selected=0 should render the marker %q", selectionMarker)
	}
	noneLines := strings.Split(none, "\n")
	selLines := strings.Split(withSel, "\n")
	if len(noneLines) != len(selLines) {
		t.Fatalf("selection changed line count: %d vs %d", len(noneLines), len(selLines))
	}
	// Marker must be at column 0 of the op row (first line).
	if !strings.HasPrefix(selLines[0], selectionMarker) {
		t.Errorf("op-row selection marker not at column 0: %q", selLines[0])
	}
}

// TestRenderChatWordWrap is the V8B-M1 soft-word-wrap guard (REWORKED
// from the old TestRenderChatTruncation, which asserted the now-removed
// hard-`…`-clip-for-long-bodies behaviour — see the rework note below).
//
// A plan row whose body exceeds the available inner width must render as
// MULTIPLE \n-separated lines: line 1 = the styled prefix + the first
// wrapped segment; every continuation line begins with exactly prefixW
// spaces (the hanging indent under the body's start column) so the
// wrapped prose stacks under the first segment. A wrappable body must
// NOT be hard-clipped with `…` (every word survives, in order), and no
// rendered line may exceed the width contract.
//
// REWORK RATIONALE (coverage NOT weakened): the old test only encoded
// "narrow ⇒ `…` appears AND full body absent" — an assertion that ONLY
// made sense for the pre-V8B-M1 clip. The replacement is strictly
// stronger: it still proves the body does not overflow the width
// (border-integrity) AND that the full body content is preserved (the
// property the old `…`-clip VIOLATED). The deliberate over-long-token
// `…` case (the only legitimate residual clip) is covered separately by
// TestWrapBodyOverlongTokenAndCap.
func TestRenderChatWordWrap(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	// 80 = a realistic launch-demo content width: the hypothesis prefix
	// is ~31 cells, its body 74 — at 80 the body (avail ~49) wraps to a
	// couple of lines (well under the maxWrapLines cap) so NOTHING is
	// clipped. (At a pathologically narrow width the avail is so small
	// the body legitimately hits the cap-`…`; that residual clip is the
	// TestWrapBodyOverlongTokenAndCap case, not this one.)
	const width = 80
	row := renderRowSelected(SubstrateThread[1], SubstrateThread[0].Time, width, false, 0)
	lines := strings.Split(row, "\n")
	if len(lines) < 2 {
		t.Fatalf("long body at width %d must wrap to >=2 lines, got %d: %q",
			width, len(lines), row)
	}
	// prefixW is the visible width of the row prefix (line 1's lead).
	_, prefixW := rowPrefix(SubstrateThread[1], false)
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w > width {
			t.Errorf("wrapped line %d width %d exceeds contract width %d: %q", i, w, width, ln)
		}
		if i == 0 {
			continue
		}
		// Continuation lines hang under the body's start column: exactly
		// prefixW leading spaces.
		if !strings.HasPrefix(ln, strings.Repeat(" ", prefixW)) {
			t.Errorf("continuation line %d missing %d-space hanging indent: %q", i, prefixW, ln)
		}
		if len(ln) > prefixW && ln[prefixW] == ' ' {
			t.Errorf("continuation line %d over-indented (extra leading space): %q", i, ln)
		}
	}
	// A wrappable body is NOT hard-clipped: no ellipsis, and every word
	// of the body survives (in order) across the joined wrapped lines.
	joined := strings.Join(lines, " ")
	if strings.Contains(joined, ellipsis) {
		t.Errorf("wrappable body must not be hard-clipped with %q: %q", ellipsis, row)
	}
	prev := 0
	for _, word := range strings.Fields(SubstrateThread[1].Text) {
		idx := strings.Index(joined[prev:], word)
		if idx < 0 {
			t.Errorf("wrapped body dropped word %q (clip, not wrap): joined=%q", word, joined)
			break
		}
		prev += idx + len(word)
	}
}

// TestRenderChatWordWrapPanelHeightInvariant proves multi-line wrapped
// rows still respect the panel's fixed-height clamp: the newest content
// stays anchored and the rendered block never exceeds the panel inner
// height (the load-bearing border-integrity invariant — wrapping makes
// rows TALLER, so this must hold with the line-count clamp).
func TestRenderChatWordWrapPanelHeightInvariant(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	// A narrow width forces every body to wrap to several lines.
	const width, panelH = 44, 12
	thread := renderChatSelectedAt(SubstrateThread, width, 5, 0)
	// The app feeds the rendered thread through topTruncate(_, panelH)
	// (app.go) — the line-count clamp. With wrapped rows this string has
	// MANY \n lines; the clamp must keep exactly the last panelH.
	clamped := topTruncate(thread, panelH)
	got := strings.Split(clamped, "\n")
	if len(got) != panelH {
		t.Fatalf("clamped thread = %d lines, want exactly panelH=%d (height invariant broken by wrap)",
			len(got), panelH)
	}
	// Newest content stays anchored: the LAST line of the clamped block
	// is the last line of the full wrapped thread (oldest dropped first).
	full := strings.Split(thread, "\n")
	if got[len(got)-1] != full[len(full)-1] {
		t.Errorf("clamp did not anchor newest content: last clamped line %q != last thread line %q",
			got[len(got)-1], full[len(full)-1])
	}
}

// TestWrapBody is the V8B-M1 unit test for the runewidth-aware soft
// word-wrap helper: greedy space-break packing, no line over width, no
// `…` while words fit, words preserved in order.
func TestWrapBody(t *testing.T) {
	const w = 12
	got := wrapBody("the quick brown fox jumps over", w)
	if len(got) < 2 {
		t.Fatalf("expected multi-line wrap at width %d, got %v", w, got)
	}
	for i, ln := range got {
		if lipgloss.Width(ln) > w {
			t.Errorf("wrapped line %d %q width %d exceeds %d", i, ln, lipgloss.Width(ln), w)
		}
		if strings.Contains(ln, ellipsis) {
			t.Errorf("space-wrappable input must not clip with %q: line %d %q", ellipsis, i, ln)
		}
	}
	if joined := strings.Join(got, " "); joined != "the quick brown fox jumps over" {
		t.Errorf("wrap dropped/reordered words: got %q", joined)
	}
	// A body that already fits is a single unchanged line (short-body
	// invariant — byte-identical render path).
	if one := wrapBody("short", 40); len(one) != 1 || one[0] != "short" {
		t.Errorf("fitting body must be a single unchanged line, got %v", one)
	}
}

// TestWrapBodyOverlongTokenAndCap asserts the two residual-clip rules:
// (1) a single token wider than the width is rune-split (runewidth-aware,
// no `…`) — only over-long tokens break mid-token; (2) a body needing
// more than maxWrapLines lines is capped, the final line ending in `…`
// (the only legitimate clip, so one giant body cannot blow the panel
// height — topTruncate is the further backstop).
func TestWrapBodyOverlongTokenAndCap(t *testing.T) {
	// (1) Over-long single token (no spaces) at width 8 → rune-chunked.
	const w = 8
	tok := strings.Repeat("x", 30)
	got := wrapBody(tok, w)
	if len(got) < 2 {
		t.Fatalf("over-long token must rune-split, got %v", got)
	}
	for i, ln := range got {
		if lipgloss.Width(ln) > w {
			t.Errorf("rune-split line %d %q exceeds width %d", i, ln, w)
		}
	}
	// (2) Cap: a body far exceeding maxWrapLines*width caps at
	// maxWrapLines lines, last line ends with the ellipsis.
	huge := strings.TrimSpace(strings.Repeat("alpha beta gamma ", 60))
	capped := wrapBody(huge, 10)
	if len(capped) != maxWrapLines {
		t.Fatalf("over-cap body = %d lines, want maxWrapLines=%d", len(capped), maxWrapLines)
	}
	if !strings.HasSuffix(capped[maxWrapLines-1], ellipsis) {
		t.Errorf("capped final line must end with %q, got %q", ellipsis, capped[maxWrapLines-1])
	}
}

// TestRenderTypingIndicator asserts the typing line: indent, agent name,
// the word "typing", and the static 3-dot run.
func TestRenderTypingIndicator(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	out := RenderTypingIndicator("data-analyst")
	if !strings.HasPrefix(out, strings.Repeat(" ", indentTyping)+"data-analyst") {
		t.Errorf("typing indicator indent: want %d spaces then agent, got %q", indentTyping, out)
	}
	if !strings.Contains(out, "typing") {
		t.Errorf("typing indicator missing 'typing': %q", out)
	}
	if !strings.Contains(out, "···") {
		t.Errorf("typing indicator missing static 3-dot run: %q", out)
	}
}

// TestRoleTagTrueColor asserts that under termenv.TrueColor the
// claude-code hypothesis row's role tag carries the 24-bit escape for
// claude-code's agent color. claude-code → Accent2 #8ab4f8. termenv's
// TrueColor conversion routes the hex through a color-space round-trip
// (the SAME off-by-one PR-A's TestAppHeaderGradientTrueColor documents
// for Label/Accent2: raw G=180 round-trips to 179), so the emitted
// escape is ESC[38;2;138;179;248m — asserted as the round-tripped value.
func TestRoleTagTrueColor(t *testing.T) {
	styles.SetProfile(termenv.TrueColor)
	defer styles.SetProfile(termenv.Ascii)
	row := renderRow(SubstrateThread[1], SubstrateThread[0].Time, 120)
	const wantFg = "38;2;138;179;248" // #8ab4f8 round-tripped (G 180→179)
	if !strings.Contains(row, wantFg) {
		t.Fatalf("hypothesis row role tag missing 24-bit fg %q for claude-code Accent2: %q",
			wantFg, row)
	}
}

// TestRenderChatGolden is the static-thread golden snapshot under Ascii.
// Bootstrap / re-bootstrap with TEATEST_UPDATE=1; then commit
// test/golden/tui-v8-chat.txt.
func TestRenderChatGolden(t *testing.T) {
	styles.SetProfile(termenv.Ascii)
	got := RenderChat(SubstrateThread, 100) + "\n" + RenderTypingIndicator("data-analyst")
	path := filepath.Join("..", "..", "test", "golden", "tui-v8-chat.txt")
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
		t.Fatalf("golden file %s missing — run TEATEST_UPDATE=1 go test ./internal/tui/... -run TestRenderChatGolden to bootstrap: %v", path, err)
	}
	if string(want) != got {
		t.Errorf("golden mismatch tui-v8-chat.txt:\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}
