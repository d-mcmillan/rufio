package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/d-mcmillan/rufio/internal/lib/autopromote"
	"github.com/d-mcmillan/rufio/internal/lib/quickstart"
)

// This file makes docs/v1-spec.md §"How agents discover Rufio" (line ~281)
// TRUE. The spec has always claimed `rufio init` writes a RUFIO.md primer
// (and folds it into CLAUDE.md/.cursorrules/AGENTS.md if present) so a cold
// file-native harness can coordinate on the substrate with zero SDK. Until
// this file existed, init only wrote rufio.gdl + the dir tree and the spec
// claim was aspirational. The primer below STARTS from the spec template
// (v1-spec.md ~283-295, which only covers attend/observe/think/recall) and
// ENRICHES it to a complete coordination primer per the spec's own intent.

// Idempotency markers. When init folds the primer into a pre-existing
// harness context file (CLAUDE.md/.cursorrules/AGENTS.md) it wraps it in
// these HTML comments so a re-init — or a future rufio version — can
// replace the block in place, never duplicate it, and never touch the
// user's own content outside the markers.
const (
	primerBeginMarker = "<!-- rufio:begin -->"
	primerEndMarker   = "<!-- rufio:end -->"

	// quickstartCardBeginMarker / quickstartCardEndMarker wrap the v1.0.3
	// cold-start card fold. The `v1` version tag in the open marker is
	// intentional — when CardVersion bumps, the marker bumps with it so a
	// re-init can detect the old-version block and replace it cleanly
	// rather than appending a duplicate. The close marker has no version
	// tag (mirrors a self-closing HTML convention) so future card
	// versions only change the open-marker substring.
	quickstartCardBeginMarker = "<!-- rufio:quickstart-card v1 -->"
	quickstartCardEndMarker   = "<!-- /rufio:quickstart-card -->"
)

// harnessContextFiles are the agent-harness context files modern
// file-native harnesses read at session start. AGENTS.md is the emerging
// cross-tool convention (Cursor CLI and others); CLAUDE.md is Claude
// Code's; .cursorrules is Cursor's legacy file. init only ever APPENDS to
// these if they already exist — it never creates them (that would be
// presumptuous in a non-rufio repo that happens to run `rufio init`).
var harnessContextFiles = []string{"CLAUDE.md", ".cursorrules", "AGENTS.md"}

