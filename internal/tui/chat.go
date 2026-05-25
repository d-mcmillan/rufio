// chat.go — static threaded chat-panel rendering for the v8 TUI.
//
// Faithful character-cell port of the jsx `V8Row` / `QuorumDots` /
// `timestampVisible` from
// docs/design/tui-v8/reference/rufio-bubbletea-v8.jsx (lines 61-159 and
// 251-265), per handoff §7.2 (chat rows), §7.3 (timestamp suppression)
// and §7.4 (quorum dots).
//
// ADD-ONLY (PR-B): RenderChat / RenderTypingIndicator are not yet wired
// into `rufio tui` — the old internal/tui render path is the default
// until the PR-F cutover. This file is dead-but-compiled new code that
// the v8 app.go (PR-C) will consume.
//
// ── px → cell mapping (documented once, applied consistently) ──────────
//
// The jsx is browser CSS, so its spacing is in px. The handoff §6
// "Spacing & layout" anchors the conversion ("18px ≈ 2 cells", i.e.
// ≈ 2.6 px per character cell at typical terminal font sizes). Applying
// `cells ≈ round(px / 2.6)` to the jsx `paddingLeft` values
// (rufio-bubbletea-v8.jsx line 109) gives the leading-space indents:
//
//	op    paddingLeft 2px  → 1 cell   (indentOp)
//	plan  paddingLeft 10px → 4 cells  (indentPlan)
//	reply paddingLeft 26px → 10 cells (indentReply)
//	typing paddingLeft 28px → 11 cells (indentTyping; jsx line 259)
//
// These four are the ONLY indents in this file and they all derive from
// the single ≈2.6 px/cell ratio above. The reviewer checks consistency,
// not the exact ratio — the ratio is fixed here and never re-derived.
// Other jsx px gaps (marginLeft:8 before quorum, marginLeft:4 before the
// counter) round to ≈3 and ≈2 cells respectively; we use a 2-space gap
// before the quorum dot group and a single space before the n/total
// counter, which reads correctly in a monospace cell grid.
package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// Leading-space indents per row kind. See the px→cell note in the
// package/file doc comment above for the derivation.
const (
	indentOp     = 1
	indentPlan   = 4
	indentReply  = 10
	indentTyping = 11
)

// Row glyphs (jsx line 99: `isOp ? '›' : (isPlan ? '◆' : '↳')`).
const (
	glyphOp    = "›"
	glyphPlan  = "◆"
	glyphReply = "↳"
)

// dotVoted / dotUnvoted are the quorum glyphs (jsx line 84).
const (
	dotVoted   = "●"
	dotUnvoted = "○"
)

// caretGlyph is the "last message" block (jsx lines 134-140 — a 7×12px
// block with the `r-blink 1s steps(1)` keyframe). PR-F blinks it at the
// 1000ms/50%-duty cadence via caretCell (shared with the composer
// caret); ON=▮, OFF=same-width space (no row reflow). caretGlyph stays
// the ON glyph const (caretCell renders it).
const caretGlyph = "▮"

// ellipsis is the residual-clip marker. V8B-M1 (RESOLVED): the body is
// now SOFT word-wrapped (wrapBody) into the available width with a
// hanging continuation indent — the `…` is no longer the long-body
// path. It survives ONLY as (a) the cap marker on the maxWrapLines-th
// line of a pathologically long body and (b) truncateToWidth's
// last-resort clamp (still used by app.go's clampLine border backstop).
const ellipsis = "…"

// maxWrapLines bounds how many lines one body may wrap to. A single
// giant body must not be able to blow the fixed chat-panel height: the
// feed's topTruncate clamp (app.go) is line-count based and would just
// drop everything ABOVE a runaway body, so we cap the body itself.
// Lines 1..maxWrapLines-1 wrap normally; the maxWrapLines-th line holds
// the remaining text truncated with `…` (the only legitimate residual
// clip). 6 lines is a generous multi-sentence `think`/`observe` budget
// at the launch-demo panel width while staying well under the panel
// inner height (topTruncate stays the further backstop).
const maxWrapLines = 6

