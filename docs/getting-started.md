---
tags:
  - installation
  - quickstart
---

# Getting started

Get from a fresh clone to a previewed history rewrite in under 10 minutes.

## Prerequisites

- **`git` on your `PATH`** — regit performs all repository access by shelling out to the real
  `git` binary; there is no bundled git.
- **Go 1.24+** — needed to install. Both install methods below compile from source
  (there are no prebuilt binaries yet).
- **Optional: an [LLM provider](reference/llm-providers.md)** — only the
  [`split`](reference/cli/split.md), [`reword`](reference/cli/reword.md), and
  [`squash`](reference/cli/squash.md) verbs use an LLM
  (Ollama, LM Studio, OpenAI, OpenRouter, or a local Claude Code CLI via `--agent`).
  Everything else — [`redate`](reference/cli/redate.md), [`retitle`](reference/cli/retitle.md),
  [`reauthor`](reference/cli/reauthor.md), [`drop-bodies`](reference/cli/drop-bodies.md),
  [`purge`](reference/cli/purge.md), [`redact`](reference/cli/redact.md),
  [`linearize`](reference/cli/linearize.md), [`undo`](reference/cli/undo.md) — works with no
  LLM configured.

## Installation

=== "go install"

    ```bash
    go install github.com/wlame/regit/cmd/regit@latest
    ```

    Installs the `regit` binary into your Go bin directory (`$GOBIN` or `$GOPATH/bin`).

=== "From source"

    ```bash
    git clone https://github.com/wlame/regit
    cd regit
    just build          # compiles to dist/regit with the version stamped
    ```

    Or install straight onto your `PATH` (uses `go install` under the hood):

    ```bash
    just install
    ```

Verify the install:

```bash
regit --version
```

Expected output:

```
regit dev
```

(`just build` and `just install` stamp a real version from git tags; a plain
`go install` build reports `dev`.)

## First run

### Launch the TUI

Run regit inside any git repository, or point it at one:

```bash
regit                       # operate on the repo in the current directory
regit --repo=/path/to/repo
```

The TUI opens on your current branch's commits. Press `?` for the full key overlay,
++p++ to preview pending changes, and ++q++ to quit. Each pane's title shows one
red letter: press it to focus that pane (++o++ commits, ++c++ calendar, ++t++
strategy).

On a terminal narrower than 88 columns the TUI uses one column. An open flow,
such as the date calendar or the redact wizard, then takes the place of the
commit list. In the date flow, press ++tab++ to switch between the flow and the
list.

### A first headless command

Every TUI action has a headless CLI equivalent, and every mutating command
**[previews by default](concepts/data-flow.md)** — running it without `--execute` is always
safe and changes nothing. Try a date
plan:

```bash
regit redate --strategy=Even-Spread --time=Working-hours --start=2026-01-01 --end=2026-03-01
```

This prints a date plan: one line per commit showing its short hash and the current author
date `->` proposed new date, followed by an ordering check. The shape looks like this
(hashes and dates come from your repository):

```
Date plan (3 commit(s)):
  4c9d1e2a  2026-07-19 14:02 -> 2026-01-06 10:41
  9b7f03c5  2026-07-19 14:02 -> 2026-02-03 11:27
  d41a8f60  2026-07-19 14:03 -> 2026-02-27 15:55
  ✓ sequential

(dry run — nothing applied; pass --execute to apply)
```

To actually rewrite the branch, re-run the same command with `--execute`:

```bash
regit redate --strategy=Even-Spread --time=Working-hours \
  --start=2026-01-01 --end=2026-03-01 --execute
```

On success regit prints the new HEAD, the backup ref it created, the verifier's report, and
— when the branch tracks a remote — the exact `git push --force-with-lease` command to
publish the rewrite.

!!! warning "Already-pushed commits need `--force`"
    If the range includes commits that already exist on the remote, `--execute` alone is
    refused. Pass `--execute --force` to confirm you really want to rewrite pushed history —
    collaborators who pulled it will need to reconcile after your force-push.

### Undo: the escape hatch

A [backup ref](concepts/data-flow.md) (`refs/regit/backup/<branch>/…`) is created before
**every** apply. If you change your mind:

```bash
regit undo          # restore the branch to the most recent regit backup
regit undo --list   # show available backups (read-only)
```

`undo` itself backs up the current tip first, so an undo is also reversible.

## Optional: register as `git re`

regit can [install](reference/cli/install.md) itself as a native-feeling git subcommand by
symlinking `git-re` next to the `regit` binary on your `PATH`:

```bash
regit install
git re              # → launches the regit TUI
git re redate --help
```

`git re <command>` and `regit <command>` are fully equivalent, and regit's help and hints
automatically show whichever form you used. `regit uninstall` removes the link. One git
quirk: a bare `git re --help` is answered by git's man-page lookup — use `git re -h` for
the command list.

## Related pages

- [Use cases](use-cases/index.md) — complete walkthroughs of real scenarios
- [CLI reference](reference/cli/index.md) — every command, flag, and exit code
- [Configuration](reference/configuration.md) — config file keys and `REGIT_*` env overrides
- [LLM providers](reference/llm-providers.md) — set up Ollama, OpenAI, or an agent CLI for split/reword/squash
