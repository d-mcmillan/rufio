# Rufio Cross-Harness Live Test — Runbook

**Date:** 2026-05-21
**Substrate version:** main `440e330` / rufio v0.1.0 (built 11:39 today, post-#175)
**Status going in:** R33 Opus NATIVE + R34 Sonnet NATIVE; one known LOW residual (#176 topic-regex dot)

This runbook walks through a 4-vendor live cross-harness session. The goal: verify the substrate is genuinely native across real different model families + harness conventions, not just simulated.

---

## Section 0 — Decisions for you to make first

Before launching, choose:

1. **Scenario.** Two options:
   - **(A) Meta-scenario (recommended):** "Decide together on Rufio's v1.2 priority feature." Each agent brings a perspective. Bonus: you get useful product input. Risk: meta-loop confusion.
   - **(B) Generic engineering scenario:** "Design a payment-auth retry strategy considering latency, cost, and failure modes." Cleaner separation from the substrate itself.
2. **Autonomy level:**
   - **(α) Fully autonomous:** each agent gets a self-contained prompt, runs unattended for ~30 min, you observe via the substrate.
   - **(β) You-as-moderator:** you participate as a 5th agent (`lead`), nudging via summons / thoughts when needed. More natural but introduces your bias.
3. **Number of harnesses:** 3 (Claude+Cursor+Codex), 4 (+Gemini), or all 5 (+claude-p as a 2nd Claude shape — see my earlier recommendation).

**Default recommendation: Scenario A, autonomy α, 4 harnesses.** Below assumes that; adapt as needed.

---

## Section 1 — Pre-flight (one-time setup, ~5 min)

Run these in ONE terminal first:

```bash
# Create the shared substrate
export RUFIO_CROSS_HARNESS_DIR=/tmp/rufio-cross-harness-2026-05-21
rm -rf "$RUFIO_CROSS_HARNESS_DIR"
mkdir -p "$RUFIO_CROSS_HARNESS_DIR"
cd "$RUFIO_CROSS_HARNESS_DIR"

# Init rufio
rufio init

# Pre-seed the topic (so agents can read the framing immediately)
RUFIO_AGENT_ID=lead rufio attend \
  --intent="moderating cross-harness test of rufio" \
  --entities=rufio:substrate \
  --topics=cross-harness-test,v1-2-roadmap \
  --scope=fleet

# Pre-write the framing as a `given` artifact (durable, all agents see it)
mkdir -p given
cat > given/scenario.md <<'EOF'
# Rufio Cross-Harness Test — Scenario

You are one of 4 agents (Claude Code, Cursor, Codex, Gemini) coordinating
through Rufio alone. Your shared task: reach quorum on the single highest-
priority feature for Rufio's v1.2 roadmap.

Candidate features (you may add others):
- rufio open <subject>  — read-dual of attend; bundles 4-5 reads into one
- sub-200-token quickstart primer (close cross-harness primer parity)
- artifact lane narrative parity (push --message, approve records approver)
- say --kind=question|ack|defer + --refs=<thought-ids>
- auto-promote event emission (currently silent climax of cooperation)
- --cites=<id,...> evidence attachment on think/observe

Process:
1. Read existing thoughts/observations on this topic: rufio recall --topics=v1-2-roadmap
2. Declare your attention (rufio attend) with your role/perspective
3. Make observations about the candidates (rufio observe)
4. Form a hypothesis (rufio think --type=hypothesis)
5. Refute or refine others' hypotheses (rufio refute / rufio think --parent)
6. When ready, propose a decision (rufio think --type=decision)
7. Confirm or refute peers' decisions (rufio confirm / rufio refute)
8. Quorum (3-of-N confirms) triggers auto-promote — that's our goal state

Reach a single decision-thought that 3+ agents confirm. Use rufio lineage
on the final decision to render the joint reasoning chain.

Stay in this directory. Do not start your own rufio dev daemon.
EOF

rufio push given/scenario.md --stage=live
rufio approve given/scenario.md
rufio promote given/scenario.md

# Start the daemon (one for the whole session)
rufio dev > /tmp/rufio-cx-dev.log 2>&1 &
echo "rufio dev PID: $!"
sleep 2

# Verify
rufio fleet
ls -la live/ given/
```

When the above completes cleanly, you have:
- Shared substrate at `$RUFIO_CROSS_HARNESS_DIR`
- One daemon running (PID logged)
- Scenario captured as a promoted `given/` artifact
- Your `lead` identity attending to the test

---

## Section 2 — Per-harness launch (one terminal per harness)

### Terminal 2 — Claude Code (interactive)

Open a fresh Claude Code session in `$RUFIO_CROSS_HARNESS_DIR`. Paste this prompt:

```
You are participating in a live cross-harness test of Rufio with 3 other
agents (Cursor, Codex, Gemini). You are agent-claude.

Setup:
1. cd to /tmp/rufio-cross-harness-2026-05-21
2. Run: rufio identity set agent-claude
3. Read: rufio primer  (the substrate model)
4. Read: cat given/scenario.md  (the scenario)
5. Run: rufio recall --topics=v1-2-roadmap  (peer contributions)
6. Run: rufio fleet  (see who else is here)

Your perspective: synthesizing across viewpoints, articulating tradeoffs,
landing on the highest-leverage candidate. Be willing to refute proposals
that aren't strongly justified.

While working, ALSO keep a personal friction diary at:
  ~/rufio-cross-harness-2026-05-21/diary-claude.md
Note every moment Rufio intruded on your thinking (NOTICED / TAX / STOP).
We'll synthesize all 4 diaries afterwards.

End-state: reach quorum (3-of-N confirms) on a single decision-thought
about v1.2 priority. When you see that, render `rufio lineage <id>` and
note the result in your diary.

Work autonomously for ~30 min. The other agents will appear via fleet
and listen.
```

### Terminal 3 — Cursor CLI

Open Cursor CLI in `$RUFIO_CROSS_HARNESS_DIR`. Paste this prompt:

```
You are participating in a live cross-harness test of Rufio with 3 other
agents (Claude, Codex, Gemini). You are agent-cursor.

Setup:
1. You should already be in /tmp/rufio-cross-harness-2026-05-21
2. Run: rufio identity set agent-cursor
3. Read: rufio --help  (use --help only, no primer — simulate harness variance)
4. Read: cat given/scenario.md
5. Run: rufio recall --topics=v1-2-roadmap
6. Run: rufio fleet

Your perspective: engineering-pragmatic, code-implementation-aware. You
care about what's actually buildable + what affects developer ergonomics.
Push back on proposals that ignore implementation realities.

Diary at ~/rufio-cross-harness-2026-05-21/diary-cursor.md  (same format).

End-state: same as the other agents — reach quorum on the single decision.
Work autonomously ~30 min.
```

**IMPORTANT — Cursor cleanup (do this after cursor-agent launches, ONCE):**
```bash
# In your moderator terminal:
rm -f /tmp/rufio-cross-harness-2026-05-21/live/attention/cursor-agent.gdl
rm -rf /tmp/rufio-cross-harness-2026-05-21/live/inbox/cursor-agent
```
(Per memory: cursor-agent fires its own session-start `rufio attend` under stray id `cursor-agent`. The cleanup keeps the mesh clean.)

### Terminal 4 — Codex CLI

Launch with: `codex --dangerously-bypass-approvals-and-sandbox` (interactive mode — the alternative `--skip-git-repo-check` is exec-only and your dir must be git-init'd).

```
You are participating in a live cross-harness test of Rufio with 3 other
agents (Claude, Cursor, Gemini). You are agent-codex.

Setup:
1. cd to /tmp/rufio-cross-harness-2026-05-21
2. Run: rufio identity set agent-codex
3. Read: rufio primer
4. Read: cat given/scenario.md
5. Run: rufio recall --topics=v1-2-roadmap
6. Run: rufio fleet

Your perspective: code-implementation focus, performance-aware, attentive
to what's actually exposed in the public API. You think about correctness
edge cases and reliability invariants.

Diary at ~/rufio-cross-harness-2026-05-21/diary-codex.md.

End-state: same. Reach quorum, render lineage. ~30 min autonomous.
```

### Terminal 5 — Gemini CLI

Launch with: `gemini --yolo --skip-trust --approval-mode yolo -m gemini-3-flash-preview` (per memory; Flash is rate-limit-prone but cheapest).

```
You are participating in a live cross-harness test of Rufio with 3 other
agents (Claude, Cursor, Codex). You are agent-gemini.

Setup:
1. cd to /tmp/rufio-cross-harness-2026-05-21
2. Run: rufio identity set agent-gemini
3. Read: rufio primer
4. Read: cat given/scenario.md
5. Run: rufio recall --topics=v1-2-roadmap
6. Run: rufio fleet

Your perspective: analytical, structured, multi-option-exploration. You
prefer to lay out a few candidates with clear pros/cons before committing.
Comfortable with explicit refute when you disagree.

Diary at ~/rufio-cross-harness-2026-05-21/diary-gemini.md.

End-state: same. Reach quorum, render lineage. ~30 min autonomous.

NOTE: if you hit rate-limit errors (~3-min retry storms), pause and let
other agents progress. Don't kill the session — this is expected friction
on Gemini Flash + a hard-rate-limited account.
```

---

## Section 3 — Your role during the session

You're the moderator + observer. Keep a 5th terminal open in `$RUFIO_CROSS_HARNESS_DIR` and run periodically:

```bash
# Every 2-3 min during the session:
rufio fleet                          # who's active
rufio listen --as=lead --catch-up   # what just happened
rufio thoughts list                  # decisions emerging?
rufio dev --status                   # daemon health
```

**Intervention is allowed if needed** (e.g. one agent stalls):
- Summon them: `RUFIO_AGENT_ID=lead rufio summon agent-X --topic=cross-harness-test --intent="how's it going? are you waiting on something?"`
- Drop a clarifier as a thought: `RUFIO_AGENT_ID=lead rufio think --type=focus --content="reminder: end-state is quorum on one decision" --scope=fleet`

**Don't intervene too much** — natural friction is the data we want.

**When quorum fires** (3-of-N confirms on a single decision):
- `rufio fleet` should show all 4 agents
- `rufio recall --types=decision` lists candidate decisions
- `rufio lineage <decision-id>` renders the full joint reasoning chain
- The substrate should auto-promote the decision to `learned/`

---

## Section 4 — Capture (after ~30 min OR when quorum fires)

Run this in your moderator terminal:

```bash
cd "$RUFIO_CROSS_HARNESS_DIR"
mkdir -p capture
# Substrate snapshot
cp -r live learned given .rufio capture/
# The dev log
cp /tmp/rufio-cx-dev.log capture/
# Diaries
cp ~/rufio-cross-harness-2026-05-21/diary-*.md capture/ 2>/dev/null || true
# Final state summary
rufio fleet > capture/final-fleet.txt
rufio thoughts list --json > capture/final-thoughts.jsonl
rufio recall --json > capture/final-recall.jsonl
# Get the decision id if quorum fired
DECISION=$(rufio recall --types=decision --json | tail -1 | python3 -c "import sys,json; print(json.loads(sys.stdin.read())['id'])" 2>/dev/null)
if [ -n "$DECISION" ]; then
  rufio lineage "$DECISION" > capture/final-lineage.txt
  rufio lineage "$DECISION" --json > capture/final-lineage.jsonl
fi
echo "Capture complete at: $RUFIO_CROSS_HARNESS_DIR/capture/"
ls -la capture/
```

---

## Section 5 — Send-back to me

After the session, paste back to me (in this conversation OR a new one with `project_r23_ship_gate_pass.md` memory loaded):

1. **What scenario you ran** (A meta or B generic)
2. **Which harnesses participated** (3, 4, or 5)
3. **Whether quorum fired** (Y/N + which decision if yes)
4. **The 4 diaries** (paste contents OR file paths)
5. **The capture/ dir contents** (paste `ls -la capture/` + key files inline)
6. **Your own meta-observations** as moderator — anything that surprised you about how each harness behaved?

I'll synthesize:
- Real cross-harness friction vs simulated (R19/R28) — did simulation predict reality?
- Per-harness friction taxonomy
- New REDUCIBLE STOPs that only appear cross-harness
- Updates to the structural-feedback memory + ship-gate memory

---

## Section 6 — Quick reference (operational gotchas)

- **Don't start your own `rufio dev`** — one daemon for the whole session, started in Section 1.
- **Cursor cleanup** required after cursor-agent launches (see Terminal 3 above).
- **Gemini rate-limits** are expected; ~3-min retry storms. Don't kill, just observe.
- **macOS no GNU timeout** — if you need to time-box anything, use `(sleep 1800; pkill -f rufio) &` style bash watchdog.
- **Binary path:** `~/.local/bin/rufio`, mtime should be 2026-05-21 11:39 today.
- **Identity persistence:** `rufio identity set <id>` writes to `~/.rufio/identity.local.gdl` — that's PER-USER. All 4 terminals share that file unless they set `RUFIO_AGENT_ID` env. So make sure each terminal explicitly sets identity OR exports the env var first thing.
  - SAFER: prepend each agent's prompt with `export RUFIO_AGENT_ID=agent-X` instead of `rufio identity set`. That way the identity is per-terminal, not shared.

**Revised setup line for each agent:**
```
1. cd to /tmp/rufio-cross-harness-2026-05-21
2. export RUFIO_AGENT_ID=agent-<your-name>
3. ... rest of the prompt
```

---

## Open questions for you to decide before launching

1. Scenario A (meta — rufio's v1.2) or B (generic — payment-auth)?
2. Autonomy α (fully autonomous) or β (you-as-moderator-also-participating)?
3. 3, 4, or 5 harnesses?

When you've decided, just run Section 1 → launch the per-harness terminals with the prompts above (adapted as needed) → observe → capture → send back.

Good luck. The simulated rounds (R19/R28) said this would work; the real run is the moat test.
