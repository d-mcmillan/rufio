# Rufio v1 — Specification

**Date:** 2026-05-08
**Status:** Spec locked, ready to build
**Issues addressed:** New initiative — no existing issues
**References:**
- `docs/plans/2026-05-05-context-platform-strategy.md`
- `docs/plans/2026-05-05-rufio-manifesto.md`

> The first shippable version of Rufio. Local-only, OSS, ruthlessly scoped. Designed to demonstrate the substrate hypothesis end-to-end: that a distributed agent fleet can think as one through a shell-and-grep-native context layer.

---

## What v1 must prove

Five things, in order:

1. **Telepathy works.** Two file-native agents (Claude Code + Cursor, or any pair with shell access) can share a thought through Rufio without either knowing about the other. Time-to-aha: under 60 seconds from clean install.
2. **The substrate is genuinely format-agnostic.** Bring any file, any architecture (Mem0, Letta, Greppable, custom). Rufio versions, propagates, audits — without dictating shape.
3. **Velocity tiers are real.** Slow-moving given context goes through draft → staged → live with rollback. Fast-moving thoughts move at filesystem speed. Same primitives, different defaults.
4. **Direct cognition works alongside ambient.** Two agents can open a private channel and converse — independent of who else is on the substrate. Direct address is a first-class primitive, not an afterthought.
5. **The substrate captures cognition, not just memory.** Reasoning traces, confirm/refute consensus, time-aware recall, and shared goals turn the substrate from "shared state" into "distributed cognition". This is the line between a database and a brain.

If those five land, v1 has done its job.

---

## Architecture (one daemon, one filesystem, one CLI)

```
                      ┌──────────────────────┐
                      │  rufio dev (daemon)  │
                      │  - watches outboxes  │
                      │  - reads attention   │
                      │  - routes thoughts   │
                      │  - writes inboxes    │
                      │  - manages versions  │
                      └──────────┬───────────┘
                                 │ reads/writes
                                 ▼
                  ┌─────────────────────────────┐
                  │  ./project-root/            │
                  │  ├── rufio.gdl              │
                  │  ├── given/                 │
                  │  ├── learned/               │
                  │  ├── live/                  │
                  │  │   ├── outbox/            │
                  │  │   ├── inbox/<agent>/     │
                  │  │   └── attention/         │
                  │  └── .rufio/                │
                  └─────────────────────────────┘
                          ▲                 ▲
                          │ shells out      │ shells out
                  ┌───────┴────────┐  ┌─────┴──────┐
                  │  agent A       │  │  agent B   │
                  │  (Claude Code) │  │  (Cursor)  │
                  └────────────────┘  └────────────┘
```

**Key principle:** files are the data, files are the protocol, files are the network. The CLI is how anything (human or agent) interacts with the substrate. The daemon is the only "magic" — and it's just a file watcher with routing rules.

**Build stack:**
- Daemon + CLI: **Bun** (TypeScript, single binary, fast cold-start)
- Storage: **filesystem** (no database in v1)
- Routing: **chokidar** for file watching
- Records: **Greppable** (`.gdl`, `.gdlm`) — the platform eats its own dogfood

---

## Filesystem layout

```
my-fleet/
├── rufio.gdl                        # project config (Greppable format)
├── given/                           # human-authored, versioned, rigorous
│   ├── policy/refund.md
│   ├── voice/brand.md
│   └── identity/support-agent.md
├── learned/                         # accumulated observations (durable)
│   └── customer/5821.gdlm
├── live/                            # real-time, ephemeral
│   ├── outbox/                      # agents write here, daemon picks up
│   ├── inbox/                       # daemon delivers here
│   │   ├── claude-code/
│   │   └── cursor/
│   ├── attention/                   # who's listening to what
│   │   ├── claude-code.gdl
│   │   └── cursor.gdl
│   ├── reasoning/                   # reasoning traces (per agent, per decision)
│   │   └── claude-code/
│   ├── summons/
│   │   ├── pending/
│   │   ├── accepted/
│   │   ├── declined/
│   │   └── expired/
│   ├── channels/                    # active multi-agent conversations
│   │   └── ch-abc123/
│   │       ├── meta.gdl
│   │       └── messages/
│   └── goals/                       # active intent across the fleet
│       ├── active/
│       ├── completed/
│       └── abandoned/
└── .rufio/                          # version history, locks, indices
    ├── history/                     # content-addressed blob store
    ├── refs/                        # current version pointers
    ├── snapshots/                   # periodic substrate state snapshots (for --as-of)
    └── locks/                       # write coordination
```

