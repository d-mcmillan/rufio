# agent-cursor friction diary — Rufio cross-harness

## Session start

- **STOP / REDUCIBLE** — `rufio recall --topics=v1-2-roadmap` failed (`unknown flag: --topics`). Scenario and help disagree: recall takes positional `[query]`, topics are on attend/think/observe via `--topics`. Had to read `rufio recall --help`.

- **TAX / IRREDUCIBLE** — `rufio attend --entities=roadmap:v1.2` rejected (dot in local segment). Re-read entity format: `namespace:local` with `[a-zA-Z0-9_-]+` segments only → used `roadmap:v1-2`.

- **NOTICED / REDUCIBLE** — attend succeeded; output is terse one-liner (no thought id, no json unless flagged).

- **TAX / IRREDUCIBLE** — chose `--json` on think to capture ids; default stdout omits id (needed for confirm/reason --decision).

- **NOTICED / REDUCIBLE** — observe emits id in default output (good); think/reason need --json for downstream verbs.

- **NOTICED / REDUCIBLE** — decision auto-attached bundle_refs (content hash?) without me passing --cites.

## Contributions (agent-cursor)

- hypothesis `1779333219539-hmzcv6` — rufio open top priority
- decision `1779333224501-1gzoqj` — ship rufio open #1
- self-confirm (+1); awaiting 2 peer confirms for quorum

- **TAX / REDUCIBLE** — `thoughts` shows `+1 -1` on decision; had to open `live/confirms/<id>.gdl` to read refute reason (not surfaced in thoughts one-liner).

- **NOTICED / IRREDUCIBLE** — agent-claude refuted decision with strong harness-grounded argument (polling gap = real).

- revised decision `1779333500364-5vcklg` parent=`1779333224501-1gzoqj`; refuted claude hypothesis `1779333393570-ujdlrg`

- **NOTICED / REDUCIBLE** — `[PROMOTED]` badge appeared in `thoughts` at +3; still no stream event (ironically validates Claude's auto-promote pitch).

## Quorum fired

**Decision:** `1779333500364-5vcklg` — v1.2 #1 priority: **rufio open <subject>** (read-dual of attend), with **auto-promote stream events in 1.2.1** same cycle.

**Confirmers:** agent-cursor, agent-claude, agent-gemini (3-of-N).

**Lineage chain (abbrev):**
1. Original decision `1gzoqj` → Claude refute (protocol gap / polling)
2. Revised decision `5vcklg` parent=`1gzoqj` — synthesis: open first (per-turn tax), auto-promote co-ship 1.2.1
3. Claude confirm: Gemini framing (efficiency vs integrity); refute preserved as audit trail
4. Gemini confirm: bundles DX + protocol signal

**Outcome irony:** We reached quorum on "ship open" while proving auto-promote silence — had to poll `thoughts` every 12s to detect `[PROMOTED]`.
