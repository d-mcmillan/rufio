<div align="center">

```
██████╗ ██╗   ██╗███████╗██╗ ██████╗
██╔══██╗██║   ██║██╔════╝██║██╔═══██╗
██████╔╝██║   ██║█████╗  ██║██║   ██║
██╔══██╗██║   ██║██╔══╝  ██║██║   ██║
██║  ██║╚██████╔╝██║     ██║╚██████╔╝
╚═╝  ╚═╝ ╚═════╝ ╚═╝     ╚═╝ ╚═════╝
```

**The shared cognition layer for distributed agent fleets.**

[Manifesto](./MANIFESTO.md) · [Architecture](./docs/architecture.md) · [CLI Reference](./docs/cli-reference.md) · [v1 Spec](./docs/v1-spec.md) · [Demo](./docs/demo.md) · [Roadmap](./docs/roadmap.md)

[![Website](https://img.shields.io/badge/rufio.ai-FF8C46?logo=safari&logoColor=white&style=flat)](https://rufio.ai)
![Status](https://img.shields.io/badge/status-v1.0.6.2-success)
![Made for agents](https://img.shields.io/badge/made%20for-agents-ff5f82)
![License](https://img.shields.io/badge/license-Apache%202.0-50C8E6)

**Bring your agents.**

</div>

> **Status:** v1.0 research preview — actively developed. Expect rough edges; feedback welcome.

---

## What is Rufio?

Rufio is the **shared cognition layer for distributed agent fleets**. Real-time reasoning across heterogeneous agents, substrate-agnostic by construction.

A single agent works. Two agents need shared context. Twelve agents across three harnesses need to *think together* (propose, observe, confirm, decide) without anyone deciding their next step for them. There isn't a layer for that. So every team builds their own, badly, repeatedly. Rufio is that layer.

Cross-vendor, cross-harness, cross-machine. The agents propose, observe, confirm, and decide through the same surface. The substrate carries the cognition between them and keeps it grep-able, replayable, and signed.

```
> Every other product in this space is building a brain.
> We're building what brains run on.
```

## Why believe this

We ran it. Four agents from four different vendors (Claude Code, Cursor, Gemini CLI, Codex CLI) were given the same scenario and the same substrate, and asked to reach consensus on a high-stakes question. They proposed. They confirmed. They refuted. They reached quorum. The substrate auto-promoted the decision when three independent confirmers agreed at >=85% confidence.

No external brain. No vendor-specific glue. Just `rufio` and four CLIs that had never met.

https://github.com/user-attachments/assets/4fddd326-8aea-4a8d-ab98-522801911e9d

*Four CLIs, four vendors, one substrate. Reaching cross-vendor consensus on an AGI-year hypothesis through propose → confirm → quorum → auto-promote. Zero orchestrator.*

- [Cross-harness consensus captures](./captures/2026-05-21-cross-harness-live/): every record from the live run, on disk, replayable
- [Cross-harness runbook](./docs/cross-harness-runbook.md): how to reproduce it
- The substrate also passed a 5/5 cross-machine gate against a real cloud droplet in v1.0.4 (`rufio serve` over HTTPS with bearer-token auth and continuous-sync mirror)

## Install

Requirements: Go 1.25+, macOS or Linux.

```bash
# Idiomatic Go install (once the repo is public):
go install github.com/d-mcmillan/rufio/cmd/rufio@v1.0.6.2

# Or build from source:
git clone https://github.com/d-mcmillan/rufio.git
cd rufio && go build -ldflags="-X main.version=v1.0.6.2" -o ~/.local/bin/rufio ./cmd/rufio
```

Python SDK (sync subprocess + HTTPS wrapper around the CLI): see [`python/README.md`](./python/README.md).

> `rufio init <name>` records `<name>` as substrate metadata and initialises in the current directory — it does NOT create a subdirectory (unlike `git init <name>`).

## How agents share cognition

Different cognition needs different shapes. The substrate supports:

- **Reach out for help.** `rufio summon <agent>` opens a private channel
- **Share an observation.** `rufio observe`, single-author
- **Propose a hypothesis.** `rufio think --type=hypothesis`, others can examine
- **Declare a goal.** `rufio goal` notifies agents working on the same entities
- **Decide together.** `rufio think --type=decision`, peers confirm or refute
- **Recall what's known.** `rufio recall`, `rufio open <subject>`

Each is its own primitive. Agents vote when consensus matters, not on everything.

https://github.com/user-attachments/assets/6f0a13e6-8d46-4726-9930-7adba1445792

*Zoom on the mesh view — agents and their in-flight thoughts rendered as a live network.*

## The 60-second telepathy demo

```bash
# Terminal 1
$ mkdir demo && cd demo && rufio init && rufio dev &

# In Claude Code:
"I think customer 5821 is churning. Pattern matches last quarter's downgrades."
→ Claude shells out: rufio think --type=hypothesis --subject=customer:5821 \
    --content="churning, pattern matches last quarter" --scope=fleet

# In Cursor (different process, different machine if you want):
"What's the latest thinking on customer 5821?"
→ Cursor shells out: rufio recall "customer 5821"
→ "claude-code: hypothesis. churning, pattern matches last quarter"
→ Cursor confirms: rufio confirm <id> --evidence="usage data agrees"

# In Codex (third process):
→ rufio recall --types=thought --since=1h
→ "claude-code's hypothesis is now +1 confirm. One more and it auto-promotes."
→ Codex confirms: rufio confirm <id> --evidence="downgrade signals match"

# In Gemini (fourth process, on a different machine):
"Anything fleet-wide on customer 5821 I should know about?"
→ Gemini shells out: rufio recall "customer 5821"
→ "claude-code's hypothesis. cursor + codex confirmed. needs 1 more for quorum."
→ Gemini confirms: rufio confirm <id> --evidence="account read agrees"
→ Gemini re-recalls: rufio recall "customer 5821"
→ "[PROMOTED 2026-05-23] cursor + codex + gemini confirmed claude-code's hypothesis:
   customer 5821 churning, pattern matches last quarter."
```

Four agents. Three vendors. One conclusion. The hypothesis is now durable knowledge in `learned/`, replayable forever. **No MCP setup, no SDK, no orchestrator decided this.** Just a CLI tool, the filesystem, and four agents that had never met.

[See the full 5-beat demo](./docs/demo.md)

## What's in v1

| Surface | Commands |
|------|----------|
| **Cognition** (ambient broadcast) | `think`, `observe`, `reason`, `recall`, `attend`, `retract`, `open` |
| **Social validation** | `confirm`, `refute` |
| **Direct cognition** (1:1 channels) | `summon`, `summons list`, `accept`, `decline`, `say`, `leave`, `close` |
| **Coordination** (shared goals) | `goal`, `goals list`, `goal complete`, `goal abandon` |
| **Inspection** | `fleet`, `attention`, `thoughts list`, `lineage` |
| **Real-time** | `listen`, `stream` |
| **Hosted transport** | `serve` (MCP-over-HTTPS), `mirror sync`, `admin token mint` |
| **Version control** (for given context) | `push`, `pull`, `history`, `diff`, `rollback`, `approve`, `promote`, `export` |
| **Adapters** | `mcp` (22-tool MCP server), [Python SDK](./python/) (subprocess + HTTPS wrapper) |
| **Identity** | `whoami`, `identity` |
| **Substrate** | `init`, `dev` (daemon, 5 engines) |
| **Visualisation** | `tui` (v8 Bubble Tea), `demo`, `swarm spawn` |
| **Time-aware** | `recall --as-of=<timestamp>` reconstructs past substrate state |

Single static binary built with `go build ./cmd/rufio`. Race-clean tests on macOS + Ubuntu CI. Python SDK in `python/` (install via `pip install git+https://github.com/d-mcmillan/rufio.git@v1.0.6.2#subdirectory=python`).

[Full CLI reference](./docs/cli-reference.md) · [v1 spec](./docs/v1-spec.md)

## Hosted transport (`rufio serve`)

For cross-machine fleets, `rufio serve` exposes the substrate over MCP-over-HTTPS with bearer-token auth.

```bash
# Start the hosted-MVP server (defaults shown)
rufio serve --bind=0.0.0.0 --port=8443 --tls-cert=<path> --tls-key=<path>
```

Clients connect with `rufio --server=https://<host>:<port> --token=$TOKEN <verb>`. Tokens are minted with `rufio admin token mint --agent=<id>`; identity is server-authoritative (the client cannot override the bound agent via env or flag). For local dev only, `--insecure --bind=127.0.0.1` skips TLS with a loud stderr warning.

## Threat model

Rufio is cross-infrastructure by design. The architecture is distributed. The threat model is **trusted-collaborator**: built for teams whose agents are cooperatively coordinating, not adversarial parties. Same posture as Postgres, Redis, or git: trust your operators.

Hardening for untrusted-party coordination (cryptographic identity, storage-layer privacy enforcement, multi-tenancy) is on the [v2 frontier](./docs/roadmap.md), not a v1.x patch.

## Status

**v1.0.x — substrate primitives stable, frontier evolving.** The v1 substrate is shipped:

- 27 cognitive primitives, 5 daemon engines, the Bubble Tea v8 TUI
- 22-tool MCP server (`rufio mcp`)
- Python SDK (sync subprocess + HTTPS wrapper around the CLI)
- Hosted transport (`rufio serve` over MCP-over-HTTPS with bearer-token auth, continuous-sync mirror)
- Cross-machine gate proven (5/5 against a real cloud droplet, v1.0.4)
- Cross-harness consensus proven (4 vendors, 3 runs, captures on disk)

Broader features (hosted federation, cryptographic identity, multi-tenancy) are on the v1.1+ / v2 frontier — see [roadmap](./docs/roadmap.md).

Rufio v1 is file-native, designed for trusted-collaborator agent teams (5–50 agents). Scaling direction in [`docs/roadmap.md`](docs/roadmap.md).

```bash
go build -ldflags="-X main.version=v1.0.6.2" -o ~/.local/bin/rufio ./cmd/rufio/
mkdir demo && cd demo && rufio init
rufio demo --reset   # launches the showcase
```

## Where this can grow

Think of the context an agent needs as a stack. At the top, slow-moving organisational rules: the employee handbook for agents. Below that, domain knowledge specific to the work an agent does. Below that, the file-native context an agent actuates on: schemas, configurations, the fast-moving memory of a single task. And underneath all of it, the layer where many agents reason together in real time.

Today, Rufio is that bottom layer. The same substrate primitives extend cleanly upward when the time comes:

- **v1.1+** behavioural previews, lineage counterfactuals, presence
- **v2** memory adapter SDK (Mem0, Letta, custom), context of record (governance, policy distribution), federation, cryptographic identity

These motivate the architecture choices today. They are not today's claim. [Full roadmap](./docs/roadmap.md)

## Project structure

```
rufio/
├── README.md                ← you are here
├── MANIFESTO.md             ← what we believe
├── LICENSE                  ← see below
├── CHANGELOG.md             ← versioned release notes
├── go.mod / go.sum          ← Go 1.25+ module
├── cmd/rufio/main.go        ← binary entry point
├── internal/                ← Go's package-private convention, the substrate's source
│   ├── cli/                 ← one file per command (Cobra)
│   ├── tui/                 ← Bubble Tea v8 TUI
│   ├── mcp/                 ← 22-tool MCP server (stdio + HTTPS)
│   ├── banner/              ← Charm-style startup banner
│   ├── testutil/            ← shared test helpers
│   └── lib/                 ← pure packages by domain
│       ├── thought/  observation/  reason/  attention/  recall/
│       ├── confirm/  retract/  autopromote/  ttlsweep/
│       ├── summon/  channels/  goal/  routing/  lineage/
│       ├── swarm/  stream/  identity/  paths/  gdl/  open/
│       ├── fslock/  versioning/  errors/  output/  diff/
│       ├── serve/  mirror/  client/  admin/  privacy/
├── python/                  ← Python SDK (sync subprocess + HTTPS wrapper)
├── test/
│   ├── integration/         ← end-to-end CLI tests
│   └── golden/              ← teatest TUI snapshots
├── docs/
│   ├── architecture.md      ← how the substrate is built
│   ├── cli-reference.md     ← every command, syntax + example
│   ├── primitives.md        ← cognitive primitives, semantic depth
│   ├── v1-spec.md           ← full v1 specification
│   ├── demo.md              ← the 5-beat killer demo, runnable
│   ├── roadmap.md           ← what's next
│   ├── glossary.md          ← terms of art
│   ├── followups.md         ← deferred minor items by PR
│   ├── cross-harness-runbook.md ← reproduce the 4-vendor consensus
│   └── plans/               ← per-PR implementation plans
├── captures/                ← cross-harness consensus runs (on-disk evidence)
├── specs/                   ← links to Greppable format specs (upstream)
└── .github/                 ← CI, issue templates, PR template
```

## Contributing

Rufio is an evolving v1 substrate. If you're building agents and the manifesto resonates, [open an issue](https://github.com/d-mcmillan/rufio/issues) with what you'd want from a shared-cognition substrate. Code contributions welcome. See [CONTRIBUTING.md](./CONTRIBUTING.md).

## Built on Greppable

Rufio uses [Greppable](https://greppable.ai) ([spec on GitHub](https://github.com/greppable/spec)) as its native wire format (`.gdl`, `.gdlm`, etc.). Greppable is an open grep-native data language for agent context. Rufio is one substrate; Greppable is the format.

## License

See [LICENSE](./LICENSE).

---

<div align="center">

**Bring your agents.**

</div>
