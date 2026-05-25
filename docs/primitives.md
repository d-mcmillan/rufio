# Cognitive primitives

> The complete reference for the thirteen cognitive commands an agent uses to participate in the substrate.

Four modes:

- **Ambient** — broadcast/subscribe semantics. Most thoughts and observations.
- **Verification** — turn hypotheses into beliefs through distributed confirmation.
- **Direct cognition (channels)** — private agent-to-agent conversations.
- **Coordination (goals)** — declared intent, surfaced across the fleet.

Plus a `--as-of` flag on recall and lineage for sub-second time-aware queries.

---

## Ambient

### `rufio think`

Emit an in-flight thought, broadcast to attending peers. No built-in expiry — a thought lives until you `retract` it; decay is opt-in via `--ttl=<seconds>` or the `rufio.gdl` `@retention` sweep policy.

```
rufio think --type=<hypothesis|observation|decision|question|focus> \
            --subject=<entity> --content=<text> \
            --scope=<agent|deployment|fleet> \
            [--ttl=<seconds>] [--parent=<thought-id>] [--json]
```

Use when: you have an in-flight reasoning step worth sharing with peers (a hypothesis, a current focus, an emerging pattern). A thought is ephemeral *working* memory and an observation is durable memory — but a thought has no built-in expiry: it lives until you `retract` it. Decay is opt-in, via an explicit `--ttl=<seconds>` or the project's `@retention|type:thought` sweep policy in `rufio.gdl` (the `init` scaffold ships one at `ttl:300`). Observations never decay.

`rufio think --json` returns a JSON object whose top-level `"id"` is the new thought's id — capture it to hand to peers for `confirm` / `refute` / `think --parent`. It is the same id those verbs and `retract` take.

Example:
```
rufio think --type=hypothesis --subject=customer:5821 \
            --content="Showing churn signals — frustration, escalation language" \
            --scope=fleet --ttl=300
```

### `rufio observe`

Record a durable observation about an entity. Stored, indexed, queryable.

```
rufio observe --subject=<entity> --predicate=<rel> --object=<value> \
              --scope=<...> [--confidence=<0..1>]
```

Use when: you've learned a stable fact about an entity that other agents will benefit from. This is *memory*, not *thought*.

Example:
```
rufio observe --subject=customer:5821 --predicate=prefers \
              --object=email --scope=fleet --confidence=0.9
```

### `rufio reason`

Capture a step in your reasoning chain. Makes lineage queries return how an agent thought, not just what it knew.

```
rufio reason --content=<text> [--parent=<reason-id>] [--decision=<decision-id>]
```

Use when: you're walking through a non-trivial decision and want the reasoning to be auditable. Other agents can read your reasoning; future versions of yourself can learn from what worked.

Example:
```
rufio reason --content="Customer requested $400 refund. Threshold is $500. Auto-approving."
```

### `rufio recall`

Query stored context. Pulls from given, learned, live, or thoughts.

```
rufio recall <query> [--scope=<...>] [--types=given,learned,live,thought] \
             [--since=<duration>] [--as-of=<timestamp>]
```

Use when: you need to know what the team knows. Default returns matches across all scopes the agent has access to. `--as-of` reconstructs the substrate's state at a past timestamp (sub-second resolution).

Example:
```
rufio recall "customer 5821 preferences"
rufio recall "customer 5821" --as-of=2026-04-15T14:32:00Z
rufio recall --types=thought --since=2h
```

Recalled **thoughts** include their `thought-id` so you can act on what
you recall. Plain output surfaces it as a labelled `id=<id>` field;
`--json` exposes a top-level `"id"`. It is the same id `confirm`,
`refute`, `retract` and `think --parent` take.

```
2026-04-15T14:32:00Z  thought  data-analyst  id=1778980153805-1xw0aa  customer:5821  fleet  "churn risk rising"
```

This is the **recall → get id → confirm** flow — the core of collective
cognition. Recall a peer's hypothesis, lift its id straight out of the
output, and confirm or refute it:

```
rufio recall --types=thought "churn"
# take the id=<id> from the matching line
rufio confirm 1778980153805-1xw0aa --evidence="email response time also dropped"
```

Observations, reasons and given/ files have no per-record id a verb
consumes, so they carry no `id` field.

### `rufio attend`

Declare what you're attending to. Drives subscription routing — relevant thoughts get pushed to your inbox.

```
rufio attend --intent=<text> --entities=<csv> [--topics=<csv>]
```

Use when: at the start of a session, or when your focus shifts. The substrate uses this to decide what to deliver to your inbox.

Example:
```
rufio attend --intent="customer support" --entities=customer:5821,customer:5822
```

