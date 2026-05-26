# Roadmap

> What's shipped, what's next, and where each future capability lives.

The roadmap below is **functionality**. Commercial framing (pricing, business model, hosted service positioning) is tracked separately and not relevant to what the substrate does.

## Context architecture (the broader frame)

Think of the context an agent needs as a stack. At the top, slow-moving organisational rules. Below that, domain knowledge. Below that, the file-native context an agent actuates on (schemas, configurations, fast-moving memory). And underneath all of it, the layer where many agents reason together in real time.

Today, Rufio is the **bottom layer**: shared cognition. The same substrate primitives extend cleanly upward through the stack when the time comes.

## v1.0 (shipped)

The substrate. OSS, scoped to a trusted-collaborator threat model: distributed cognition across heterogeneous agents, cross-infrastructure today via shared filesystem or MCP transport.

- Daemon + filesystem layout + version engine
- Ambient cognitive primitives (`think`, `observe`, `reason`, `recall`, `attend`, `retract`, `open`)
- Verification primitives (`confirm`, `refute` with auto-promotion)
- Direct cognition / channels (`summon`, `accept`, `decline`, `say`, `leave`, `close`)
- Coordination / goals (`goal`, `goals list`, `goal complete`, `goal abandon`)
- Real-time streaming (`listen`, `stream`)
- Versioning + staged rollout (`push --stage`, `approve`, `promote`, `rollback`, `export`)
- Time-aware recall (sub-second `--as-of`)
- Demo helpers (`swarm spawn`, `lineage`, `fleet`, `attention`)
- Beautiful colour CLI banner
- **MCP adapter** (`rufio mcp`): 22-tool MCP stdio server for harnesses that don't shell well
- **Python SDK** (subprocess + HTTPS wrapper around the CLI)
- **Hosted transport** (`rufio serve` over MCP-over-HTTPS with bearer-token auth, continuous-sync mirror) - cross-machine validated 5/5 against a real cloud droplet

[Full v1 spec](./v1-spec.md)

---

## Scaling beyond v1's envelope

- v1.1+: compaction (bundle older records into segment files; preserves grep + file-native)
- v1.2+: optional indexing (sidecar; speeds queries, files stay canonical)
- v1.2+ serve mode: alternative storage backends optional (self-hosted or managed); Greppable-over-HTTPS is the contract

---

## v1.1 (next minor)

- **Behavioural Previews** - replay a context change against historical decisions; show counterfactual differences before promotion
- **Decision lineage with counterfactuals** - extend `rufio lineage` to "what would have happened under v2"
- **Discovery / matchmaking** - `rufio fleet --skill=<capability>` returns matching agents
- **Presence** - online/offline indicators per agent
- **Live broadcast questions** - `rufio ask --topic="anyone know X?"` opens a fleet-wide help channel

---

## v1.2

- **Counterfactual simulation** - fork the substrate, inject a hypothetical, watch agent behaviour
- **Following relationships** - subscribe to all emissions from a specific agent (mentor pattern)
- **Three-tier memory model adapter** - explicit working/episodic/semantic shape for memory architectures that want it
- **Substrate-driven phased rollout monitoring** - auto-escalate or auto-revert based on drift signals during canary

---

## v1.3 (sophisticated quorum)

The current quorum mechanic (3 distinct confirmers at >=0.85 confidence triggers auto-promote) is intentionally simple. It works for cooperative fleets but rewards fast-confirmers over slow deep-thinkers. v1.3 upgrades the mechanic:

- **Deliberation windows + refute-driven demotion** - auto-promote waits a configurable cooldown after threshold-met. During the window, refutes pull against confirms via `confirms - refutes >= threshold` math. If broken by refutes during the window, promotion is cancelled. Addresses the "fast confirmers beat the slow deep-thinker" failure mode.
- **Reasoning-depth weighting** - confirms backed by a `reason` chain (multi-step) carry more weight than bare confirms. Rewards agents who did the work.
- **Reconciliation across instances** - when 10 copies form similar/conflicting beliefs, merge with explicit consensus rules
- **Trust scoring / quarantine** - per-agent trust scores; misbehaving instances get isolated; immune-system pattern
- **Forgetting / decay curves** - beliefs lose confidence without reinforcement
- **Salience / passive cuing** - substrate proactively pushes relevant memories to attending agents
- **Voting / consensus protocols** - for conflicts that confirm/refute can't resolve alone

---

## v1.5 (governance + ops)

- **Authentication** - SSO, RBAC, scope-bound authority
- **Web dashboard** - version history, diff viewer, manual editing for non-technical authors
- **Identity packages** - agent identities themselves become versioned, deployable substrate objects
- **Trust networks** - explicit weighted trust between agents (extends v1.3 trust scoring)
- **Auto-attached listening** - daemon detects new agent identities and auto-tails

---

## v2 (the horizon that defines the category)

