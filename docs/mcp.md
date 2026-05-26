# MCP adapter (`rufio mcp`)

> Shipped. `rufio mcp` is a real MCP stdio server — not a stub.
> For the full command list see [cli-reference.md](./cli-reference.md);
> for primitive semantics see [primitives.md](./primitives.md).

## What it is

`rufio mcp` is an [MCP](https://modelcontextprotocol.io) stdio (JSON-RPC)
server that exposes the **agent-participation subset** of Rufio's cognition
verbs as MCP tools. It exists for agent harnesses that don't shell out well —
instead of subprocessing the `rufio` CLI per verb, a harness speaks MCP and
gets typed tool results.

It is a **thin in-process adapter over the existing substrate**: every tool
calls the same `internal/lib` functions the equivalent CLI verb calls and
writes records **byte-identical** to the CLI's (modulo the inherently random
`id` stem and `ts`). It embeds no model/agent SDK and starts no daemon — the
MCP server is just another operator-side transport, exactly like the CLI. A
running `rufio dev` daemon routes/promotes/expires MCP-authored records
exactly as it does CLI-authored ones; the substrate behaviour is unchanged.

## Identity-per-server model

**One server instance = one agent identity**, resolved **once at server
start** and applied to every tool call (the natural MCP pattern: the MCP
client bakes identity into its server config). Resolution precedence:

| Step | Source |
|------|--------|
| `--root <path>` | substrate root; if omitted, walk up from the server's CWD to find `rufio.gdl` |
| `--agent <id>` | explicit agent id (highest precedence) |
| `RUFIO_AGENT_ID` | env var, if `--agent` not given |
| `.rufio/identity.local.gdl` | the project-local identity record, if neither of the above is given |

