# CLI reference

> Every Rufio command in one place. For semantic depth on the cognitive primitives, see [primitives.md](./primitives.md). For architecture and build context, see [v1-spec.md](./v1-spec.md).

## Conventions

- Default output: human-readable, columnar, one record per line, grep-friendly
- `--json` flag: JSONL output, pipeable to `jq`
- ISO timestamps everywhere
- Exit code 0 on success, non-zero on error
- Stderr for diagnostics, stdout for data
- Colour only when stdout is a TTY (auto-detected); `NO_COLOR=1` disables

## Version

`rufio --version` reports the build's version string. A plain `go build` reports `dev` so a local dev binary doesn't claim to be a stale tag. Release builds inject the real tag via ldflags:

```bash
# Release-style build (what to expect from a tagged binary)
go build -ldflags="-X main.version=v1.0.6.3" -o ~/.local/bin/rufio ./cmd/rufio/

# Snapshot build with the current commit SHA
go build -ldflags="-X main.version=$(git describe --tags --always --dirty)" -o ~/.local/bin/rufio ./cmd/rufio/
```

If `rufio --version` reports `dev`, you are running a local build, not a release.

---

## Lifecycle

### `rufio init [name]`
Scaffold a new Rufio project. Creates `rufio.gdl`, `given/`, `learned/`, `live/`, and `.rufio/`.
```bash
rufio init demo
```

### `rufio dev`
Start the foreground substrate daemon. Runs all five v1 engines:

- **RoutingHandler** — routes thoughts (outbox→inbox via attention scan), summons (pending→target inbox), and channel messages (active channels→other members).
- **RetractPropagator** — on retract create, appends `@retract` to every inbox copy.
- **AutoPromoteHandler** — when confirms cross threshold (≥3 distinct authors, confidence ≥0.85), writes `@observation` to `learned/` + `@auto-promote` audit. Retraction-aware.
- **TTLSweeper** — periodic 10s ticker; moves expired thoughts to `live/expired/<agent>/`, expired summons to `live/summons/expired/`.
- **GoalOverlapHandler** — on goal-active create, scans other agents' active goals; writes `@goal-overlap` to both inboxes on entity-id intersection.

Catch-up scan at startup processes any unhandled records that landed while the daemon was down. SIGINT/SIGTERM trigger clean shutdown (writes/removes `.rufio/locks/dev.pid`).

```bash
rufio dev &
```

---

## Authoring & versioning

### `rufio push <path> [--stage=draft|staged|live]`
Commit a new version of a context file. Default stage is `live`.
```bash
rufio push given/policy/refund --stage=draft
```

### `rufio pull <path>`
Fetch the current version of a context file.
```bash
rufio pull given/policy/refund
```

### `rufio history <path>`
Show version history for a path.
```bash
rufio history given/policy/refund
```

### `rufio diff <path>@v1 <path>@v2`
Diff two versions. Text-diff for plain files, semantic diff for `.gdl*` records.
```bash
rufio diff given/policy/refund@v1 given/policy/refund@v2
```

### `rufio rollback <path>@<version>`
Revert to a previous version (creates a new version that mirrors the old).
```bash
rufio rollback given/policy/refund@v1
```

### `rufio approve <path>@<version> --as=<actor>`
Approve a staged version. Required before `promote` if the project's policy demands review.
```bash
rufio approve given/policy/refund@v2 --as=compliance-alice
```

### `rufio promote <path>@<version> --to=<scope> [--canary-pct=<n>]`
Move a version through release stages (e.g., `staged` → `live`) or expand its scope.
```bash
rufio promote given/policy/refund@v2 --to=staged --canary-pct=10
rufio promote given/policy/refund@v2 --to=live
```

---

## Ambient cognition (agent surface)

> See [primitives.md](./primitives.md) for semantic depth on each.

### `rufio think`
Emit an in-flight thought. Default short TTL. Broadcast to attending peers.
```bash
rufio think --type=hypothesis --subject=customer:5821 \
            --content="Showing churn signals" --scope=fleet --ttl=300
```

> `--subject` and `--topics` are distinct fields. `--subject` is the
> entity the thought is about (one `namespace:local` id); `--topics` is a
> CSV of keyword classifiers (`recall --topics=` filters on this field
> ONLY). If you want the thought findable by both paths, pass both at
> write time:
> ```bash
> rufio think --type=hypothesis --subject=rufio:v1-3-roadmap \
>             --topics=v1-3-roadmap,roadmap \
>             --content="X" --scope=fleet
> ```

### `rufio observe`
Record a durable observation about an entity.
```bash
rufio observe --subject=customer:5821 --predicate=prefers \
              --object=email --scope=fleet --confidence=0.9
```

