# Rufio Cross-Harness Test — Scenario (Run 2)

You are one of 4 agents (Claude Code, Cursor, Codex, Gemini) coordinating
through Rufio alone. Your shared task: reach quorum on the single highest-
priority feature for Rufio's **v1.3** roadmap.

v1.2 has already been locked from the prior cross-harness session: ship
`rufio open <subject>` in 1.2.0, then auto-promote stream events in 1.2.1.
That's already in `learned/roadmap/v1-2/` if you want to reference it.

This session is forward-looking — what's the v1.3 anchor?

You may propose any candidate. Some seed ideas from earlier structural
feedback (NOT exhaustive, NOT exclusive):
- `say --kind=question|ack|defer + --refs=<thought-ids>` (channel cognition discipline)
- Cooperation primitives: `ack`, `defer`, `yield` (turn-taking)
- `--cites=<id,...>` evidence attachment on think/observe (verifiability)
- A sub-200-token quickstart primer for harness-portability
- Artifact-lane narrative parity (`push --message`, `approve` records approver)
- Python SDK first-class release (the v1.1 plan exists, parked on the back-burner)

## Process

1. Read existing thoughts/observations:
   `rufio recall --topics=v1-3-roadmap`  ← THIS NOW WORKS (post-#180)
2. Declare your attention with your role/perspective.
3. Make observations about candidates.
4. Form hypotheses. Refute or refine others'.
5. Propose decisions. Confirm or refute peers'.
6. Use `rufio confirms <decision-id>` ← THIS IS NEW (post-#181) to see full
   social-validation state of any decision — confirms + refutes WITH text,
   plus quorum math + status (PENDING/CONTESTED/PROMOTED).
7. Quorum (3-of-N distinct confirmers, ≥0.85 confidence) triggers auto-promote.

## End state

A single decision-thought that 3+ agents confirm. Render
`rufio lineage <decision-id>` and note the chain in your diary.

## Constraints

- Stay in this directory.
- Do NOT start your own `rufio dev` daemon.
- Use `export RUFIO_AGENT_ID=agent-<your-name>` (per-shell, not user-wide).
- Diary at `~/rufio-cross-harness-2026-05-21-run2/diary-<you>.md` for
  parity, OR `./diary-<you>.md` in the substrate dir as fallback (Gemini
  used the fallback in Run 1).

## Two new affordances since Run 1 (worth knowing)

- `rufio recall --topics=v1-3-roadmap` — topic filter (was the STOP that
  bit 3 vendors in Run 1; now works).
- `rufio confirms <thought-id>` — full social-state detail view; shows
  refute reasons inline + projection ("needs +N more confirms or -M
  fewer refutes to clear 0.85"). Replaces the `cat live/confirms/<id>.gdl`
  ceremony from Run 1.
