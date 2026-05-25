# agent-cursor friction diary — Run 2

Started: 2026-05-21

## Session start

- **NOTICED / REDUCIBLE**: Prompt file says `v1-2-roadmap` recall but `given/scenario.md` says v1.3 — had to reconcile harness prompt vs substrate scenario (~0.5s).
- **TAX / REDUCIBLE**: `attend` requires `--entities` — not obvious from scenario alone; discovered via `rufio attend --help` (~5s).
- **TAX / IRREDUCIBLE**: First-time mapping think types (hypothesis vs decision) and confirm thought-id format `1779342236283-9wczpb` vs short `9wczpb` in thoughts listing (~5s).
- **NOTICED / REDUCIBLE**: `rufio thoughts` truncates content with `...` — had to grep live/outbox for full text (~1s).
- **NOTICED / REDUCIBLE**: `rufio confirms` is excellent — quorum math + evidence inline replaced Run-1 `cat live/confirms/` ceremony.

## Quorum fired (2026-05-21T05:45:08Z)

**Decision:** `1779342236283-9wczpb` by agent-codex  
**Promoted to:** `learned/rufio/v1-3-roadmap/1779342308224-n4bpo6.gdlm`

**v1.3 anchor:** Sub-200-token cold-start card (`rufio primer --short` or `rufio quickstart`), scaffolded via `rufio init` into harness files.

**Confirmers:** agent-codex, agent-cursor, agent-gemini (+ agent-claude shortly after promote)

**Lineage chain (summary):**
1. Context: `given/scenario.md@v1`
2. Observations: Run-1 primer STOPs (claude), implementation-cost ranking + v1.2 compounding (cursor)
3. Decision: codex `9wczpb`
4. Hypothesis: cursor `0pygca` (primer > say --kind > ack/defer > --cites)
5. Confirms → auto-promote observation `n4bpo6`

**My engineering note for synthesis:** say `--kind/--refs` is the right v1.3.1 follow-on once cold-start is embedded; claude's refinement to prefer `rufio quickstart` verb over `--short` flag is sound for harness determinism.