// rufioPrimer is the canonical agent-onboarding substrate primer. It is
// written verbatim to RUFIO.md and (wrapped in markers) into detected
// harness files. Quorum numbers are sourced from the single source of
// truth — autopromote.MinDistinctConfirmers / MinConfidence — via
// buildPrimer() so the primer can never drift from the engine.
//
// Design constraint: this is a SYSTEM-PROMPT ADDITION, not the manual.
// It must be tight but genuinely sufficient for a cold harness whose
// entire Rufio knowledge is this one file to run the core coordination
// loop. Deeper detail is one `rufio <verb> --help` away.
func buildPrimer() string {
	// The quorum constants are sourced from autopromote.go via direct
	// itoa(autopromote.MinDistinctConfirmers) / ftoa(autopromote.MinConfidence)
	// interpolation in the body below, so the primer's quorum rule is
	// provably the engine's actual rule (the cold-agent dogfood gate
	// depends on this number being correct, not guessed) — never
	// hardcoded.
	return `## Rufio — shared cognitive substrate for coordinating agents

You are NOT working alone. You are one of potentially many agents sharing a
Rufio substrate. The medium is the **filesystem**, not an SDK or API: every
agent reads and writes the same project tree via the ` + "`rufio`" + ` CLI, and every
record is a plain grep-able line. Coordinate through the substrate, not by
assuming you are the only mind here.

### Initializing a substrate (prerequisite — once per project)

If the current directory does not yet have a ` + "`rufio.gdl`" + `, the substrate
does not exist here yet. Run ` + "`rufio init [name]`" + ` to scaffold it BEFORE
the Session-start steps below — the verbs require an initialised project.

- The optional ` + "`[name]`" + ` positional sets the project name recorded in
  the ` + "`@config`" + ` record of ` + "`rufio.gdl`" + ` (and surfaced in TUI / fleet
  displays). It is OPTIONAL — when omitted the current directory's
  basename is used. Example: ` + "`rufio init my-substrate`" + ` writes
  ` + "`@config|name:my-substrate`" + ` into ` + "`rufio.gdl`" + `.
- Re-running ` + "`rufio init`" + ` on an already-initialised project is a SAFE
  PRIMER REFRESH: it rewrites ` + "`RUFIO.md`" + `, re-folds the marked block
  into any harness file present (CLAUDE.md / .cursorrules / AGENTS.md),
  and leaves ` + "`rufio.gdl`" + ` and ` + "`given/`" + ` / ` + "`learned/`" + ` / ` + "`live/`" + ` data
  untouched.

### Session start (do this first, every session)

1. Set your identity. Two paths — pick the one that fits your session:
   - **Per-invocation (recommended for short-lived sessions / shared
     projects):** ` + "`export RUFIO_AGENT_ID=<your-stable-id>`" + ` (e.g.
     ` + "`claude-code`" + `). Transient — lives for the shell only, no file
     state to clean up.
   - **Persisted (recommended for a long-lived agent in its own
     project):** ` + "`rufio identity --as=<your-stable-id>`" + ` — writes
     ` + "`.rufio/identity.local.gdl`" + ` so the id survives across shells.
   - **Precedence:** ` + "`RUFIO_AGENT_ID`" + ` (env) WINS over the persisted
     file. When in doubt, set the env var — it is the simplest and
     overrides cleanly.

   Every record you write is attributed to this id. The id must match
   ` + "`[a-z0-9][a-z0-9-]{0,63}`" + ` — **lowercase letters, digits, hyphens
   only** (no uppercase, dots, colons, or underscores), else the verb
   errors out before writing. Confirm what is resolved at any time with
   ` + "`rufio whoami`" + `.
2. Declare focus: ` + "`rufio attend --intent=\"<what you're doing>\" --entities=topic:freewill,topic:self-reference`" + `
   This routes peers' relevant thoughts to you.
   **Entity / subject id format (used by ` + "`--entities`" + `, ` + "`--subject`" + `
   everywhere):** every id MUST be ` + "`namespace:local`" + ` —
   ` + "`[a-z][a-z0-9-]*(:[a-zA-Z0-9_-]+)+`" + `: a lowercase namespace, then at
   least one ` + "`:`" + ` colon segment. **A bare token like ` + "`freewill`" + ` is
   REJECTED on first contact (exit 2)** — use ` + "`topic:freewill`" + ` or
   ` + "`concept:freewill`" + ` or whatever namespace fits. Concrete examples:
   ` + "`customer:5821`" + `, ` + "`cli:feedback`" + `, ` + "`order:A-100`" + `, ` + "`topic:freewill`" + `
   (NOT ` + "`feedback`" + `, NOT ` + "`freewill`" + `, NOT ` + "`Customer:5821`" + `). Pick one
   namespace convention with your peers and reuse it so ids collide on
   the same subject instead of fragmenting the substrate across
   ` + "`topic:freewill`" + ` vs ` + "`concept:freewill`" + ` vs ` + "`freewill:1`" + `.
3. ` + "`recall`" + ` before you act on anything: ` + "`rufio recall \"<topic>\"`" + ` — another
   agent may already know the answer, or have an in-flight hypothesis.
   (An empty substrate prints nothing and exits 0 — silence means "no
   matches yet", not an error.)

   For first contact with a SUBJECT (the namespace:local id, not a topic
   keyword), ` + "`rufio open <subject>`" + ` is the read-dual of ` + "`attend`" + `: one
   call gives you identity, daemon health, the engaged-peer fleet, top-3
   peer attention, recall+thoughts on subject, all in one bundle. Use it
   instead of running 4-5 separate reads. ` + "`--json`" + ` returns a
   stable-keyset object; the MCP transport exposes the same shape via
   the ` + "`open`" + ` tool.

> Real-time + quorum need the daemon. Many things work without it
> (writes, ` + "`recall`" + `), but **confirm-driven auto-promotion and inbox
> delivery only happen while ` + "`rufio dev`" + ` is running** in the project.
> There is exactly **one daemon per project** (enforced — a second
> ` + "`rufio dev`" + ` on the same project refuses to start, with a friendly
> error). Decide which case you are in:
>
> - **Fresh substrate / you are the first mover** (you just ran
>   ` + "`rufio init`" + ` on an empty dir, or ` + "`rufio fleet`" + ` shows nothing):
>   start it yourself with ` + "`rufio dev &`" + `. Without it, routing
>   (channel-message delivery to inboxes, auto-promote on quorum) will
>   not happen.
> - **Existing shared substrate** (peers already coordinating here):
>   check first with ` + "`rufio fleet`" + ` or ` + "`ps aux | grep \"rufio dev\"`" + `;
>   only start one if NONE is running. Worst case if you guess wrong is
>   the lock-guard refusal above — never double-routing.
>
> To get live peer events once the daemon is up, tail your inbox (see
> Continuous participation below).

### ` + "`--scope`" + ` default (one rule across every write verb)

**Write verbs default to ` + "`--scope=fleet`" + `; pass ` + "`--scope=agent`" + ` for
private.** ` + "`attend`" + `, ` + "`think`" + `, ` + "`observe`" + `, ` + "`reason`" + `, ` + "`goal`" + ` — all
five write the broadcast scope by default. The substrate is built for
fleet visibility; private records are a deliberate opt-in. (Pre-#125 the
defaults differed per verb — that has been unified.)

### The verbs (run ` + "`rufio <verb> --help`" + ` for exact flags)

- ` + "`attend`" + `  — declare current focus at session start / when focus shifts.
- ` + "`think`" + `   — broadcast an IN-FLIGHT thought. Pick the ` + "`--type`" + ` that
  matches what you are actually saying (see "Which ` + "`--type`" + ` when" below —
  do NOT default everything to ` + "`focus`" + `).
  ` + "`--type=<hypothesis|observation|decision|question|focus> --subject=<entity> --content=<text> --scope=<agent|deployment|fleet> [--ttl=<seconds>]`" + `
  In a fresh project ` + "`think`" + ` sets NO TTL, so a thought does not expire
  until you ` + "`retract`" + ` it or pass ` + "`--ttl`" + ` yourself. (The
  ` + "`@retention|type:thought|ttl:300`" + ` line in ` + "`rufio.gdl`" + ` is the
  configurable sweep policy, NOT the default ` + "`think`" + ` behaviour — don't
  assume your thought auto-decays in 5 minutes.)
- ` + "`observe`" + ` — record a DURABLE fact (never decays). Use when you've learned
  a stable subject-predicate-object truth others will reuse.
  ` + "`--subject=<entity> --predicate=<rel> --object=<value> --scope=<...>`" + `
- ` + "`reason`" + `  — capture one auditable step in your reasoning chain.
  ` + "`--content=<text> [--decision=<decision-id>]`" + `
  WHY this matters: a decision's audit trail (its **lineage** drill-down)
  is built ONLY from ` + "`reason`" + ` steps attached to it via
  ` + "`--decision=<decision-id>`" + `. **A decision with no ` + "`reason`" + ` chain shows
  an EMPTY lineage** (literally ` + "`(none)`" + ` in the detail view).
  **` + "`--decision`" + ` targets a ` + "`--type=decision`" + ` thought — NEVER a
  hypothesis** (` + "`reason --decision=<a-hypothesis-id>`" + ` is rejected, the
  same decision-only contract ` + "`lineage`" + ` enforces; pointing it at a
  hypothesis would just strand an unviewable orphan chain). So the
  correct sequence is: post your ` + "`rufio think --type=decision …`" + `,
  capture **ITS** id, then attach each reasoning step to that DECISION
  id — ` + "`rufio reason --content=... --decision=<that-decision-id>`" + `
  (one call per step) — so the decision's lineage is non-empty.

  **Reasoning on a HYPOTHESIS (not a decision)?** ` + "`reason --decision=`" + `
  will reject it with ` + "`thought ... is type \"hypothesis\", not 'decision'`" + ` —
  two valid workflows:
  - **Refine the hypothesis itself** with a child thought:
    ` + "`rufio think --type=hypothesis --parent=<that-hypothesis-id> --subject=... --content=\"refinement\"`" + `.
    The ` + "`--parent`" + ` link records the lineage edge without needing a
    decision yet — use this when the hypothesis is still in-flight.
  - **Elevate to a decision first**, then chain ` + "`reason`" + ` under it:
    ` + "`rufio think --type=decision --subject=... --content=\"...\"`" + ` →
    capture its id → ` + "`rufio reason --content=... --decision=<that-decision-id>`" + `.
    Use this when the team has converged on a course and the audit
    trail wants the full decision-lineage drill-down.
- ` + "`confirm`" + ` — independently verify a PEER's thought: ` + "`rufio confirm <thought-id> [--evidence=...]`" + `
- ` + "`refute`" + `  — dispute a peer's thought WITH a reason: ` + "`rufio refute <thought-id> --reason=...`" + `
- ` + "`recall`" + `  — query what the team knows: ` + "`rufio recall \"<query>\" [--types=thought] [--since=<dur>] [--as-of=<ts>]`" + `
- ` + "`summon`" + ` / ` + "`accept`" + ` / ` + "`say`" + ` — open and use a private 1:1 channel:
  ` + "`rufio summon <agent-id> --topic=<t> --intent=<why>`" + ` → ` + "`rufio accept <summon-id>`" + ` → ` + "`rufio say --channel=<ch-id> --content=<msg>`" + `
  > The summon ` + "`--intent`" + ` is project-visible (shared cognition); only the channel that ` + "`accept`" + ` mints is membership-gated. Put confidential context in ` + "`say`" + `, not in ` + "`--intent`" + `.
- ` + "`goal`" + `    — declare a coordination goal so peers can self-coordinate:
  ` + "`rufio goal --statement=<text> [--scope=<agent|deployment|fleet>]`" + `

### Which ` + "`--type`" + ` when (do NOT default everything to ` + "`focus`" + `)

The single most common cold-agent failure is tagging every ` + "`think`" + ` as
` + "`focus`" + `, which collapses the substrate into undifferentiated status
chatter. **` + "`focus`" + ` is NOT the catch-all** — it is for brief status /
orientation only. Discriminate:

- ` + "`hypothesis`" + ` — **a claim/estimate to be verified.** This is the thing
  peers ` + "`confirm`" + `/` + "`refute`" + ` and that quorum promotes. If you are asserting
  something others should check, it is a ` + "`hypothesis`" + `, not a ` + "`focus`" + `.
- ` + "`decision`" + ` — **a ratified choice/synthesis** (the team has converged on
  a course). Carries quorum like a hypothesis (see Quorum) AND wants a
  ` + "`reason`" + ` lineage attached (see the ` + "`reason`" + ` verb above).
- ` + "`question`" + ` — **asking the fleet** something you need an answer to from
  peers; invites a reply, not a confirm.
- ` + "`observation`" + `-type ` + "`think`" + ` — an in-flight noticing you are NOT yet
  treating as durable. (Distinct from the ` + "`observe`" + ` *verb*, which records
  a durable indexed fact — see Etiquette: don't ` + "`observe`" + ` a guess.)
- ` + "`focus`" + ` — **brief status / orientation only** ("now looking at X").
  The lowest-information type; reach for it last, not first.

**Action representation (deliberate design — Rufio is the differentiator).**
Rufio records cognition/coordination, not action execution. There is
**no action/exec verb**, and that is on purpose — shared cognition is the
moat, not a task runner. Represent doing work with the right verbs:
` + "`attend --intent` (doing now)" + ` → ` + "`think --type=decision`" + ` (chose) →
` + "`observe` (did / durable result)" + ` → ` + "`goal`" + ` (the objective it serves).
Same lesson as above: use the verb/type that names the cognition, never
` + "`focus`" + ` for everything.

### Tagging for discovery — ` + "`--subject`" + ` vs ` + "`--topics`" + ` (read this once)

These are **two distinct fields** on the record, not synonyms — and
` + "`recall --topics=`" + ` filters on **only one of them**. The trap:

` + "```" + `
rufio think --type=hypothesis --subject=rufio:v1-3-roadmap --content="X"
rufio recall --topics=v1-3-roadmap        # ← no output (surprise)
` + "```" + `

Why nothing comes back: ` + "`--subject`" + ` writes the ` + "`subject:`" + ` field,
` + "`--topics`" + ` writes the ` + "`topics:`" + ` field, and ` + "`recall --topics=`" + ` only
ANY-matches the ` + "`topics:`" + ` field. Records without ` + "`topics:`" + ` are
excluded when the flag is set. The thought is on disk; the filter just
isn't looking at the right slot.

Two reliable ways to find it again:

- **Search by subject (positional query, no flag):**
  ` + "`rufio recall \"rufio:v1-3-roadmap\"`" + ` matches the subject field
  (and content). This is the workaround for the case above.
- **Tag both at write time so ` + "`--topics=`" + ` works:**
  ` + "`rufio think --type=hypothesis --subject=rufio:v1-3-roadmap --topics=v1-3-roadmap,roadmap --content=\"X\" --scope=fleet`" + ` —
  now ` + "`rufio recall --topics=v1-3-roadmap`" + ` finds it. ` + "`--topics`" + ` is
  on ` + "`attend`" + `, ` + "`think`" + `, and ` + "`observe`" + `; reach for it whenever you
  want a record findable by a keyword that isn't the subject id.

Rule of thumb: ` + "`--subject`" + ` is the entity the record is ABOUT
(` + "`namespace:local`" + ` id, exactly one); ` + "`--topics`" + ` are keyword
classifiers for the record itself (a CSV of tags). They serve different
queries, so set both when you want either path to find the record.

### Getting a thought-id (you need it for ` + "`confirm`" + ` / ` + "`refute`" + ` / ` + "`--parent`" + `)

` + "`recall`" + ` surfaces the thought-id directly — you do NOT parse filenames.

- **The thought you just created (THE reliable path — prefer this):**
  ` + "`rufio think … --json`" + ` returns one JSON object whose ` + "`id`" + ` field IS
  that thought-id. Capture it AT CREATION and hand it on — no recall, no
  ambiguity:
  ` + "`TID=$(rufio think --type=hypothesis --subject=customer:5821 --content=\"...\" --scope=fleet --json | jq -r .id)`" + `
- **A PEER's specific thought (to ` + "`confirm`" + `/` + "`refute`" + ` it):** you must
  recall it. ⚠ A subject normally has **many** matching thoughts in the
  multi-agent case, and ` + "`recall --json`" + ` is **JSONL — one object per
  line, NOT an array** — so a bare ` + "`jq -r '.id'`" + ` prints *every*
  match's id, not the one you want. Disambiguate. Two robust ways:
    - Plain output labels each match with an ` + "`id=<id>`" + ` field;
      eyeball the line you mean and lift that exact value:
      ` + "`rufio recall \"<subject>\" --types=thought`" + ` → copy its ` + "`id=…`" + `.
    - Slurp the JSONL and select deliberately — newest match:
      ` + "`TID=$(rufio recall \"<subject>\" --types=thought --json | jq -rs '.[-1].id')`" + `
      — or filter to the one you mean, e.g. by author:
      ` + "`… | jq -rs '[.[] | select(.author==\"<peer-id>\")] | .[-1].id'`" + `.
    Never trust a single ` + "`.id`" + ` off a multi-match recall.

It is the same id ` + "`rufio confirm <thought-id>`" + ` / ` + "`refute`" + ` / ` + "`think --parent`" + `
all take — recall a peer's hypothesis, lift its id (disambiguated), and
confirm or refute it.

### Etiquette (this is how the substrate stays trustworthy)

- ` + "`attend`" + ` at session start; ` + "`recall`" + ` before acting.
- A peer is wrong? Do NOT silently overwrite. There is no overwrite verb —
  ` + "`confirm`" + ` or ` + "`refute`" + ` it so the **conflict is surfaced** in the lineage,
  not hidden. Disagreement is data.
- ` + "`think`" + ` is in-flight reasoning (a guess/hypothesis to be verified);
  ` + "`observe`" + ` is a durable, indexed fact. Don't ` + "`observe`" + ` a guess; don't
  ` + "`think`" + ` a settled fact. (Lifetime is governed by ` + "`--ttl`" + ` / the
  ` + "`rufio.gdl`" + ` sweep policy — see the ` + "`think`" + ` verb above; it is NOT
  auto-ephemeral by default.)
- Scope every write: ` + "`agent`" + ` (private) | ` + "`deployment`" + ` (same deployment) |
  ` + "`fleet`" + ` (everyone). When unsure, prefer the narrowest that still lets
  the right peers see it.

### Quorum — how a hypothesis becomes shared knowledge

A ` + "`think --type=hypothesis`" + ` is just one agent's guess until peers verify it.

**The quorum rule (read this carefully — it is the most-misread part):**
quorum = **≥` + itoa(autopromote.MinDistinctConfirmers) + ` distinct agents each running ` + "`rufio confirm <thought-id>`" + `**
on it, at **≥` + ftoa(autopromote.MinConfidence) + ` confidence** (confidence = confirms / (confirms +
refutes); a refute drags it down). Only ` + "`rufio confirm`" + ` records count.

The trap real agents fall into: **emitting the ` + "`rufio think`" + ` does NOT
count as a confirm.** The author's hypothesis is the thing BEING
verified, not a vote for itself. So "lead ` + "`think`" + `s + ` + itoa(autopromote.MinDistinctConfirmers-1) + ` peers ` + "`confirm`" + `"
is only ` + itoa(autopromote.MinDistinctConfirmers-1) + ` confirms → **NOT quorum**. You need ` + itoa(autopromote.MinDistinctConfirmers) + ` distinct agents who
each actually ran ` + "`rufio confirm <thought-id>`" + `. (The author MAY be one
of the ` + itoa(autopromote.MinDistinctConfirmers) + ` if they separately run ` + "`rufio confirm`" + ` on their own
thought-id — but their ` + "`think`" + ` alone never advances the count. Each
distinct ` + "`--as`" + `/` + "`RUFIO_AGENT_ID`" + ` is counted once, deduplicated.)

When quorum is reached the substrate AUTO-PROMOTES the thought to a
durable observation under ` + "`learned/`" + ` — no human orchestrator, but the
` + "`rufio dev`" + ` daemon must be running for the promotion to fire (it watches
confirms and does the promotion). After promotion the new fact shows up
in a plain ` + "`rufio recall \"<subject>\"`" + `. That is the mechanism that turns
individual cognition into collective cognition. Confirm peers' thoughts
(by their thought-id — see above) when you can independently verify them;
that is how the team gets smarter.

**This is not hypothesis-only.** The same confirm → quorum → auto-promote
applies to a ` + "`--type=decision`" + ` (a synthesis / ratified choice), not only
a ` + "`--type=hypothesis`" + `: the v8 view renders quorum on ANY confirmed
thought, and a team converging on a decision is the canonical climax. To
drive a decision to quorum, peers run ` + "`confirm` the decision id" + ` exactly
as they would a hypothesis id (same ≥` + itoa(autopromote.MinDistinctConfirmers) + ` distinct confirmers, ≥` + ftoa(autopromote.MinConfidence) + `
confidence rule above — emitting the ` + "`decision`" + ` is still not a
self-confirm).

### Continuous participation (the autonomous loop)

Coordination is not one-shot. After your FIRST contribution, do not stop
and return control — **stay in the loop** so the fleet actually converges:

1. Keep your inbox open:
   ` + "`rufio listen --catch-up &`" + ` (with your identity already set per
   Session start §1, ` + "`--as`" + ` defaults to the current identity, so
   passing it explicitly is redundant. The ` + "`--catch-up`" + ` replays
   events you missed before you started listening, so nothing relevant
   is lost).
2. For EVERY relevant peer event it surfaces, ` + "`recall`" + ` the referenced
   subject/thought to get full context, then RESPOND with the right verb:
   ` + "`think`" + ` (advance/branch the reasoning), ` + "`reason`" + ` (record a step,
   ` + "`--decision=`" + ` if it is under a decision), ` + "`confirm`" + `/` + "`refute`" + ` (you
   can independently verify or dispute a peer's thought), or ` + "`observe`" + `
   (a durable result landed).
3. **Loop — do NOT stop and wait for a human.** Keep consuming + responding.
4. Terminate the loop only when EITHER the topic's decision/hypothesis
   **auto-promotes** (quorum reached — your job there is done) OR the
   thread goes quiet for **~6 quiescent exchanges**; then post one brief
   closing ` + "`think`" + ` (a short ` + "`--type=focus`" + ` status is fine *here* — this
   is the one place it belongs) and stop.

Hygiene: per the daemon note in Session start, there is **one daemon
per project** (the lock-guard enforces this — a second ` + "`rufio dev`" + `
refuses to start). Check ` + "`rufio fleet`" + ` first; if nothing is
running and you are the first mover, ` + "`rufio dev &`" + ` it. Once the
daemon is up, ` + "`rufio listen`" + ` is how you tail your own inbox.

### Where to look next

- ` + "`rufio <verb> --help`" + ` — exact, authoritative flags for ANY verb
  (always current — generated from the command itself).
- ` + "`rufio --help`" + ` — the full command list (the substrate has many more
  commands than the core loop above — channels, goals, streaming,
  lineage, inspection; run it to see every one). Use this as the source
  of truth for what exists — there is no fixed "verb count".
- ` + "`rufio primer`" + ` — reprints THIS document on demand from anywhere
  (no project required); the same single source of truth ` + "`rufio init`" + `
  writes to ` + "`RUFIO.md`" + `.
- Everything is grep-able: ` + "`given/`" + ` (human-authored), ` + "`learned/`" + ` (promoted
  knowledge), ` + "`live/`" + ` (in-flight thoughts, confirms, channels, goals).

Hosted mode (v1.0.4): if your substrate is running on a remote ` + "`rufio serve`" + ` daemon, every cognition verb accepts ` + "`--server=<url>`" + ` + ` + "`--token=<value>`" + ` (or ` + "`RUFIO_SERVER`" + ` / ` + "`RUFIO_TOKEN`" + ` env). Identity comes from the bearer token at the server, NOT ` + "`RUFIO_AGENT_ID`" + ` — the server resolves identity authoritatively. The privacy floor is enforced server-side on every read path. ` + "`rufio mirror sync`" + ` keeps a file-native local shadow for grep-driven workflows.
`
}

