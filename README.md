# regit

A terminal UI for **safely rewriting the commit and author dates** of your
current git branch. Built for the common situation where a burst of work — often
AI-assisted, or a recreated/squashed history — leaves every commit stamped on the
same day. `regit` lets you spread those commits across a calendar with a rich set
of strategies, edit messages, and apply the change with strong safety guarantees.

```
┌ regit · myrepo · branch: main · 24 commits ────────────────────────────────┐
│ COMMITS (24)                  │ 2026  [ ◀ ] [ ▶ ]                           │
│ ▸ a1b2c3   Fix login bug      │       Mon ░░▓░░ ░░░░░ ▓▓░░░ ░░░░░ ░░░       │
│     06-05 14:22 → 03-02 10:14 │       Wed ░▓░░░ ░░░░░ ░░▓▓▓ ▓░░░░ ░░░       │
│   d4e5f6   Add user model     │       Fri ░░░░░ ░▓▓▓░ ░░░░░ ░░░░░ ███       │
│     06-05 14:25 → 03-02 11:40 │       Less ·▇▇▇▇ More                       │
│   7890ab   Refactor API   ⚑   │       Selected: 2026-03-02 (Mon) · 2 commits│
│     06-05 14:30 → 03-09 09:02 │       ✓ sequential                         │
├───────────────────────────────┴─────────────────────────────────────────────┤
│ ● Even Spread · 24 commits edited · ✓ sequential                            │
│ [s]trategy [e]dit [p]review [S]plit [w]reword [u]ndo [A]pply [r]eset [?]     │
└──────────────────────────────────────────────────────────────────────────────┘
```

## Core guarantees

- **File contents never change.** Rewrites replay history with `git commit-tree`
  reusing each commit's original tree object, so the snapshot is byte-for-byte
  identical. A post-apply `git diff --stat` check enforces this.
- **Commits stay in order.** Dates are forced monotonic along the parent chain;
  a child is never dated before its parent.
- **Every apply is reversible.** A backup ref is created before each rewrite,
  and an in-app undo restores it.

## Install

```bash
go install github.com/wlame/regit/cmd/regit@latest
```

Requires the `git` binary on your `PATH` and Go 1.26+ to build.

## Usage

Run inside any git repository:

```bash
regit                 # operate on the repo in the current directory
regit --repo=/path/to/repo
```

### Keys

| Key | Action |
|-----|--------|
| `tab` | switch focus between commit list and calendar |
| `↑/↓` `j/k` | move selection (list) / cursor by week (calendar) |
| `←/→` | move calendar cursor by one day |
| `[` / `]` | previous / next year |
| `0`–`9` | set a per-day quota on the calendar cursor day |
| `g` | apply accumulated quotas (Manual Pin) |
| `s` | open the strategy palette |
| `e` / `enter` | edit selected commit (title, body, date) — `ctrl+d` drops the body |
| `p` | preview before/after dates |
| `S` | split the selected commit into smaller commits with an LLM (review, then `enter` to apply · `esc` to cancel) |
| `w` | reword the selected commit with the LLM — pick title/body variant with `←/→` (title) and `j/k` (body), `b` toggles no-body, `enter` to apply, `esc` to cancel |
| `A` | apply the rewrite (creates a backup first) |
| `u` | undo the last apply |
| `r` | reset pending edits |
| `?` | help · `q` quit |

## Strategies

`regit` separates **where** a commit lands (day) from **when** during that day
(time), so a small menu yields many combinations.

**Day assignment:** Even Spread · Per-Day Quota · Manual Pin · Rate Fill ·
Working Days · Weekends Only · Offset Shift · Preserve-Relative ·
Backwards-from-Deadline · Compress.

**Time of day:** Preserve original · Working hours (with optional lunch gap) ·
Evening · Uniform random · Fixed time · Jitter.

## Safety model

| Guard | Behavior |
|-------|----------|
| Dirty working tree | apply is **blocked** until you commit or stash |
| Out-of-order plan | apply is **blocked** until violations are resolved |
| Already-pushed commits | apply requires **typing the branch name** to confirm |
| Backup | `refs/regit/backup/<unix-ts>` created before each apply |
| Undo | restores the most recent backup from within the TUI |

