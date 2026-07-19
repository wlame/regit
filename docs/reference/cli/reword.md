---
tags:
  - reference
  - cli
---

# reword

```text
regit reword [<commit-ish>…] [flags]
```

Regenerate commit message(s) with an [LLM](../llm-providers.md). Each targeted commit's diff (plus optional
context such as the README or file states) is sent to the model, which proposes a new
title and body; only the message changes — trees, dates, and authorship are untouched.
Headless runs request exactly one proposal per commit and preview it until `--execute`.

## Positional arguments

| Argument | Default | Description |
|---|---|---|
| `<commit-ish>…` | `HEAD` | The commit(s) to reword. |

## Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--llm` | VALUE | — | provider/model, e.g. `ollama/qwen2.5-coder`. |
| `--agent` | VALUE | — | Agent CLI to drive instead of HTTP. |
| `--agent-args` | VALUE | — | Replace default agent args. |
| `--agent-scan` | BOOL | `false` | Agent may browse repo files read-only for extra context (requires `--agent`). |
| `--llm-base-url` | VALUE | — | Override the OpenAI-compatible endpoint. |
| `--prompt` | VALUE | — | Short guidance passed to the LLM. |
| `--context` | A,B | — | Context keys to include, e.g. `readme,filestates`. |
| `--context-lines` | N | `0` | `git diff -U` depth (0 = config default). |
| `--no-cache` | BOOL | `false` | Ignore cached proposals. |
| `--clear-cache` | BOOL | `false` | Delete the reword cache and exit. |
| `--repo` | VALUE | `.` | Path to the git repository. |
| `--execute` | BOOL | `false` | Apply the reword (default: dry-run preview). |
| `--force` | BOOL | `false` | Allow rewriting already-pushed commits. |
| `--sign` | BOOL | `false` | Re-sign rewritten commits with your configured signing key. |

??? example "Examples"

    Correct:

    ```bash
    # preview a new message for HEAD
    regit reword HEAD --llm=ollama/qwen2.5-coder

    # reword two commits and apply
    regit reword HEAD~2 HEAD --llm=openai/gpt-4o --execute
    ```

    Incorrect:

    ```bash
    # BAD: --agent-scan without --agent is an error — scan mode drives an agent CLI
    regit reword --agent-scan HEAD
    ```

## Limitations

- Headless always requests exactly one variant (the TUI browses several).
- [`--agent-scan`](../llm-providers.md) requires `--agent`.

## Related pages

- [split](split.md)
- [retitle](retitle.md)
- [LLM providers](../llm-providers.md)
- [Use case: polish commit messages](../../use-cases/polish-commit-messages.md)