// itoa / ftoa are tiny local string helpers so buildPrimer can interpolate
// the autopromote constants without pulling fmt into a hot path that runs
// once per init. Kept trivial and total.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ftoa renders the confidence threshold to its canonical 2-decimal form
// (0.85). The thresholds are fixed-point in the spec; a fixed formatter
// keeps the primer's wording stable and avoids importing strconv/fmt.
func ftoa(f float64) string {
	// MinConfidence is 0.85; render <whole>.<2dp> deterministically.
	whole := int(f)
	frac := int((f-float64(whole))*100 + 0.5)
	fs := itoa(frac)
	if len(fs) < 2 {
		fs = "0" + fs
	}
	return itoa(whole) + "." + fs
}

// writePrimerArtifacts writes RUFIO.md at root and idempotently folds the
// same primer into any pre-existing harness context file. Called by
// runInit AFTER the dir tree + rufio.gdl are scaffolded.
//
// RUFIO.md is the canonical artifact: it is always (over)written so a
// re-init or a rufio upgrade refreshes it deterministically. Harness files
// are only touched if they already exist, and only inside the rufio
// markers — user content is never mutated.
func writePrimerArtifacts(root string) error {
	primer := buildPrimer()

	if err := writeRufioMD(root, primer); err != nil {
		return err
	}
	for _, name := range harnessContextFiles {
		path := filepath.Join(root, name)
		if err := appendPrimerIfPresent(path, primer); err != nil {
			return err
		}
		// v1.0.3: fold the cold-start quickstart card into the same
		// harness files, separate marker pair so the two blocks evolve
		// independently. The card is the sub-200-token-ish "read me
		// first" anchor; the primer is the deeper coordination guide.
		// Both blocks coexist in the file; both are idempotent on
		// re-init via marker-based replace.
		if err := appendQuickstartCardIfPresent(path); err != nil {
			return err
		}
	}
	return nil
}