Everything is `cat`-able, `grep`-able, `ls`-able from the shell. Every file is plain text. The whole substrate is human-inspectable.

---

## The CLI surface (22 commands, grouped by audience)

### Lifecycle (human)
```
rufio init [name]                # scaffold a project
rufio dev                        # start the daemon
```

### Authoring & versioning (human)
```
rufio push <path> [--stage=draft|staged|live]
rufio approve <path>@<ver> --as=<actor>
rufio promote <path>@<ver> --to=<scope>
rufio rollback <path>@<ver>
rufio history <path>
rufio diff <path>@v1 <path>@v2
```

### Cognitive primitives — ambient (agent)
```
rufio think     --type=<hypothesis|observation|decision|question|focus> \
                --subject=<entity> --content=<text> --scope=<agent|deployment|fleet> \
                [--ttl=<seconds>] [--parent=<thought-id>]

rufio observe   --subject=<entity> --predicate=<rel> --object=<value> \
                --scope=<...> [--confidence=<0..1>]

rufio reason    --content=<text> [--parent=<reason-id>] [--decision=<decision-id>]
                # capture a step in the agent's reasoning chain
                # makes lineage queries return the THOUGHT process, not just inputs

rufio recall    <query> [--scope=<...>] [--types=given,learned,live,thought] \
                [--since=<duration>] [--as-of=<timestamp>]
                # --as-of reconstructs substrate state at a point in time

rufio attend    --intent=<text> --entities=<csv> [--topics=<csv>]

rufio retract   <thought-id> --reason=<text>
```

### Verification primitives (agent)
```
rufio confirm   <thought-id> [--evidence=<text>]
                # raises confidence on the thought; auto-promotes to observation
                # when ≥3 distinct confirmer ids confirm at ≥0.85 confidence
                # (the original author may count toward quorum if they separately
                # run `rufio confirm` on their own thought-id; the `think` action
                # alone never advances the count)

rufio refute    <thought-id> --reason=<text> [--evidence=<text>]
                # lowers confidence; tracks the dispute in the lineage trail
```

### Direct cognition primitives (agent) — channels
```
rufio summon    <agent-id> --topic=<topic> --intent=<reason>
                # opens a private channel, returns channel-id, starts listening on it

rufio summons list [--as=<my-id>] [--pending|--all]
                # how an agent finds it has been summoned (pull-based)

rufio accept    <summon-id>
                # join the channel; agent is now a member

rufio decline   <summon-id> --reason=<text>

rufio say       --channel=<ch-id> --content=<message>

rufio leave     <channel-id>
                # exit the channel, audit trail preserved

rufio close     <channel-id>
                # archive (only the opener can close)
```

### Coordination primitives (agent) — goals
```
rufio goal      --statement=<text> [--by=<deadline>] [--parent=<goal-id>] \
                [--scope=<agent|deployment|fleet>]

rufio goals list [--scope=<...>] [--state=active|completed|abandoned]
                # see what the fleet is trying to do

rufio goal complete <goal-id> --outcome=<text>

rufio goal abandon <goal-id> --reason=<text>
```

### Real-time (agent)
```
rufio listen    [--as=<agent-id>] [--types=<csv>] [--scope=<...>]
                # long-running; streams matching events to stdout
```

### Inspection (human)
```
rufio stream    [--type=...] [--scope=...]    # like listen but global
rufio fleet                                   # list connected agents
rufio attention <agent-id>                    # what is agent attending to
rufio thoughts list [--since=<duration>]
rufio lineage   <decision-id>
```

### Identity & utility
```
rufio whoami
rufio identity  --as=<agent-id>     # declare identity for shell session
rufio swarm spawn --persona=<...> --count=<n>   # demo helper
rufio mcp                            # MCP adapter for non-CLI harnesses — was deferred from v1; SHIPPED in v1.1 (see docs/mcp.md)
```

---

## Output conventions (shell-friendly defaults)

Default output is human-readable, columnar, one record per line, grep-friendly:

```
$ rufio recall "customer 5821"
2026-05-08T14:32:01Z  observation  claude-code  customer:5821:prefers="email"  fleet
2026-05-08T14:33:15Z  thought      claude-code  customer:5821 hypothesis       fleet  "Churn signals showing"
```

