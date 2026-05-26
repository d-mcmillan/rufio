# `specs/` — Greppable format specifications

> Rufio's wire format is [Greppable](https://greppable.ai). Rather than vendor copies of the spec into this repo, we link back to the canonical source so updates are immediately visible and there's no drift.

## What Rufio uses

The Rufio build implements two of the seven Greppable layer specs:

| Spec | What Rufio uses it for | Build need |
|------|------------------------|------------|
| **GDL** ([spec](https://github.com/greppable/spec/blob/main/specs/GDL-SPEC.md)) | The base format. Every Rufio record (`rufio.gdl`, attention files, summons, channels, goals, thoughts) uses `@type\|key:value\|key:value`. | **Critical** |
| **GDLM** ([spec](https://github.com/greppable/spec/blob/main/specs/GDLM-SPEC.md)) | The memory layer. `.gdlm` files in `learned/` use this shape for observations and supersedes chains. | **Yes** |

The other five Greppable layers — [GDLS](https://github.com/greppable/spec/blob/main/specs/GDLS-SPEC.md) (schema), [GDLC](https://github.com/greppable/spec/blob/main/specs/GDLC-SPEC.md) (code maps), [GDLA](https://github.com/greppable/spec/blob/main/specs/GDLA-SPEC.md) (API contracts), [GDLD](https://github.com/greppable/spec/blob/main/specs/GDLD-SPEC.md) (diagrams), [GDLU](https://github.com/greppable/spec/blob/main/specs/GDLU-SPEC.md) (unstructured docs) — are not implemented in v1. Rufio hosts files of these types as opaque content (versions, propagates, audits) without parsing them. If a future Rufio integration needs to deep-parse one of these layers, link to the spec then.

## Version Rufio is building against

- **Upstream:** `github.com/greppable/spec`
- **Targeting commit:** `ae6e2833ec6d4905c3e995055a42705e84c6b20d` (2026-03-11)

When Rufio's parser is implemented, the build can be validated against this exact commit. If Greppable ships a breaking change later, this README's commit pin is the contract — bump deliberately, not by accident.

## Why link, not vendor

Considered vendoring all seven specs into this repo. Decided against it:

- Vendored files **drift** — they go stale silently, and re-syncs are chores that don't happen.
- Vendored files **pollute commit history** with upstream changes that aren't really Rufio's work.
- Vendored files **double the maintenance surface** — bug-fixing a spec means upstreaming, then re-vendoring.
- The **only** benefit of vendoring (build self-containment) is solved more simply by `git clone greppable` next to your Rufio checkout.

If offline-build self-containment ever becomes a real requirement (CI runners with no internet, air-gapped environments), vendor *only* GDL-SPEC.md and GDLM-SPEC.md at that point — not all seven.

## How `given/` directories are read

Files in `given/` are hosted as opaque content records — one file = one record. If your knowledge format packs multiple records per file (e.g., Greppable's canonical layers like GDLC, GDLA, GDLS, GDLD, or third-party variants), split them into one-record-per-file for record-level indexing. A `--parse-records` mode for multi-record files is a v1.0.7+ candidate.