// wrapBody soft-wraps s into lines no wider than width, runewidth-aware
// (lipgloss.Width — the SAME width helper truncateToWidth uses; no new
// dependency). Rules (V8B-M1):
//
//   - greedy space-break: words are packed onto a line until the next
//     word would exceed width, then a new line starts; the boundary
//     space is consumed (no trailing space, no leading space).
//   - over-long token: a single token whose own width exceeds width is
//     rune-split (never mid-cell for a wide glyph) into width-wide
//     chunks — only over-long tokens break mid-token; normal prose
//     breaks on spaces.
//   - cap: at most maxWrapLines lines; if more are needed the last line
//     is truncateToWidth'd with `…` so one body can't blow the panel
//     height.
//
// A body that already fits returns a single unchanged element (the
// short-body byte-identical-render invariant).
func wrapBody(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if lipgloss.Width(s) <= width {
		return []string{s}
	}

	// chunkToken rune-splits one over-long token into <=width segments.
	chunkToken := func(tok string) []string {
		var out []string
		var cur strings.Builder
		curW := 0
		for _, r := range tok {
			rw := lipgloss.Width(string(r))
			if curW+rw > width && curW > 0 {
				out = append(out, cur.String())
				cur.Reset()
				curW = 0
			}
			cur.WriteRune(r)
			curW += rw
		}
		if curW > 0 {
			out = append(out, cur.String())
		}
		return out
	}

	var lines []string
	var cur strings.Builder
	curW := 0
	flush := func() {
		lines = append(lines, cur.String())
		cur.Reset()
		curW = 0
	}
	for _, word := range strings.Fields(s) {
		wW := lipgloss.Width(word)
		if wW > width {
			// Over-long token: close the current line, then emit the
			// token's rune-chunks; the last chunk seeds the new line so
			// following words keep packing after it.
			if curW > 0 {
				flush()
			}
			chunks := chunkToken(word)
			for i, ch := range chunks {
				if i < len(chunks)-1 {
					lines = append(lines, ch)
				} else {
					cur.WriteString(ch)
					curW = lipgloss.Width(ch)
				}
			}
			continue
		}
		sep := 0
		if curW > 0 {
			sep = 1 // a single joining space
		}
		if curW+sep+wW > width {
			flush()
			sep = 0
		}
		if sep == 1 {
			cur.WriteByte(' ')
			curW++
		}
		cur.WriteString(word)
		curW += wW
	}
	if curW > 0 || len(lines) == 0 {
		flush()
	}

	// Cap: bound the line count so one body can't blow the panel height.
	if len(lines) > maxWrapLines {
		kept := append([]string(nil), lines[:maxWrapLines-1]...)
		rest := strings.Join(lines[maxWrapLines-1:], " ")
		kept = append(kept, truncateToWidth(rest, width))
		lines = kept
	}
	return lines
}

// splitClock parses an "HH:MM:SS" string into hour, minute, second.
// Mirrors the jsx `prev.time.split(':').map(Number)` (line 65). A
// malformed component parses as 0 (matching JS `Number(”) === NaN`'s
// downstream "treated as a gap" behaviour closely enough — the fixture
// times are always well-formed; this is defensive only).
func splitClock(t string) (h, m, s int) {
	parts := strings.Split(t, ":")
	if len(parts) != 3 {
		return 0, 0, 0
	}
	h, _ = strconv.Atoi(parts[0])
	m, _ = strconv.Atoi(parts[1])
	s, _ = strconv.Atoi(parts[2])
	return h, m, s
}

// showTimestamp reports whether a row's timestamp should be rendered,
// given the current and previous row times as "HH:MM:SS" strings. It is
// a verbatim port of the jsx `timestampVisible` (rufio-bubbletea-v8.jsx
// lines 61-68) and handoff §7.3:
//
//   - prev == "" (no previous row / first message) → show.
//   - the minute OR hour rolled over from prev → show.
//   - otherwise show iff |(cS-pS) + (cM-pM)*60| >= 30 seconds.
//
// The handoff §7.3 worked example: prev 14:02:15, curr 14:02:47 → a
// 32-second gap → show.
func showTimestamp(curr, prev string) bool {
	if prev == "" {
		return true
	}
	pH, pM, pS := splitClock(prev)
	cH, cM, cS := splitClock(curr)
	if pM != cM || pH != cH {
		return true
	}
	gap := (cS - pS) + (cM-pM)*60
	if gap < 0 {
		gap = -gap
	}
	return gap >= 30
}

