---
tags:
  - use-cases
  - reword
  - squash
  - retitle
---

# Polish commit messages before pushing

## Scenario

The feature works and the branch is about to be pushed, but the log is a mess:
"wip", "fix", half-sentence titles, a run of five tiny fixup commits, and a
`feat:` prefix convention the target repo doesn't use. A developer wants to clean
all of this up in a few minutes — regenerate the weak messages, collapse the fixup
runs, normalize the titles — without touching a single file. Every verb below
rewrites messages and metadata only; trees are reused and verified unchanged.

All four commands **preview by default** and apply with `--execute`. Exit codes
throughout: `0` ok / preview, `1` usage error, `2` guard blocked, `3` apply/LLM
error.

## Prerequisites

- A clean working tree
- For [`reword`](../reference/cli/reword.md) (and LLM-summarized
  [`squash`](../reference/cli/squash.md)): an [LLM backend](../reference/llm-providers.md) — e.g.
  `OPENAI_API_KEY` exported for `--llm=openai/gpt-4o`, or a local model via
  `--llm=ollama/qwen2.5-coder`

## Complete example

```bash
# 1. Regenerate the two weakest messages
regit reword HEAD~2 HEAD --llm=openai/gpt-4o
regit reword HEAD~2 HEAD --llm=openai/gpt-4o --execute

# 2. Collapse a run of fixup commits into one (A^..B includes A)
regit squash 68e4dc2f^..d8cae3ce
regit squash 68e4dc2f^..d8cae3ce --execute

# 3. Strip a conventional-commit prefix from recent titles
regit retitle --match='^feat: ' --replace='' --commits=HEAD~5..HEAD
regit retitle --match='^feat: ' --replace='' --commits=HEAD~5..HEAD --execute

# 4. Drop noisy bodies, keep the titles
regit drop-bodies --commits=HEAD~10..HEAD --execute
```

## Walkthrough

### Step 1: Reword weak messages with an LLM

```bash
regit reword HEAD~2 HEAD --llm=openai/gpt-4o
```

Takes any number of commit-ishes (default `HEAD`). For each commit the model sees
the diff plus repository context (`--context=A,B`, e.g. `readme,filestates`;
`--context-lines=N` controls diff depth) and proposes a replacement title and
body. Headless mode requests exactly one proposal per commit; re-run with
`--execute` to apply it. Ranked title/body alternatives live in the TUI — run
plain `regit`, where reword lets you browse several variants per commit with
background prefetch.

### Step 2: Squash fixup runs

```bash
regit squash 68e4dc2f^..d8cae3ce
```

Range semantics are git's own: `A..B` **excludes** `A`, so use `A^..B` to include
it. Several chains can be given comma- or space-separated, each collapsing into
one commit:

```bash
regit squash da24bc9b..685199d3,687114d3,6ffd7976..a2df4cc2 --execute
```

Preview of a three-commit chain:

```
Squash 3 commit(s) into 1 commit(s)
  3 commits → "Add feature part 2." · wlame <vvlame@gmail.com> · 2026-07-19 14:00 · 68e4dc2f..d8cae3ce

(dry run — nothing applied; pass --execute to apply)
```

Squash works without an LLM (the first commit's message is kept). Add
`--llm=…` for a generated summary of the whole chain, and `--with-diffs=true` to
include each commit's diff in the LLM input — better summaries at the cost of a
larger prompt. Singletons (a selected commit with no selected neighbor) are
ignored, and a chain cannot cross a merge commit.

### Step 3: Normalize titles with a regex

```bash
regit retitle --match='^feat: ' --replace='' --commits=HEAD~5..HEAD
```

`--match` is an RE2 regex tested against each subject; `--replace` supports `$1`
capture references. Preview output lists every change:

```
Title changes:
  1aa390cf  "feat: add rate limiter" -> "add rate limiter"
  d8cae3ce  "feat: wire limiter into router" -> "wire limiter into router"

(dry run — nothing applied; pass --execute to apply)
```

Subjects that would become empty are skipped.

### Step 4: Drop leftover bodies

```bash
regit drop-bodies --commits=HEAD~10..HEAD --execute
```

Strips the body of each commit in the range and keeps titles untouched; commits
without a body are skipped. Omit `--commits` to cover the whole branch.

## Expected output

Every command prints its full plan (new messages, squash chains, title diffs)
followed by `(dry run — nothing applied; pass --execute to apply)`. With
`--execute` it reports the new HEAD and the [backup ref](../concepts/data-flow.md), e.g.
`Applied. New HEAD a6d3d8f6 (backup refs/regit/backup/main/1784484349-0)` — and
[`regit undo`](../reference/cli/undo.md) restores that backup if you change your mind. Rewriting
already-pushed commits additionally requires `--force`.

## Variations

- **Local model instead of a hosted one** — `--llm=ollama/qwen2.5-coder` needs no
  API key; `--agent=claude` drives a local agent CLI instead of HTTP.
- **Guide the tone** — `--prompt="imperative mood, no scope prefixes"` steers
  reword and squash summaries.
- **Extra context for reword** — [`--agent-scan=true`](../reference/llm-providers.md)
  (requires `--agent`) lets the agent browse repository files read-only for more context.
- **Cache controls** — `--no-cache=true` ignores cached proposals;
  `--clear-cache=true` deletes a verb's cache and exits.

## Related pages

- [reword command reference](../reference/cli/reword.md)
- [squash command reference](../reference/cli/squash.md)
- [retitle command reference](../reference/cli/retitle.md)
- [drop-bodies command reference](../reference/cli/drop-bodies.md)