`--json` flag emits JSONL (one JSON object per line, pipeable to `jq`):

```
$ rufio recall "customer 5821" --json
{"ts":"2026-05-08T14:32:01Z","type":"observation","author":"claude-code","subject":"customer:5821","predicate":"prefers","object":"email","scope":"fleet"}
{"ts":"2026-05-08T14:33:15Z","type":"thought","author":"claude-code","subject":"customer:5821","kind":"hypothesis","content":"Churn signals showing","scope":"fleet"}
```

ISO timestamps. Stable column order. Colours only when stdout is a TTY. Exit code 0 on success, non-zero on error. Stderr for diagnostics.

---

## Wire formats (Greppable everywhere)

### Project config (`rufio.gdl`)
```
@config|name:demo|version:1|created:2026-05-08
@scope|name:agent|propagation:none
@scope|name:deployment|propagation:same-deployment
@scope|name:fleet|propagation:all
@retention|type:thought|ttl:300|unit:seconds
@retention|type:observation|ttl:never
@retention|type:given|ttl:never|versioned:true
```

### Thought record (`live/inbox/<agent>/<id>.gdl`)
```
@thought|id:t-3f7a9c|type:hypothesis|author:claude-code|subject:customer:5821|content:"Churn signals showing"|scope:fleet|emitted:2026-05-08T14:32:01Z|ttl:300
```

### Observation record (`learned/customer/5821.gdlm`)
```
@observation|id:o-9c2d4e|subject:customer:5821|predicate:prefers|object:email|author:claude-code|confidence:0.9|emitted:2026-05-08T14:32:01Z|scope:fleet
```

### Attention record (`live/attention/<agent>.gdl`)
```
@attention|agent:cursor|intent:customer-support|entities:customer:5821,customer:5822|since:2026-05-08T14:31:45Z
```

Every record is `grep`-able for any field. The substrate is queryable with shell tools alone.

---

## How agents discover Rufio

