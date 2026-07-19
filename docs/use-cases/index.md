---
tags:
  - use-cases
---

# Use cases

regit excels in these scenarios. Each page shows a complete working example with a
step-by-step walkthrough, the exact output to expect, and common variations. All of
them follow the same rhythm: run the command to **preview**, inspect the plan, then
re-run with `--execute` to apply — with a [backup ref](../concepts/data-flow.md) and
[`regit undo`](../reference/cli/undo.md) behind you.

<div class="grid cards" markdown>

- :material-calendar-range: **[Spread same-day commits](spread-same-day-commits.md)** — an AI-assisted burst left 30 commits stamped today; spread them over past weeks with [`redate`](../reference/cli/redate.md)
- :material-call-split: **[Split an oversized commit](split-large-commit.md)** — one giant commit mixing refactor, feature, and tests becomes 2–4 semantic commits via [`split`](../reference/cli/split.md) and an [LLM](../reference/llm-providers.md)
- :material-message-draw: **[Polish commit messages](polish-commit-messages.md)** — pre-push cleanup with [`reword`](../reference/cli/reword.md), [`squash`](../reference/cli/squash.md), [`retitle`](../reference/cli/retitle.md), and [`drop-bodies`](../reference/cli/drop-bodies.md)
- :material-shield-lock: **[Remove secrets and files from history](remove-secrets-from-history.md)** — purge an accidentally tracked build directory and redact a leaked API key with [`purge`](../reference/cli/purge.md) and [`redact`](../reference/cli/redact.md)

</div>

## Related pages

- [Getting started](../getting-started.md) — install and first run
- [CLI reference](../reference/cli/index.md) — every command and flag
- [Data flow](../concepts/data-flow.md) — the preview/execute convention and the guarded apply protocol
