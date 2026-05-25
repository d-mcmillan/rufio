// tabs.go — per-tab body content + the lineage drill-down overlay.
//
// RE-SCOPE (2026-05-15, PR-D §3): the nav is Rufio's domain
// (`substrate · fleet · channels · goals · memory`), not the v8
// prototype labels. The v8 *visual language* (borderless rows, role-
// colored agent ids, ` · ` Line separators, VDim timestamps) is kept
// verbatim per docs/design/tui-v8/README.md §7; only the data + nav are
// Rufio's. substrate is the existing chat panel (chat.go); the other
// four tabs render their fixture (fixtures.go) here, in the SAME visual
// language — each shows REAL different content, never a placeholder.
//
// New v8 tab/drill-down rendering lives HERE (not in render_*.go) so it
// never collided with the OLD TUI's render_channels/goals/lineage files
// while both coexisted. (Those legacy files + the RUFIO_TUI_PREVIEW gate
// were deleted at the G4 cutover, 2026-05-17 — v8 is now the
// unconditional `rufio tui`.)
//
// Every renderer takes the content width and renders to it; app.go
// applies its clampBlock backstop so the rounded panel border stays
// intact at every width (the PR-C border-integrity contract).
package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/d-mcmillan/rufio/internal/tui/styles"
)

// fitLine renders a single content line and hard-clamps it to width so
// it cannot overflow the panel interior (the PR-C border contract — same
// truncateToWidth chat.go uses).
func fitLine(s string, width int) string {
	if lipgloss.Width(s) > width {
		return truncateToWidth(s, width)
	}
	return s
}

// agentID renders an agent id in its v8 agent color (the role-colored
// treatment from handoff §7.2, reused for the list tabs).
func agentID(id string) string {
	return lipgloss.NewStyle().Foreground(styles.AgentColor(id)).Render(id)
}

func dim(s string) string {
	return lipgloss.NewStyle().Foreground(styles.Palette.Dim).Render(s)
}

func vdim(s string) string {
	return lipgloss.NewStyle().Foreground(styles.Palette.VDim).Render(s)
}

func fg(s string) string {
	return lipgloss.NewStyle().Foreground(styles.Palette.Fg).Render(s)
}

func warm(s string) string {
	return lipgloss.NewStyle().Foreground(styles.Palette.Warm).Render(s)
}

// sep is the ` · ` Line-color separator used between row segments
// throughout the v8 language.
func sep() string { return styles.Hairline.Render(" · ") }

// renderFleetTab renders the fleet roster (FleetAgents) as borderless v8
// rows: `<agent-id colored> · <intent dim> · <entities> · <last-seen
// vdim>` (PR-D §3). The mesh visualisation of the fleet is PR-E — not
// here.
func renderFleetTab(width int) string {
	lines := make([]string, 0, len(FleetAgents)+1)
	lines = append(lines, fitLine(
		lipgloss.NewStyle().Foreground(styles.Palette.Accent).Bold(true).Render("◆ fleet")+
			dim("  "+strconv.Itoa(len(FleetAgents))+" agents · customer:5821"), width))
	lines = append(lines, "")
	for _, a := range FleetAgents {
		row := agentID(a.ID) + sep() + dim(a.Intent) + sep() +
			fg(a.Entities) + sep() + vdim(a.LastSeen)
		lines = append(lines, fitLine(row, width))
	}
	return strings.Join(lines, "\n")
}

// renderChannelsTab renders the channel list + the selected channel's
// `@say` transcript in the v8 chat-row language (data-mapping §2 keeps
// channels distinct from the broadcast substrate). selected indexes
// ChannelThreads; app.go default-selects the first channel. List rows:
// `ch-id · opener→target · topic`; transcript rows: `<by colored> ·
// <text> <time vdim>` (PR-D §3).
// PR-G3: the channel set is now a PARAMETER (data-source-agnostic — the
// renderer does not know fixture from live). app.go passes a.tabs.Channels
// (the live projected set, loadTabs/projectChannels) instead of the
// ChannelThreads fixture global. The render logic is otherwise UNCHANGED
// (the v8 visual language is locked); only the data source moved to the
// caller per the locked G3 constraint ("prefer feeding them live data,
// not rewriting them").
func renderChannelsTab(chans []Channel, width, selected int) string {
	if len(chans) == 0 {
		return dim("(no channels)")
	}
	if selected < 0 || selected >= len(chans) {
		selected = 0
	}
	lines := make([]string, 0, len(chans)+8)
	lines = append(lines, fitLine(
		lipgloss.NewStyle().Foreground(styles.Palette.Accent).Bold(true).Render("◆ channels")+
			dim("  "+strconv.Itoa(len(chans))+" private"), width))
	lines = append(lines, "")
	for i, ch := range chans {
		marker := "  "
		idStyle := lipgloss.NewStyle().Foreground(styles.Palette.FgMute)
		if i == selected {
			marker = lipgloss.NewStyle().Foreground(styles.Palette.Accent).Render("▸ ")
			idStyle = lipgloss.NewStyle().Foreground(styles.Palette.Accent).Bold(true)
		}
		route := agentID(ch.Opener) + dim("→") + agentID(ch.Target)
		row := marker + idStyle.Render(ch.ID) + sep() + route + sep() + dim(ch.Topic)
		lines = append(lines, fitLine(row, width))
	}
	lines = append(lines, "")
	ch := chans[selected]
	lines = append(lines, fitLine(dim("─ "+ch.ID+" ─"), width))
	for _, m := range ch.Msgs {
		row := "  " + agentID(m.By) + sep() + fg(m.Text) + " " + vdim(m.Time)
		lines = append(lines, fitLine(row, width))
	}
	return strings.Join(lines, "\n")
}