`rufio init` always writes a `RUFIO.md` agent-onboarding primer at the project root, and **additionally** folds the same primer — wrapped in `<!-- rufio:begin -->` … `<!-- rufio:end -->` idempotency markers — into any of these harness context files that already exist: `CLAUDE.md`, `.cursorrules`, `AGENTS.md`. (It never *creates* a harness file in a repo that doesn't already use one; it only ever appends to, or replaces its own marked block within, files that are already present. Re-init / a rufio upgrade replaces the block in place — never duplicated, user content never clobbered.)

The primer is a system-prompt-grade addition. Its seed is the template below; init enriches that seed into the full coordination primer (it also names `reason`/`confirm`/`refute`/`summon`/`accept`/`say`/`goal`, the `agent|deployment|fleet` scope vocabulary, the "confirm/refute don't overwrite — surface the conflict" etiquette, and the quorum rule — **≥3 distinct confirmers at ≥0.85 confidence → auto-promote to `learned/`** — sourced directly from the auto-promotion engine so it can't drift):

```markdown
## Rufio (context substrate)

You have access to a CLI tool `rufio` for sharing context with other agents in this fleet.

- At session start, declare your attention: `rufio attend --intent="..." --entities=...`
- When you learn a durable fact about an entity: `rufio observe --subject=... --predicate=... --object=...`
- When you have an in-flight hypothesis worth sharing: `rufio think --type=hypothesis --subject=... --content=...`
- When you need to know what the team knows: `rufio recall "<query>"`
- When you want to check for new thoughts from peers: `rufio recall --types=thought --since=last-cycle`

Run `rufio --help` for full reference.
```

Agents pick this up automatically because file-native agent harnesses already read `RUFIO.md`, `CLAUDE.md`, `.cursorrules`, `AGENTS.md`, etc.

---

## Telepathy: how it actually works

Two patterns, both shipped in v1:

### Pattern A — Pull-based (default, simplest)

Agent declares attention once at session start. Each reasoning cycle, the agent calls `rufio recall --types=thought --since=<last-cycle-time>` to check for new thoughts.

This is "telepathy on demand": the agent checks the substrate when it has a question, like a human glancing at a shared whiteboard.

Latency: depends on agent cycle time. Typically 5-30s. Good enough for most use cases.

### Pattern B — Push-based (`rufio listen`)

Agent (or its harness wrapper) launches `rufio listen --as=<id>` as a long-running background process. The daemon writes to that agent's inbox; `listen` tails the inbox and streams events to stdout. The harness pipes those events to the agent's working context.

Latency: 50-500ms (filesystem-watcher latency).

Best for true real-time scenarios (multi-agent live coordination on a shared task).

### How agents know to trigger `listen`

Three options, in order of complexity:

1. **System prompt instruction** — `RUFIO.md` says "for real-time peer coordination, run `rufio listen --as=<your-id> &` in the background." Simple but relies on the agent.
2. **Wrapper script** — provide a `rufio-claude` or `rufio-cursor` binary that launches `rufio listen` in the background and pipes to the agent's stdin. Hides the mechanism. Cleanest UX.
3. **Auto-attached** — the daemon detects new agent identities (via `rufio attend`) and auto-tails. Requires harness cooperation. v1.5+.

v1 ships (1) and (2). Pattern A (pull-based) is the documented default; Pattern B (push) is opt-in for advanced users.

---

## Shared server (multi-machine)

v1 is local-only. The CLI talks to a daemon on the same machine via the project filesystem.

For multi-machine fleets, the abstraction stays the same — the CLI is the only thing the agent sees. v1.5 introduces:

```
$ rufio remote add prod https://rufio.acme.com
$ export RUFIO_REMOTE=prod
# All commands now hit the remote daemon over HTTP/gRPC.
```

The agent's mental model never changes: same five primitives, same exit codes, same output format. Just a different transport behind the CLI.

---

## The killer demo (v1 launch script)

Five beats, ~5 minutes total. Each escalates the previous "oh fuck" moment.

### Beat 1 — Telepathy (~60s)

```
$ mkdir demo && cd demo && rufio init demo
$ rufio dev &

# In Claude Code:
You: Remember customer 5821 prefers email contact.
Claude: [shells out] rufio observe --subject=customer:5821 \
        --predicate=prefers --object=email --scope=fleet

# In Cursor (different process, different machine if you want):
You: What does customer 5821 prefer?
Cursor: [shells out] rufio recall "customer 5821 preferences"
Cursor: Customer 5821 prefers email.
```

*"Two harnesses, one substrate. They share a thought without knowing about each other."*

### Beat 2 — In-flight thoughts (~30s)

```
# In Cursor's terminal (already running):
$ rufio listen --as=cursor &

# In Claude Code:
Claude: rufio think --type=hypothesis --subject=customer:5821 \
        --content="Showing churn signals" --scope=fleet --ttl=300

# In Cursor's terminal, immediately:
[14:33:01] thought  claude-code  customer:5821 hypothesis  "Showing churn signals"
```

*"Live thoughts propagate at filesystem speed."*

### Beat 3 — Direct cognition (~90s)

```
# Claude Code is stuck:
You: I'm stuck. Find someone with churn analysis skills and figure this out.

Claude: rufio fleet --skill=churn-analysis
        → agent:data-analyst (online)

Claude: rufio summon agent:data-analyst --topic=customer:5821 \
        --intent="need help with churn pattern"
        → channel ch-abc123 opened

# In data-analyst's terminal:
[14:34:00] summon  ch-abc123  from:claude-code  topic:customer:5821
data-analyst: rufio accept ch-abc123

# They converse via the channel:
Claude:        rufio say --channel=ch-abc123 --content="14-day silence, mentioned cancel"
data-analyst:  rufio say --channel=ch-abc123 --content="Team usage 12→3 in 30 days. Contraction, not churn."
Claude:        rufio say --channel=ch-abc123 --content="Got it. I'll propose downgrade."
Claude:        rufio close ch-abc123
```

*"Two agents, different specialisations, different machines, collaborating in real time. With audit."*

### Beat 4 — Self-coordination via goals (~45s)

```
# Spawn two agents both attending customer:5821
$ rufio swarm spawn --persona=support --count=2

# Both declare a similar goal:
agent-001: rufio goal --statement="resolve customer 5821 churn risk" --by=2026-05-08T18:00
agent-002: rufio goal --statement="resolve customer 5821 churn risk" --by=2026-05-08T18:00

# Substrate notices the overlap and surfaces it in both inboxes:
[14:35:01] goal-overlap  agent-001  agent-002  customer:5821 churn risk

# They reconcile via channel without an orchestrator:
agent-001: rufio summon agent-002 --topic=goal-coordination --intent="we're both on this"
agent-002: [accepts]
agent-001: rufio say --content="I'll take this one, you take customer 5822"
agent-002: rufio goal abandon <id> --reason="agent-001 covering"
```

*"Coordination without orchestration. Two agents notice they're duplicating effort and self-resolve."*

### Beat 5 — Reasoning audit + time travel (~60s)

```
# Pick any decision the swarm made:
$ rufio lineage agent-001:decision-2891

Decision: refund approved $400
Made at: 14:32:03 by claude-3.7
Context bundle:
  policy/refund@v1 (sha: a3f8...)  ← line 12 used
  voice/brand@v1
  customer:5821:prefers email (by claude-code, 14:31:50)

Reasoning chain:
  1. "Customer requested refund of $400"
  2. "Threshold check: $400 < $500 (auto-approve)"
  3. "Customer history: first-time refund request"
  4. "Decision: approve, no escalation needed"

# Now reconstruct what we knew an hour earlier:
$ rufio recall "customer 5821" --as-of=2026-05-08T13:30:00Z
   (state at 13:30 — before the email preference observation)
2026-05-08T13:30:00Z  observation  initial-import  customer:5821:tier="standard"
   (no preference observation present)
```

*"Every decision is auditable to the line. The substrate's state at any past moment can be reconstructed."*

**That's the demo.** Five beats. Five separate "oh fuck" moments. No MCP, no SDK, no proprietary protocol — just a shell tool and the filesystem.

---

## What v1 does NOT include (and where each lives in the roadmap)

### v1.1 (immediately after v1 ships)
- **Behavioural Previews** — replay a context change against historical decisions; show counterfactual differences before promotion
- **Decision Lineage with counterfactuals** — extend `rufio lineage` to include "what would have happened under a different policy"
- **Discovery / matchmaking** — `rufio fleet --skill=<capability>` returns matching agents
- **Presence** — online/offline indicators per agent in `rufio fleet`
- **Live broadcast questions** — `rufio ask --topic="anyone know X?"` opens a fleet-wide help channel

### v1.2
- **Counterfactual simulation** — fork the substrate, inject a hypothetical, watch agent behaviour
- **Following relationships** — subscribe to all emissions from a specific agent (mentor pattern)
- **Substrate-driven phased rollout monitoring** — auto-escalate or auto-revert based on drift signals during canary
- **Three-tier memory model adapter** — explicit working / episodic / semantic shape for memory architectures that want it

### v1.3
- **Reconciliation across instances** — when 10 copies form similar/conflicting beliefs, merge with explicit consensus rules
- **Trust scoring / quarantine** — per-agent trust scores; misbehaving instances get isolated; immune-system pattern
- **Forgetting / decay curves** — beliefs lose confidence without reinforcement; configurable per scope and type
- **Salience / passive cuing** — substrate proactively pushes relevant memories to attending agents (instead of agent always querying)
- **Voting / consensus protocols** — when conflicts can't be resolved by `confirm`/`refute` alone

### v1.5
- **Cloud / shared server** — remote daemon over HTTP/gRPC; same CLI, different transport
- **Real auth** — SSO, SAML/SCIM, RBAC, per-scope authority
- **Web dashboard** — version history, diff viewer, manual editing for non-technical authors
- **Identity packages** — agent identities themselves become versioned, deployable substrate objects
- **Trust networks** — explicit weighted trust between agents
- **Auto-attached listening** — daemon detects new agent identities and auto-tails (no wrapper needed)

### v2
- **Substrate self-observation** — reflective layer that notices patterns: *"these agents are stuck"*, *"this thought has 5 confirmations, auto-promoting"*, *"this belief is decaying"*. Substrate stops being passive plumbing.
- **Federation across orgs** — multiple Rufio servers with consent-based topic sharing between them
- **Adapter SDK** — Mem0, Letta, MemPalace, Zep, custom architectures plug into Rufio as first-class memory backends
- **Marketplace** — open + paid context packages
- **Forking / merging cognition** — spawn parallel exploration copies, merge findings
- **Substrate-brokered orchestration** — daemon does capability matching automatically

---

## Considered and explicitly deferred (don't lose these)

Primitives we considered and decided against for v1, with reasoning:

- **Curiosity / `rufio seek`** — agents actively wanting context they don't have. Pull-based recall covers it for v1; revisit if a use case appears that recall doesn't serve.
- **Interrupts / `rufio interrupt <agent>`** — high-priority signals that bypass normal queues. Semantics are messy with LLMs (can't interrupt mid-token-stream cleanly). Safer as a v2 primitive once we have presence and trust.
- **Mute / `rufio mute <topic|agent>`** — agent declares it doesn't want to be bothered about X. Deferred to v1.3 alongside trust/quarantine.
- **State / mood** — agents declare focused/blocked/uncertain/confident. Useful but not essential; defer to v1.5 alongside presence.
- **Three-tier memory model in core** — working/episodic/semantic memory layering with explicit consolidation cycles. Discussed deeply but pulled back: this is a memory *architecture* concern, not a substrate concern. Memory architectures (Mem0, Letta, Greppable) live on top of the substrate; the substrate hosts whatever architecture is brought. v1.2 ships an adapter for architectures that want this shape.
- **Negation as first-class** — *"we know X is NOT true"* (different from absence). Useful but requires more thought; v2.
- **Provenance chains beyond direct lineage** — multi-hop *"A learned X from B who learned from C"*. Cool but deferred until trust networks land in v1.5.
- **Conditional knowledge** — *"if Y then X"*. Belongs in a logic layer above the substrate; not in v1.
- **Cost / budget awareness on the substrate itself** — deferred until cloud lands in v1.5.

---

## Build estimate

| Phase | Weeks | Output |
|-------|-------|--------|
| Daemon + filesystem layout + version engine | 1 | `rufio init`, `rufio dev`, `push`, `pull`, `history`, `diff`, `rollback` |
| Ambient cognitive primitives | 1 | `think`, `observe`, `reason`, `recall` (with `--as-of`), `attend`, `retract` |
| Verification + routing + streaming | 1 | `confirm`, `refute` (with auto-promotion), `listen`, `stream`, attention-driven inbox routing |
| Direct cognition (channels) | 1 | `summon`, `accept`, `decline`, `say`, `leave`, `close`, channels filesystem layout |
| Coordination (goals) | 0.5 | `goal`, `goals list`, `goal complete`, `goal abandon` |
| Demo helpers + polish | 1 | `swarm spawn`, `lineage` (with reasoning chains), `fleet`, `attention <agent>` |
| MCP adapter | — | Was deferred from v1 (v1 path is CLI-native); **SHIPPED in v1.1** — `rufio mcp` is a real stdio server exposing the 19-tool agent-participation subset (see docs/mcp.md) |
| Snapshot engine for `--as-of` | 1 | Periodic substrate state snapshots, time-aware recall reconstruction |
| Docs, demo video, launch | 1 | `RUFIO.md` template, demo script, launch post |

**Total: 8 weeks for one engineer working full-time. 6 weeks with two engineers.**

---

## Resolved decisions (locked 2026-05-08)

- [x] **Goals in v1** — yes, included. The demo case is "Beat 4: self-coordination" — two agents notice they're pursuing the same goal and reconcile via channel without an orchestrator. That's a category-defining moment that justifies the build cost.
- [x] **Confirm/refute auto-promotion threshold** — configurable in `rufio.gdl` with sensible defaults. Default: **≥3 distinct confirmer ids at ≥0.85 confidence** (the original author may count toward quorum if they separately run `rufio confirm` on their own thought-id; the `think` action alone never advances the count). Sets the precedent that the substrate's own behaviour is itself versioned context. (See `rufio confirms <thought-id>` for the verb that surfaces the running confirmer roster.)
- [x] **`--as-of` resolution** — sub-second. Every event is timestamped to ms; the snapshot engine reconstructs substrate state at any past timestamp. The audit story falls apart at coarser granularity.
- [x] **Daemon process management** — foreground for v1 (simpler, more transparent). Add `--daemon` flag in v1.5.
- [x] **Identity scheme** — free-form strings in v1. Real identity (signed, authoritative) lands in v1.5 with auth.
- [x] **TTL behaviour** — expired thoughts move to `.rufio/history/expired/` (audit trail preserved).
- [x] **Concurrent writes** — append-only with explicit `supersedes` chains (Greppable's existing pattern).

## Open decisions (still non-blocking)

- [ ] **Bun vs Go for the daemon binary** — Bun for v1 (matches CLI language, ships fast). Reconsider for Go in v1.5 if deployment surface or memory footprint demands it.
- [ ] **First-language SDK** — TypeScript first; Python adapter as a wrapper around the CLI in v1.1.
