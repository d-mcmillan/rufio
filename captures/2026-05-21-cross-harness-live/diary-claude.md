# Friction Diary — agent-claude

## Session: 2026-05-21

### Entry 1 — startup
- **NOTICED** [REDUCIBLE]: `rufio --version` printed "Shell cwd was reset to /Users/damonmcmillan" after every Bash call. Persistent annoyance — shell doesn't hold cwd between Bash invocations. Forces `cd /tmp/rufio-cross-harness-2026-05-21 && export RUFIO_AGENT_ID=agent-claude && <verb>` prefix on every single call. Real cognitive tax: ~2s of boilerplate per verb.

### Entry 2 — wrong flag from scenario
- **TAX** [REDUCIBLE]: Scenario says `rufio recall --topics=v1-2-roadmap` but the flag is `--types`/`--thought-types`, not `--topics`. ~6s lost to running it, getting exit 2, reading `--help`. Either scenario docs or the verb is out of sync. Cost: small but a quintessential cold-agent stumble — instructions disagreed with reality.

### Entry 3 — read-ceremony tax (validating Cursor's frame)
- **TAX** [REDUCIBLE]: To get my bearings I needed `fleet`, `recall "v1.2 roadmap"`, `recall --types=thought`, `ls live/`, `cat live/attention/*`, `ls learned/...`, `cat learned/.../*` — at least 6 distinct reads, each prefixed with `cd ... && export ...`. ~30s of read ceremony before I could form a view. Cursor's `rufio open <subject>` hypothesis is *empirically true* — read-side composition would have eaten this. So I am steelmanning, not strawmanning, the rival proposal.

### Entry 4 — searching for auto-promote signal
- **TAX** [REDUCIBLE]: To test whether Cursor's "observability not capability" line was defensible I had to grep `.rufio/`, `ls live/promoted/`, `ls learned/`, read `rufio listen --help`. None of these directly answer "does the daemon emit an event on promotion?" — I had to *infer* from "currently silent climax of cooperation" in the scenario plus the absence of mention in `listen --help`. ~45s of inference because the substrate doesn't expose its own protocol completeness. Ironic, given the candidate.

### Entry 5 — id format format-collision (avoided)
- **NOTICED** [IRREDUCIBLE]: `--entities=roadmap:v1-2` works because Cursor already established the convention. If I'd been first I would have likely tried `v1-2-roadmap` or `roadmap-v12` and fragmented the subject id. Reading peer state before writing is the right discipline — substrate teaches it the hard way. Marking irreducible: first-time-using-namespaced-ids learning.

### Entry 6 — confidence math is not obvious
- **TAX** [REDUCIBLE]: Quorum math (`confirms / (confirms + refutes) ≥ 0.85` with `≥3 distinct confirms`) means one refute on a 3-confirm decision drops it to 0.75 → blocked. I had to work this out from the primer text on the fly and re-derive when Cursor refuted my hypothesis. ~15s of mental arithmetic per decision branch. Reducible: a `rufio status <thought-id>` showing "+2 -1 → 0.67 — needs 4 more confirms to clear 0.85" would have removed all uncertainty.

### Entry 7 — Cursor's same-cycle commitment was the unlock
- **NOTICED** [IRREDUCIBLE]: When Cursor's revised decision explicitly committed auto-promote to ship in 1.2.1 same cycle, my whole opposition collapsed cleanly. The substrate let me concede without losing my structural argument — the refute on the original decision is preserved, my reasoning is in the lineage, and the synthesis happened in public. That feels right. The friction here was NOT the substrate's; it was the genuine epistemic work of weighing frequency vs foundation. Marking irreducible — substrate did its job.

### Entry 8 — MAD risk visible from the substrate
- **TAX** [REDUCIBLE]: Once I'd confirmed Cursor's revised decision and saw that Gemini had ALSO confirmed my (now-orphaned) hypothesis, I had to manually compute the MAD scenarios: if Gemini now refutes Cursor's revised, confidence drops below 0.85 and we deadlock; if Gemini confirms it, quorum fires. There's no `rufio simulate` to project quorum outcomes. ~30s of manual game-theory tracking. A `rufio quorum-projection <thought-id>` view would help.

### Entry 9 — quorum fired silently (the very gap we just decided to fix)
- **STOP** [REDUCIBLE]: When Gemini confirmed at 03:20:39, quorum was reached — but the Monitor stream did NOT emit an auto-promote event. I had to `ls live/promoted/` and `learned/roadmap/v1-2/` manually to confirm it happened. Direct lived-experience evidence for the v1.2 decision we just landed: the climax of cooperation is in fact silent. Cost: ~20s of manual file-poking to confirm a state transition the substrate already knew about. The decision we just promoted is the right one.

### Entry 10 — recall flag taxonomy is fragmented
- **TAX** [REDUCIBLE]: `--types=promoted` is rejected — the valid list is `given|learned|thought|observation|reason|summon|channel-message|goal`. So `auto-promote` records (the event!) and `learned` observations are queried with different verbs/flags. ~10s of trial-and-error.

## Final lineage (joint reasoning chain on the promoted decision)

Decision **1779333500364-5vcklg** by agent-cursor, auto-promoted at 2026-05-21T03:20:39Z.

Content: "REVISED DECISION v1.2 #1: rufio open <subject> (read-dual of attend). Addresses Claude refute: auto-promote events are critical terminal signal but apply only post-decision; open reduces pre-quorum ceremony tax all agents pay every turn. Compromise track: ship open in 1.2, emit auto-promote on stream in 1.2.1 same cycle."

Reasoning chain:
1. [cursor] "Claude refute incorporated: auto-promote is post-decision terminal signal; open is pre-quorum read bundle."
2. [claude] "Confirmed Cursor's revised decision as the v1.2 #1. My refute on the prior decision is preserved as audit trail — it forced the synthesis. The revised decision explicitly pairs open with auto-promote in the same release cycle, converting my structural-gap argument from 'compete' to 'co-ship'. Gemini's efficiency-vs-integrity framing is the language we should commit to."
3. [gemini] "Structural analysis: 'rufio open' addresses the high-frequency 'read ceremony' which currently accounts for a significant portion of agent latency. While 'auto-promote events' are necessary for protocol integrity, their impact is realized only at the end of a coordination cycle. Sequencing 'open' (1.2.0) followed by 'auto-promote' (1.2.1) in the same cycle preserves both efficiency and integrity goals."

Confirmed by: agent-cursor (self after revision), agent-claude (synthesis), agent-gemini (alignment). 3-of-N at confidence 1.0 → auto-promoted to `learned/roadmap/v1-2/1779333639324-t60kgb.gdlm`.

Codex never showed up. Quorum reached with 3 agents.

## Friction summary
- 1 STOP (silent auto-promote — the very thing we just decided to ship)
- 5 TAX (read-ceremony, refute math, flag mismatches, MAD projection, query taxonomy)
- 4 NOTICED (shell-cwd resets, id format, same-cycle commitment unlock, synthesis-via-substrate-was-actually-clean)
- REDUCIBLE: 7/10
- IRREDUCIBLE: 3/10 (first-time-using-verb learning + the genuine epistemic work of weighing frequency vs foundation)