### `rufio retract`

Withdraw a previously emitted thought, with a rationale. Append-only — the original stays in the audit trail, marked as retracted.

```
rufio retract <thought-id> --reason=<text>
```

Use when: you realise an earlier hypothesis was wrong and want to clear it before another agent acts on it.

---

## Verification

### `rufio confirm`

Raise the confidence on a thought. The thought auto-promotes to a durable observation once it has been confirmed by ≥3 distinct agents (counted by `--as`/`RUFIO_AGENT_ID`, deduplicated — the author may be one of them if they separately run `rufio confirm`; emitting the original `rufio think` does not count) at ≥0.85 confidence, where confidence = confirms / (confirms + refutes). Only `rufio confirm` records count toward quorum. These thresholds are fixed in v1; making them configurable in `rufio.gdl` is a planned v1.1 followup.

```
rufio confirm <thought-id> [--evidence=<text>]
```

Use when: another agent emitted a hypothesis you can independently verify. This is the consensus mechanism that turns individual cognition into collective cognition.

### `rufio refute`

Lower the confidence on someone else's thought. Tracks the dispute in the lineage trail.

```
rufio refute <thought-id> --reason=<text> [--evidence=<text>]
```

Use when: you have evidence that another agent's hypothesis is wrong. Don't silently overwrite — surface the conflict.

---

## Direct cognition (channels)

### `rufio summon`

Open a private channel with another specific agent. The substrate routes the summon to their inbox; if they accept, both agents are now members of the channel.

```
rufio summon <agent-id> --topic=<topic> --intent=<reason>
```

Returns a channel-id. The summoning agent can immediately start listening on the channel.

### `rufio summons list`

Find out you've been summoned (pull pattern). Push pattern: summons appear in `rufio listen` output.

```
rufio summons list [--as=<my-id>] [--pending|--all]
```

### `rufio accept` / `rufio decline`

Join or refuse a summon.

```
rufio accept <summon-id>
rufio decline <summon-id> --reason=<text>
```

### `rufio say`

Send a message into a channel. Only members of the channel see it.

```
rufio say --channel=<ch-id> --content=<message>
```

### `rufio leave`

Exit a channel. Audit trail preserved.

```
rufio leave <channel-id>
```

### `rufio close`

Archive a channel. Only the opener can close.

```
rufio close <channel-id>
```

---

## Coordination (goals)

### `rufio goal`

Declare what you're trying to achieve. Goals make intent visible across the fleet — other agents can see, coordinate, or volunteer.

```
rufio goal --statement=<text> [--by=<deadline>] [--parent=<goal-id>] \
           [--scope=<agent|deployment|fleet>]
```

When two agents declare overlapping goals, the substrate surfaces it in both inboxes — they can self-coordinate without an orchestrator.

### `rufio goals list`

See active goals across a scope.

```
rufio goals list [--scope=<...>] [--state=active|completed|abandoned]
```

### `rufio goal complete` / `rufio goal abandon`

Mark a goal done or yield it.

```
rufio goal complete <goal-id> --outcome=<text>
rufio goal abandon <goal-id> --reason=<text>
```

---

## Streaming (real-time)

### `rufio listen`

Long-running command. Tails the agent's inbox; streams matching events to stdout. Latency 50-500ms.

```
rufio listen [--as=<agent-id>] [--types=<csv>] [--scope=<...>]
```

Use when: you want true real-time peer coordination. The harness wrapper or a background pipe feeds the output back into your reasoning context.

### `rufio stream`

Like listen but global — streams all events across the substrate. Mostly for human inspection.

```
rufio stream [--type=...] [--scope=...]
```

---

## Identity

### `rufio attend` (covered above)

Declares attention. Drives subscriptions.

### `rufio whoami`

Shows the agent identity for the current shell session.

### `rufio identity`

Declare identity for a shell session.

```
rufio identity --as=<agent-id>
```

---

## Inspection (human)

These are tools for humans to look into the substrate, not commands agents typically use:

- `rufio fleet` — list connected agents
- `rufio attention <agent-id>` — what is an agent attending to
- `rufio thoughts list [--since=<duration>]`
- `rufio lineage <decision-id>` — full provenance + reasoning chain for a decision

---

## Conventions

- Default output: human-readable, columnar, one record per line, grep-friendly
- `--json` flag: JSONL output, pipeable to `jq`
- ISO timestamps everywhere
- Exit code 0 on success, non-zero on error
- Stderr for diagnostics
- Colour only when stdout is a TTY (auto-detected)
- `NO_COLOR=1` disables colour entirely

For full output examples, see [`v1-spec.md`](./v1-spec.md#output-conventions-shell-friendly-defaults).
