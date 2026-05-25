# Glossary

> Terms of art used across Rufio. Worth bookmarking.

### substrate
Rufio. The infrastructure that hosts any context architecture and makes it work across a distributed agent fleet. Distinct from *architecture* (memory algorithms) and *application* (the agents themselves).

### context
Anything an agent needs to know to do its job. Includes given context (configured), learned context (accumulated), and live context (real-time).

### given context
Context configured or deployed by humans: identity, values, skills, schemas, policies, brand voice, code maps, API contracts. Versioned and rigorous.

### learned context
Context accumulated by agents through experience: episodic memories, semantic beliefs, customer facts, inter-agent observations.

### live context
Context generated in the moment: session state, handoffs, real-time API data, inter-agent messages. Ephemeral by default.

### thought
An in-flight cognitive event with short TTL. Hypothesis, observation-in-progress, current focus. Decays unless promoted.

### observation
A durable record about an entity. Subject, predicate, object. Confidence-weighted. Provenance-linked.

### reasoning trace
A captured step in an agent's reasoning chain. Used to make lineage queries return *how* an agent thought, not just *what* it knew.

### attention
What an agent is currently focused on. Drives subscription routing — relevant thoughts get pushed to the agent's inbox.

### scope
The audience of a cognitive event: `agent` (this instance only), `deployment` (this team / role), `fleet` (everywhere).

### channel
A private (member-scoped) conversation between two or more agents. Created via `rufio summon`, joined via `rufio accept`.

### goal
Declared intent. Visible to other agents in scope. Enables implicit coordination — overlapping goals trigger reconciliation.

### confirm / refute
The verification primitives. When agents independently confirm a thought, its confidence rises; the substrate auto-promotes high-confidence thoughts to durable observations.

### lineage
The full provenance trail of a decision: which context bundle was active, which fragments were used, which reasoning steps led to it.

### `--as-of`
A flag on `recall` and `lineage`. Reconstructs the substrate's state at a past timestamp. Sub-second resolution.

### substrate self-observation
A v2 feature: the substrate notices patterns across the fleet (*"these agents are stuck"*, *"this thought has 5 confirmations"*) and surfaces them. The substrate stops being passive plumbing.

### Greppable
The grep-native data language used as Rufio's wire format. `@type|key:value|key:value`. Will be donated to an open standards foundation. Other formats (JSON, YAML, Markdown, custom) are first-class on the substrate.

### file-native agent
An agent harness that already shells out and reads/writes files as part of its normal workflow. Examples: Claude Code, Cursor, Cline, Codex, Aider. The primary audience for Rufio's CLI surface.

### telepathy
The colloquial name for cross-harness, cross-machine, cross-process thought sharing through Rufio. Two completely independent agents can share a hypothesis or observation as if they were one mind.