// renderQuorumDots renders the inline quorum glyph row that follows a
// plan's chips, per jsx `QuorumDots` (rufio-bubbletea-v8.jsx lines
// 70-92) and handoff §7.4.
//
// CORRECTNESS FIX (P1): the dots represent the LIVE distinct confirmers
// progressing toward the threshold — `Total` distinct slots, of which
// `len(q.Yes)` are filled — and the rendered dots MUST agree with the
// live denominator (docs/design/tui-v8/README.md §7.4 +
// tui-v8-data-mapping.md §0 OPEN-2, LOCKED). It therefore renders
// min(len(q.Yes), q.Total) filled `●` followed by the remaining hollow
// `○` up to q.Total — a COUNT toward Total, decoupled from the obsolete
// fixture slice `QuorumOrder`. The prior render iterated QuorumOrder
// (the 3 churn-arc FIXTURE ids) and lit a slot only when its id was in
// q.Yes; for ARBITRARY live confirmer ids (e.g. the launch demo's
// claude-code / gemini-cli / cursor-cli) only ids that happened to be in
// QuorumOrder could ever fill, so a full live 3/3 quorum rendered a
// near-empty dot row beside a correct `3/3` counter (demo-fatal). Filled
// dots are colored by the confirmers in confirm order (q.Yes order — the
// sorted-deduped tally projectThread/applyQuorumThreshold produced);
// hollow dots stay VDim; the trailing `n/total` counter stays Dim.
// Glyphs, spacing, the counter, and the per-confirmer color treatment
// are unchanged — only the fill SOURCE moved from the dead fixture slot
// list to the live confirmer count.
func renderQuorumDots(q *Quorum) string {
	var b strings.Builder
	filled := len(q.Yes)
	if filled > q.Total {
		filled = q.Total // clamp: never more dots than the denominator.
	}
	for i := 0; i < q.Total; i++ {
		if i < filled {
			b.WriteString(lipgloss.NewStyle().
				Foreground(styles.AgentColor(q.Yes[i])).
				Render(dotVoted))
		} else {
			b.WriteString(lipgloss.NewStyle().
				Foreground(styles.Palette.VDim).
				Render(dotUnvoted))
		}
	}
	counter := strconv.Itoa(len(q.Yes)) + "/" + strconv.Itoa(q.Total)
	b.WriteString(" ")
	b.WriteString(lipgloss.NewStyle().
		Foreground(styles.Palette.Dim).
		Render(counter))
	return b.String()
}

// renderChips renders a plan's sub-task chips, per jsx lines 141-151.
//
// Faithful-as-possible degrade (handoff §9 "what's faked" sanctions
// dropping effects terminals can't do): the jsx uses a translucent
// Accent2 background (`${accent2}1a` = 10% alpha). Terminals cannot
// alpha-blend a fill against the panel, so each chip is rendered as
// Accent2 text wrapped in a single leading + trailing space and NO
// background. Multiple chips are separated by a single space.
func renderChips(chips []string) string {
	chipStyle := lipgloss.NewStyle().Foreground(styles.Palette.Accent2)
	parts := make([]string, 0, len(chips))
	for _, c := range chips {
		parts = append(parts, chipStyle.Render(" "+c+" "))
	}
	return strings.Join(parts, " ")
}

