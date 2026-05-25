package styles

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// esc is the ANSI Control Sequence Introducer. lipgloss emits color and
// attribute codes as ESC[ … m sequences; its absence means a string is
// plain text.
const esc = "\x1b["

// TestPaletteTokens asserts every one of the 16 v8 palette tokens has
// the exact hex from the handoff §6 / jsx BT_V8 (the literal source of
// truth). A drift here is a design-token regression.
func TestPaletteTokens(t *testing.T) {
	cases := []struct {
		name string
		got  lipgloss.Color
		want string
	}{
		{"Bg", Palette.Bg, "#13111c"},
		{"Panel", Palette.Panel, "#1a1726"},
		{"Panel2", Palette.Panel2, "#1f1c2e"},
		{"Fg", Palette.Fg, "#ece9f5"},
		{"FgMute", Palette.FgMute, "#a39db8"},
		{"Dim", Palette.Dim, "#7d7798"},
		{"VDim", Palette.VDim, "#4a4665"},
		{"Label", Palette.Label, "#c4b5fd"},
		{"Accent", Palette.Accent, "#a78bfa"},
		{"Accent2", Palette.Accent2, "#8ab4f8"},
		{"Accent3", Palette.Accent3, "#d8b4fe"},
		{"Good", Palette.Good, "#a8e6a3"},
		{"Warm", Palette.Warm, "#f5b78a"},
		{"Line", Palette.Line, "#2d2742"},
		{"Ring", Palette.Ring, "#4a4470"},
		{"Particle", Palette.Particle, "#c4b5fd"},
	}
	if len(cases) != 16 {
		t.Fatalf("expected 16 palette tokens, table has %d", len(cases))
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("Palette.%s = %q, want %q", c.name, string(c.got), c.want)
		}
	}
}

// TestAgentColorKnown checks all mapped agents resolve to the right v8
// token, including `operator → Accent3` (present in the handoff §6 table
// but absent from the jsx V8_AGENT_COLORS object) and the 3 Rufio
// customer:5821 churn-arc agents added in the PR-D re-scope.
func TestAgentColorKnown(t *testing.T) {
	cases := []struct {
		id   string
		want lipgloss.Color
	}{
		{"runner-h-prime", Palette.Accent},
		{"claude-code-1287", Palette.Accent2},
		{"cursor-44", Palette.Fg},
		{"gemini-2-fde", Palette.Good},
		{"codex-research", Palette.Label},
		{"surfer-h-99", Palette.Warm},
		{"tester-h-12", Palette.Dim},
		{"operator", Palette.Accent3},
		// Rufio churn-arc agents (PR-D re-scope §1).
		{"claude-code", Palette.Accent2},
		{"cursor", Palette.Fg},
		{"data-analyst", Palette.Good},
		// Launch-demo vendor harnesses (P2): codex-cli is the 4th, now
		// on its own dedicated Steel token (lavender/Label read too
		// close to the purple body text — maintainer eyeball).
		{"codex-cli", Palette.Steel},
	}
	if len(cases) != 12 {
		t.Fatalf("expected 12 known agents, table has %d", len(cases))
	}
	for _, c := range cases {
		if got := AgentColor(c.id); got != c.want {
			t.Errorf("AgentColor(%q) = %q, want %q", c.id, string(got), string(c.want))
		}
	}
}

