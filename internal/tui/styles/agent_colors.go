package styles

import (
	"hash/fnv"

	"github.com/charmbracelet/lipgloss"
)

// agentColors maps a substrate agent id to its v8 color token. The color
// drives the chat role tag, the mesh node, and the quorum dot for that
// agent (handoff §6 "Per-agent colors").
//
// Source reconciliation: the prototype `V8_AGENT_COLORS` object in
// rufio-bubbletea-v8.jsx (lines 33-41) defines the first SEVEN entries
// but OMITS `operator`. The handoff §6 per-agent table (README line 140)
// explicitly lists `operator → Accent3 (#d8b4fe, mauve)`, and the jsx
// V8Row renders operator rows in Accent3. The handoff §6 table is the
// authority here, so `operator → Accent3` is INCLUDED. This map<->jsx
// delta is intentional and reported in the PR-A handoff, not silently
// reconciled.
//
// RE-SCOPE (2026-05-15, PR-D): the Rufio customer:5821 churn-arc
// fixtures use the REAL agent ids `claude-code` / `cursor` /
// `data-analyst`. These are ADDED here (re-scope §1 / data-mapping §0
// "Fixtures — RESOLVED"); they reuse the PR-A palette tokens so the v8
// visual language is unchanged. The original fictional ids are KEPT (a
// not-yet-removed superset is harmless and PR-A's agent-color test still
// asserts them). `operator → Accent3` is shared by both the old and the
// new fixture sets.
var agentColors = map[string]lipgloss.Color{
	"runner-h-prime":   Palette.Accent,  // #a78bfa, violet
	"claude-code-1287": Palette.Accent2, // #8ab4f8, blue
	"cursor-44":        Palette.Fg,      // #ece9f5, white
	"gemini-2-fde":     Palette.Good,    // #a8e6a3, green
	"codex-research":   Palette.Label,   // #c4b5fd, lavender
	"surfer-h-99":      Palette.Warm,    // #f5b78a, peach
	"tester-h-12":      Palette.Dim,     // #7d7798, muted
	"operator":         Palette.Accent3, // #d8b4fe, mauve (handoff §6 table)

	// Rufio customer:5821 churn-arc agents (PR-D re-scope §1).
	"claude-code":  Palette.Accent2, // #8ab4f8, blue
	"cursor":       Palette.Fg,      // #ece9f5, white
	"data-analyst": Palette.Good,    // #a8e6a3, green

	// Launch-demo harness ids (P2): the FOUR real vendor harnesses the
	// live demo coordinates (claude-code / gemini-cli / cursor-cli /
	// codex-cli). The four are mutually distinct AND distinct from
	// operator's Accent3 so the chat role tags, mesh nodes, and quorum
	// dots render visibly-different harnesses at the auto-promote climax.
	// `claude-code` is already mapped above (Accent2, blue) — kept as-is,
	// distinct from the others; only gemini-cli / cursor-cli / codex-cli
	// are added here. codex-cli (OpenAI Codex CLI, the 4th vendor harness)
	// → Steel/steel-grey: a NEW dedicated identity token (NOT a reused
	// one). It was originally mapped to Label/lavender, but the maintainer
	// eyeballed it and lavender read too close to the purple-ish body /
	// agent-name text, so codex-cli now has its own token. Steel is
	// mutually distinct from the other 3 DEBATING harnesses
	// (claude-code=Accent2 blue / gemini-cli=Good green / cursor-cli=Warm
	// peach) and from operator=Accent3 mauve, AND it is deliberately
	// cooler/greyer than the muted body/agent-name text FgMute (#a39db8)
	// and distinct from Fg/Dim — the separation the eyeball required.
	"gemini-cli": Palette.Good,  // #a8e6a3, green
	"cursor-cli": Palette.Warm,  // #f5b78a, peach
	"codex-cli":  Palette.Steel, // #9aa6b8, steel grey
}

// fallbackPalette is the curated, mutually-distinct token set an
// UNMAPPED agent id deterministically hashes into (#67-U1). Rationale:
// the prior flat `→ Palette.Fg` made every not-yet-mapped agent render
// identically (the live dogfood's claude-a / claude-b / claude-c /
// claude-d were all one colour — indistinguishable role tags, mesh
// nodes, quorum dots). A stable per-id hash into this set makes distinct
// unmapped agents read distinct while staying inside the v8 token
// language.
//
// HARD CONSTRAINTS this set satisfies (asserted by TestAgentColorFallback):
//   - never Palette.Fg (the old flat sink — distinct agents must differ);
//   - never Palette.Dim — mesh.go:232 keys the pulse-skip on
//     `AgentColor(id) == Palette.Dim`; a hashed Dim would corrupt the
//     mesh pulse logic (LOAD-BEARING sentinel);
//   - never Bg/Panel/Panel2/Line/VDim (illegible on the native bg);
//   - distinct from the RESERVED known-agent identity tokens
//     (Accent2/Good/Warm/Accent3/Steel) so a fallback agent never
//     masquerades as a known harness;
//   - mutually distinct (every entry a different hex).
//
// The members are the remaining legible v8 tokens: Accent (violet),
// Label (lavender), FgMute (muted grey-violet), Ring (deep ring-violet)
// — four well-separated hues/values, enough spread for the dogfood's
// four claude-* agents and any other unmapped id.
var fallbackPalette = []lipgloss.Color{
	Palette.Accent, // #a78bfa violet
	Palette.Label,  // #c4b5fd lavender
	Palette.FgMute, // #a39db8 muted grey-violet
	Palette.Ring,   // #4a4470 deep ring-violet
}

// AgentColor returns the v8 color token for the given agent id. A MAPPED
// id resolves to its exact agentColors token (this path is byte-
// unchanged — every existing fixture/harness id is unaffected). An
// UNMAPPED id is deterministically hashed (FNV-1a, stable across calls
// and runs) into fallbackPalette so distinct not-yet-mapped agents
// render visibly distinct rather than collapsing to one flat colour
// (#67-U1). The empty string and odd ids (e.g. a trailing-space
// `"operator "`) are just inputs to the hash — always safe, always
// deterministic.
func AgentColor(id string) lipgloss.Color {
	if c, ok := agentColors[id]; ok {
		return c
	}
	h := fnv.New32a()
	// fnv.New32a().Write never returns an error (hash.Hash contract);
	// the write is on the raw id bytes so every byte (incl. a trailing
	// space) contributes to a stable, deterministic index.
	_, _ = h.Write([]byte(id))
	return fallbackPalette[h.Sum32()%uint32(len(fallbackPalette))]
}