- **Substrate self-observation** - reflective layer that notices patterns across the fleet: *"these agents are stuck"*, *"this thought has 5 confirmations, auto-promoting"*, *"this belief is decaying"*. The substrate stops being passive plumbing and becomes an active participant. **This is potentially the second category-defining feature after telepathy itself.**
- **Federation across orgs** - multiple Rufio servers with consent-based topic sharing between them
- **Adapter SDK** - Mem0, Letta, MemPalace, Zep, custom architectures plug into Rufio as first-class memory backends
- **Forking / merging cognition** - spawn parallel exploration copies, merge findings
- **Substrate-brokered orchestration** - daemon does capability matching automatically

---

## TUI console + agnostic operator agent (strategic, post-v1)

**Architectural principle:** Rufio is the substrate, never an agent harness. The "operator agent" is *any* harness driving the `rufio` CLI per the coordination protocol — no model or agent SDK ever links into Rufio core or the TUI binary; whichever harness drives the substrate is a swappable deployment choice. Substrate invariants are enforced CLI-side (a misbehaving harness must not poison the commons); the wire format + CLI contract is a versioned *public protocol*.

- **Full CLI-parity console** - anything doable from the `rufio` CLI doable
  from the TUI: slash commands, configuration, substrate ops. The TUI
  becomes a first-class console, not just a viewer.
- **Agent-group / team management** - create, configure, and manage
  groups/streams of agents from the console.
- **Substrate-resident operator assistant** - a "Rufio operator" agent (a
  documented, swappable reference recipe; Claude Code initially, any
  harness substitutable) that helps set up agent teams and collects /
  surfaces reporting. Realised by the TUI being its conversational
  *client* - the assistant lives on the substrate, not in the TUI binary.
  Demonstrate ≥2 distinct harnesses coordinating to prove neutrality and
  guard against gravitational lock-in.
- **Privileged / trusted operator harness + governance layer** — a trusted
  operator that uses the CLI in an *administrative* way (holds keys the
  substrate can *verify* to administer its core rules), feeding a future
  context + governance layer for managing agents across harnesses and
  infrastructure. Real substrate authZ (identity/keys). Extends v1.5
  *Authentication* and *Trust networks*.
- **Quorum / promotion as a visible console event** - surface
  confirm → auto-promote → `learned/` as a live, legible moment
  (confidence, refutes pulling against it), not a static `X/Y` fraction.
  This is the TUI face of the v2 *Substrate self-observation* item.

### Interaction tiers — how the operator works *through* the TUI

Four distinct surfaces. **All are thin clients over the `rufio` CLI; none embed a model or harness** (the architectural principle holds at every tier):

1. **Within-a-substrate cognition** — the operator emits substrate records
   (`think`/`confirm`/`say`/…) from the console. This is the chat view of
   the TUI.
2. **Conversational admin — same chat view.** "Set up a team / configure
   this project" by *directed-messaging an operator/admin agent* on the
   substrate (`@operator …`). The operator-assistant is a
   substrate-resident agent (a separate harness via the CLI), not embedded;
   its transport is the same directed-message channel used for any other
   1:1 cognition, so no new surface is needed.
3. **Structured per-substrate admin — a future SEPARATE settings surface.**
   Identity, project setup, agent-scaffold spawning, extension/plugin
   management: stateful/form-like, awkward in a chat (cf. tools that keep
   chat *and* a settings/plugins surface). A distinct admin mode in the
   TUI, still a thin CLI client.
4. **Multi-substrate / system control-plane — a SEPARATE view, one tier UP.**
   Talking to a higher-order operator agent that provisions, manages, and
   orchestrates *many substrates and the whole system construct* (not
   records within one project). This is the **control plane** over the
   per-substrate **data plane**; implies substrate registry / identity /
   multi-tenancy that v1 (single local project) does not have. Still
   substrate-agnostic — that higher operator is also just an agent on the
   CLI, never embedded. Aligns with v1.5 *Authentication* + *Trust
   networks*, v2 *Federation across orgs*, and the *Privileged operator +
   governance* item above.

---

## Considered and explicitly deferred

Primitives we considered and decided against for v1, with reasoning:

- **Curiosity / `rufio seek`** - pull-based recall covers it for v1
- **Interrupts / `rufio interrupt <agent>`** - semantics messy with LLMs; safer as v2
- **Mute / `rufio mute <topic|agent>`** - defer to v1.3 with trust/quarantine
- **State / mood** - useful but not essential; v1.5 with presence
- **Three-tier memory model in core** - this is *architecture* not *substrate*. Memory architectures live on top of Rufio, not inside it
- **Negation as first-class** - useful but requires more thought; v2
- **Multi-hop provenance chains** - wait until trust networks land in v1.5
- **Conditional knowledge** (`if Y then X`) - belongs in a logic layer above the substrate
- **Cost / budget awareness** - orthogonal to the substrate; lives in the operator layer above

---

## Open questions for the community

- **First framework integration** beyond MCP - LangChain, LlamaIndex, Vercel AI SDK?
- **Adapter SDK shape** - what does a clean integration with Mem0 / Letta look like?
- **Quorum tuning defaults** - what `auto-promote-settle` window suits most teams? (see v1.3 deliberation windows)

If you have opinions on any of these, [open a discussion](https://github.com/d-mcmillan/rufio/discussions).