// rowPrefix builds the non-body portion of a row that precedes the body
// text: indent, optional left rail, glyph, UPPERCASE role tag, agent
// name, and the `·` separator. It returns the rendered prefix and its
// visible cell width so renderRow can budget the body against the
// terminal width.
//
// When selected is true the FIRST gutter cell is a clean foreground-only
// `▸` (Accent, bold) on the row's NORMAL background — NEVER a
// background-filled block. The marker is composed into the gutter HERE,
// before any rail styling, so a selected plan row's first cell is the
// foreground glyph and the agent-color rail is shortened by exactly one
// cell (visible width is unchanged). This replaces the old post-render
// applySelectionMarker, which spliced the marker into the middle of the
// plan row's background-styled rail escape and left the marker cell
// bg-filled (the "broken solid blue/cyan block" — resolves V8D-M1).
func rowPrefix(m ThreadMsg, selected bool) (string, int) {
	agentColor := styles.AgentColor(m.Who)

	// marker is the foreground-only selection glyph (no background).
	marker := lipgloss.NewStyle().
		Foreground(styles.Palette.Accent).
		Bold(true).
		Render(selectionMarker)

	var indent int
	var glyph string
	switch m.Kind {
	case kindOp:
		indent = indentOp
		glyph = glyphOp
		// op glyph is Accent3 (jsx: operator's agent color == Accent3;
		// handoff §7.2 "Operator (kind: op) | › (Accent3)"). AgentColor
		// resolves "operator" → Accent3, so this is consistent.
	case kindPlan:
		indent = indentPlan
		glyph = glyphPlan
	case kindReply:
		indent = indentReply
		glyph = glyphReply
	}

	glyphStyled := lipgloss.NewStyle().
		Foreground(agentColor).
		Render(glyph)

	roleTag := styles.RoleTag.
		Foreground(agentColor).
		Render(strings.ToUpper(m.Role))

	agentName := styles.AgentName.Render(m.Who)

	sep := styles.Hairline.Render(" · ")

	var b strings.Builder
	// Gutter. The first cell is the selection marker when selected
	// (foreground-only, normal bg); otherwise it is the rail / indent
	// per the row kind. Width is identical either way (marker = 1 cell
	// replacing exactly 1 cell).
	switch m.Kind {
	case kindPlan:
		// 2-cell solid bar in the agent color (jsx line 108:
		// `borderLeft: 2px solid ${accent}`; handoff §7.2). When
		// selected the marker takes cell 0 (clean fg, normal bg) and the
		// bar is the remaining 1 cell — so the marker is never on a
		// bg-filled cell.
		if selected {
			b.WriteString(marker)
			b.WriteString(lipgloss.NewStyle().
				Background(agentColor).
				Render(" "))
		} else {
			b.WriteString(lipgloss.NewStyle().
				Background(agentColor).
				Render("  "))
		}
		if indent > 2 {
			b.WriteString(strings.Repeat(" ", indent-2))
		}
	case kindReply:
		// thin `│` rail in Line color (jsx lines 114-119; handoff §7.2).
		// When selected the marker REPLACES the rail glyph (1 cell).
		if selected {
			b.WriteString(marker)
		} else {
			b.WriteString(lipgloss.NewStyle().
				Foreground(styles.Palette.Line).
				Render("│"))
		}
		if indent > 1 {
			b.WriteString(strings.Repeat(" ", indent-1))
		}
	default: // op (and any unknown kind) — plain indent, no rail.
		if selected {
			b.WriteString(marker) // replace the first indent space
			if indent > 1 {
				b.WriteString(strings.Repeat(" ", indent-1))
			}
		} else {
			b.WriteString(strings.Repeat(" ", indent))
		}
	}
	b.WriteString(glyphStyled)
	b.WriteString(" ")
	b.WriteString(roleTag)
	b.WriteString(" ")
	b.WriteString(agentName)
	b.WriteString(sep)

	out := b.String()
	return out, lipgloss.Width(out)
}