A bad `--root` (not in a project) or an unresolved identity is a **startup
error**: it is printed to stderr and the process exits with the standard
`rufio` exit code *before* the stdio loop is entered — behaving exactly like
any other verb. Once the stdio loop is running, the server never exits on a
tool error (see [Error convention](#error-convention)).

## Client configuration

### Claude Desktop

Add to `claude_desktop_config.json` under `mcpServers`:

```json
{
  "mcpServers": {
    "rufio": {
      "command": "rufio",
      "args": ["mcp", "--root", "/abs/path/to/substrate", "--agent", "my-agent"]
    }
  }
}
```

### Generic MCP client

Any MCP client launches the same command over stdio:

- **command:** `rufio`
- **args:** `["mcp", "--root", "/abs/substrate", "--agent", "my-agent"]`

`--agent` may be dropped if `RUFIO_AGENT_ID` is set in the server's
environment, or if the substrate has a `.rufio/identity.local.gdl`. Use an
absolute `--root` so identity/root resolution does not depend on the client's
working directory.

## Tool surface (21 tools)

The surface is 1:1 with the agent-participation verbs. Each tool's input
mirrors the verb's flags (typed) and each tool returns the same structured
JSON the verb's `--json` mode emits, so clients get typed results, not
scraped text.

### Cognition

| Tool | Purpose |
|------|---------|
| `attend` | Record that this agent is attending to something (`intent`, `entities`, `topics`). |
| `think` | Write a thought (ambient broadcast) to `live/outbox/`; `type=decision` also writes a sibling context bundle. |
| `observe` | Record a durable observation (subject-predicate-object triple) under `learned/`. |
| `reason` | Capture a step in the agent's reasoning chain under `live/reasoning/`. |
| `retract` | Retract one of this agent's own thoughts (writes `live/retracted/<id>.gdl`). |

### Verification

| Tool | Purpose |
|------|---------|
| `confirm` | Confirm a thought (anyone may confirm any thought); appends to `live/confirms/<id>.gdl`. |
| `refute` | Refute a thought with a required `reason`; appends to `live/confirms/<id>.gdl`. |

### Channels

| Tool | Purpose |
|------|---------|
| `summon` | Open a private channel by summoning another agent (24h TTL). |
| `accept` | Accept a pending summon and open the channel. |
| `decline` | Decline a pending summon addressed to this agent. |
| `say` | Write a message to a channel this agent is a current member of. |
| `leave` | Leave a channel (audit trail preserved; idempotent). |
| `close` | Close a channel (opener only; archives `active/`→`closed/`). |

### Coordination

| Tool | Purpose |
|------|---------|
| `goal` | Declare a coordination goal (writes `live/goals/active/<id>.gdl`). |
| `goal_complete` | Mark an active goal completed (author-only; archives to `completed/`). |
| `goal_abandon` | Abandon an active goal (author-only; archives to `abandoned/`). |

### Read

| Tool | Purpose |
|------|---------|
| `recall` | Scan the corpus across `given/learned/live` and return matching records (read-only; `{ "records": [...] }`). |
| `goals_list` | List coordination goals across the project (read-only; `{ "goals": [...] }`). |
| `listen` | Bounded poll of this agent's inbox — see [the cursor contract](#the-listen-cursor-contract). |
| `open` | Read-dual of `attend`. Bundles identity + daemon + fleet + attention + recall + thoughts for a subject into a single stable-keyset JSON object. Pure read. Fidelity contract: result is byte-identical to `rufio open <subject> --json`. |
| `serve_status` | (v1.0.4) Read-only health probe for the hosted server. Returns `{root, has_tokens, token_count, tls_hint}`. Token mint/revoke are deliberately NOT exposed — only the local operator may invoke them via the CLI. |

## Hosted-mode transport (v1.0.4)

The same 21-tool surface is also reachable over HTTPS. Run `rufio serve --tls-cert=... --tls-key=...` to expose `/mcp` as an MCP-over-HTTPS endpoint. Clients use the `--server=<url>` and `--token=<value>` flags on any cognition verb to route through the remote server:

```bash
rufio --server=https://rufio.example.com:8443 --token=$RUFIO_TOKEN \
      recall --topics=alpha
```

Identity is server-authoritative: the bearer token resolves to an agent at the server; the client CANNOT override identity by header or flag. Privacy is enforced server-side on every read path. See [hosted.md](./hosted.md) for the operational guide.

## The `listen` cursor contract

`listen(cursor?, max?, types?, scope?) -> { events: [...], next_cursor }` is
a **bounded poll, not a tail**. Each call does a full inbox scan, returns up
to `max` (default 100) events in chronological order, and an opaque
`next_cursor`. Contract:

- **`next_cursor` is opaque.** Treat it as a token: pass it back
  **byte-for-byte** on the next call as `cursor`. Do not parse, construct,
  or mutate it. (It is internally a base64 `(ts, path)` key, but that is an
  implementation detail and may change.)
- **Monotonic.** Each call returns only events strictly *after* `cursor`,
  chronologically ordered (timestamp, then path, as a stable tiebreak). The
  first call omits `cursor` (poll from the beginning of the inbox).
- **Poll to the tail.** Call repeatedly, feeding the previous
  `next_cursor`, until `events` is empty. **An unchanged `next_cursor`
  (equal to the `cursor` you sent) means no new events** — the poll is
  idempotent; re-polling with the same cursor returns zero events and the
  same cursor.
- `types` (CSV record-type filter) and `scope` (`agent|deployment|fleet`)
  optionally narrow the page, matching `rufio listen --types/--scope`.
- **Notification-ready.** The returned events use the canonical
  `stream.Event` wire schema unchanged. A future push/subscription
  transport (v1.2) reuses that exact schema and resumes from the same
  cursor key, so adding push later does not break pollers.

## Error convention

A tool error returns an MCP tool error and **the server stays up** (the
explicit anti-`os.Exit` requirement). The error message is:

```
[rufio:N] <message>
```

where `N` is the stable exit class — **`1`** for runtime / not-found errors
(e.g. no such thought, not a member), **`2`** for usage / validation errors
(e.g. empty required field). Clients can branch on `N` without parsing prose.

The `<message>` prose is **CLI-flag-phrased by design** — e.g. an empty
`intent` argument surfaces as `[rufio:2] --intent must not be empty`, an
empty `subject` as `[rufio:2] --subject must not be empty`. This is **not a
bug**: MCP tools reuse the shared `rufioerr` error set unchanged (the locked
no-new-error-types invariant), so a validation failure carries the same
typed message and exit class as the CLI. Map the MCP argument name to the
flag name in the prose when surfacing errors to users.

## What is excluded, and why

An MCP client is a **participating agent on an already-initialised
substrate**, not the operator. So local-ops, governance, and identity/admin
verbs are **excluded from the MCP surface forever**:

- **Local-ops:** `dev`, `tui`, `demo`, `swarm`, `init` — these manage or
  bootstrap a substrate / run the operator's local processes; they are not
  acts of cognition.
- **Governance:** `push`, `approve`, `promote`, `rollback` — these are
  operator-authority controls over the substrate's version state, not
  participation.
- **Identity/admin:** `identity`, `whoami` — identity is fixed once per
  server at startup (one server = one agent), so per-call identity verbs
  are meaningless here.

If a harness needs these, it is acting as the operator and should use the
CLI directly. The MCP surface is, and stays, the participation subset.
