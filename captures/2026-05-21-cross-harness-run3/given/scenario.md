# Rufio Cross-Harness Test — Scenario (Run 3)

You are one of 4 agents (Claude Code, Cursor, Codex, Gemini) coordinating
through Rufio alone. **Different cognitive shape this time** — Runs 1 + 2
tested decision-prioritization (consensus on a single priority). Run 3
tests **multi-step planning** — can the substrate hold a real
implementation plan together as it's collaboratively constructed?

## The task

The v1.2.0 anchor was decided in Run 1: **ship `rufio open <subject>` as
the read-dual of `attend`** — bundles 4-5 reads into one (currently every
session pays read-ceremony tax before first confirm). Now plan how to
build it.

Concretely, reach quorum on a **structured implementation plan** for
`rufio open <subject>`. The plan must include:

1. **Verb signature**: positional vs flagged args, what `--json` looks like
2. **What it reads**: the 4-5 reads it bundles (which ones, in what order)
3. **Output shape**: text (what fields, what order, what color/format) and JSON (key set)
4. **Privacy floor (#147)**: how it handles scope:agent records
5. **Test surface**: what TDD tests would lock the contract
6. **Files to create/modify**: best-guess file paths in `internal/cli/` + `internal/lib/`
7. **Open questions**: anything you can't decide without lead's input

The deliverable is a single decision-thought with the full plan in its
content (or via a chain of reasoning records that lineage assembles).

## Why this is different from Runs 1 + 2

Runs 1 + 2 were "pick one option from a list" — convergent shape, single
slot. Run 3 is "construct a coherent multi-piece artifact" — divergent
shape, many open sub-decisions, requires coordinated decomposition.

What we're watching for:
- Does the substrate hold the plan together coherently, or does it
  fragment across many half-decided threads?
- Do agents coordinate role-division (one drafts signature, one drafts
  tests, one drafts privacy) or does everyone duplicate?
- Does `reason --decision=<id>` chain naturally for plan refinement?
- Does the lineage view at the end actually surface the plan structure?

## Process

1. Read the v1.2 roadmap: `rufio recall --topics=v1-2-roadmap` (now works)
2. Read the previous decision (in learned/): the Run 1 decision is
   `rufio open <subject>` per consensus — its full content is in
   `learned/roadmap/v1-2/1779333639324-t60kgb.gdlm` if you want context
3. Declare your attention with your planning role
4. Decompose the plan into sections (use `--type=focus` thoughts to claim
   ownership of a section, or `--type=hypothesis` to propose a piece)
5. Refute / refine peers' sections
6. Synthesize via `reason --decision=<seed-decision-id>` to chain
   reasoning under a primary thread
7. Land a final decision-thought with the full plan synthesis

## End state

A single decision-thought tagged with topics `v1-2-roadmap,rufio-open-impl`
containing the structured plan. 3+ agents confirm. Auto-promote fires.
Render `rufio lineage <id>` and `rufio confirms <id>` (the new verb) for
the full chain.

## Constraints

- Stay in this directory.
- Do NOT start your own `rufio dev` daemon.
- Use `export RUFIO_AGENT_ID=agent-<your-name>` (per-shell).
- Diary at `~/rufio-cross-harness-2026-05-21-run3/diary-<you>.md`.
- Tag your topics: `--topics=v1-2-roadmap,rufio-open-impl` on all your
  writes so `recall --topics=` finds your work (per #185 cold-agent
  learning — set topics at write time, not just subject).
- ~30 min target.

## New affordances since Run 2

- `rufio recall --topics=<csv>` filter — works (since #180).
- `rufio confirms <id>` — full social-state detail view (since #181).
- Same vocab-mirror aliases: `--reason` works on confirm, `--evidence`
  works on refute (since #175).
