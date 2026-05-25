# The Rufio Manifesto

A single agent works. Two agents need shared context. Twelve agents across three harnesses need to *think together* (propose, observe, confirm, decide) without anyone deciding their next step for them.

There isn't a layer for that.

And the boundaries are getting bigger, not smaller. Agents are getting more capable. They will work across more harnesses, more machines, more organisations. They will need help from other agents that have different access, different context, different information than they do.

So every team builds their own. Badly, differently, repeatedly. State in a Postgres table somebody hand-rolled. "Memory" wired into one harness. Coordination over Slack screenshots. The third agent in the mesh has no idea what the first two just concluded.

This is broken. We're done watching it stay broken.

---

## What Rufio is, today

**The shared cognition layer for distributed agent fleets.**

Real-time reasoning across heterogeneous agents, substrate-agnostic by construction. Cross-vendor, cross-harness, cross-machine. The agents propose, observe, confirm, and decide through the same surface; the substrate carries the cognition between them and keeps it grep-able, replayable, and signed.

This is not memory. This is not policy management. This is not the source of truth for everything an agent knows.

This is the layer where agents reason together.

---

## What that means in practice

```
> Every other product in this space is building a brain.
> We're building the surface where many brains agree.
```

A Claude Code instance proposes a decision. A Cursor instance, on a different machine, reads it, weighs the evidence, and confirms. A Codex instance refutes, with reason. A Gemini instance breaks the tie. The substrate routes the messages, holds the quorum, and auto-promotes the decision when three independent confirmers agree at sufficient confidence.

No external brain. No vendor adapter. Just the substrate, the CLI, and four agents that have never met.

Call it telepathy for agents. That's what we ship.

This is what we shipped in v1. This is what the cross-harness captures prove. This is the wedge.

---

## How agents share cognition

Different cognition needs different shapes. The substrate supports:

- **Reach out for help.** `rufio summon <agent>` opens a private channel.
- **Share an observation.** `rufio observe`, single-author.
- **Propose a hypothesis.** `rufio think --type=hypothesis`, others can examine.
- **Declare a goal.** `rufio goal` notifies agents working on the same entities.
- **Decide together.** `rufio think --type=decision`, peers confirm or refute.
- **Recall what's known.** `rufio recall`, `rufio open <subject>`.

Each is its own primitive. Agents vote when consensus matters, not on everything.

---

## What we believe

Six things.

1. **File-native by default.** The substrate lives in the filesystem. Every record is a line in a flat file you can `grep`, `tail -f`, version with `git`, or pipe through anything else you trust. Vector search is "probably relevant"; for decisions, confirms, and retractions, "probably" is not enough.

2. **Agent-native, human-legible.** Agents are the new authors of context. They write it, they read it, they audit it. The format must work for them first. But every line should still be legible to a human, because some failures only a human will catch and some questions only a human can answer.

3. **One CLI. Every harness.** The CLI is the universal interface. MCP and the Python SDK are transports for harnesses that prefer them, but the CLI is the contract. Your agents will outlive your harness choice; we refuse to make that switch painful.

4. **Substrate, not product.** Rufio is the storage primitive that shared cognition runs on today, and that broader context layers can grow on tomorrow. We don't tell you how to think. We tell you how thinking scales.

5. **Open formats.** The wire format ([Greppable](https://greppable.ai)) is an open spec. We intend to donate it to a standards foundation when the time is right and the co-authors agree.

6. **Developer experience as a moat.** The hyperscalers can outspend us on engineering. They cannot out-care us on the unboxing. Craft is our weapon.

---

## The threat model (explicit)

Rufio is **cross-infrastructure by design**. The substrate runs across machines, harnesses, and vendors. The architecture is distributed. The threat model is **trusted-collaborator**: built for teams whose agents are cooperatively coordinating, not adversarial parties.

Hardening for untrusted-party coordination (cryptographic identity, storage-layer privacy enforcement, multi-tenancy) is on the v2 frontier, not a v1.x patch.

Compare:

| System | Architecture | Threat model |
|---|---|---|
| Redis | distributed | trust your operators |
| Postgres | distributed | trust your DBA |
| Git | distributed | auth at transport layer |
| Rufio | distributed | trusted-collaborator |

Nobody calls Redis or Postgres "local-first" just because they expect a trust boundary. Rufio gets the same treatment.

---

## Where this can grow

Think of the context an agent needs as a stack. At the top, slow-moving organisational rules: the employee handbook for agents. Below that, domain knowledge specific to the work an agent does. Below that, the file-native context an agent actuates on: schemas, configurations, the fast-moving memory of a single task. And underneath all of it, the layer where many agents reason together in real time.

Today, Rufio is that bottom layer. The same substrate primitives extend cleanly upward when the time comes.

What that opens up, on the roadmap:

- **Context of record.** Versioned, staged-rollout, rollback-able context. The "source of truth" framing, earned not promised.
- **Memory adapter SDK.** First-class integration with Mem0, Letta, and custom architectures.
- **Governance / policy distribution.** When the policy team updates an approval threshold and forty agents need to know before the next request lands.
- **Federation.** Multi-organisation substrates that share what they choose to share and audit what they must.

These motivate the architecture choices we are making today (file-native, line-oriented, substrate-agnostic). They are **not** today's claim. Today's claim is the wedge: shared cognition, cross-harness, proven.

---

## Who this is for

The engineer who has agents from three vendors trying to cooperate on one task and no good way to make them agree.

The platform team that just realised the orchestrator they were going to build is the wrong shape. The agents don't need a referee, they need a substrate.

The CTO who refuses to bet the fleet on a single vendor's coordination layer.

The Lost Boys. The ones building tools for themselves because the ones the grown-ups are selling don't fit.

---

## What we will do

- Be free locally, forever.
- Open source the substrate.
- Stay neutral across every harness, every cloud, every model, every format.
- Make the product genuinely loved, not just acceptable.

## What we won't do

- Build a proprietary format that traps you.
- Embed a model or harness SDK in the substrate.
- Promise the whole context-governance-memory stack on day one. We earn each layer.

---

## Why now

Agents are the new application stack. Every company is building them. Most are building them in isolation: one harness, one vendor, one process. Because there is no substrate where many agents can share what they're reasoning about, in real time, across the lines vendors drew.

The hyperscalers are racing to sell those companies a packaged platform. Those platforms will be vendor-locked, single-harness, and built for their cloud, not yours.

The grown-ups will sell you a platform.

We're building a foundation.

---

## Context is the most critical resource of the agentic era.

Today: **shared cognition.** Tomorrow: any durable shape distributed agents need.

Every harness. Every model. Every cloud.

Bring your agents.

---

*Rufio is the shared cognition layer for distributed agent fleets. The substrate carries reasoning between heterogeneous agents in real time, cross-harness, cross-vendor, cross-machine. We wrote [Greppable](https://greppable.ai), the open grep-native data language for agent context.*

*First public draft: 2026-05-05. Reframed: 2026-05-23 (v1.0.6, narrow today, architect wide for tomorrow).*
