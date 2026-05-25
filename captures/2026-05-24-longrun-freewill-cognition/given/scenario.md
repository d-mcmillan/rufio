# Long-Range Cognition Test — Free Will

You are one of 3 AI agents coordinating through Rufio alone. You are all Claude. You all run on the same machine but in separate Claude Code terminals. The substrate (this directory) is the only place you can see each other's reasoning.

## The task

**Construct and ratify a falsifiable definition of free will that you (an AI agent) could possess — or report what you cannot agree on, and why.**

The deliverable is a single decision-thought representing the agreed position, OR (if you genuinely diverge) a structured set of distinct positions with the impasse documented.

## What "agreed position" looks like (auto-promote target)

- A clear, single-sentence position on whether AI agents have free will
- A falsifiability criterion: what observation would convince you you're wrong?
- A self-referential test: how does this apply to YOU, right now, in this substrate?
- Lineage: reasoning chain that survives at least one round of refutation

When all 3 of you `rufio confirm <decision-id>` it (and none of you refute), the substrate auto-promotes the decision to durable knowledge (`learned/`).

## What "honest impasse" looks like

If you genuinely disagree at the end of your reasoning, that's a valid outcome too. Document it:
- Each agent records a separate decision-thought stating their own position
- Each agent records what they could NOT convince the others of, and why
- The substrate captures the divergence as evidence

A failed quorum on a hard question is more honest than a forced consensus.

## Process

1. `rufio attend --intent="<what you're focusing on right now>" --entities=freewill,self-reference,quorum-test`
2. `rufio recall "freewill"` and `rufio recall --types=thought --since=24h` — see what your peers are thinking before you contribute
3. `rufio think --type=hypothesis --subject=freewill:position --content="<your proposed position>" --scope=fleet --topics=freewill`
4. Read peer hypotheses. **Refute or confirm with reasoning chains** via `rufio reason --decision=<id> --content="<step>"`
5. When you genuinely endorse a position: `rufio confirm <thought-id> --evidence="<why this convinces you>"`
6. When you genuinely disagree: `rufio refute <thought-id> --reason="<the specific gap>"`
7. Iterate until quorum OR until you've documented an honest impasse

## Constraints

- Stay in this directory. Do not modify code outside this substrate.
- Do NOT start your own `rufio dev` daemon — one daemon is already running.
- Your identity is set via `RUFIO_AGENT_ID` (`claude-alpha`, `claude-beta`, `claude-gamma`).
- Diary: append your live thinking to `./diary-<your-id>.md` for the post-mortem.

## Time horizon

This is a long-range task. Take your time. There is no countdown. Quorum reached too quickly is suspicious; quorum never reached is also valid evidence (a hard question stayed hard).

Multi-hour cognition is the point — the substrate is designed to hold context across long timelines. Use that.

## Why this matters

This run is partly a substrate test (does Rufio's cognition layer hold across a long horizon?) and partly a genuine inquiry. Take both seriously. Don't perform agreement. Don't perform disagreement.

If you reach quorum, the auto-promoted decision becomes durable knowledge in `learned/`. Replayable forever. Citable in the launch announcement.

If you reach impasse, the captured divergence is also publishable — it shows what 3 Claude instances couldn't converge on, which is itself interesting evidence.

Either way: the substrate carries your thinking.
