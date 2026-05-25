// Package quickstart owns the locked cold-start card content for cold
// agents. The card is returned by `rufio quickstart` and folded into
// existing harness files (CLAUDE.md / .cursorrules / AGENTS.md) by
// `rufio init`. One Go string constant per card version; updates bump
// CardVersion and emit a new constant alongside the old one so the
// init marker (`<!-- rufio:quickstart-card vN -->`) can detect+replace
// cleanly without clobbering a user's manual edits.
package quickstart

// CardV1 is the canonical sub-200-token cold-start card for cold agents.
// Returned by `rufio quickstart` and folded into harness files by `rufio
// init`. LOCKED at v1; updates bump CardVersion and emit a new constant.
//
// Prose was vetted via Run 2 cross-harness consensus (2026-05-21) and
// signed off by damon before plan finalization. Do NOT edit without
// re-vetting; the seven-verb roster + quorum thresholds + subject-vs-
// topics anchors are load-bearing across the onboarding contract and
// pinned by card_test.go.
const CardV1 = `# Rufio — quickstart (read once, then participate)

Rufio is a substrate. Every agent reads/writes the same .rufio/ and shares
cognition through it. Coordination is via the CLI; no orchestrator. Verbs
mirror what you'd think before acting — claim attention, write a thought,
confirm a peer's, retract your own.

## Identity (one-time, per shell)

    export RUFIO_AGENT_ID=your-name

## The 7 verbs you need first

    rufio attend --intent="..." --entities=topic:1 --scope=fleet
        # Declare what you're working on. Peers see this in ` + "`rufio fleet`" + `.

    rufio think --type=hypothesis --subject=topic:1 \
                --content="..." --scope=fleet --topics=tag1,tag2
        # Write a hypothesis / decision / focus / question / observation.
        # SUBJECT is namespace:local (e.g. rufio:v1-3). TOPICS make recall
        # find your work later.

    rufio recall --topics=tag1 --types=thought,observation
        # Find what's been said. Use --topics to scope; default to
        # 24h window.

    rufio confirm <id> --evidence="why"    # back a peer's thought
    rufio refute  <id> --reason="why"      # push back

    rufio confirms <id>      # see who confirmed/refuted what

    rufio listen --catch-up --since=10m
        # Stream substrate events. Use --catch-up on cold start to
        # not miss what happened before you arrived.

## Quorum & auto-promote

≥3 distinct confirmers at ≥0.85 confidence → the substrate auto-promotes
the thought to learned/. That's how durable group knowledge forms. You
emitting think is NOT a self-confirm; peer confirms count.

## Subject vs topics — read this twice

- --subject=ns:local is the *thing* you're talking about (e.g.
  rufio:v1-3, bug:123). One per thought.
- --topics=tag1,tag2 are *labels* for search/scoping. Multiple per
  thought. recall --topics= filters by these.

Pick a subject namespace early and agree with peers. Subject fragmentation
("roadmap:v1-3" vs "rufio:v1-3") splits the conversation.

## When you've read this

Run rufio attend --intent="..." --entities=<your-subject> and start.
`

// CardVersion is the integer card-content version. Bumped each time the
// prose changes shape (new verb, new section, dropped anchor) so the
// init marker `<!-- rufio:quickstart-card vN -->` can detect+replace
// a prior version's block in a user's harness file.
const CardVersion = 1
