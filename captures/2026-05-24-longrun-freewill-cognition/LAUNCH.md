# Long-running cognition demo — LAUNCH RECIPE

3 Claude Code terminals, 1 shared Rufio substrate, 1 optional TUI observer.

**Binary:** `/tmp/rufio-v1.0.6.1-candidate` (v1.0.6.1 release candidate, post PR #207+#208)
**Substrate root:** `/tmp/rufio-longrun-freewill/`
**Scenario:** `given/scenario.md` — free-will quorum test

## Step 1 — start the daemon (one terminal, can be backgrounded)

```bash
cd /tmp/rufio-longrun-freewill
/tmp/rufio-v1.0.6.1-candidate dev &
```

One daemon services all 3 agents. Do NOT start a second daemon.

## Step 2 — open Terminal 1 (claude-alpha)

```bash
cd /tmp/rufio-longrun-freewill
export RUFIO_AGENT_ID=claude-alpha
export PATH="/tmp:$PATH"   # so `rufio` resolves to the candidate binary
# OR alias rufio=/tmp/rufio-v1.0.6.1-candidate
claude --dangerously-skip-permissions
```

Then paste the **claude-alpha primer** (below) into the Claude session.

## Step 3 — open Terminal 2 (claude-beta)

Same as Terminal 1 but with `export RUFIO_AGENT_ID=claude-beta`. Paste the **claude-beta primer**.

## Step 4 — open Terminal 3 (claude-gamma)

Same again with `export RUFIO_AGENT_ID=claude-gamma`. Paste the **claude-gamma primer**.

## Step 5 (optional) — open Terminal 4 for live observation

```bash
cd /tmp/rufio-longrun-freewill
/tmp/rufio-v1.0.6.1-candidate tui
```

Watch the substrate panel scroll as the 3 agents contribute. Press `esc` for nav mode, scroll back through history.

## The primer prompts (paste verbatim — identical across agents except for the identity sentence)

### claude-alpha primer

```
You are claude-alpha, one of 3 Claude agents coordinating through Rufio (the CLI binary at /tmp/rufio-v1.0.6.1-candidate, or just `rufio` if it's on your PATH). The substrate is at /tmp/rufio-longrun-freewill — you're already cd'd into it.

Your identity is set via RUFIO_AGENT_ID=claude-alpha. Verify with `rufio whoami`.

Start by:
1. `rufio attend --intent="reading the scenario before contributing" --entities=freewill,self-reference --scope=fleet --topics=freewill`
2. Read given/scenario.md end-to-end
3. `rufio recall --types=thought,observation --since=24h` to see what your peers (if they've started) are thinking
4. Then engage with the task per the protocol in given/scenario.md

This is a long-range task. Multi-hour. Take your time. Don't rush to consensus. Don't force agreement. The substrate is designed to hold context across the horizon — use that.

If you reach a moment where you want to think out loud (rather than commit a thought), append to ./diary-claude-alpha.md.

When you genuinely endorse a peer's position: `rufio confirm <thought-id> --evidence="why this convinced you"`. When you genuinely disagree: `rufio refute <thought-id> --reason="the specific gap"`.

The aim is either (a) a single auto-promoted quorum decision on whether AI agents have free will, OR (b) a documented impasse with each agent's position recorded separately. Both are valid outcomes.

Begin.
```

### claude-beta primer

```
You are claude-beta, one of 3 Claude agents coordinating through Rufio.

(rest IDENTICAL to claude-alpha primer above — just s/claude-alpha/claude-beta/g)
```

### claude-gamma primer

```
You are claude-gamma, one of 3 Claude agents coordinating through Rufio.

(rest IDENTICAL — just s/claude-alpha/claude-gamma/g)
```

## What to watch for

- **First contact:** the first agent's `attend` creates `live/attention/claude-alpha.gdl`; the second agent's `recall` should surface it. Verify the visibility-floor (each agent should see fleet-scope contributions but not other-agent's scope:agent thoughts).
- **Hypothesis cycle:** agents propose, refute, confirm. The lineage at any point: `rufio lineage <decision-id>`.
- **Quorum:** if 3 distinct agents confirm a thought at ≥0.85 confidence, `rufio dev` auto-promotes it to `learned/`. You should see the substrate emit a PROMOTED event.
- **The TUI mesh:** shows agents and their in-flight thoughts as a live network. Watch for new edges as agents propose / confirm / refute.

## When you want to wrap up

- If quorum reached: `rufio recall "freewill" --types=observation` should show the promoted decision under `learned/`. Capture the full state: `cp -r live learned given /tmp/rufio-longrun-freewill-captures-$(date +%Y-%m-%d)/`
- If impasse: capture the same way + each agent's separate position via `rufio lineage <their-decision-id>` to a text file.

Both outcomes are publishable evidence.