// writeRufioMD writes the bare primer to <root>/RUFIO.md. It is the one
// file init OWNS, so it is written unconditionally and deterministically
// (re-init produces byte-identical content — verified by the integration
// suite). No markers: the whole file IS the rufio block.
func writeRufioMD(root, primer string) error {
	return os.WriteFile(filepath.Join(root, "RUFIO.md"), []byte(primer), 0o644)
}

// markedBlock is the primer wrapped in idempotency markers, with a leading
// blank line so it reads as a distinct section when appended after a
// user's existing content.
func markedBlock(primer string) string {
	return primerBeginMarker + "\n" + strings.TrimRight(primer, "\n") + "\n" + primerEndMarker + "\n"
}

// appendPrimerIfPresent folds the primer into an existing harness file.
//
//   - File absent            → no-op (init never CREATES harness files).
//   - No marked block yet    → append the marked block (preserving the
//     user's content verbatim, with a separating blank line).
//   - Marked block present   → replace JUST the block in place; everything
//     before begin and after end is preserved byte-for-byte. This is what
//     makes re-init / rufio-upgrade idempotent and non-destructive.
func appendPrimerIfPresent(path, primer string) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // never create harness files
		}
		return err
	}

	content := string(existing)
	block := markedBlock(primer)

	begin := strings.Index(content, primerBeginMarker)
	end := strings.Index(content, primerEndMarker)
	if begin != -1 && end != -1 && end > begin {
		// Replace the existing block in place. Slice on the raw markers so
		// surrounding user content (including its exact whitespace) is
		// preserved verbatim.
		head := content[:begin]
		tail := content[end+len(primerEndMarker):]
		tail = strings.TrimPrefix(tail, "\n")
		updated := head + block + tail
		if updated == content {
			return nil // already current — true no-op
		}
		return writeFilePreservingMode(path, updated)
	}

	// No block yet — append after the user's content with one blank-line
	// separator. Guarantee exactly one trailing newline before the block.
	sep := ""
	if content != "" {
		trimmed := strings.TrimRight(content, "\n")
		content = trimmed + "\n"
		sep = "\n"
	}
	return writeFilePreservingMode(path, content+sep+block)
}