// TestAgentColorFallback asserts the #67-U1 fallback contract for an
// UNMAPPED agent id. The contract DELIBERATELY CHANGED (was: every
// unmapped id → Palette.Fg; a flat fallback made the dogfood's
// claude-a/b/c/d all render identically). The new contract:
//
//   - deterministic & stable: the same id yields the SAME colour on
//     every call / every run (a pure hash of the id);
//   - never the flat Palette.Fg sink (so distinct unmapped agents read
//     distinct), and never the Palette.Dim sentinel (mesh.go:232 uses
//     `AgentColor(id) == Palette.Dim` as a load-bearing pulse-skip — a
//     hashed Dim would corrupt the mesh pulse logic), and never any
//     illegible structural tone (Bg/Panel/Panel2/Line/VDim);
//   - drawn from the curated fallbackPalette set (and only that set);
//   - two distinct ids GENERALLY map to distinct colours.
//
// A KNOWN id must still resolve to its mapped colour (regression guard —
// the `if c,ok:=agentColors[id];ok` path is untouched by U1).
func TestAgentColorFallback(t *testing.T) {
	// The curated fallback set membership (used by the assertions below).
	inFallbackSet := func(c lipgloss.Color) bool {
		for _, f := range fallbackPalette {
			if f == c {
				return true
			}
		}
		return false
	}

	// fallbackPalette itself must be well-formed: non-empty, and every
	// member must be a legible non-sentinel tone (never Fg sink, never
	// the Dim pulse sentinel, never a structural/illegible tone). It is
	// also ideally distinct from the reserved KNOWN-agent identity tokens
	// so a fallback agent never masquerades as a known harness.
	if len(fallbackPalette) == 0 {
		t.Fatal("fallbackPalette must be non-empty")
	}
	banned := map[lipgloss.Color]string{
		Palette.Fg:     "Fg (flat sink — distinct agents must read distinct)",
		Palette.Dim:    "Dim (mesh.go:232 pulse-skip sentinel)",
		Palette.Bg:     "Bg (illegible)",
		Palette.Panel:  "Panel (illegible)",
		Palette.Panel2: "Panel2 (illegible)",
		Palette.Line:   "Line (illegible)",
		Palette.VDim:   "VDim (illegible)",
	}
	reserved := map[lipgloss.Color]string{
		Palette.Accent2: "Accent2 (claude-code)",
		Palette.Good:    "Good (gemini-cli)",
		Palette.Warm:    "Warm (cursor-cli)",
		Palette.Accent3: "Accent3 (operator)",
		Palette.Steel:   "Steel (codex-cli)",
	}
	seen := map[lipgloss.Color]bool{}
	for i, f := range fallbackPalette {
		if why, bad := banned[f]; bad {
			t.Errorf("fallbackPalette[%d] = %q is BANNED: %s", i, string(f), why)
		}
		if why, res := reserved[f]; res {
			t.Errorf("fallbackPalette[%d] = %q collides with a reserved known-agent identity token: %s",
				i, string(f), why)
		}
		if seen[f] {
			t.Errorf("fallbackPalette[%d] = %q is a duplicate — the set must be mutually distinct",
				i, string(f))
		}
		seen[f] = true
	}

	// Per-id contract for a spread of unmapped ids incl. the empty
	// string and an odd trailing-space id.
	unmapped := []string{"unknown-agent", "", "RUNNER-H-PRIME", "operator ",
		"claude-a", "claude-b", "claude-c", "claude-d"}
	for _, id := range unmapped {
		got := AgentColor(id)
		// Deterministic & stable: a second call yields the same colour.
		if again := AgentColor(id); again != got {
			t.Errorf("AgentColor(%q) not deterministic: %q then %q",
				id, string(got), string(again))
		}
		if got == Palette.Fg {
			t.Errorf("AgentColor(%q) = Palette.Fg — the flat sink is gone (U1)", id)
		}
		if got == Palette.Dim {
			t.Errorf("AgentColor(%q) = Palette.Dim — would corrupt the mesh pulse sentinel (mesh.go:232)", id)
		}
		if !inFallbackSet(got) {
			t.Errorf("AgentColor(%q) = %q is not in the curated fallbackPalette set",
				id, string(got))
		}
	}

	// Two distinct ids generally map to distinct colours: across the
	// claude-a..d dogfood set the fallback must produce more than one
	// colour (it would be useless if they all collided).
	dogfood := map[lipgloss.Color]bool{}
	for _, id := range []string{"claude-a", "claude-b", "claude-c", "claude-d"} {
		dogfood[AgentColor(id)] = true
	}
	if len(dogfood) < 2 {
		t.Errorf("claude-a..d all hashed to one colour (%d distinct) — the fallback must spread distinct agents",
			len(dogfood))
	}

	// Regression guard: a KNOWN id is STILL its exact mapped colour
	// (the agentColors lookup path is byte-unchanged by U1).
	if got := AgentColor("claude-code"); got != Palette.Accent2 {
		t.Errorf("KNOWN id claude-code = %q, want mapped Palette.Accent2 %q (U1 must not touch the mapped path)",
			string(got), string(Palette.Accent2))
	}
	if got := AgentColor("operator"); got != Palette.Accent3 {
		t.Errorf("KNOWN id operator = %q, want mapped Palette.Accent3 %q",
			string(got), string(Palette.Accent3))
	}
}

// TestAsciiProfileStripsColor asserts that under termenv.Ascii (the
// NO_COLOR / golden-snapshot profile) a styled string carries no ANSI
// escape codes at all.
func TestAsciiProfileStripsColor(t *testing.T) {
	SetProfile(termenv.Ascii)
	out := BodyPlan.Render("rollback")
	if strings.Contains(out, esc) {
		t.Fatalf("Ascii profile leaked ANSI escapes: %q", out)
	}
	// Tab style is bold+colored; Ascii must drop both encodings too.
	if strings.Contains(TabActive.Render("substrate"), esc) {
		t.Fatalf("Ascii profile leaked escapes in TabActive: %q",
			TabActive.Render("substrate"))
	}
}