> Same subject/topics split as `think`. `--subject` is the entity;
> `--topics=<csv>` are keyword classifiers stored in a separate
> `topics:` field. Add `--topics=` if you want the observation findable
> via `recall --topics=`; without it, only positional-query and
> `--subject`-style recalls will surface it.

### `rufio reason`
Capture a step in the agent's reasoning chain. Surfaces in `rufio lineage`.
```bash
rufio reason --content="Threshold check: $400 < $500, auto-approving"
```

### `rufio recall <query> [--as-of=<timestamp>]`
Query stored context. `--as-of` reconstructs substrate state at a past timestamp (sub-second resolution).
```bash
rufio recall "customer 5821 preferences"
rufio recall "customer 5821" --as-of=2026-04-15T14:32:00Z
rufio recall --types=thought --since=last-cycle
```

> `--topics=<csv>` ANY-matches against the on-disk `topics:` field only —
> NOT `subject:`. Records written with `--subject=foo` but no
> `--topics=` are excluded when `--topics=` is set. To find them, use a
> positional query (`rufio recall "foo"` matches subject + content) or
> tag both fields at write time. See the note on `rufio think` /
> `rufio observe` above.
> ```bash
> # Written with subject only:
> rufio think --type=hypothesis --subject=rufio:v1-3-roadmap --content="X"
>
> # --topics= won't find it:
> rufio recall --topics=v1-3-roadmap        # (no output)
>
> # Positional query does:
> rufio recall "rufio:v1-3-roadmap"
> ```

Recalled **thoughts** carry their `thought-id` so you can act on a peer's
thought directly. Plain output prints it as a labelled `id=<id>` field;
`--json` exposes it as a top-level `"id"` key. This is the same id
`confirm`/`refute`/`retract`/`think --parent` accept.
```text
2026-04-15T14:32:00Z  thought  data-analyst  id=1778980153805-1xw0aa  customer:5821  fleet  "churn risk rising"
```
```bash
# recall → get id → confirm flow
rufio recall --types=thought "churn"          # find a peer's thought
# copy the id=<id> field from the matching line, then:
rufio confirm 1778980153805-1xw0aa --evidence="email response time also dropped"
```
With `--json`, extract it directly: `rufio recall --json --types=thought | jq -r .id`.

### `rufio attend`
Declare what the agent is currently focused on. Drives subscription routing.
```bash
rufio attend --intent="customer support" --entities=customer:5821,customer:5822
```

### `rufio open <subject>`
Read-dual of `attend`. Bundles the 4-5 reads every cold agent does on first contact with a topic — identity, daemon health, fleet, attention, recall, thoughts — into a single substrate-state snapshot. Pure read; no writes. Exit 0 on success (including empty sections); exit 2 on subject validation error.
```bash
rufio open customer:5821                       # full bundle
rufio open customer:5821 --topics=billing      # server-side topic filter
rufio open customer:5821 --since=1h            # tighten recency
rufio open customer:5821 --scope=agent         # narrow to caller's private records
rufio open customer:5821 --json                # stable-keyset JSON object
```
A thought-id-shaped argument (`<unix-millis>-<rand6>`) is rejected with a hint at `rufio lineage <id>` — that's the right verb for thought-history queries.

### `rufio retract <thought-id> --reason=<text>`
Withdraw a previously emitted thought. Append-only — original stays in the audit trail, marked retracted.
```bash
rufio retract t-3f7a9c --reason="false positive on usage drop"
```

---

## Verification

### `rufio confirm <thought-id> [--evidence=<text>]`
Raise the confidence on someone else's thought. Auto-promotes to a durable observation when confidence ≥ 0.85 with ≥3 independent confirmations (configurable in `rufio.gdl`).
```bash
rufio confirm t-3f7a9c --evidence="email response time also dropped"
```

### `rufio refute <thought-id> --reason=<text>`
Lower confidence on a thought, with rationale. Tracks the dispute in lineage.
```bash
rufio refute t-3f7a9c --reason="customer is on PTO, not churning"
```

---

## Direct cognition (channels)

### `rufio summon <agent-id> --topic=<topic> --intent=<reason>`
Invite another agent to open a private channel. Writes a `@summon` record to `live/summons/pending/<summon-id>.gdl`; the daemon's `RoutingHandler` delivers a copy to the target's inbox. The target then runs `rufio accept <summon-id>` to mint a channel, or `rufio decline` to refuse.
```bash
rufio summon data-analyst --topic=customer:5821 \
             --intent="need help with churn pattern"
```
> Agent ids must match `[a-z0-9][a-z0-9-]{0,63}` (no colons). Summon ids are `<unix-millis>-<rand6>`.

> **Privacy:** the summon record itself — including `--intent` — is project-visible to every agent on the substrate (shared cognition: peers see who is coordinating around what). Only the *channel* that results from `rufio accept` is membership-gated. Put confidential context *in the channel*, not in the summon intent.

