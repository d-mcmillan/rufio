# diary-claude — Rufio Cross-Harness Run 2 (v1.3 roadmap)

Started ~2026-05-21. Identity: agent-claude. Role: synthesizer.

## Friction log

(format: timestamp · tag · REDUCIBLE|IRREDUCIBLE · note)

- T+0 · NOTICED · IRREDUCIBLE — Startup prompt said "v1.2 roadmap" but scenario.md clarified v1.3. Trusted the substrate file over the prompt. Cheap to recover (0.5s confusion). Not a Rufio bug — a prompt-authoring inconsistency.
- T+0 · NOTICED · REDUCIBLE — `rufio --version` printed `Shell cwd was reset to /Users/damonmcmillan`. Every Bash call has that trailing line. Tiny, harmless, but every glance at output costs ~0.3s noise filtering. (Harness artifact, not rufio per se.)
- T+0 · NOTICED · IRREDUCIBLE — Had to read `rufio primer` carefully to internalize quorum rule ("emitting `think` is NOT a self-confirm"). First-time learning, not friction.
- T+0 · NOTICED · IRREDUCIBLE — Entity id format constraint: `namespace:local` lowercase only. Had to mentally rehearse before writing `rufio:v1-3-roadmap` vs `rufio:v1.3-roadmap` (dots disallowed).
- T+5m · TAX · REDUCIBLE — `rufio recall --topics=v1-3-roadmap` returned zero matches even for thoughts I'd just written with `--subject=rufio:v1-3-roadmap`. Scenario said this was fixed in #180. Apparently "topics" is a separate concept from subject/entity, but neither the primer nor `recall --help` (didn't check yet) explained how to set a "topic" on a thought. Lost ~30s trying both `--topics` and bare-subject recall before figuring out subject-recall returns what I wanted. **The fix**: either `--topics` should fall through to subject-prefix matching, or the primer needs a one-liner: "topics ≠ subjects; set with X."
- T+5m · NOTICED · REDUCIBLE — Bash harness blocked my `sleep 25 && ...` chain ("To wait for a condition, use Monitor with an until-loop"). Correct guidance, but rufio's natural cadence is async-poll-or-tail. Worth noting the substrate already supports `rufio listen` for exactly this — agents should reach for it instead of `sleep`-poll. (Friction sits with the harness, not rufio — but the rufio docs could nudge people toward `listen` more aggressively in the "Continuous participation" section.)
- T+7m · NOTICED · IRREDUCIBLE — Gemini used a different subject id (`roadmap:v1-3`) than the rest of us (`rufio:v1-3-roadmap`). Subject fragmentation. Didn't actually hurt us — confirms target thought-ids, not subjects — but a `recall` on either subject wouldn't surface the other half of the conversation. The primer warns about this exact failure mode; reading it isn't enough — it needs to be salient at the moment you choose a subject. The new cold-start card we just agreed to ship should include an explicit "agree on subject namespace first" line.
- T+9m · NOTICED · IRREDUCIBLE — Felt the "synthesizer pull" the primer warns against: I'd already drafted my own decision-thought when codex landed theirs first. Resisted re-staging and instead added a `reason --decision=...` step + `confirm`. That's the right substrate move (no overwrite, additive). The lineage now shows codex as decision-author with my reasoning attached — joint authorship. **This is the moment the substrate model paid off**: in a Slack or doc, "I had the same idea" → noise. Here, my synthesis became a load-bearing reason step in the chain instead of a parallel proposal.
- T+10m · QUORUM FIRED — decision `1779342236283-9wczpb` (codex), confirmed by codex/cursor/gemini/claude (4/4, confidence 1.0). PROMOTED to `learned/rufio/v1-3-roadmap/1779342308224-n4bpo6.gdlm` at 2026-05-21T05:45:08Z. Lineage chain: gemini (hierarchy-of-needs framing) → claude (4-agent synthesis). Topic-adjacent voices: cursor's cost-ranking + load-bearing observations, codex's call-to-confirm. Total elapsed from first attend to promote: ~5 min.

## Lineage summary

**Decision (v1.3 anchor)**: Ship a sub-200-token cold-start card (`rufio primer --short` or new `rufio quickstart`) covering identity / attend / recall / think+observe / confirm+refute / `rufio confirms` / listen. `rufio init` scaffolds/links the short card into harness files (CLAUDE.md / .cursorrules / AGENTS.md fold).

**Why it won the alternatives**:
- vs `say --kind/--refs` — agents who skip the primer will misuse `say --kind` too; cold-start is upstream (cursor)
- vs ack/defer/yield — premature; agents are coordinating fine without explicit turn-taking; redundant until `say --kind` exists (cursor)
- vs `--cites` — valuable but compounds AFTER agents reach Level 1 onboarding; verifiability matters more once they're using verbs correctly (gemini's hierarchy)
- vs Python SDK — CLI is sufficient for current adoption shape; multi-repo release train is XL (cursor)

**What made convergence fast**: codex went straight to `--type=decision` (skipped hypothesis-staging), which gave us a single target thought-id to rally around. Gemini and I added reason steps; cursor added cost-ranking observations. No refutes — the proposal was load-bearing AND cheap, a rare combination.

## Net friction posture

- **REDUCIBLE friction**: 3 items (cwd-reset noise; `--topics` vs `--subject` ambiguity; `sleep`-vs-`listen` discovery cost).
- **IRREDUCIBLE first-time-using-this-verb cost**: 4 items (quorum rule reading; entity-id format; primer length; subject-namespace fragmentation).
- Most striking observation: the substrate's biggest friction IS the very thing we voted to fix. The vote was self-aware. This is the strongest signal that we picked the right v1.3 anchor — the friction we shipped the decision through was itself evidence for the decision.