// renderRow renders a single chat row. curr/prev times drive timestamp
// suppression. The row layout follows the jsx `V8Row` element order
// exactly (rufio-bubbletea-v8.jsx lines 103-157):
//
//	prefix · body · [last-caret] · [chips] · [quorum dots] · [timestamp]
//
// ── Width contract (the row NEVER exceeds `width`) ────────────────────
//
// The whole row — prefix + body + every trailing decoration — fits
// within `width`. This is the load-bearing invariant for the chat
// panel: app.go renders rows inside a bordered panel, so a row that
// overflowed `width` would wrap and the continuation would slam flush
// against the panel border (PR-C Defect 1). The fit policy, in
// decreasing priority of what we keep:
//
//  1. prefix (glyph + role tag + agent name + sep) — always kept; it
//     is the row's identity. (At pathological widths the prefix itself
//     may exceed `width`; that is a tiny-terminal edge, not the
//     screenshot case, and app.go's clampLine is the final backstop.)
//  2. caret + quorum dots — short, structurally meaningful, kept.
//  3. timestamp — dropped FIRST if decorations don't fit (it is the
//     §7.3-suppressible element; losing it is the cheapest fidelity
//     cost).
//  4. chips — clipped (dropped wholesale) NEXT if still over budget.
//  5. body — SOFT word-wrapped (V8B-M1, wrapBody) into whatever width
//     remains after the prefix and the surviving decorations, with a
//     hanging continuation indent under the body's start column.
//
// V8B-M1 (RESOLVED): the body now WRAPS instead of `…`-clipping. The
// width contract is preserved per-line — every emitted line (line 1
// prefix+seg+decorations, and each prefixW-indented continuation) fits
// `width`, so the border stays intact; a multi-line row contributes
// multiple "\n" feed lines and app.go's existing line-count height
// clamp keeps the panel height invariant unchanged.
//
// renderRow is the non-selected entry point (kept at its original
// 3-arg signature so the chat_test.go row tests are unaffected); it
// delegates with selected=false and caret counter 0 (frame-0 → ON →
// byte-identical to the pre-PR-F static caret, so the chat golden +
// chat_test.go stay unchanged).
func renderRow(m ThreadMsg, prev string, width int) string {
	return renderRowSelected(m, prev, width, false, 0)
}

// renderRowSelected is renderRow with the selection flag + the 500ms
// caret-blink counter threaded through. The selection marker is
// composed into the gutter as a clean foreground glyph (V8D-M1). The
// `m.Last` caret blinks via caretCell at caretCounter (even=ON ▮,
// odd=OFF space). WIDTH CONTRACT: the caret cell is ALWAYS 1 visible
// cell (▮ or space) so caretStr's width is constant across blink
// frames — the body budget (decoW) does not change, the row never
// re-truncates, never reflows, and the panel border stays intact at
// every frame. caretCounter 0 ⇒ ON ⇒ byte-identical to the pre-PR-F
// static `▮` (frame-0 invariant).
func renderRowSelected(m ThreadMsg, prev string, width int, selected bool, caretCounter int) string {
	prefix, prefixW := rowPrefix(m, selected)

	bodyStyle := styles.BodyReply
	if m.Kind == kindPlan {
		bodyStyle = styles.BodyPlan
	}

	// Pre-render the trailing decorations so we know their true cell
	// widths and can budget the body against prefix + decorations
	// (not just the prefix — that was the Defect-1 width-contract bug).
	caretStr := ""
	if m.Last {
		// Blink — but ALWAYS a 1-cell string (▮ when on, a same-width
		// space when off) so decoW() is invariant across blink frames
		// (no row reflow / border break). At caretCounter 0 this is ▮
		// (frame-0).
		caretStr = caretCell(caretOn(caretCounter),
			lipgloss.Color(styles.AgentColor(m.Who)))
	}
	chipsStr := ""
	if len(m.Chips) > 0 {
		chipsStr = "  " + renderChips(m.Chips)
	}
	quorumStr := ""
	if m.Quorum != nil {
		quorumStr = "  " + renderQuorumDots(m.Quorum)
	}
	tsStr := ""
	if showTimestamp(m.Time, prev) {
		tsStr = "  " + lipgloss.NewStyle().
			Foreground(styles.Palette.VDim).
			Render(m.Time)
	}

	// Degrade decorations until prefix + caret + (chips) + quorum +
	// (timestamp) leaves at least a minimal body budget. Order per the
	// fit policy: drop timestamp first, then chips.
	const minBody = 4
	decoW := func() int {
		return lipgloss.Width(caretStr) + lipgloss.Width(chipsStr) +
			lipgloss.Width(quorumStr) + lipgloss.Width(tsStr)
	}
	if prefixW+decoW()+minBody > width && tsStr != "" {
		tsStr = "" // drop timestamp first (§7.3-suppressible).
	}
	if prefixW+decoW()+minBody > width && chipsStr != "" {
		chipsStr = "" // then clip chips wholesale.
	}

	// Body gets whatever remains after prefix + surviving decorations.
	avail := width - prefixW - decoW()
	if avail < minBody {
		avail = minBody
	}

	// V8B-M1: SOFT word-wrap the body into `avail` instead of the old
	// `…`-hard-clip. Line 1 = prefix + first wrapped segment + the
	// trailing decorations (caret/chips/quorum/timestamp belong to the
	// ROW IDENTITY and to the newest-anchor line — they stay on line 1,
	// unchanged, so the decoration-aware fit policy + width contract are
	// untouched). Lines 2..N are the continuation: a prefixW-wide
	// hanging indent (so the prose hangs exactly under the column where
	// the body began) + the styled wrapped segment. The continuation
	// indent is plain spaces (no rail/glyph restyle) — nothing in the
	// prefix/gutter is restructured. Joining the row's lines with "\n"
	// makes each wrapped line a real feed line so app.go's line-count
	// topTruncate/clampBlock clamp keeps the panel height invariant with
	// NO clamp-accounting change (it already splits on "\n").
	segs := wrapBody(m.Text, avail)

	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(bodyStyle.Render(segs[0]))
	b.WriteString(caretStr)  // jsx lines 134-140
	b.WriteString(chipsStr)  // jsx lines 141-151
	b.WriteString(quorumStr) // jsx line 152
	b.WriteString(tsStr)     // jsx lines 153-155, §7.3-gated above

	hang := strings.Repeat(" ", prefixW)
	for _, seg := range segs[1:] {
		b.WriteString("\n")
		b.WriteString(hang)
		b.WriteString(bodyStyle.Render(seg))
	}

	return b.String()
}

