---
tags:
  - use-cases
  - split
  - llm
---

# Split an oversized commit into semantic pieces

## Scenario

A developer reviewed their branch before opening a PR and found one giant commit
that mixes a refactor, a new feature, and its tests — the kind of commit that makes
reviewers wince. They want it broken into 2–4 focused commits, each with a coherent
change set and its own message, without manually replaying hunks through
`git add -p`.

[`regit split`](../reference/cli/split.md) sends the commit's hunks to an
[LLM](../reference/llm-providers.md), which proposes semantic groups;
regit then rebuilds the commit as a short chain of commits. The proposal is only
data — regit performs the rewrite itself through its
[guarded apply path](../concepts/data-flow.md), and the
split commits always sum to **exactly the same tree**: the safety core verifies that
the final state is byte-for-byte identical before the branch ref moves.

## Prerequisites

- An LLM backend: a local model via [Ollama](https://ollama.com) (no API key), a
  hosted provider (`OPENAI_API_KEY` / `OPENROUTER_API_KEY` / `REGIT_LLM_API_KEY`
  in the environment), or a local agent CLI via `--agent`
- A clean working tree

## Complete example

```bash
# 1. Preview: ask a local model to propose a split of HEAD
regit split HEAD --llm=ollama/qwen2.5-coder

# 2. Apply the proposed split
regit split HEAD --llm=ollama/qwen2.5-coder --execute
```

## Walkthrough

### Step 1: Preview the proposal

```bash
regit split HEAD --llm=ollama/qwen2.5-coder
```

The positional argument is any commit-ish (`HEAD` is the default — a hash or
`HEAD~n` works too). regit extracts the commit's hunks, sends them to the model
with repository context, and prints the proposed commit groups: each planned
commit's title and the hunks assigned to it. Nothing is applied — split, like all
mutating verbs, previews by default.

The proposal is cached (keyed by the commit and every input that affects the
output), so re-running the command — including the `--execute` run — reuses it
instead of paying for a second LLM call.

### Step 2: Apply

```bash
regit split HEAD --llm=ollama/qwen2.5-coder --execute
```

regit takes a backup ref, replays the branch with the one commit replaced by the
proposed chain, then verifies the end state: the tree after the last split commit
must be identical to the original commit's tree. File contents never change —
only how the change is sliced into commits.

### Step 3: Undo if needed

```bash
regit undo
```

[`undo`](../reference/cli/undo.md) restores the branch to the pre-split backup.

## Expected output

The preview lists the proposed commits (title plus the hunks grouped under each)
and ends with the standard dry-run notice; the `--execute` run reports the new
HEAD and the backup ref, and confirms verification passed. Exit codes: `0` ok /
preview, `1` usage error, `2` guard blocked (dirty tree, pushed commits without
`--force`), `3` apply/LLM error.

Commits that are too small to split, single-hunk, pure-rename, or binary-only are
skipped rather than force-split.

## Variations

### Validate every intermediate commit

```bash
regit split HEAD --llm=ollama/qwen2.5-coder --test-cmd='go test ./...' --execute
```

`--test-cmd` runs the given command against each intermediate commit in a
[scratch worktree](../concepts/data-flow.md). If a midpoint fails (e.g. code split apart
from the declaration it needs), regit feeds the failure back to the LLM in a repair loop
and retries with an adjusted grouping. The command runs with a
[scrubbed environment](../reference/llm-providers.md) — API keys and tokens are stripped
first.

### Cap the number of commits

```bash
regit split HEAD --llm=ollama/qwen2.5-coder --max-commits=2
```

`--max-commits=N` clamps the proposal (`0` means the config default, 4).

### Guide the grouping

```bash
regit split HEAD --llm=ollama/qwen2.5-coder --prompt="separate the API changes from the test updates"
```

`--prompt` passes short free-text guidance to the model.

### Use a hosted model or an agent CLI

```bash
regit split HEAD --llm=openai/gpt-4o
regit split HEAD --agent=claude
```

`--llm=provider/model` covers any OpenAI-compatible endpoint (`ollama`,
`lmstudio`, `openai`, `openrouter`, or `--llm-base-url=URL` for a custom one);
`--agent` drives a local agent CLI instead, reusing its existing authentication.

### Cache controls

```bash
regit split HEAD --llm=ollama/qwen2.5-coder --no-cache=true   # ignore any cached proposal
regit split --clear-cache=true                                 # delete the split cache and exit
```

## Related pages

- [split command reference](../reference/cli/split.md)
- [LLM providers](../reference/llm-providers.md) — backends, base URLs, API-key environment variables
- [Configuration](../reference/configuration.md) — `split.*` keys: max commits, context, repair attempts, cache
- [undo command reference](../reference/cli/undo.md)
