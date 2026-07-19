---
tags:
  - reference
  - configuration
---

# Configuration

regit reads an optional YAML config file that seeds the TUI's forms and provides
defaults for the headless CLI. Every value has a built-in default, so the file is
entirely optional.

## File location

```
$XDG_CONFIG_HOME/regit/config.yaml
```

falling back to:

```
~/.config/regit/config.yaml
```

A missing file is fine — regit runs on built-in defaults.

## Precedence

Values are resolved lowest to highest precedence:

1. Built-in defaults
2. YAML config file
3. `REGIT_*` environment variables (env wins)

!!! warning "Loading is strict"
    A typo'd key or unusable value is a **hard error at startup**, not a silent
    fallback: unknown YAML keys, an unrecognized `timezone`, and an inverted
    workday window (`workday_end` not after `workday_start`) all abort with a
    clear message. This keeps a misspelled key from silently reverting a setting
    to its default.

## Top-level keys

| Key | Type | Default | Env var | Description |
|-----|------|---------|---------|-------------|
| `timezone` | string | `local` | `REGIT_TIMEZONE` | IANA zone name (e.g. `Europe/Berlin`) or `local` for the machine zone. Used when computing new commit dates. |
| `workday_start` | HH:MM | `09:00` | `REGIT_WORKDAY_START` | Start of the default working-hours window. |
| `workday_end` | HH:MM | `18:00` | `REGIT_WORKDAY_END` | End of the working-hours window. Must be after `workday_start`. |
| `lunch_start` | HH:MM | *(empty)* | — | Start of an optional lunch gap that time models avoid. Empty disables the gap. |
| `lunch_end` | HH:MM | *(empty)* | — | End of the lunch gap. |
| `default_strategy` | string | `Even Spread` | `REGIT_DEFAULT_STRATEGY` | Day-assignment strategy pre-selected when the TUI launches. |
| `default_time_model` | string | `Working hours` | `REGIT_DEFAULT_TIME_MODEL` | Time-of-day model pre-selected when the TUI launches. |
| `jitter_minutes` | int | `20` | — | Default ± spread (in minutes) for the Jitter time model. |
| `link_dates` | bool | `true` | `REGIT_LINK_DATES` | Keep author and committer dates equal by default. |
| `weekdays` | list of strings | `[mon, tue, wed, thu, fri]` | — | Enabled weekday short names (`sun`…`sat`) for weekday-masked strategies. |
| `seed` | int64 | `0` | `REGIT_SEED` | Seed for randomized strategies, making them reproducible. `0` means "derive from the current time". |

## `llm` section

Configures the optional language model used by [`split`](cli/split.md),
[`reword`](cli/reword.md), and [`squash`](cli/squash.md).
Off unless a provider/model is set here or `--llm`/`--agent` is passed. See
[LLM providers](llm-providers.md) for the full provider and credential story.

| Key | Type | Default | Env var | Description |
|-----|------|---------|---------|-------------|
| `llm.provider` | string | *(empty)* | `REGIT_LLM_PROVIDER` | Default provider (`ollama`, `lmstudio`, `openai`, `openrouter`). |
| `llm.model` | string | *(empty)* | `REGIT_LLM_MODEL` | Default model name for the provider. |
| `llm.base_url` | string | *(empty)* | — | Override the provider's default OpenAI-compatible endpoint. |
| `llm.temperature` | float | `0` | — | Accepted but currently unused — chat requests always send temperature `0` for deterministic output. |
| `llm.timeout_sec` | int | `60` | — | Per-request timeout in seconds for HTTP backends. |

!!! note "API keys are not config"
    There is no API-key config field, by design. Keys come only from environment
    variables (`REGIT_LLM_API_KEY`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY`) —
    see [LLM providers](llm-providers.md#api-keys).

## `split` section

Defaults for the LLM-backed commit-splitting feature.

| Key | Type | Default | Env var | Description |
|-----|------|---------|---------|-------------|
| `split.max_commits` | int | `4` | `REGIT_SPLIT_MAX_COMMITS` | Maximum commits one split can produce. |
| `split.lines_per_commit` | int | `40` | — | Target changed-line count per resulting commit, used to pick the split size. |
| `split.min_split_lines` | int | `20` | — | Minimum changed lines a commit needs before splitting it is considered. |
| `split.recursion_depth` | int | `2` | — | Accepted but currently unused by the split engine. |
| `split.context` | list of strings | `[readme]` | — | Extra context sources included in the prompt (e.g. `readme`). |
| `split.repair_attempts` | int | `2` | — | LLM repair-loop attempts when `--test-cmd` validation fails. |
| `split.wip_prefix` | string | `"WIP: "` | — | Subject prefix marking intermediate work-in-progress commits. |
| `split.cache` | bool | `true` | — | Accepted but currently unused — split proposals are always cached under `.git/regit/cache/`; pass `--no-cache` to bypass per run. |

## `reword` section

Defaults for the LLM-backed commit-rewording feature.

| Key | Type | Default | Env var | Description |
|-----|------|---------|---------|-------------|
| `reword.variants` | int | `3` | `REGIT_REWORD_VARIANTS` | Number of ranked message alternatives to generate per commit. |
| `reword.context` | list of strings | `[readme]` | — | Extra context sources included in the prompt (e.g. `readme`, `filestates`). |
| `reword.context_lines` | int | `30` | — | Diff context lines sent to the model. |
| `reword.prefetch` | int | `1` | — | Accepted but currently unused — the TUI always prefetches proposals one commit ahead. |
| `reword.cache` | bool | `true` | — | Accepted but currently unused — reword proposals are always cached under `.git/regit/cache/`; pass `--no-cache` to bypass per run. |

## Environment overrides

Every supported `REGIT_*` override (env beats the file; unparsable numbers and
bools are ignored and fall back to the file/default):

- `REGIT_TIMEZONE`
- `REGIT_WORKDAY_START`
- `REGIT_WORKDAY_END`
- `REGIT_DEFAULT_STRATEGY`
- `REGIT_DEFAULT_TIME_MODEL`
- `REGIT_SEED`
- `REGIT_LINK_DATES`
- `REGIT_LLM_PROVIDER`
- `REGIT_LLM_MODEL`
- `REGIT_SPLIT_MAX_COMMITS`
- `REGIT_REWORD_VARIANTS`

## Full example

A complete `config.yaml` showing every key at its built-in default:

```yaml
timezone: local
workday_start: "09:00"
workday_end: "18:00"
lunch_start: ""
lunch_end: ""
default_strategy: Even Spread
default_time_model: Working hours
jitter_minutes: 20
link_dates: true
weekdays: [mon, tue, wed, thu, fri]
seed: 0

llm:
  provider: ""
  model: ""
  base_url: ""
  temperature: 0
  timeout_sec: 60

split:
  max_commits: 4
  lines_per_commit: 40
  min_split_lines: 20
  recursion_depth: 2
  context: [readme]
  repair_attempts: 2
  wip_prefix: "WIP: "
  cache: true

reword:
  variants: 3
  context: [readme]
  context_lines: 30
  prefetch: 1
  cache: true
```

## Related pages

- [LLM providers](llm-providers.md) — provider setup, API keys, and the agent backend
- [CLI reference](cli/index.md) — flags that override these defaults per invocation
- [redate](cli/redate.md) — the strategies and time models these defaults seed
- [Getting started](../getting-started.md) — first run without any configuration
