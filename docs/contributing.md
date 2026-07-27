---
tags:
  - contributing
  - development
---

# Contributing

Contributions welcome — issues, docs, tests, and code.

## Setup

You need:

- Go 1.24+
- `git` on your `PATH` (regit shells out to it for all repository access)
- [`just`](https://github.com/casey/just) — the single dev entrypoint

```bash
git clone https://github.com/wlame/regit
cd regit
just build
```

`just build` compiles the binary to `dist/regit`, stamping the version from
`git describe --tags --dirty --always`. Run `just` with no arguments to list
every recipe.

## Tests

```bash
just test        # full test suite
just test-race   # race detector + coverage profile
just cover       # per-function coverage; fails below the 80% floor
```

## Full gates

```bash
just ci      # fmt-check + vet + build + race tests + coverage gate
just e2e     # end-to-end tests against the built binary
just check   # pre-push battery: ci + e2e + docs drift + a fuzz burst
```

GitHub Actions runs the exact same `just ci` and `just e2e` recipes, so local
and CI results cannot drift. CI also runs the test suite on macOS and Windows,
and against git 2.25 to catch accidental use of newer git flags.

## Generated docs blocks

Marked blocks in `README.md` and `AGENTS.md` are generated from the
[capability registry](concepts/architecture.md) in code.

```bash
just docs-sync    # regenerate the blocks after changing the registry
just docs-check   # verify the blocks match the code (a drift test runs this in CI)
```

!!! warning "Edit the source, not the blocks"
    Never hand-edit content inside the marked blocks — change the registry (or
    the renderer in `internal/docs`) and run `just docs-sync`, or `just check`
    will fail on drift.

## Docs site

```bash
just docs-build   # build the site (strict — fails on any warning)
just docs-serve   # serve locally with live reload
```

Both run MkDocs through `uv`; no local Python environment setup is needed.

## Pull requests

1. Create a focused branch — one logical change per PR.
2. Add tests for new behavior; keep coverage at or above 80%.
3. Ensure CI passes (`just ci` locally is the same gate; `just check` before
   pushing is the full battery).
4. Open a PR against `main` with a clear description of what changed and why.

## Related pages

- [Architecture](concepts/architecture.md) — package layout and layering rules
- [CLI reference](reference/cli/index.md) — the command surface you may be extending
- [Changelog](changelog.md) — release status
