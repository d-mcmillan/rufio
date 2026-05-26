# Contributing to Rufio

Rufio is a v1 substrate research preview. The CLI surface is locked, the daemon is stable, and the project is actively developed. Contributions are welcome.

## Where to start

1. **Read the [manifesto](./MANIFESTO.md) and [architecture](./docs/architecture.md).** They explain what Rufio is, what it is not, and why.
2. **Try the [demo](./docs/demo.md).** A clean install + five beats of cross-vendor consensus in under 10 minutes.
3. **Skim the [v1 spec](./docs/v1-spec.md).** It's the authoritative description of the v1 surface.

## Useful contributions

- **Use cases.** Open an issue tagged `use-case` describing the context-sharing pain you're living with in your agent stack. Real-world friction shapes the roadmap.
- **Spec feedback.** v1 is locked, but not sacred. If a primitive is missing, named badly, or scoped wrong, say so — issues tagged `spec-feedback` get prioritised.
- **Bug reports.** Reproducible bug reports are gold. Include `rufio --version`, the verb, the expected vs observed output, and any relevant files in `live/` or `.rufio/`.
- **Code PRs.** Welcome. Open an issue first for anything beyond a one-line fix, so we can discuss approach before you spend time.

## Code standards

- **Language:** Go 1.25+ (modules). Single static binary built with `go build ./cmd/rufio`.
- **Formatting:** `gofmt -l .` must be empty before commit. `go vet ./...` clean.
- **Tests:** `go test ./...` clean on `main`. Integration tests build the binary once and exec it against `t.TempDir()` workdirs — no mocks for filesystem or process operations.
- **Errors:** Typed error structs implementing `error` + `ExitCode() int`. Never return raw `errors.New(...)` from public APIs.
- **Commits:** Conventional Commits prefixes (`feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`).
- **Branches:** `feat/<short>`, `fix/<short>`, `chore/<short>`. Never push directly to `main`.

## Pull-request flow

1. Fork the repo, branch from `main`.
2. Make focused changes — one concern per PR.
3. Ensure `go test ./...`, `go vet ./...`, and `gofmt -l .` are clean.
4. Open a PR against `main` with a clear description of the change and motivation.
5. CI must pass. Reviewers will respond.

## Code of conduct

Be kind. We're all early, we're all figuring it out. If someone is being a jerk, [email the maintainer](mailto:damon.f.mcmillan@gmail.com) directly.

## License

By contributing to Rufio, you agree your contributions are licensed under the [Apache License 2.0](./LICENSE).
