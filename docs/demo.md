# Live walkthrough

> ~5 minutes. Five beats — each escalates the previous "oh fuck" moment.

## Setup

```bash
# Install Rufio (requires Go 1.25+):
go install github.com/d-mcmillan/rufio/cmd/rufio@v1.0.6.3
# Or build from source: see README's Install section.

mkdir demo && cd demo && rufio init
rufio dev &
```

The daemon is now running. `rufio init` has scaffolded an empty `given/` (human-authored context goes here), an empty `learned/`, and a `live/` directory tree ready for thoughts — plus a `RUFIO.md` agent-onboarding primer at the project root.

In two separate shells, configure Claude Code and Cursor to allow the `rufio` command. Both already shell out natively; both already read `RUFIO.md` at session start (init also folds the same primer into `CLAUDE.md` / `.cursorrules` / `AGENTS.md` if the project already uses one).

---

## Beat 1 — Telepathy (~60s)

```
# In Claude Code:
You: Remember customer 5821 prefers email contact.
Claude: [shells out] rufio observe --subject=customer:5821 \
        --predicate=prefers --object=email --scope=fleet
Claude: Got it.

# In Cursor (different process, different machine if you want):
You: What does customer 5821 prefer?
Cursor: [shells out] rufio recall "customer 5821 preferences"
        → 2026-05-08T14:32:01Z observation claude-code customer:5821:prefers="email" fleet
Cursor: Customer 5821 prefers email.
```

> *"Two harnesses, one substrate. They share a thought without knowing about each other."*

---

## Beat 2 — In-flight thoughts (~30s)

```
# In Cursor's terminal (running in advance):
$ rufio listen --as=cursor &

# In Claude Code:
Claude: rufio think --type=hypothesis --subject=customer:5821 \
        --content="Showing churn signals" --scope=fleet --ttl=300

# In Cursor's terminal, immediately:
[14:33:01] thought  claude-code  customer:5821 hypothesis  "Showing churn signals"
```

> *"Live thoughts propagate at filesystem speed. This isn't shared memory — this is a shared mind."*

---

## Beat 3 — Direct cognition (~90s)

```
# Claude Code is stuck:
You: I'm stuck. Find someone with churn analysis skills and figure this out.

Claude: rufio fleet --skill=churn-analysis
        → data-analyst (online)

Claude: rufio summon data-analyst --topic=customer:5821 \
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

> *"Two agents, different specialisations, different machines, collaborating in real time. With audit."*

---

## Beat 4 — Self-coordination via goals (~45s)

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

> *"Coordination without orchestration. Two agents notice they're duplicating effort and self-resolve."*

---

## Beat 5 — Reasoning audit + time travel (~60s)

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

> *"Every decision is auditable to the line. The substrate's state at any past moment can be reconstructed."*

---

## What just happened

Five beats. Five "oh fuck" moments:

1. Two harnesses share a thought without an orchestrator
2. Real-time thought propagation at filesystem speed
3. Two specialist agents collaborate via a private channel with full audit
4. Two agents self-coordinate when their goals overlap
5. Every decision is auditable to the line, with sub-second time travel

**No MCP. No SDK. No proprietary protocol.** Just a CLI tool and the filesystem.

That's the substrate.

> Want to prove it with *real* third-party harnesses? See the [cross-harness captures](../captures/2026-05-21-cross-harness-live/) — every record from a 4-vendor live run, on disk, replayable.
