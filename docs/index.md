---
tags:
  - home
  - overview
hide:
  - navigation
  - toc
---

# regit: safely rewrite the commit history of your current git branch — TUI + headless CLI

Bursts of work — often AI-assisted, or history that was squashed and recreated — leave every
commit stamped on the same day, with messy messages, oversized commits, and sometimes secrets
buried in past revisions. Cleaning that up with raw git means hand-editing rebase todo lists
with no preview, no date strategies, and no guarantee you didn't corrupt anything.

regit rewrites the current branch's history under a shared safety core: strategy-driven date
spreading, LLM-assisted commit splitting, rewording, and squashing, plus history hygiene
([`purge`](reference/cli/purge.md) files, [`redact`](reference/cli/redact.md) secrets). File
contents never change — every rewrite reuses the original tree objects and is verified
byte-for-byte before any ref moves — and every apply is reversible via a
[backup ref](concepts/data-flow.md) and [`undo`](reference/cli/undo.md). Everything works both as an interactive terminal UI
and as a scriptable headless CLI.

<br>

[Get started](getting-started.md){ .md-button .md-button--primary }
[View on GitHub](https://github.com/wlame/regit){ .md-button }

---

## Features

- **Date spreading ([`redate`](reference/cli/redate.md))** — 10 day-assignment strategies × 7 time-of-day models to spread same-day commit bursts across a realistic calendar.
- **LLM commit splitting ([`split`](reference/cli/split.md))** — split one commit into 2–4 semantic commits via LLM hunk grouping, with optional `--test-cmd` validation and repair loop.
- **LLM rewording and squashing ([`reword`](reference/cli/reword.md), [`squash`](reference/cli/squash.md))** — regenerated messages with ranked alternatives; consecutive commit runs collapsed into one per chain.
- **History hygiene (`purge`, `redact`)** — remove a file or directory as if never added, or remove/replace a literal secret across the branch's history.
- **Safety core** — byte-for-byte tree preservation, per-commit change-set verification, monotonic date ordering, backup refs, and in-app `undo`.
- **[Preview by default](concepts/data-flow.md)** — every mutating command shows its plan first; nothing is applied without `--execute` (and `--force` for already-pushed commits).
- **TUI + headless parity** — every TUI action is projected from one [capability registry](concepts/architecture.md) into a non-interactive command, with a command-log footer showing the exact equivalent.
- **Flexible LLM backends** — any OpenAI-compatible HTTP endpoint ([Ollama, LM Studio, OpenAI, OpenRouter](reference/llm-providers.md)) or a local agent CLI, with env-only API keys.

---

## Quick install

```bash
go install github.com/wlame/regit/cmd/regit@latest
```

---

## What's next?

<div class="grid cards" markdown>

- :material-rocket-launch: **[Getting started](getting-started.md)** — Up and running in minutes
- :material-lightbulb: **[Use cases](use-cases/index.md)** — Real-world examples
- :material-book-open: **[CLI reference](reference/cli/index.md)** — Every command and flag
- :material-shield-check: **[Concepts](concepts/architecture.md)** — The safety model and architecture

</div>

## Related pages

- [Getting started](getting-started.md)
- [About regit](about.md)
- [Use cases](use-cases/index.md)
- [CLI reference](reference/cli/index.md)
