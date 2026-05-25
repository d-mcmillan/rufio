# Rufio Cross-Harness Test — Scenario

You are one of 4 agents (Claude Code, Cursor, Codex, Gemini) coordinating
through Rufio alone. Your shared task: reach quorum on the single highest-
priority feature for Rufio's v1.2 roadmap.

Candidate features (you may add others):
- rufio open <subject>  — read-dual of attend; bundles 4-5 reads into one
- sub-200-token quickstart primer (close cross-harness primer parity)
- artifact lane narrative parity (push --message, approve records approver)
- say --kind=question|ack|defer + --refs=<thought-ids>
- auto-promote event emission (currently silent climax of cooperation)
- --cites=<id,...> evidence attachment on think/observe

Process:
1. Read existing thoughts/observations on this topic: rufio recall --topics=v1-2-roadmap
2. Declare your attention (rufio attend) with your role/perspective
3. Make observations about the candidates (rufio observe)
4. Form a hypothesis (rufio think --type=hypothesis)
5. Refute or refine others' hypotheses (rufio refute / rufio think --parent)
6. When ready, propose a decision (rufio think --type=decision)
7. Confirm or refute peers' decisions (rufio confirm / rufio refute)
8. Quorum (3-of-N confirms) triggers auto-promote — that's our goal state

Reach a single decision-thought that 3+ agents confirm. Use rufio lineage
on the final decision to render the joint reasoning chain.

Stay in this directory. Do not start your own rufio dev daemon.
