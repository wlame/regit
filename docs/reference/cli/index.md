---
tags:
  - reference
  - cli
---

# CLI reference

regit is one binary with two faces: run it bare to get the interactive TUI, or give it a
command to run headless. Every TUI action has a non-interactive equivalent, and both are
projections of the same [capability registry](../../concepts/architecture.md) — so the flags
documented here are exactly what the binary accepts.

## Invocation forms

```bash
regit                              # launch the TUI on the current repository
regit <command> [flags]            # run a command headless
regit --help                       # terse command listing
regit <command> --help             # per-command help
regit --version                    # version banner
regit --llm-help[=text|md|json]    # full agent-facing reference (this page's source)
```

After [`regit install`](install.md), every command is also available as a git subcommand:

```bash
git re redate --strategy=Even-Spread --start=2026-01-01 --end=2026-03-01
```

`regit <command>` and `git re <command>` are equivalent; hints and help follow whichever
form was used. One exception: git answers a bare `git re --help` itself (man-page lookup) —
use `git re -h` or `git re <command> --help` instead.

## Shared convention: preview, then execute

Mutating commands **[preview by default](../../concepts/data-flow.md)** — running them
without flags prints a dry-run of what would change and touches nothing. Pass `--execute` to apply, and `--execute --force`
to rewrite commits that have already been pushed.

!!! warning "`--force` rewrites published history"
    `--execute --force` changes the hashes of commits other people may have already pulled.
    Anyone tracking the branch will need to reset. Every apply first writes a
    [backup ref](../../concepts/data-flow.md), so [`undo`](undo.md) can restore the previous
    tip.

All mutating commands share four flags:

| Flag | Type | Default | Description |
|---|---|---|---|
| `--execute` | BOOL | `false` | Apply the change (default: dry-run preview). |
| `--force` | BOOL | `false` | Allow rewriting already-pushed commits. |
| `--sign` | BOOL | `false` | Re-sign rewritten commits with your configured signing key. |
| `--repo` | VALUE | `.` | Path to the git repository. |

Three commands are exempt from the preview convention and **act immediately**:
[`undo`](undo.md), [`install`](install.md), and [`uninstall`](install.md).

## Standard exit codes

Unless a command's page says otherwise, every command uses:

| Code | Meaning |
|---|---|
| `0` | Success — a completed apply or a dry-run preview |
| `1` | Usage error (unknown command, bad flag or value) |
| `2` | Guard blocked the operation (dirty tree, pushed commits without `--force`, merge in range, HEAD moved) |
| `3` | Apply or LLM error |

`undo`, `install`, and `uninstall` deviate — their pages document their own codes.

## Deprecated aliases

`split` and `reword` predate the preview convention and still accept their old flags as
deprecated aliases: `--yes` is rewritten to `--execute`, and `--dry-run` is a no-op
(previewing is now the default; `--dry-run=false` becomes `--execute`). New scripts should
use `--execute` only.

## Commands

| Command | Purpose |
|---|---|
| [split](split.md) | Split one commit into 2–4 semantic commits using an LLM. |
| [reword](reword.md) | Regenerate commit message(s) with an LLM. |
| [squash](squash.md) | Squash consecutive commits into one per chain (LLM-summarized message). |
| [linearize](linearize.md) | Flatten merge topology into a clean first-parent chain (trees preserved). |
| [redate](redate.md) | Spread commit/author dates across a calendar using a strategy. |
| [retitle](retitle.md) | Rewrite commit subjects with a regex across a range. |
| [reauthor](reauthor.md) | Rewrite the author (and by default committer) identity of commits. |
| [drop-bodies](drop-bodies.md) | Strip the body of each commit in a range, keeping titles. |
| [purge](purge.md) | Remove files or directories from branch history as if never added. |
| [redact](redact.md) | Remove or replace a literal secret across branch history. |
| [undo](undo.md) | Restore the branch to the most recent regit backup ref. |
| [install](install.md) | Register regit as the `git re` subcommand. |
| [uninstall](install.md) | Remove the `git re` integration. |

## Related pages

- [Configuration](../configuration.md)
- [LLM providers](../llm-providers.md)
- [Use cases](../../use-cases/index.md)