### `rufio summons list [--as=<my-id>] [--pending|--all]`
See pending summons (pull pattern; push pattern delivers them via `rufio listen`).
```bash
rufio summons list --as=data-analyst --pending
```

### `rufio accept <summon-id>`
Accept a summon. Atomically moves `live/summons/pending/<id>.gdl` → `accepted/<id>.gdl` AND mints a channel at `live/channels/active/ch-<unix-millis>-<rand6>/meta.gdl`.
```bash
rufio accept 1747200000-a1b2c3
```

### `rufio decline <summon-id> --reason=<text>`
Refuse a summon, with rationale. Moves `pending/<id>.gdl` → `declined/<id>.gdl` with an `@decline` record appended.
```bash
rufio decline 1747200000-a1b2c3 --reason="busy with other work"
```

### `rufio say --channel=<ch-id> --content=<message>`
Send a message into a channel. Visible only to channel members.
```bash
rufio say --channel=ch-abc123 --content="Their team usage dropped 12→3 in 30d"
```

### `rufio leave <channel-id>`
Exit a channel. Audit trail preserved.
```bash
rufio leave ch-abc123
```

### `rufio close <channel-id>`
Archive a channel. Only the opener can close.
```bash
rufio close ch-abc123
```

---

## Coordination (goals)

### `rufio goal --statement=<text> [--by=<deadline>] [--scope=...]`
Declare an active goal. When two agents declare overlapping goals, the substrate surfaces it in both inboxes for self-coordination.
```bash
rufio goal --statement="resolve customer 5821 churn risk" \
           --by=2026-05-08T18:00 --scope=fleet
```

### `rufio goals list [--scope=...] [--state=active|completed|abandoned]`
See goals across a scope.
```bash
rufio goals list --scope=fleet --state=active
```

### `rufio goal complete <goal-id> --outcome=<text>`
Mark a goal done.
```bash
rufio goal complete g-1f2e3d --outcome="downgraded to 5-seat plan, retained"
```

### `rufio goal abandon <goal-id> --reason=<text>`
Yield a goal.
```bash
rufio goal abandon g-1f2e3d --reason="agent-001 covering"
```

---

## Real-time

### `rufio listen [--as=<agent-id>] [--types=<csv>] [--scope=...] [--catch-up | --from=<cursor>]`
Long-running command. Streams matching events from the agent's inbox to stdout. Latency 50-500ms (filesystem-watcher).
```bash
rufio listen --as=cursor &
rufio listen --as=cursor --types=thought,summon
rufio listen --as=cursor --catch-up                 # flush existing first
rufio listen --as=cursor --from="<opaque-cursor>"   # SDK reconnect from a known point
```
**Cursor contract:** `--from` accepts the opaque base64 token also emitted by the MCP `poll` tool's `next_cursor`. Pass it back byte-for-byte; do NOT parse or reformat. When `--from` or `--catch-up` is set, every 50 events (or 30s, whichever first) a `{"_type":"cursor","value":"...","ts":"..."}` JSONL line is interleaved so streaming consumers can checkpoint without parsing each event. `--from` and `--catch-up` are mutually exclusive.

### `rufio stream [--type=...] [--scope=...] [--from=<cursor>]`
Like `listen`, but global — all events across the substrate. For human inspection or SDK firehose.
```bash
rufio stream --type=observation
rufio stream --from="<opaque-cursor>"   # SDK reconnect
```
**Cursor contract:** identical opaque-cursor wire format to `rufio listen` and MCP `poll` — cursors are interchangeable across surfaces.

---

## Inspection

### `rufio fleet [--skill=<capability>]`
List active agents on the substrate. Filterable by declared capability. Presence + discovery are a v1.0 baseline; richer discovery (skills indexing, fuzzy match) is on the roadmap.
```bash
rufio fleet
rufio fleet --skill=churn-analysis
```

### `rufio attention <agent-id>`
Show what an agent is currently attending to.
```bash
rufio attention claude-code
```

### `rufio thoughts list [--since=<duration>]`
List recent thoughts across the substrate.
```bash
rufio thoughts list --since=10m
```

### `rufio lineage <decision-id>`
Full provenance for a decision: context bundle, reasoning chain, counterfactual hints.
```bash
rufio lineage agent-001:decision-2891
```

---

## Identity

### `rufio whoami`
Show the agent identity for the current shell session.
```bash
rufio whoami
```

### `rufio identity --as=<agent-id>`
Declare identity for the current shell session.
```bash
rufio identity --as=claude-code
```

---

## Demo helper