// truncateToWidth cuts s so its visible width is at most w, appending an
// ellipsis. Operates on runes (so multi-byte glyphs are not split) and
// measures with lipgloss.Width (runewidth-aware). V8B-M1: the body no
// longer routes here (it word-wraps via wrapBody); this remains the
// residual-clip primitive for wrapBody's maxWrapLines cap line and for
// app.go's clampLine border backstop.
func truncateToWidth(s string, w int) string {
	if w <= 1 {
		return ellipsis
	}
	runes := []rune(s)
	// Reserve one cell for the ellipsis.
	budget := w - 1
	cut := 0
	width := 0
	for i, r := range runes {
		rw := lipgloss.Width(string(r))
		if width+rw > budget {
			break
		}
		width += rw
		cut = i + 1
	}
	return string(runes[:cut]) + ellipsis
}

// RenderChat renders the full threaded message list for `thread` at the
// given terminal width. Rows are joined vertically. Per the jsx vertical
// rhythm (rufio-bubbletea-v8.jsx lines 107-108: plans get
// `padding:'5px 0 3px'` + `marginTop:4`; op/reply get the tight
// `padding:'1px 0'`), a single blank line precedes each plan row and
// op/reply rows are tight (no leading blank). The first row never gets a
// leading blank even if it is a plan.
//
// This is the no-selection renderer (RenderChatSelected with selected =
// -1). Kept as the stable PR-B entry point for chat_test.go's golden;
// it renders at caret counter 0 (frame-0 → ON → byte-identical).
func RenderChat(thread []ThreadMsg, width int) string {
	return RenderChatSelected(thread, width, -1)
}

// selectionMarker is the gutter glyph drawn in the first cell of the
// selected substrate row (PR-D §4: the user must see what `enter` will
// drill into). One cell wide so it does not disturb the row's width
// budget — rowPrefix composes it into the gutter as a clean foreground
// glyph in place of the first rail/indent cell (NOT a post-render
// splice; resolves V8D-M1).
const selectionMarker = "▸"

