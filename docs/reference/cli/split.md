---
tags:
  - reference
  - cli
---

# split

```text
regit split [<commit-ish>] [flags]
```

Split one commit into 2–4 semantic commits using an [LLM](../llm-providers.md). The model
groups the commit's hunks into coherent pieces and proposes a message for each; regit
replays them as separate commits while preserving the combined content
[byte for byte](../../concepts/data-flow.md). By default the proposal is
only previewed — pass `--execute` to apply it.

## Positional arguments

| Argument | Default | Description |
|---|---|---|
| `<commit-ish>` | `HEAD` | The commit to split — hash, `HEAD~n`, or branch. |

## Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--llm` | VALUE | — | provider/model, e.g. `ollama/qwen2.5-coder`. |
| `--agent` | VALUE | — | Agent CLI to drive instead of HTTP. |
| `--agent-args` | VALUE | — | Replace default agent args; `{schema}` is injected. |
| `--llm-base-url` | VALUE | — | Override the OpenAI-compatible endpoint. |
| `--max-commits` | N | `0` | Clamp on resulting commits (0 = config/auto). |
| `--prompt` | VALUE | — | Short guidance passed to the LLM. |
| `--test-cmd` | VALUE | — | Validate each intermediate commit before applying. |
| `--no-cache` | BOOL | `false` | Ignore any cached proposal. |
| `--clear-cache` | BOOL | `false` | Delete the split cache and exit. |
| `--repo` | VALUE | `.` | Path to the git repository. |
| `--execute` | BOOL | `false` | Apply the split (default: dry-run preview). |
| `--force` | BOOL | `false` | Allow rewriting already-pushed commits. |
| `--sign` | BOOL | `false` | Re-sign rewritten commits with your configured signing key. |

??? example "Examples"

    Correct:

    ```bash
    # preview a split of HEAD (dry-run)
    regit split HEAD --llm=ollama/qwen2.5-coder

    # apply the split
    regit split HEAD --llm=ollama/qwen2.5-coder --execute
    ```

    Incorrect:

    ```bash
    # BAD: --yes is a deprecated alias for --execute — use --execute instead
    regit split HEAD --yes
    ```

## Limitations

- Requires an LLM (`--llm` or `--agent`).
- Skips commits that are too small, single-hunk, pure-rename, or binary-only.

## Related pages

- [reword](reword.md)
- [squash](squash.md)
- [LLM providers](../llm-providers.md)
- [Use case: split an oversized commit](../../use-cases/split-large-commit.md)
