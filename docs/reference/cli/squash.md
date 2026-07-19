---
tags:
  - reference
  - cli
---

# squash

```text
regit squash <range>… [flags]
```

Squash consecutive commits into one per chain. Each given range becomes a single commit
whose content equals the chain's combined result; the message defaults to the first
commit's, or an [LLM](../llm-providers.md) can summarize the whole chain when
`--llm`/`--agent` is given.
Several chains can be squashed in one run.

## Positional arguments

| Argument | Default | Description |
|---|---|---|
| `<range>…` | required | Chain spec(s), git range semantics: `A^..B` includes A; comma- or space-separated for several chains. |

## Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--llm` | VALUE | — | provider/model, e.g. `ollama/qwen2.5-coder`. |
| `--agent` | VALUE | — | Agent CLI to drive instead of HTTP. |
| `--agent-args` | VALUE | — | Replace default agent args; `{schema}` is injected. |
| `--llm-base-url` | VALUE | — | Override the OpenAI-compatible endpoint. |
| `--with-diffs` | BOOL | `false` | Include each commit's diff in the LLM input. |
| `--prompt` | VALUE | — | Short guidance passed to the LLM. |
| `--no-cache` | BOOL | `false` | Ignore any cached summary. |
| `--clear-cache` | BOOL | `false` | Delete the squash cache and exit. |
| `--repo` | VALUE | `.` | Path to the git repository. |
| `--execute` | BOOL | `false` | Apply the squash (default: dry-run preview). |
| `--force` | BOOL | `false` | Allow rewriting already-pushed commits. |
| `--sign` | BOOL | `false` | Re-sign rewritten commits with your configured signing key. |

??? example "Examples"

    Correct:

    ```bash
    # preview squashing a chain (dry-run); ^ includes 6f3a
    regit squash 6f3a^..a2df

    # squash several chains at once
    regit squash da24bc9b..685199d3,687114d3,6ffd7976..a2df4cc2 --execute

    # LLM summary using diffs, then apply
    regit squash 6f3a^..a2df --llm=ollama/qwen2.5-coder --with-diffs --execute
    ```

    Easy to get wrong:

    ```bash
    # git semantics: A..B EXCLUDES da24bc9b — use da24bc9b^..685199d3 to include it
    regit squash da24bc9b..685199d3
    ```

## Limitations

- `A..B` uses git semantics: commits AFTER A through B (A excluded). Use `A^..B` to include A.
- Ignores singletons (a selected commit with no selected neighbor).
- Cannot squash across a merge commit.
- Works without an LLM (first commit's message is the default); `--llm`/`--agent` enables a summary.

Exit codes follow the [standard set](index.md#standard-exit-codes); exit `0` also covers
"nothing to squash".

## Related pages

- [split](split.md)
- [linearize](linearize.md)
- [LLM providers](../llm-providers.md)
- [Use case: polish commit messages](../../use-cases/polish-commit-messages.md)