// TestTrueColorProfileEmitsColor asserts that under termenv.TrueColor a
// foreground-colored style emits a 24-bit (38;2;r;g;b) escape sequence.
func TestTrueColorProfileEmitsColor(t *testing.T) {
	SetProfile(termenv.TrueColor)
	out := BodyPlan.Render("rollback")
	if !strings.Contains(out, esc) {
		t.Fatalf("TrueColor profile emitted no ANSI escape: %q", out)
	}
	// #ece9f5 → rgb(236,233,245); lipgloss writes a 24-bit fg as
	// ESC[38;2;236;233;245m.
	if !strings.Contains(out, "38;2;236;233;245") {
		t.Fatalf("TrueColor profile did not emit 24-bit fg for Fg token: %q", out)
	}
	// Restore a deterministic profile for any later test ordering.
	SetProfile(termenv.Ascii)
}

// TestDetectAndApplyProfileNoColor asserts NO_COLOR=<non-empty> forces
// the Ascii profile (no escapes), matching detectProfile's tightened
// NO_COLOR handling.
func TestDetectAndApplyProfileNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	p := DetectAndApplyProfile()
	if p != termenv.Ascii {
		t.Fatalf("DetectAndApplyProfile with NO_COLOR=1 = %v, want Ascii", p)
	}
	if got := detectProfile(); got != termenv.Ascii {
		t.Fatalf("detectProfile with NO_COLOR=1 = %v, want Ascii", got)
	}
	if strings.Contains(GovLabel.Render("GOV"), esc) {
		t.Fatalf("NO_COLOR profile leaked escapes: %q", GovLabel.Render("GOV"))
	}
}

// TestStyleAttributes asserts the composed styles carry the attributes
// PR-B/C/D depend on: TabActive is bold, Panel has a rounded border,
// and the muted/dim foregrounds resolve to the right palette tokens.
func TestStyleAttributes(t *testing.T) {
	SetProfile(termenv.TrueColor)
	defer SetProfile(termenv.Ascii)

	if !TabActive.GetBold() {
		t.Error("TabActive must be bold (handoff §7.1)")
	}
	if !RoleTag.GetBold() {
		t.Error("RoleTag must be bold (handoff §7.2)")
	}
	if !GovLabel.GetBold() {
		t.Error("GovLabel must be bold (handoff §7.7)")
	}
	if Panel.GetBorderStyle() != lipgloss.RoundedBorder() {
		t.Error("Panel must use RoundedBorder (handoff §6 border radius)")
	}
	// PR-E.1: the forced Panel bg (#1a1726) is REMOVED — Rufio uses the
	// terminal's NATIVE background for theme portability (the forced
	// two-tone bg renders patchy and fights non-dark themes). This
	// assertion is INVERTED (was `== Palette.Panel`): the Panel must
	// carry NO background, and the border must be the visible Ring tone
	// (#4a4470) so the structure reads on the native bg. Re-adding
	// `.Background(Palette.Panel)` makes this RED.
	if got := Panel.GetBackground(); got != (lipgloss.NoColor{}) {
		t.Errorf("Panel must have NO forced background (PR-E.1 native bg), got %#v", got)
	}
	if got := Panel.GetBorderTopForeground(); got != Palette.Ring {
		t.Errorf("Panel border fg = %v, want Palette.Ring %v (PR-E.1 visible border)", got, Palette.Ring)
	}
	if got := SectionRule.GetForeground(); got != Palette.Ring {
		t.Errorf("SectionRule fg = %v, want Palette.Ring %v (PR-E.1 full-width rules)", got, Palette.Ring)
	}
	if got := AgentName.GetForeground(); got != Palette.FgMute {
		t.Errorf("AgentName fg = %v, want Palette.FgMute %v", got, Palette.FgMute)
	}
	if got := BodyReply.GetForeground(); got != Palette.FgMute {
		t.Errorf("BodyReply fg = %v, want Palette.FgMute %v", got, Palette.FgMute)
	}
	if got := TabInactive.GetForeground(); got != Palette.Dim {
		t.Errorf("TabInactive fg = %v, want Palette.Dim %v", got, Palette.Dim)
	}
	if got := TabPlaceholder.GetForeground(); got != Palette.VDim {
		t.Errorf("TabPlaceholder fg = %v, want Palette.VDim %v", got, Palette.VDim)
	}
	if got := Hairline.GetForeground(); got != Palette.Line {
		t.Errorf("Hairline fg = %v, want Palette.Line %v", got, Palette.Line)
	}
}

// TestSetProfileRebuildsStyles asserts SetProfile actually rebuilds the
// table — switching Ascii→TrueColor changes rendered output even though
// the package-level vars were already populated.
func TestSetProfileRebuildsStyles(t *testing.T) {
	SetProfile(termenv.Ascii)
	ascii := BodyPlan.Render("x")
	SetProfile(termenv.TrueColor)
	truecolor := BodyPlan.Render("x")
	if ascii == truecolor {
		t.Fatalf("SetProfile did not rebuild styles: ascii=%q truecolor=%q",
			ascii, truecolor)
	}
	SetProfile(termenv.Ascii)
}