> Rewriting history that others have pulled requires a force-push and can
> disrupt collaborators. `regit` warns you, but use it deliberately on shared
> branches.

## Configuration

Optional YAML at `$XDG_CONFIG_HOME/regit/config.yaml` (defaults shown):

```yaml
timezone: local              # IANA name (e.g. Europe/Berlin) or "local"
workday_start: "09:00"
workday_end: "18:00"
lunch_start: ""              # e.g. "12:30" to carve out a gap
lunch_end: ""
default_strategy: Even Spread
default_time_model: Working hours
jitter_minutes: 20
link_dates: true             # keep author == committer date
weekdays: [mon, tue, wed, thu, fri]
seed: 0                      # fix for reproducible randomized strategies
```

Any value can be overridden per-run with `REGIT_*` environment variables
(`REGIT_TIMEZONE`, `REGIT_DEFAULT_STRATEGY`, `REGIT_SEED`, …).

## Commit splitting

`regit split` splits one existing commit into 2–4 smaller, semantically
meaningful commits using an external LLM to propose how to group the diff hunks.
It operates on diff hunks and git tree objects — language-agnostic by design.

```bash
regit split <commit-ish>          # commit to split (default: HEAD)
  --llm=ollama/qwen2.5-coder      # or openai/gpt-4o, openrouter/..., lmstudio/...
  --agent=claude                  # drive an agent CLI instead of HTTP
                                  # (uses --json-schema/--bare for reliable structured output)
  --agent-args="-p --output-format json {schema}"
                                  # replace default agent args; {schema} is substituted
                                  # with the inline JSON schema at runtime
  --llm-base-url=<url>            # override the inferred OpenAI-compatible endpoint
  --max-commits=4                 # clamp on resulting commits (auto-scales with diff size)
  --prompt="keep tests separate"  # short user guidance passed to the LLM
  --test-cmd="go test ./..."      # validate each intermediate commit before applying
  --dry-run                       # print proposal and write cache; move nothing
  --yes                           # skip confirmation (CI / one-shot)
  --no-cache                      # ignore any cached proposal (force a fresh LLM call)
  --clear-cache                   # delete the split cache directory and exit
  --repo=<path>                   # path to the git repository (default: current dir)
```

### API key resolution

Credentials are read from environment variables only — never from flags.
Resolution order for each run:

1. `REGIT_LLM_API_KEY` — tool-wide override
2. Provider-standard variable: `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, …
3. No key required for local providers (`ollama`, `lmstudio`)

### Safety invariants

Two invariants are enforced before the branch ever moves; if either fails the
operation aborts and changes nothing:

1. **Local** — the last sub-commit's tree equals the original commit's tree
   (`Tₖ == C.TreeHash`). The union of the sub-commit diffs equals the original
   diff: nothing is added, dropped, or altered.
2. **Global** — after replaying the untouched tail on top of the split, the
   final `HEAD` tree is byte-identical to the original `HEAD` tree. The only
   structural change to the repository is that commit `C` became `k` commits.

Every split is preceded by a backup ref and can be undone.

### Cache

Split proposals are cached at `.git/regit/cache/split/<commitHash>.json` so
repeated runs with the same inputs skip the LLM call. Use `--no-cache` to force
a fresh proposal, or `--clear-cache` to wipe the cache directory entirely.

### Optional test command

`--test-cmd` runs a shell command after each intermediate stage in an isolated
worktree. A failing stage triggers up to two LLM regrouping attempts; if it
still fails the stage is marked `WIP:` in the commit subject. The test command
must pass on the original commit before splitting begins; if it does not,
`regit` aborts with a clear message so you can fix `--test-cmd`.

## Commit rewording

`regit reword` regenerates the commit message(s) of one or more existing commits
using an external LLM. It reads the commit's diff with a wide context window
(default 30 lines), asks the LLM to propose ranked title + body alternatives, and
lets you accept or reject each proposal before the branch is rewritten. The same
safety machinery used by every other rewrite operation protects the operation:
a backup ref is created before anything moves, and the final tree is verified
byte-identical to the original.

```bash
regit reword [<commit-ish>...]     # commits to reword (default: HEAD)
  --llm=ollama/qwen2.5-coder       # or openai/gpt-4o, openrouter/..., lmstudio/...
  --agent=claude                   # drive an agent CLI instead of HTTP
  --agent-args="<flags>"           # replace default agent invocation args
  --llm-base-url=<url>             # override the inferred OpenAI-compatible endpoint
  --prompt="focus on the why"      # short user guidance passed to the LLM
  --variants=3                     # ranked alternatives to request (0 = config default)
  --context=readme,filestates      # comma-separated context keys to include in prompt
  --context-lines=30               # git diff -U context depth (0 = config default, default: 30)
  --agent-scan                     # agent inspects the commit itself via git show
                                   # (read-only sandbox; requires --agent)
  --dry-run                        # print proposals and write cache; apply nothing
  --yes                            # skip Y/n confirmation (CI / one-shot)
  --no-cache                       # ignore cached proposals (force a fresh LLM call)
  --clear-cache                    # delete the reword cache directory and exit
  --repo=<path>                    # path to the git repository (default: current dir)
