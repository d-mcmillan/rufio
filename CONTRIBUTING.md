# Contributing to Rufio

Rufio is pre-release. The architecture is locked, the spec is published ([`docs/v1-spec.md`](./docs/v1-spec.md)), and the build is starting. This document will evolve as the project matures.

## Right now (pre-release)

**The most useful contributions today:**

1. **Read the [manifesto](./MANIFESTO.md) and [architecture](./docs/architecture.md).** If something there is wrong, missing, or unclear, [open an issue](https://github.com/d-mcmillan/rufio/issues) — this is the cheapest moment to change minds.
2. **Push back on the spec.** [v1-spec.md](./docs/v1-spec.md) is locked but not sacred. If you've built distributed agent systems and you think a primitive is missing, named badly, or scoped wrong, say so. Issues tagged `spec-feedback` get prioritised.
3. **Tell us what you'd want from a substrate.** What context-sharing pain are you living with right now in your agent stack? Issues tagged `use-case`.
4. **Don't open code PRs yet.** The build hasn't started. PRs to `main` will be closed politely. Once v1 architecture is implemented, this guidance changes.

## When v1 lands

This section will be expanded with:

- Code style guide (Biome / Prettier config — TBD)
- Test discipline (every CLI command needs an integration test against a real filesystem)
- PR template (already in `.github/PULL_REQUEST_TEMPLATE.md`)
- Commit conventions (Conventional Commits)
- Branch policy (trunk-based, short-lived feature branches)
- Sign-off / DCO requirements

## Code of conduct

Be kind. Don't be a jerk to anyone working on agent infrastructure — we're all early, we're all figuring it out. If someone is being a jerk, [email the maintainer](mailto:damon.f.mcmillan@gmail.com) directly.

## License

By contributing to Rufio, you agree your contributions are licensed under the [Apache License 2.0](./LICENSE).
