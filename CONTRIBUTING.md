# Contributing

Contributions welcome — issues, docs, tests, and code.

The full guide lives at **<https://wlame.github.io/regit/contributing/>**
(source: [`docs/contributing.md`](docs/contributing.md)). This file is the short
version, kept at the root so GitHub can find it.

## Getting set up

You need the Go toolchain, `git` on your `PATH` (regit shells out to it for all
repository access), and [`just`](https://github.com/casey/just).

```bash
git clone https://github.com/wlame/regit
cd regit
just build          # compiles to dist/regit
just --list         # every recipe
```

## Before you open a pull request

```bash
just check
```

That runs the full battery: `ci` (format, vet, build, race tests, the coverage
gate), the end-to-end tests against the built binary, the generated-docs drift
check, a dependency vulnerability scan (needs network), and a short fuzz burst.
CI runs the same `ci`, `e2e`, and `vulncheck` recipes, so those gates cannot
disagree between your machine and CI.

The coverage gate is 85% overall, with a floor of 90% on `internal/safety` and
`internal/gitio` — the packages that rewrite a user's history.

## Conventions worth knowing before you write code

- **Read [`AGENTS.md`](AGENTS.md) first.** It maps the codebase, names the
  invariants, and lists the things that are easy to get wrong. It is written for
  LLM agents but is the fastest orientation for a human too.
- **All git access goes through `internal/gitio`.** Calling `exec.Command("git",
  …)` anywhere else breaks auditability and testability.
- **Every action is one row in the capability registry.** `--help`,
  `--llm-help`, the TUI action panel, and the transparency footer are all
  projections of it, so adding a verb means adding a row — not touching four
  surfaces.
- **Never hand-edit between `<!-- regit:begin … -->` markers** in `README.md` or
  `AGENTS.md`. Those blocks are generated; change the code and run
  `just docs-sync`. A drift test fails CI otherwise.
- **Commit messages are a single imperative sentence** starting with a capital
  and ending with a full stop, with no `feat:`/`fix:` prefix and no trailers.

## Reporting a security issue

Do not open a public issue. See [`SECURITY.md`](SECURITY.md).
