# Architecture

> The substrate is a daemon, a filesystem, and a CLI. That's the whole product.

## Three interfaces, one substrate

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
                  │  └── .rufio/                │
                  └─────────────────────────────┘
                          ▲                 ▲
                          │ shells out      │ shells out
                  ┌───────┴────────┐  ┌─────┴──────┐
                  │  agent A       │  │  agent B   │
                  │  (Claude Code) │  │  (Cursor)  │
                  └────────────────┘  └────────────┘
```

**The key principle:** files are the data, files are the protocol, files are the network.

The daemon is small (one process watching directories, applying routing rules, writing files). The CLI is how anything — human or agent — interacts with the substrate. There is no proprietary protocol to learn. Everything in Rufio is `cat`-able, `grep`-able, `ls`-able from the shell.

## Substrate vs. architecture vs. application

| Layer | What it is | Examples |
|-------|-----------|----------|
| **Substrate** | Infrastructure for distributed context at fleet scale: identity, versioning, distribution, nervous system, coordination, lineage, governance | **Rufio** |
| **Architecture** | Opinions about how context should be structured and behave: memory algorithms, consolidation models, retrieval strategies | Greppable, Mem0, Letta, MemPalace, custom in-house systems |
| **Application** | The agents themselves consuming context | Customer support bot, sales copilot, internal ops agent |

Rufio doesn't compete with Mem0, Letta, or Greppable on memory algorithms. **Rufio is what any of them want to live on top of when their customers need fleet-scale infrastructure.**

## Three kinds of context, one substrate

| Type | Examples | Origin | Update cadence |
|------|----------|--------|---------------|
| **Given** | Identity, values, skills, schemas, policies, brand voice, code maps, API contracts | Configured / deployed by humans | Quarterly to yearly |
| **Learned** | Episodic memories, semantic beliefs, customer facts, inter-agent observations | Accumulated through agent experience | Continuous |
| **Live** | Session state, handoffs, real-time API data, inter-agent messages | Generated in the moment | Sub-second to minutes |

All three live in the same project root, with the same identity, RBAC, audit, and observability primitives. Splitting them across two or three products is what every competitor will do; unifying them is the wedge.

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
│   ├── attention/                   # who's listening to what
│   ├── reasoning/                   # reasoning traces
│   ├── summons/                     # pending/accepted/declined/expired
│   ├── channels/                    # active multi-agent conversations
│   └── goals/                       # active intent across the fleet
└── .rufio/                          # version history, snapshots, locks
    ├── history/                     # content-addressed blob store
    ├── refs/                        # current version pointers
    ├── snapshots/                   # for --as-of time-aware recall
    └── locks/                       # write coordination
```

## Why CLI-first, not MCP-first

File-native agents (Claude Code, Cursor, Cline, Codex, Aider) prefer the CLI because:

- They've been trained on the entire history of Unix; they've seen approximately zero MCP examples
- CLI output is deterministic, parseable text
- CLI is composable (`rufio recall ... | grep ... | head -1`)
- CLI is streamable (`rufio listen | while read line; ...`)
- Errors are native (exit codes + stderr)
- No protocol setup, no SDK install — just `npm install -g rufio`

MCP is a second-class adapter for harnesses that don't shell well (older chatbots, web-only agents). The MCP surface exposes a curated **agent-participation subset** of the CLI (cognition, verification, channels, coordination, plus `listen`), deliberately excluding operator/governance/identity verbs — see [`docs/mcp.md`](./mcp.md) for the full contract.

## Telepathy in one paragraph

When Agent A runs `rufio observe --subject=customer:5821 --predicate=prefers --object=email --scope=fleet`, the daemon writes a file to `learned/customer/5821.gdlm` and notifies any subscribers. When Agent B (which has previously declared `rufio attend --entities=customer:5821`) runs `rufio recall "customer 5821 preferences"`, it gets that observation back as text, with provenance. Two completely independent agents — different harnesses, different processes, different machines — just shared a thought through a shared file routed by a small daemon. **That's the substrate.**

## Pull vs. push

Two patterns ship in v1:

- **Pull (default):** agent calls `rufio recall --types=thought --since=<last-cycle>` periodically. Telepathy on demand. Latency: agent's reasoning cycle (5-30s).
- **Push (`rufio listen`):** long-running command tails the agent's inbox; events stream to stdout. Latency: 50-500ms (filesystem-watcher latency).

For most use cases, pull is enough. Push is for true real-time multi-agent coordination.

## Local now, shared later

v1 is local-only — agents on the same machine share through the local filesystem. The CLI is the abstraction. v1.5 introduces remote daemons:

```bash
$ rufio remote add prod https://rufio.acme.com
$ export RUFIO_REMOTE=prod
# All commands now hit the remote daemon over HTTP/gRPC.
```

The agent's mental model never changes. Same primitives, same exit codes, same output format. Just a different transport behind the CLI.

## Why files (and not a database)

- **Files are universal.** Every language, every harness, every shell can read them. No driver, no SDK, no protocol.
- **Files are inspectable.** When something goes wrong, you `cat` the file and read it. Try doing that with a vector DB.
- **Files are versionable.** Append-only, content-addressed, git-blame-able.
- **Files are auditable.** Every fetch leaves a record. Compliance officers can read the actual context.
- **Files are recoverable.** No corrupted database state. Worst case, you `rm -rf .rufio/` and re-init.

The trade-off: files are slower than in-memory caches at extreme write rates. v2 introduces optional storage backends (RocksDB, FoundationDB) for write-heavy workloads. The CLI surface stays identical.

## Wire format: Greppable

Every record in Rufio is a Greppable line — `@type|key:value|key:value`. Examples in [`docs/v1-spec.md`](./v1-spec.md#wire-formats-greppable-everywhere).

Greppable will be donated to an open standards foundation. Other formats (JSON, YAML, Markdown, custom schemas) are first-class on the substrate; Greppable is the format Rufio uses for its own internal records (config, attention, summons, channel meta).

---

For full details, see [`v1-spec.md`](./v1-spec.md).