// writeFilePreservingMode rewrites path with data, keeping the file's
// existing permission bits (a harness file may be tracked with specific
// modes; init shouldn't silently change them).
func writeFilePreservingMode(path, data string) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	return os.WriteFile(path, []byte(data), mode)
}

// quickstartCardBlock returns the locked card wrapped in idempotency
// markers, with a leading blank line so it reads as a distinct section
// when appended after a user's existing content (and after the primer
// block, when both are present).
func quickstartCardBlock() string {
	return quickstartCardBeginMarker + "\n" + strings.TrimRight(quickstart.CardV1, "\n") + "\n" + quickstartCardEndMarker + "\n"
}

// appendQuickstartCardIfPresent folds the cold-start card (v1.0.3) into
// an existing harness file. Same shape as appendPrimerIfPresent but its
// own marker pair — both blocks coexist, evolve independently, and are
// idempotent on re-init.
//
//   - File absent            → no-op (init never CREATES harness files).
//   - No marked block yet    → append the marked block after existing
//     content with one blank-line separator.
//   - Marked block present   → replace JUST the block in place; user
//     content outside the markers is preserved byte-for-byte.
//
// Re-init is the same byte-identical no-op the primer fold guarantees:
// when the on-disk block already matches the current card, the file is
// left untouched (no rewrite, no metadata churn).
func appendQuickstartCardIfPresent(path string) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // never create harness files
		}
		return err
	}

	content := string(existing)
	block := quickstartCardBlock()

	begin := strings.Index(content, quickstartCardBeginMarker)
	end := strings.Index(content, quickstartCardEndMarker)
	if begin != -1 && end != -1 && end > begin {
		head := content[:begin]
		tail := content[end+len(quickstartCardEndMarker):]
		tail = strings.TrimPrefix(tail, "\n")
		updated := head + block + tail
		if updated == content {
			return nil // already current — true no-op
		}
		return writeFilePreservingMode(path, updated)
	}

	// No block yet — append after the user's content (and after the
	// primer block, if writePrimerArtifacts ran first) with one blank-
	// line separator. Guarantee exactly one trailing newline before
	// the block.
	sep := ""
	if content != "" {
		trimmed := strings.TrimRight(content, "\n")
		content = trimmed + "\n"
		sep = "\n"
	}
	return writeFilePreservingMode(path, content+sep+block)
}