```

Multiple commit-ishes may be supplied; they are de-duplicated and processed in
root-first history order. In headless mode exactly one variant is requested per
commit (no TUI to choose from); use the TUI's `w` key to browse multiple ranked
alternatives interactively.

### API key resolution

Credentials are read from environment variables only — never from flags or config
files. Resolution order for each run:

1. `REGIT_LLM_API_KEY` — tool-wide override
2. Provider-standard variable: `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, …
3. No key required for local providers (`ollama`, `lmstudio`)

### Agent safety modes

By default `regit` gathers the commit's diff itself and sends it to the agent as
context — **the agent has no access to the repository**. This is the safest mode
and works with any backend.

With `--agent-scan` the agent is instead given read-only access to the repository
so it can run `git show` and other inspection commands itself. The subprocess is
launched with `--permission-mode plan` and a restricted `--allowedTools` list
(git read commands only), with its working directory set to the repository root.
This mode requires `--agent`.

### Cache

Reword proposals are cached at `.git/regit/cache/reword/<commitHash>.json`.
The cache key incorporates the diff hash (or commit hash in agent-scan mode),
backend name, context keys, user hint, and variant count, so any input change
produces a cache miss. Use `--no-cache` to force a fresh LLM call without
clearing the cache, or `--clear-cache` to wipe the entire cache directory.

### Background prefetch pipeline

When rewording multiple commits, `regit` starts generating the next commit's
proposal while you are reading and accepting the current one. The pipeline buffer
depth is controlled by `reword.prefetch` in the config (default: 1 ahead). The
TUI also prefetches the next commit below the cursor when you press `w`.

### Configuration defaults

```yaml
reword:
  variants: 3          # ranked alternatives per commit (TUI; headless always uses 1)
  context_lines: 30    # git diff -U context depth passed to each diff call
  context: [readme]    # supplementary context keys included in the prompt
  prefetch: 1          # how many commits ahead to generate concurrently
  cache: true          # persist proposals to .git/regit/cache/reword/
```

## Development

```bash
go build ./...
go test ./... -cover
```

Or via `just` (see `just --list` for all recipes):

```bash
just build
just test
just ci
just split HEAD -- --llm=ollama/qwen2.5-coder --dry-run
```

The codebase is layered so the interesting logic is pure and testable:

- `internal/strategy` — pure date-distribution engine (no git, no UI)
- `internal/gitio` — the only package that shells out to `git`
- `internal/safety` — guards, backup refs, undo
- `internal/split` — commit-splitting orchestrator (extract → group → build → validate → repair → apply)
- `internal/llm` — LLM backend abstraction (OpenAI-compatible HTTP + agent CLI)
- `internal/config` — YAML + env configuration
- `internal/ui` — Bubble Tea terminal interface
- `internal/model` — shared domain types

## License

MIT © wlame