// renderGoalsTab renders coordination goals as cards (PR-D §3): a state
// badge + statement (Fg) + ` · ` + author (colored) + time (vdim), with
// the `@goal-overlap` line indented under it in Warm (overlap =
// attention-grabbing).
// PR-G3: the goal set is now a PARAMETER (data-source-agnostic — the
// renderer does not know fixture from live). app.go passes a.tabs.Goals
// (the live projected set, loadTabs/projectGoals) instead of the
// GoalCards fixture global. The render logic + the locked v8 visual
// chrome (header label, badge, ⤷ overlap line) are UNCHANGED; only the
// data source moved to the caller per the locked G3 constraint.
func renderGoalsTab(cards []GoalCard, width int) string {
	lines := make([]string, 0, len(cards)*3+2)
	lines = append(lines, fitLine(
		lipgloss.NewStyle().Foreground(styles.Palette.Accent).Bold(true).Render("◆ goals")+
			dim("  "+strconv.Itoa(len(cards))+" active · customer:5821"), width))
	lines = append(lines, "")
	for i, g := range cards {
		if i > 0 {
			lines = append(lines, "")
		}
		badge := lipgloss.NewStyle().Foreground(styles.Palette.Good).Render("[" + g.State + "]")
		row := badge + " " + fg(g.Statement) + sep() + agentID(g.Author) + " " + vdim(g.Time)
		lines = append(lines, fitLine(row, width))
		if g.Overlap != "" {
			lines = append(lines, fitLine("    "+warm("⤷ "+g.Overlap), width))
		}
	}
	return strings.Join(lines, "\n")
}

// renderMemoryTab renders durable `learned/` observations as v8 rows
// (PR-D §3): `<subject:predicate=object> · <author colored> · <ago
// vdim>`. The subject:predicate=object triple is Fg; the predicate is
// Accent2 so the relation reads at a glance.
// PR-G3: the observation set is now a PARAMETER (data-source-agnostic —
// the renderer does not know fixture from live). app.go passes
// a.tabs.Memory (the live projected set — the G0 walkLearned VERBATIM via
// loadMemory) instead of the MemoryEntries fixture global. The render
// logic + the locked v8 visual chrome are UNCHANGED; only the data source
// moved to the caller per the locked G3 constraint.
func renderMemoryTab(entries []MemoryEntry, width int) string {
	lines := make([]string, 0, len(entries)+2)
	lines = append(lines, fitLine(
		lipgloss.NewStyle().Foreground(styles.Palette.Accent).Bold(true).Render("◆ memory")+
			dim("  "+strconv.Itoa(len(entries))+" observations · learned/"), width))
	lines = append(lines, "")
	for _, m := range entries {
		triple := fg(m.Subject) + dim(":") +
			lipgloss.NewStyle().Foreground(styles.Palette.Accent2).Render(m.Predicate) +
			dim("=") + fg(m.Object)
		row := triple + sep() + agentID(m.Author) + sep() + vdim(m.Ago)
		lines = append(lines, fitLine(row, width))
	}
	return strings.Join(lines, "\n")
}

// Lineage-overlay box geometry (#132). styles.Panel has no .Width(), so
// it shrink-wraps to the LONGEST content line; a real decision Statement
// (~280 chars) made the box ~290 cols wide and lipgloss.Place could not
// fit it in the terminal → a full-terminal-width band that broke the
// screen. The box is now bounded: total width ≤ lineageMaxBoxW (and ≤
// terminal − 2·margin), content soft-wraps to the text budget, and the
// box still shrink-wraps NARROWER than the cap when content is short — so
// the existing short-fixture golden (longest line 62 cols → 70-col box)
// renders byte-identically.
//
// lipgloss v1.1.0 Style.Width(W) sets the padding+content frame width
// (border is +2 OUTSIDE); so box-total = W+2, and the text area =
// W − lineageHPad. We wrap text to a budget T, measure the widest line
// produced, then set Width = thatMax + lineageHPad so the box shrink-
// wraps to the content (never re-wrapping it) but never exceeds the cap.
const (
	lineageMaxBoxW = 76 // box total cap (border + padding + content)
	lineageMargin  = 4  // min terminal gutter on each side
	lineageBorder  = 2  // rounded border, 1 col each side (added OUTSIDE Width)
	lineageHPad    = 6  // Padding(1,3) → 3 cols each side (inside Width)
)

