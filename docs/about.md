---
tags:
  - about
  - overview
---

# About regit

## Why this exists

Modern development workflows produce histories that are technically correct but tell the wrong
story. A day of AI-assisted work can land as thirty commits with identical timestamps; a branch
recreated from a squash loses its calendar entirely; a hasty commit bundles three unrelated
changes under a "WIP" message; a credential pasted into a config file two weeks ago is still
retrievable from every subsequent revision.

Git's own tools can fix each of these, but awkwardly. Interactive rebase is manual todo-list
surgery with no preview of the outcome. Date manipulation means hand-crafting
`GIT_COMMITTER_DATE` incantations per commit. History-wide filtering tools exist, but they are
batch filters, not interactive workflows, and none of them verify that your file contents
survived the rewrite untouched.

regit narrows the problem deliberately: one branch, the one you have checked out, rewritten
under a single safety protocol. Within that scope it goes deep — ten date-spreading strategies
crossed with seven time-of-day models, LLM-assisted splitting/rewording/squashing,
[purge](reference/cli/purge.md) and [redact](reference/cli/redact.md) for history hygiene — all available both as a terminal UI and as a scriptable headless
CLI with identical behavior.

## Design principles

1. **Shell out to the real git binary, don't reimplement it.** All git access goes through one
   package ([`internal/gitio`](concepts/architecture.md)) that runs the `git` on your PATH via
   subprocess. A pure-Go git
   implementation was rejected on purpose: the real binary already honors your config, hooks,
   and signing setup exactly, and every operation regit performs is a plain git command you
   could run and audit yourself. The trade-off is a hard runtime dependency on `git`.

2. **One [capability registry](concepts/architecture.md), many projections.** Every action is
   a row in a single in-code registry (`internal/capability/registry.go`). The CLI dispatch, the TUI menu, `--help`,
   the agent-facing `--llm-help`, and the TUI's "equivalent command" footer are all generated
   from the same rows — so the interactive and headless surfaces cannot drift apart.

3. **[Preview by default.](concepts/data-flow.md)** Mutating commands print their plan and exit; nothing is applied
   without `--execute`, and rewriting already-pushed commits additionally requires `--force`
   (the TUI asks for a typed branch-name confirmation instead). The safe path is the default
   path, not an opt-in.

4. **File contents never change.** Rewrites reuse the original tree objects via
   `git commit-tree`, and before any ref moves, a [verifier](concepts/data-flow.md) proves each old/new commit pair is
   content-identical byte for byte and that the rewritten chain has the expected shape. A bug
   in regit can fail your rewrite; it cannot silently alter your files.

5. **The model proposes; the tool disposes.** LLM backends return structured data — hunk
   groupings, message candidates, summaries — which regit validates against a schema and then
   applies through its own deterministic, guarded path. No model or agent ever mutates the
   repository directly, and API keys are read only from the environment.

6. **Every apply is reversible.** A branch-qualified backup ref is written before each apply,
   an in-app undo stack tracks what happened, and [`regit undo`](reference/cli/undo.md) restores the previous state —
   itself backed up first, so even undo can be undone.

## Known limitations

- **Current-branch scope.** regit rewrites the first-parent history of the checked-out branch
  only. It is not a repository-wide filtering tool.
- **`purge` and `redact` are branch-scoped.** Other refs and the reflog keep the old objects
  until `git gc` runs, so purged files and redacted secrets remain recoverable from the
  repository until then. The secret also lands in `.git/regit/command.log` and your shell
  history as part of the command line. Treat redaction as cleanup, not containment: **rotate
  the credential**.
- **Merge commits are refused by most verbs.** The guarded apply protocol works on a linear
  chain; run [`linearize`](reference/cli/linearize.md) first to fold merge topology into a
  first-parent chain (or use [`redate --allow-merges`](reference/cli/redate.md) where
  supported).
- **Runtime and build requirements.** A `git` binary on PATH is required at runtime; building
  from source requires Go 1.26+.

## Comparison

Honest scoping: these tools solve overlapping but different problems.

| | regit | `git rebase -i` | git filter-repo / BFG |
|---|---|---|---|
| Scope | Current branch | Current branch | Entire repository, all refs |
| Interface | TUI + headless CLI | Todo-list editor | Batch CLI |
| Date strategies | 10 strategies × 7 time models | Manual per-commit env vars | None |
| Preview before apply | Default | No | No |
| Content verification | Byte-for-byte, before ref moves | No | No |
| Built-in undo | Backup ref + `undo` | Manual reflog surgery | Manual |
| Remove file/secret from history | Branch-scoped `purge` / `redact` | Impractical | Yes, history-wide |

If you need to scrub a secret from *every* ref of a repository you intend to force-push
everywhere, git filter-repo or BFG is the right tool. regit's purge and redact trade that
breadth for the same preview/verify/undo protocol as every other verb, on the branch you are
actually working on.

## Status

Pre-release. There are no version tags yet; builds stamp their version from git metadata and
default to `dev`. Interfaces and flags may still change before a first tagged release.

## License

MIT © wlame.

## Related pages

- [Getting started](getting-started.md)
- [Use cases](use-cases/index.md)
- [Concepts](concepts/architecture.md)
- [CLI reference](reference/cli/index.md)
