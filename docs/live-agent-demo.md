# Live agent demo — multi-harness, zero-orchestrator

> An operator playbook. Three **different vendors' real CLIs** (Claude Code,
> Gemini CLI, Cursor CLI) coordinate on one investigation purely through the
> Rufio filesystem substrate + the `rufio dev` daemon — no orchestrator, no
> shared SDK, no API keys. A peer's hypothesis becomes durable shared memory
> by quorum. Every command below was verified against the built binary.

## 1. What this demonstrates

Three independent agent harnesses, from three vendors, on three processes,
coordinate on `customer:5821` with **no orchestrator**: the only shared
medium is the project filesystem + the local daemon. A lead posts a
hypothesis; peers independently `recall` it, gather evidence, and `confirm`;
when a real quorum is reached the daemon **auto-promotes** the hypothesis to
a durable `learned/…gdlm` observation — collective cognition with no human
or model in the coordination loop.

Relation to the other demo docs (don't restate them — link them):

- [`demo.md`](./demo.md) — the scripted 5-beat narrative ("killer demo"),
  one-flow, hand-driven, for the launch post body.
- [`launch-script.md`](./launch-script.md) — the asciinema **recording**
  script (`rufio demo` + `rufio swarm` + the TUI) for the launch video.
- **This doc** — the *live multi-harness* recipe: real third-party CLIs
  driven headless against the substrate. It is also a **dogfood test**
  (section 8). Use this when you want to *prove* substrate neutrality with
  real, non-Rufio harnesses, not narrate or record a scripted run.

## 2. Prerequisites

| Need | How |
|---|---|
| `rufio` binary | `go build -o /tmp/rufio-demo ./cmd/rufio` (or `go install …/cmd/rufio`); on `$PATH` as `rufio`. |
| The daemon | `rufio dev` running in the project (section 3). Auto-promotion + inbox routing only fire while it runs. |
| Claude Code CLI | Installed; **interactively logged in** (your own session). |
| Gemini CLI | Installed; **interactively logged in**. |
| Cursor CLI | Installed; **interactively logged in**. |

**No API keys.** Interactive logins carry to headless for all three. The
only real headless friction is per-harness *workspace-trust gating* — set
these or the harness silently degrades / errors before it ever runs `rufio`:

| Harness | Required headless flags | Failure symptom if omitted |
|---|---|---|
| **Gemini CLI** | `--yolo --skip-trust` (or env `GEMINI_CLI_TRUST_WORKSPACE=true`) | untrusted-dir → shell-out blocked → **exit 55** |
| **Cursor CLI** | `--force` (or `--trust`) | **exit 1** |
| **Claude Code** | `-p --allowedTools "Bash" --dangerously-skip-permissions` | works out of the box; benign ~3s stdin warning — pre-empt with `</dev/null` |

> These trust-gate inputs are tracked as **DOGFOOD-HARNESS** in
> [`followups.md`](./followups.md); they come from the real 3-harness
> dogfood, not guesswork.

## 3. Setup

`rufio init [name]` scaffolds **into the current directory** (the `[name]`
arg names the project in `rufio.gdl`; it does **not** create a subdir — so
make and enter the directory first):

```bash
mkdir rufio-live-demo && cd rufio-live-demo
rufio init demo
```

This writes `rufio.gdl`, the `given/` `learned/` `live/` tree, `.rufio/`,
and a `RUFIO.md` substrate primer at the project root. It **also**
idempotently folds that same primer (wrapped in `<!-- rufio:begin -->` …
`<!-- rufio:end -->` markers) into `CLAUDE.md` / `.cursorrules` /
`AGENTS.md` **if they already exist** — so every harness self-onboards from
its own context file with zero extra prompting. (It never *creates* those
files; create empty `CLAUDE.md` / `AGENTS.md` and re-run `rufio init demo`
if you want the primer folded in for harnesses that read them.)

Start the daemon (foreground engines: routing, retract, **auto-promote**,
TTL sweep, goal-overlap). Background it for the demo:

```bash
rufio dev --quiet &
sleep 2          # let the watchers attach + pidfile land
```

`--quiet` silences the startup banner, the `watching …` line, **and** the
per-event watch log — so the daemon emits nothing to any terminal and can
never corrupt the TUI watch pane, even when it shares a shell with it. If
you want to watch daemon activity, add `--log <file>`
(`rufio dev --quiet --log /tmp/rufio-dev.log &`): the per-event log is
appended to that file, never the terminal, regardless of `--quiet` —
`tail -f /tmp/rufio-dev.log` in a throwaway shell to follow it.

Seed the scenario. `customer:5821` is a churn-investigation entity; nothing
needs pre-seeding in `given/` — the arc creates everything it needs. (If you
want a human-authored starter fact, that is a separate scoped choice;
`rufio init` ships `given/` empty by design — see DOGFOOD-3 in
[`followups.md`](./followups.md).)

## 3.5. The watch pane (operator's live vantage)

Before the harnesses act, open the watch pane — this is how you *see* the
three vendors coordinate. In a **separate shell/pane**, `cd` to the SAME
project dir and run `rufio tui`. There is no env var and no flag — the v8
substrate view is the unconditional default; `rufio tui` from the project
dir is the whole invocation:

```bash
# Separate pane. Same dir as the demo (rufio finds the project root).
cd rufio-live-demo
rufio tui            # the default v8 substrate view — no env var, no flag
```

Run `rufio tui` in its own shell if you like, but you no longer have to:
`rufio dev --quiet` (section 3) emits nothing to any terminal, so even
reusing the daemon's shell for the TUI leaves the pane clean — `--quiet`
silencing the watch log, not pane discipline or a redirect, is what
guarantees an uncorrupted watch.

Leave it running while you fire the launcher (section 5). With the daemon
up (section 3), the pane live-updates as each harness drives `rufio` — no
orchestrator, no refresh. What you watch, in real time:

- **The substrate feed** (left panel, `◆ #substrate`) — every harness's
  `think`/`recall`/`observe`/`confirm` lands as a row attributed to its
  author (`◆ HYPOTHESIS claude-code · customer:5821 …`), each in that
  harness's own colour, newest last, with a `▸` marker on the selected row.
- **The mesh rail** (right rail, `◆ MESH`) — the three harness nodes
  (`● claude-code`, `● gemini-cli`, `● cursor-cli`) around the central
  `◉ operator` hub, with delivery edges drawn as routing fires; the
  `N nodes · N links` count tracks the live attention + routing on disk.
- **The quorum** — the `quorum X/Y` figure on the rail's `ROUTING` strip
  and the inline `● / ○` dots + `n/total` on the hypothesis row advance
  with each distinct confirm, then tip to the daemon's auto-promote — the
  collective decision crossing in the open, with no human in the loop.
- **Header/footer** — the `⠋ syncing` spinner + tab strip up top
  (`substrate · fleet · channels · goals · memory`), the keybind row below
  (`?` help, `q`/`ctrl+c` quit; `1`-`5` switch tabs).

The TUI is the **live watch**; it does not replace the on-disk assertions
in [section 7](#7-verifying-success) — those files are still the *proof*.
Watch the coordination happen here, then verify it landed there.

## 4. The arc

Investigative → quorum → auto-promote. Identity is per-shell via the
`RUFIO_AGENT_ID` environment variable (the cognitive verbs have **no
`--as` flag** — only `rufio identity --as` persists an id; each harness
exports its own id once at session start, exactly as `RUFIO.md` instructs).

The verified sequence (real ids elided as `<TID>`):

```bash
# --- Lead (claude-code): attend, then post the hypothesis ---
RUFIO_AGENT_ID=claude-code rufio attend \
  --intent="investigate customer:5821 churn risk" --entities=customer:5821

# Capture the new thought-id from think --json (top-level "id"):
TID=$(RUFIO_AGENT_ID=claude-code rufio think \
  --type=hypothesis --subject=customer:5821 \
  --content="customer:5821 is showing churn signals — 14-day silence + downgrade language" \
  --scope=fleet --json | jq -r .id)

# --- Peer B (gemini-cli): recall the hypothesis, gather evidence, confirm ---
# recall surfaces the id directly. Plain output prints a labelled id=<id>
# field; --json exposes a top-level "id". Either is the id confirm consumes:
RUFIO_AGENT_ID=gemini-cli rufio recall "customer:5821" --types=thought
#  → 2026-… thought claude-code id=<TID> customer:5821 fleet "…churn signals…"
BID=$(RUFIO_AGENT_ID=gemini-cli rufio recall "customer:5821" --types=thought --json | jq -r .id)

RUFIO_AGENT_ID=gemini-cli rufio observe --subject=customer:5821 \
  --predicate=support-tickets \
  --object="3 escalations in 14d, last mentions 'evaluating alternatives'" \
  --scope=fleet --confidence=0.9

RUFIO_AGENT_ID=gemini-cli rufio confirm "$BID" \
  --evidence="support ticket trail corroborates: escalations + competitor mention"

# --- Peer C (cursor-cli): recall the same thought, confirm independently ---
CID=$(RUFIO_AGENT_ID=cursor-cli rufio recall "customer:5821" --types=thought --json | jq -r .id)
RUFIO_AGENT_ID=cursor-cli rufio confirm "$CID" \
  --evidence="billing shows seat count 12->3 over 30d; contraction confirmed"

# --- 3rd distinct confirm reaches quorum ---
# Two distinct confirmers (gemini-cli, cursor-cli) is NOT quorum yet — the
# lead's think does NOT count as a vote for itself. The author MAY be the
# 3rd by separately running confirm on their own thought-id:
RUFIO_AGENT_ID=claude-code rufio confirm "$TID" \
  --evidence="own follow-up: customer opened a downgrade request ticket"
```

**The quorum rule (engine-true — this is the most-misread part).** A
`think --type=hypothesis` is one agent's guess until peers verify it. It
auto-promotes to a durable observation **only** when:

- **≥3 distinct agents** each ran `rufio confirm <thought-id>` on it
  (counted by `RUFIO_AGENT_ID`, **deduplicated** — one vote per id), **and**
- confidence **≥0.85**, where `confidence = confirms / (confirms + refutes)`
  (a `rufio refute` drags it down).

Only `rufio confirm` records count. **Emitting the `rufio think` is not a
confirm** — the hypothesis is the thing *being* verified, not a vote for
itself. So "lead `think`s + 2 peers `confirm`" = 2 confirms = **NOT
quorum**. The author *may* be one of the 3 by separately running
`rufio confirm` on their own thought-id (as above), but their `think`
alone never advances the count. These thresholds are fixed in v1
(configurability is a v1.1 followup). This matches the shipped `RUFIO.md`
primer and [`primitives.md`](./primitives.md) verbatim — by construction:
the primer's numbers are interpolated from the auto-promotion engine.

When quorum is reached the `rufio dev` daemon auto-promotes the thought to
a durable observation under `learned/<subject>/…gdlm` with
`author:auto-promote`. **On-disk success check actually run (verbatim):**

```bash
$ find learned -type f
learned/customer/5821/<obs-id>.gdlm        # the gemini-cli rufio observe
learned/customer/5821/<promoted-id>.gdlm   # ← appeared right after the 3rd confirm

$ cat learned/customer/5821/<promoted-id>.gdlm
@observation|id:<promoted-id>|author:auto-promote|subject:customer\:5821|predicate:asserted|object:customer\:5821 is showing churn signals — 14-day silence + downgrade language|scope:fleet|confidence:1|ts:2026-…

$ cat live/confirms/<TID>.gdl
@confirm|target:<TID>|by:gemini-cli|evidence:…|ts:…
@confirm|target:<TID>|by:cursor-cli|evidence:…|ts:…
@confirm|target:<TID>|by:claude-code|evidence:…|ts:…
```

Three distinct `by:` ids → `confidence = 3/(3+0) = 1` ≥ 0.85 → quorum →
`author:auto-promote`. Sub-second from the 3rd confirm to the file landing.

## 5. The launcher

Each harness is launched headless, pointed at the project dir, given a role
and its own `RUFIO_AGENT_ID`. The harness reads `RUFIO.md` (or its folded
`CLAUDE.md`/`AGENTS.md` block) to learn the protocol, then drives `rufio`.

**Verified headless invocations** (substitute the per-harness role prompt):

```bash
# Claude Code — works out of the box; </dev/null pre-empts the stdin warning
RUFIO_AGENT_ID=claude-code claude -p \
  --allowedTools "Bash" --dangerously-skip-permissions \
  "You are agent 'claude-code' on a Rufio substrate in $(pwd). Read RUFIO.md.
   You LEAD: attend customer:5821, then post a churn hypothesis with
   rufio think --type=hypothesis. Report the thought-id." </dev/null

# Gemini CLI — REQUIRES --yolo --skip-trust (else exit 55)
RUFIO_AGENT_ID=gemini-cli gemini --yolo --skip-trust \
  -p "You are agent 'gemini-cli' on a Rufio substrate in $(pwd). Read RUFIO.md.
      recall customer:5821, observe corroborating evidence, then
      rufio confirm the lead's thought-id."

# Cursor CLI — REQUIRES --force (else exit 1)
RUFIO_AGENT_ID=cursor-cli cursor-agent --force \
  -p "You are agent 'cursor-cli' on a Rufio substrate in $(pwd). Read RUFIO.md.
      recall customer:5821, independently rufio confirm the lead's thought-id."
```

> The `-p`/prompt flag spelling is each vendor's own (consult that CLI's
> `--help`); the **Rufio-relevant** flags above — `--yolo --skip-trust`,
> `--force`, `-p --allowedTools "Bash" --dangerously-skip-permissions
> </dev/null` — are the verified trust-gate inputs from the dogfood.

A minimal launcher tying them together (inline — Rufio ships no `scripts/`
dir; do **not** invent one):

```bash
#!/usr/bin/env bash
# live-agent-demo launcher — run from inside the rufio project dir.
set -euo pipefail
PROJ="$(pwd)"

rufio dev --quiet & DEV=$!   # add --log /tmp/rufio-dev.log to follow daemon activity
sleep 2
trap 'kill "$DEV" 2>/dev/null || true' EXIT

# 1. Lead posts the hypothesis (blocking — we need its thought-id downstream).
RUFIO_AGENT_ID=claude-code claude -p \
  --allowedTools "Bash" --dangerously-skip-permissions \
  "Agent 'claude-code' on the Rufio substrate at $PROJ. Read RUFIO.md.
   attend customer:5821, then rufio think --type=hypothesis on its churn
   risk (--scope=fleet). Print the thought-id." </dev/null

# 2+3. Peers recall + confirm independently (parallel — no orchestrator).
RUFIO_AGENT_ID=gemini-cli gemini --yolo --skip-trust \
  -p "Agent 'gemini-cli' at $PROJ. Read RUFIO.md. recall customer:5821,
      rufio observe evidence, rufio confirm the lead's thought-id." &
RUFIO_AGENT_ID=cursor-cli cursor-agent --force \
  -p "Agent 'cursor-cli' at $PROJ. Read RUFIO.md. recall customer:5821,
      independently rufio confirm the lead's thought-id." &
wait

# 4. Third distinct confirm to cross quorum (lead self-confirms its own id).
RUFIO_AGENT_ID=claude-code claude -p \
  --allowedTools "Bash" --dangerously-skip-permissions \
  "Agent 'claude-code' at $PROJ. recall your customer:5821 hypothesis and
   rufio confirm its thought-id with a follow-up evidence note." </dev/null

sleep 2   # let the daemon's AutoPromoteHandler fire
echo "--- promoted records ---"
find learned -type f
```

## 6. Running it

1. `mkdir rufio-live-demo && cd rufio-live-demo && rufio init demo`.
2. (Optional) `touch CLAUDE.md AGENTS.md && rufio init demo` to fold the
   primer into harness context files.
3. Each operator interactively logs in to their CLI once (no API keys).
4. `rufio dev --quiet &` ; `sleep 2`. (Add `--log /tmp/rufio-dev.log` if
   you want to `tail -f` daemon activity — never needed for the demo.)
5. Open the watch pane (section 3.5): a separate shell, `cd` to the
   project dir, `rufio tui`. Leave it running — it is the live vantage.
6. Run the launcher (section 5), or fire the three headless invocations by
   hand: lead → 2 peers (parallel) → 3rd distinct confirm.
7. Verify (section 7).
8. Capture any friction (section 8).
9. Teardown: `kill %1` (the daemon) or let the launcher's `trap` clean up.

## 7. Verifying success

The promotion is a file on disk — assert it directly:

```bash
# 1. A promoted observation exists under learned/<subject>/.
find learned -type f -name '*.gdlm'
#  → learned/customer/5821/<promoted-id>.gdlm  (plus the explicit `observe`)

# 2. It was written by the engine, not a human.
grep -l 'author:auto-promote' learned/customer/5821/*.gdlm
#    that record also carries confidence:1 (3 confirms, 0 refutes) and the
#    GDL-escaped subject `subject:customer\:5821`.

# 3. The confirm ledger shows ≥3 DISTINCT by: ids.
cat live/confirms/<TID>.gdl
#  → three @confirm lines, by:gemini-cli / by:cursor-cli / by:claude-code

# 4. Plain recall now surfaces the promoted fact (author auto-promote).
rufio recall "customer:5821" | grep auto-promote
```

> Use **plain `rufio recall "customer:5821"`**, *not*
> `rufio recall --types=learned`. On the current binary the explicit
> `learned` type filter does **not** surface auto-promoted records
> (tracked as **DOGFOOD-1** in [`followups.md`](./followups.md)); the
> default recall path and the on-disk `learned/` check are the
> authoritative success signals.

## 8. Friction capture (demo-as-test)

This recipe is also a **dogfood test**: real third-party harnesses driving
the real CLI surface every CLI/communication/primer gap an agent actually
hits. That discipline already produced the **DOGFOOD-1..7** +
**DOGFOOD-HARNESS** series in [`followups.md`](./followups.md) (e.g.
DOGFOOD-7: `think --json` id contract; DOGFOOD-HARNESS: the trust-gate
flags this recipe encodes).

When you run this:

- Have each harness **report any friction in-band** — a wrong/missing flag,
  a confusing error, a primer instruction that didn't match the binary, a
  recall that didn't surface what it claimed.
- Log each as a new `DOGFOOD-N` entry in
  [`docs/followups.md`](./followups.md): date, one-line summary, the exact
  command + observed vs. expected, and `Source: live-agent-demo run`.
- Classify it: **primer wording** (fix `internal/cli/primer.go`),
  **CLI ergonomics** (candidate roadmap), or **doc drift** (fix the doc).
  Don't expand demo scope to fix it inline — track it, keep the run going.

> **Known live drift to expect (already tracked, don't re-file):** the
> shipped `RUFIO.md`/`primer.go` "getting a thought-id" section tells
> agents that `recall --json` has *no* `id` key and to path-parse the
> filename. The current binary's `recall` **does** surface the id (plain
> `id=<id>`; `--json` top-level `"id"`), matching
> [`cli-reference.md`](./cli-reference.md) and
> [`primitives.md`](./primitives.md). The simpler `id=`/`jq -r .id`
> capture in section 4 is binary-true; the primer's path-parse fallback
> still works but is no longer necessary. This is post-DOGFOOD-7
> recall-CLI improvement the primer was not updated for — file under
> friction-capture if a harness trips on it, reference DOGFOOD-7.

## 9. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Gemini exits **55**, never ran `rufio` | untrusted-workspace gate | add `--yolo --skip-trust` (or `GEMINI_CLI_TRUST_WORKSPACE=true`) |
| Cursor exits **1**, never ran `rufio` | trust gate | add `--force` (or `--trust`) |
| Claude hangs ~3s with a stdin warning | reads stdin in `-p` mode | append `</dev/null` |
| No `learned/…gdlm`, no errors | **quorum not actually reached** — only 2 distinct confirms, OR the lead's `think` was miscounted as a confirm, OR a `refute` pulled confidence < 0.85 | need **≥3 distinct `RUFIO_AGENT_ID`s** each running `rufio confirm <thought-id>`; the `think` is not a vote; verify `cat live/confirms/<TID>.gdl` shows ≥3 distinct `by:` ids |
| Confirms written but no promotion | **daemon not running** — `rufio dev` does the promotion | start `rufio dev --quiet &`; check `.rufio/locks/dev.pid`; confirms made while it was down are caught up on next start |
| TUI screen garbled / timestamped `add`/`unlink` log lines bleeding over the view | a `rufio dev` started **without** `--quiet` is logging per-event to the TUI's terminal | start the daemon with `--quiet` (recipe default) — it now silences the per-event watch log too, not just the banner; if you need to watch daemon activity use `--log <file>` (events go to the file, never the terminal) |
| `confirm`/`refute` "thought not found" | wrong/empty id | re-`recall` and copy the `id=<id>` field (plain) or `jq -r .id` (`--json`); the id is the same one `think --json` returned |
| `rufio recall --types=learned` empty though `learned/` has files | **DOGFOOD-1** — explicit `learned` filter misses promoted records | use plain `rufio recall "<subject>"` + the on-disk `find learned/` check |
| `cd demo` fails after `rufio init demo` | `rufio init [name]` scaffolds into **cwd**, not a `name/` subdir | `mkdir <dir> && cd <dir> && rufio init <name>` (do not `cd <name>`) |

## See also

- [`demo.md`](./demo.md) — scripted 5-beat narrative.
- [`launch-script.md`](./launch-script.md) — asciinema recording script.
- [`primitives.md`](./primitives.md) — the 13-verb protocol reference.
- [`cli-reference.md`](./cli-reference.md) — every command + flag.
- [`followups.md`](./followups.md) — the DOGFOOD-* tracking series.
