---
tags:
  - reference
  - llm
---

# LLM providers

The [`split`](cli/split.md), [`reword`](cli/reword.md), and [`squash`](cli/squash.md)
verbs use a language model to propose hunk groupings, commit messages, and squash
summaries. This page covers how to point
regit at a provider, where credentials come from, and what the model is — and is
not — allowed to do.

## LLM features are opt-in

regit works fully without any LLM configured: [`redate`](cli/redate.md),
[`retitle`](cli/retitle.md), [`reauthor`](cli/reauthor.md),
[`drop-bodies`](cli/drop-bodies.md), [`linearize`](cli/linearize.md),
[`purge`](cli/purge.md), [`redact`](cli/redact.md), and [`undo`](cli/undo.md) never touch
a model.
Running an LLM-backed verb with nothing configured fails fast with a clear
message telling you to pass `--llm=provider/model` or `--agent=<cmd>` — nothing
else is affected.

## Flag surface

| Flag | Purpose |
|------|---------|
| `--llm=<provider>/<model>` | Select an HTTP provider and model, e.g. `--llm=ollama/qwen2.5-coder`. |
| `--llm-base-url=<url>` | Override the provider's default endpoint (self-hosted, vLLM, proxies). |
| `--agent=<cmd>` | Drive a local agent CLI (e.g. `claude`) as the backend instead of HTTP. |
| `--agent-args=<argv>` | Advanced: replace the default agent argv; a `{schema}` placeholder is substituted with the JSON schema at call time. |
| `--prompt=<hint>` | Free-text guidance appended to the model's instructions. |
| `--no-cache` | Ignore the proposal cache and recompute. |
| `--clear-cache` | Wipe the feature's cached proposals and exit — nothing is proposed or applied. |

!!! note "Provider/model parsing"
    `--llm` is parsed on the **first** slash only, so models whose names contain
    slashes work naturally: `--llm=openrouter/anthropic/claude-x` means provider
    `openrouter`, model `anthropic/claude-x`.

## Providers

| Provider | Default base URL | API key env var |
|----------|------------------|-----------------|
| `ollama` | `http://localhost:11434/v1` | none (local) |
| `lmstudio` | `http://localhost:1234/v1` | none (local) |
| `openai` | `https://api.openai.com/v1` | `OPENAI_API_KEY` |
| `openrouter` | `https://openrouter.ai/api/v1` | `OPENROUTER_API_KEY` |

All providers speak the OpenAI-compatible `/v1/chat/completions` API, so any
compatible server works via `--llm-base-url=`.

## API keys

API keys come from environment variables **only** — never from flags, the config
file, or logs. Resolution order:

1. `REGIT_LLM_API_KEY` — tool-wide override, beats the provider-standard vars
2. Provider-standard var: `OPENAI_API_KEY`, `OPENROUTER_API_KEY`
3. None — local providers (`ollama`, `lmstudio`) need no key

```bash
export OPENAI_API_KEY=sk-...
regit reword HEAD --llm=openai/gpt-4o
```

## Setting a default provider

Set `llm.provider` and `llm.model` in the [config file](configuration.md#llm-section),
or export `REGIT_LLM_PROVIDER` / `REGIT_LLM_MODEL`, and the `--llm` flag can be
omitted entirely:

```yaml
llm:
  provider: ollama
  model: qwen2.5-coder
```

```bash
regit split HEAD          # uses ollama/qwen2.5-coder from config
```

## Agent backend

`--agent=<cmd>` drives a local agent CLI as a subprocess instead of calling an
HTTP API, reusing the host's existing agent session for auth — no API key
needed. The default command drives `claude` headless:

```
claude -p --output-format json --bare --json-schema {schema}
```

The prompt is fed on stdin. `--bare` skips the host project's CLAUDE.md, hooks,
plugins, and MCP servers so output stays deterministic. Calls are bounded by a
5-minute timeout. `--agent-args=` replaces this argv when you need a different
agent binary or flags; keep the `{schema}` placeholder so regit can inject the
JSON schema.

## Trust model

The model only ever **proposes structured data** — a hunk grouping, a set of
message variants, a squash summary. regit validates the proposal against a
schema and its own invariants, then performs the git operations itself through
the same [guarded apply path](../concepts/data-flow.md) every verb uses (backup ref,
content verification, undo). The LLM never mutates the repository.

!!! warning "Agent scan is defense-in-depth, not a sandbox"
    `reword --agent-scan` (requires `--agent`) lets the agent browse repository
    files for extra context. Access is deliberately limited to the read-only
    file tools **Read, Grep, and Glob — no shell of any kind**: even
    innocent-looking read-only commands can escape (`git show` accepts
    `--output=<file>` to write arbitrary files; `find` allows `-exec` to run
    arbitrary commands), so no `Bash(...)` allowance is safe. regit also scrubs
    secret-bearing environment variables (names containing `API_KEY`, `TOKEN`,
    `SECRET`, `PASSWORD`, `AWS_`, `GH_`, `GITHUB_`, `OPENAI`,
    `OPENROUTER`, or `ANTHROPIC`) before spawning the agent or a `--test-cmd`, and
    denies the agent access to SSH/AWS/GPG key stores, `/etc`, `/proc`, and
    `.env` files. Treat all of this as defense-in-depth for a mostly-trusted
    local agent — not as a sandbox for running against untrusted repositories.

## Caching

Proposals are cached repo-scoped under `.git/regit/cache/`, keyed by a
fingerprint of every input that affects the output (commit content, feature
parameters, prompt hint) plus the model name — changing any of them naturally
misses the cache. `--no-cache` recomputes without reading the cache;
`--clear-cache` wipes the feature's cache and exits without running.

## Related pages

- [Configuration](configuration.md) — the `llm`, `split`, and `reword` config sections
- [split](cli/split.md) — split one commit into semantic commits
- [reword](cli/reword.md) — regenerate commit messages, including `--agent-scan`
- [squash](cli/squash.md) — collapse commit runs with LLM summaries