// renderLineageOverlay renders the decision drill-down as the tree
// `rufio lineage <id>` produces (PR-D §4): a header
// (`Decision: <statement>` / `by <author> · <time>` / `Subject:
// <subject>`), a `Context bundle:` block (one ref per line, `(none)` if
// empty), and a numbered `Reasoning chain:` (`(none)` if empty). Long
// values soft-wrap (reusing chat.go wrapBody). Boxed in a rounded Panel
// border bounded to lineageMaxBoxW, centered in width/height. `esc`
// (handled in app.go) closes it.
func renderLineageOverlay(d *DecisionLineage, width, height int) string {
	accent := lipgloss.NewStyle().Foreground(styles.Palette.Accent).Bold(true)
	label := lipgloss.NewStyle().Foreground(styles.Palette.Label).Bold(true)
	bullet := lipgloss.NewStyle().Foreground(styles.Palette.Accent2)
	stepNum := lipgloss.NewStyle().Foreground(styles.Palette.Accent)

	// Bounded box: total width ≤ cap AND ≤ terminal − 2·margin; the
	// text budget T is that minus the border + padding chrome.
	boxW := lineageMaxBoxW
	if avail := width - 2*lineageMargin; avail < boxW {
		boxW = avail
	}
	inner := boxW - lineageBorder - lineageHPad
	if inner < 1 {
		inner = 1
	}

	var b strings.Builder
	// Statement: "Decision: " prefix then the (possibly long) statement,
	// soft-wrapped to the inner budget; continuation lines hang under
	// the prefix indent so the prose stays a readable block.
	stmt := wrapBody("Decision: "+d.Statement, inner)
	for i, ln := range stmt {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(accent.Render(ln))
	}
	b.WriteString("\n")
	b.WriteString(dim("by ") + agentID(d.Author) + sep() + vdim(d.Time))
	b.WriteString("\n")
	b.WriteString(dim("Subject: ") + fg(d.Subject) + dim("  #"+d.ID))
	b.WriteString("\n\n")

	b.WriteString(label.Render("Context bundle:"))
	b.WriteString("\n")
	if len(d.Bundle) == 0 {
		b.WriteString("  " + dim("(none)"))
		b.WriteString("\n")
	}
	for _, ref := range d.Bundle {
		// Wrap each ref to the inner budget less the "  • " prefix;
		// continuation lines align under the ref text.
		wrapped := wrapBody(ref, inner-4)
		for j, ln := range wrapped {
			if j == 0 {
				b.WriteString("  " + dim("•") + " " + bullet.Render(ln))
			} else {
				b.WriteString("    " + bullet.Render(ln))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")

	b.WriteString(label.Render("Reasoning chain:"))
	b.WriteString("\n")
	if len(d.Chain) == 0 {
		b.WriteString("  " + dim("(none)"))
	}
	for i, step := range d.Chain {
		n := stepNum.Render(strconv.Itoa(i + 1))
		// Wrap each step to the inner budget less the "  N. " prefix
		// (4 cols: 2-space indent + number + ". "); continuation lines
		// align under the step text.
		wrapped := wrapBody(step, inner-4)
		for j, ln := range wrapped {
			if j == 0 {
				b.WriteString("  " + n + dim(". ") + fg(ln))
			} else {
				b.WriteString("\n" + "    " + fg(ln))
			}
		}
		if i < len(d.Chain)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n\n")
	b.WriteString(dim("press esc to close"))

	// Shrink-wrap NARROWER than the cap when content is short: measure
	// the actual widest produced TEXT line (already ≤ inner — wrapBody
	// guarantees it for the wrapped fields; the few non-wrapped header
	// lines are clamped here defensively) and size the Panel to exactly
	// fit it. For the short fixture maxText = 62 → Width(68) → 70-col
	// box, byte-identical to the pre-#132 golden; for long/wrapped
	// content maxText = inner → a bounded box never wider than boxW.
	// Width(W) is the padding+content frame (lipgloss v1.1.0); the text
	// area is W − lineageHPad, so set W = maxText + lineageHPad to make
	// the content area exactly maxText (no Lip Gloss re-wrap).
	maxText := 0
	for _, ln := range strings.Split(b.String(), "\n") {
		if w := lipgloss.Width(ln); w > maxText {
			maxText = w
		}
	}
	if maxText > inner {
		maxText = inner
	}
	if maxText < 1 {
		maxText = 1
	}

	box := styles.Panel.
		Padding(1, 3).
		Width(maxText + lineageHPad).
		MaxWidth(boxW).
		Render(b.String())

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}