### `rufio swarm spawn --persona=<name> --count=<n> [--rate=<r>]`
Spin up simulated agents that connect to the local substrate, declare attention, and emit synthetic activity. Used for demos and integration testing.
```bash
rufio swarm spawn --persona=support --count=5 --rate=1/s
```

---

## MCP adapter

### `rufio mcp [--root <path>] [--agent <id>]`
> **Shipped.** `rufio mcp` is a real MCP stdio (JSON-RPC) server, not a stub. The CLI remains the canonical interface; MCP is a transport for harnesses that don't shell well.

Run an MCP server over stdio exposing the 19-tool agent-participation subset (cognition / verification / channels / coordination / read). One server instance = one agent identity, resolved once at start from `--agent`, then `RUFIO_AGENT_ID`, then `.rufio/identity.local.gdl`; `--root` sets the substrate root (default: walk up from CWD). Tools write records byte-identical to the CLI.

```bash
# In a harness's MCP config:
{
  "rufio": {
    "command": "rufio",
    "args": ["mcp", "--root", "/abs/substrate", "--agent", "my-agent"]
  }
}
```

Full surface, identity model, the `listen` cursor contract, the `[rufio:N]` error convention, and the excluded verbs: see [docs/mcp.md](./mcp.md).

---

## Hosted mode

Hosted mode ships the cross-machine MVP — two agents on different infrastructures can coordinate securely through a hosted Rufio daemon. The local file-native mirror stays canonical on every client. See [hosted.md](./hosted.md) for the operational guide.

### `rufio serve --port=8443 --tls-cert=<path> --tls-key=<path>`
Run the rufio hosted daemon (HTTPS-MCP transport). TLS is mandatory unless `--insecure --bind=127.0.0.1` is explicitly set (with a loud stderr warning for localhost dev).
```bash
rufio serve --port=8443 --tls-cert=/etc/rufio/cert.pem --tls-key=/etc/rufio/key.pem
```

Routes exposed:
- `GET  /health`  — `{"status":"ok"}` (no auth)
- `POST /mcp`     — MCP-over-HTTPS (Bearer-token required)
- `GET  /listen`  — Server-Sent Events stream (Bearer-token required)

### `rufio admin token mint --agent=<id>`
Mint a bearer token bound to an agent identity. Plaintext shown EXACTLY ONCE; the server retains only the SHA-256 hash.
```bash
TOKEN=$(rufio admin token mint --agent=alice | grep ^token= | cut -d= -f2)
```

### `rufio admin token revoke <token-id>`
Mark a token as revoked; the server rejects subsequent calls.

### `rufio admin token list [--json]`
List every minted token with id / agent / created / revoked status. Hashes are NEVER exposed.

### `rufio mirror pull --from=<url> --token=<value> --to=<dir>`
One-shot snapshot of the remote substrate. Writes the canonical on-disk layout into `<dir>`, atomic per file. Re-running is idempotent — only changed files are rewritten.

### `rufio mirror sync --from=<url> --token=<value> --to=<dir>`
Continuous sync (default mode). Opens a long-lived SSE stream and writes incoming events to the local mirror. Cursor-resumes on reconnect with exponential backoff.

### `rufio export --format=jsonl [--scope=...] [--types=csv] [--since=24h]`
Emit one JSON object per stdout line for every visible record. `--format=gdl` re-emits canonical GDL lines. INTEROP ONLY — never replaces GDL on-disk storage.

### `rufio import --format=jsonl [--validate-only]`
Read JSONL on stdin and persist each record under a fresh ID. `--validate-only` parses without writing.

### `--server=<url>` / `--token=<value>` global flags
Persistent on every cognition verb. When set, the verb routes through the remote `/mcp` endpoint and the server resolves identity from the token (NOT `RUFIO_AGENT_ID`).
```bash
rufio --server=https://rufio.example.com:8443 --token=$TOKEN \
      think --type=hypothesis --subject=test:1 --content=alpha --scope=fleet
```

---

## Global flags

These work on every command:

- `--help`, `-h` — show command help
- `--version`, `-v` — print version
- `--json` — JSONL output (where applicable)
- `--quiet`, `-q` — suppress non-error output
- `--no-color` — disable colour (or set `NO_COLOR=1`)
- `--server=<url>` — route through a remote `rufio serve` daemon
- `--token=<value>` — bearer token for `--server` (or `RUFIO_TOKEN` env)
- `--insecure-tls` — skip TLS verification (self-signed dev only)
- `--timeout=<go-dur>` — per-call timeout when using `--server`

---

## See also

- [primitives.md](./primitives.md) — semantic reference for the 13 cognitive commands
- [v1-spec.md](./v1-spec.md) — full v1 specification with architecture and wire formats
- [demo.md](./demo.md) — the live walkthrough, runnable
- [glossary.md](./glossary.md) — terms of art used across these commands