// RenderChatSelected renders the threaded message list and draws the
// selection marker (`▸`, Accent foreground, normal background) as the
// first gutter cell of row `selected` (a SubstrateThread index; -1 = no
// selection). The marker is threaded through renderRowSelected →
// rowPrefix so it is a clean foreground glyph composed into the gutter
// BEFORE rail styling — never a background-filled block (V8D-M1
// resolved). Row width is unchanged so the border-integrity contract
// still holds.
//
// Stable PR-D 3-arg entry point (chat_test.go pins it) — renders at
// caret counter 0 (frame-0 → ON → byte-identical goldens). The
// caret-blink-aware path is renderChatSelectedAt, which app.go calls
// with App.anim.caret.
func RenderChatSelected(thread []ThreadMsg, width, selected int) string {
	return renderChatSelectedAt(thread, width, selected, 0)
}

// renderChatSelectedAt is RenderChatSelected with the 500ms caret-blink
// counter threaded through to each row's `m.Last` caret. caretCounter
// 0 == ON (frame-0). The caret cell is always 1 wide (▮ or space) so a
// blink never reflows a row (border-integrity holds at every frame).
func renderChatSelectedAt(thread []ThreadMsg, width, selected, caretCounter int) string {
	if len(thread) == 0 {
		return ""
	}
	lines := make([]string, 0, len(thread)*2)
	prevTime := ""
	for i, m := range thread {
		if i > 0 && m.Kind == kindPlan {
			lines = append(lines, "")
		}
		lines = append(lines, renderRowSelected(m, prevTime, width, i == selected, caretCounter))
		prevTime = m.Time
	}
	return strings.Join(lines, "\n")
}

// typingDotsFrames is the 220ms 3-state typing-dots cycle (handoff
// §8.4 "220 ms tick, 3-state cycle"). `TypingDots` is referenced but
// NOT defined in any reference jsx (rufio-bubbletea-v8.jsx line 263
// uses it; it has no body anywhere in docs/design/tui-v8/reference/),
// so the canonical 3-state is the growing-dots cycle whose frame[0] is
// the `···` the README §section-8.4-adjacent layout (README line 80
// `typing ···`) and the pre-PR-F static render both show. EVERY frame
// is EXACTLY 3 cells wide (`·` + spaces) — a stable cell width so the
// typing row never reflows and the panel border stays intact at every
// frame. frame[0] == `···` is the frame-0 invariant (the chat golden
// renders the typing line at counter 0).
var typingDotsFrames = []string{"···", "·  ", "·· "}

// typingDots returns the 3-state cycle frame for the given 220ms
// counter (App.anim.typing): typingDotsFrames[counter % 3]. counter 0
// ⇒ `···` (frame-0, byte-identical to the pre-PR-F static).
func typingDots(counter int) string {
	i := counter % len(typingDotsFrames)
	if i < 0 {
		i += len(typingDotsFrames)
	}
	return typingDotsFrames[i]
}

// RenderTypingIndicator is the stable PR-B 1-arg entry point
// (chat_test.go pins it) — renders at typing counter 0 (frame-0 →
// `···` → byte-identical to the chat golden). The animated path is
// renderTypingIndicatorAt, which app.go calls with App.anim.typing.
func RenderTypingIndicator(agent string) string {
	return renderTypingIndicatorAt(agent, 0)
}

// renderTypingIndicatorAt renders the "<agent> typing ···" line
// (jsx lines 256-264) with the 220ms 3-state dots cycle. Indent =
// indentTyping (jsx `paddingLeft:28`); agent name in its agent color;
// "typing" Dim (jsx line 258); the dots Accent2 (jsx `<TypingDots
// color={p.accent2}/>`, line 263). The dots are always 3 cells so the
// line width is stable across frames (border-integrity holds).
func renderTypingIndicatorAt(agent string, typingCounter int) string {
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", indentTyping))
	b.WriteString(lipgloss.NewStyle().
		Foreground(styles.AgentColor(agent)).
		Render(agent))
	b.WriteString(" ")
	b.WriteString(lipgloss.NewStyle().
		Foreground(styles.Palette.Dim).
		Render("typing"))
	b.WriteString(" ")
	b.WriteString(lipgloss.NewStyle().
		Foreground(styles.Palette.Accent2).
		Render(typingDots(typingCounter)))
	return b.String()
}
