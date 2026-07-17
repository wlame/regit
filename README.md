# regit

A terminal UI for **safely rewriting the commit and author dates** of your
current git branch. Built for the common situation where a burst of work — often
AI-assisted, or a recreated/squashed history — leaves every commit stamped on the
same day. `regit` lets you spread those commits across a calendar with a rich set
of strategies, edit messages, and apply the change with strong safety guarantees.

```
┌ regit · myrepo · main · 24 commits ───────────────────────────────────────────┐
├────────────────────────────────┬──────────────────────────────────────────────┤
│ COMMITS (24)                   │ DETAIL a1b2c3                                │
│ ▸ a1b2c3  Fix login bug        │ alice <alice@example.com>                    │
│    06-05 14:22 → 03-02 10:14   │ authored 06-05 14:22                         │
│   d4e5f6  Add user model       │       → 03-02 10:14                          │
│    06-05 14:25 → 03-02 11:40   │ 3 files · +42 −7                             │
│   7890ab  Refactor API         │ Fix login bug                                │
├────────────────────────────────┴──────────────────────────────────────────────┤
│ [?]help [q]uit [tab]focus [e]dit [.]detail [p]review [A]pply [m]actions …     │
└───────────────────────────────────────────────────────────────────────────────┘
```

The commit detail fills the right column by default; launching Date/time spread
(the redate flow) swaps in the calendar and strategy panes for the duration of
that flow (see Keys below).

## Core guarantees

- **File contents never change.** Rewrites replay history with `git commit-tree`
  reusing each commit's original tree object, so the snapshot is byte-for-byte
  identical. A per-commit change-set verifier enforces this before the branch
  moves — it proves every rewritten commit is byte-identical by comparing git
  tree hashes, and aborts (changing nothing) otherwise.
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

The full keymap, grouped the same way as the in-app `?` help overlay
(this section is generated from the code — `just docs-sync`):

<!-- regit:begin keys -->
**GLOBAL**

| Key | Action |
|-----|--------|
| `?` | toggle this help |
| `q / ctrl+c` | quit |
| `tab` | cycle focus across the list and the active flow's widget |

**FLOW**

| Key | Action |
|-----|--------|
| `tab` | focus the active flow's widget (the calendar), when one is open |
| `esc` | exit the active flow, returning the zone to the commit detail |

**ACTIONS (any pane)**

| Key | Action |
|-----|--------|
| `e / enter` | edit the selected commit (title, body, date) |
| `.` | show full detail for the selected commit (overlay when narrow) |
| `p` | preview the pending changes (read-only) |
| `A` | apply: preview the before/after, then enter to apply (confirm if pushed) |
| `U` | undo the last apply |
| `R` | reset all pending edits |
| `L` | toggle the command-log footer (read-only) |
| `m` | open the action panel for the current selection |
| `↑/↓ · enter · esc` | action panel: move · run · close |

**COMMIT LIST (focused)**

| Key | Action |
|-----|--------|
| `d` | toggle date grouping in the commit list |
| `a` | toggle the author column in the commit list |
| `↑/↓ or k/j` | move the selection |
| `ctrl+d/u` | half page down / up (pgdn/pgup move a full page) |
| `g / G` | newest / oldest commit (home/end do the same) |
| `} / {` | next / previous day group |

**SELECTION (commit list)**

| Key | Action |
|-----|--------|
| `space` | toggle the cursor commit in the selection set |
| `s` | select all active commits |
| `n` | clear the selection |
| `v` | visual range — anchor, move to extend, v/enter to fix, esc to cancel |
| `esc` | cancel the selection (back to Active if a flow is open, else Single) |
| `(reword)` | with a set selected, Reword runs multi-reword over every chosen commit |

**BRANCH & BASE**

| Key | Action |
|-----|--------|
| `B` | open the branch picker — enter switches branch, tab sets it as the base |
| `b` | set/clear the base at the cursor commit (freeze older history) |

**CALENDAR (redate flow)**

| Key | Action |
|-----|--------|
| `↑/↓` | move the cursor by one day |
| `←/→` | move the cursor by one week |
| `0-9` | pin a per-day quota (Manual Pin strategy active); live-stages the distribution |
| `space` | pick/unpick the day (Picked Days strategy active); reshuffles live |

**STRATEGY PANE (redate flow)**

| Key | Action |
|-----|--------|
| `↑/↓` | move between fields |
| `←/→` | change strategy (on the top row) / time model |
| `enter` | apply the strategy with the current parameters |
| `tab / esc` | back to the commit list |
<!-- regit:end keys -->

## Commands (headless)

Every interactive action has a non-interactive equivalent. Mutating commands
**preview by default**; pass `--execute` to apply, and `--execute --force` to
rewrite already-pushed commits. `undo` is exempt and restores immediately.

<!-- regit:begin commands -->
| Command | What it does |
|---------|--------------|
| `regit split` | Split one commit into 2–4 semantic commits using an LLM. |
| `regit reword` | Regenerate commit message(s) with an LLM (one proposal headless). |
| `regit squash` | Squash consecutive commits into one per chain (LLM-summarized message). |
| `regit redate --strategy=…` | Spread commit/author dates across a calendar using a strategy. |
| `regit retitle --match=…` | Rewrite commit subjects with a regex across a range. |
| `regit reauthor` | Rewrite the author (and by default committer) identity of commits. |
| `regit drop-bodies` | Strip the body of each commit in a range, keeping titles. |
| `regit purge` | Remove files or directories from branch history as if never added. |
| `regit undo` | Restore the branch to the most recent regit backup ref. |
<!-- regit:end commands -->

```bash
regit redate --strategy=Even-Spread --time=Working-hours \
  --start=2026-01-01 --end=2026-03-01            # preview
regit redate --strategy=Even-Spread --time=Working-hours \
  --start=2026-01-01 --end=2026-03-01 --execute  # apply
regit redate --strategy=Compress --compress-day=2026-06-05 \
  --commits=abc12345,def67890 --execute          # redate only the given commits

regit --help                # the command list
regit redate --help         # one command's flags
regit --llm-help            # full agent-facing documentation
```

`--commits` narrows a redate to an explicit target set (a range, a single ref, or
a comma-separated list); every commit left out acts as a fixed anchor bounding the
ones that move. In the TUI, selecting commits (Set or Range) before staging scopes
Dates the same way. Press `L` to toggle the **command-log footer**, which shows
the exact headless command for each action you apply — including the `--commits=`
it would take to reproduce a scoped redate (also written to
`.git/regit/command.log`).

## Strategies

`regit` separates **where** a commit lands (day) from **when** during that day
(time), so a small menu yields many combinations.

**Day assignment:**
<!-- regit:begin strategies -->
Even Spread · Manual Pin · Picked Days · Rate Fill · Working Days · Weekends Only · Offset Shift · Preserve-Relative · Backwards-from-Deadline · Compress.
<!-- regit:end strategies -->

**Time of day:**
<!-- regit:begin time-models -->
Preserve original · Working hours · Evening hours · Non-working hours · Uniform random · Fixed time · Jitter.
<!-- regit:end time-models -->

## Safety model

| Guard | Behavior |
|-------|----------|
| Dirty working tree | apply is **blocked** until you commit or stash |
| Out-of-order plan | apply is **blocked** until violations are resolved |
| Already-pushed commits | apply requires **typing the branch name** to confirm |
| Backup | `refs/regit/backup/<branch>/<ts>-<seq>` created before each apply; an existing backup is never overwritten |
| Undo | `U` restores the most recent backup taken **on the current branch**; cross-branch restores are refused (legacy flat backups need an explicit `regit undo --to=<ref>`) |
| Shallow clone | apply is **blocked** — history is incomplete; run `git fetch --unshallow` first |
| Merge in range (redate) | apply is **blocked** until you pass `--allow-merges`; side-branch dates stay unchanged |
| Signed commits | rewriting drops signatures; regit **warns** first, and `--sign` re-signs with your configured key |
| Undo past new work | `undo` **refuses** to unwind when HEAD moved past the backup unless you pass `--force`; it always backs up the current tip first |

> Rewriting history that others have pulled requires a force-push and can
> disrupt collaborators. `regit` warns you, but use it deliberately on shared
> branches.

Before applying, every mutating command also reports any tags/branches in the
active range that the rewrite would orphan; after rewriting a branch with an
upstream, it prints the exact `git push --force-with-lease <remote> <branch>`
for your branch's upstream remote (not always `origin`) to publish the result.

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

llm:                         # optional LLM used by split / reword / squash summaries
  provider: ""               # ollama, lmstudio, openai, openrouter ("" = LLM off)
  model: ""                  # e.g. qwen2.5-coder
  base_url: ""               # override the provider's default endpoint
  temperature: 0             # reserved — requests currently always send 0
  timeout_sec: 60

split:                       # commit-splitting defaults (see "Commit splitting")
  max_commits: 4             # clamp on resulting commits per split
  lines_per_commit: 40       # sizing heuristic for the auto max-commit scale
  min_split_lines: 20        # diffs smaller than this are never split
  recursion_depth: 2         # bound on recursive re-splitting
  context: [readme]          # context keys included in the LLM prompt
  repair_attempts: 2         # LLM regroup attempts when --test-cmd fails
  wip_prefix: "WIP: "        # subject prefix for stages that still fail tests
  cache: true                # persist proposals to .git/regit/cache/split/

reword:                      # commit-rewording defaults (see "Commit rewording")
  variants: 3                # ranked alternatives per commit (TUI; headless uses 1)
  context: [readme]          # context keys included in the prompt
  context_lines: 30          # git diff -U context depth
  prefetch: 1                # commits generated ahead during multi-reword
  cache: true                # persist proposals to .git/regit/cache/reword/
```

These settings can be overridden per-run with `REGIT_*` environment variables
(the environment always beats the file):

<!-- regit:begin env-vars -->
`REGIT_TIMEZONE` · `REGIT_WORKDAY_START` · `REGIT_WORKDAY_END` · `REGIT_DEFAULT_STRATEGY` · `REGIT_DEFAULT_TIME_MODEL` · `REGIT_SEED` · `REGIT_LINK_DATES` · `REGIT_LLM_PROVIDER` · `REGIT_LLM_MODEL` · `REGIT_SPLIT_MAX_COMMITS` · `REGIT_REWORD_VARIANTS`
<!-- regit:end env-vars -->

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
  --execute                       # apply the split (default: dry-run preview)
  --force                         # allow rewriting already-pushed commits
  --sign                          # re-sign rewritten commits with your configured signing key
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
2. **Global** — after replaying the untouched tail on top of the split, every
   commit's snapshot is byte-identical to its original (the final `HEAD` tree
   matches the original `HEAD` tree). The only structural change to the repository
   is that commit `C` became `k` commits.

Both are enforced by the per-commit change-set verifier: the split declares an
**empty** change-set, so the boundary pair `C → Cₖ` plus each replayed tail pair
must be content-identical or the operation aborts before the branch moves. Every
split is also preceded by a backup ref and can be undone.

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

## Commit squashing

`regit squash` collapses consecutive runs of commits into one commit per run,
in the TUI (the `m` action panel's Squash action, a tabbed wizard with one tab per chain) or
headless:

```bash
regit squash 6f3a^..a2df                 # preview squashing one chain (dry-run)
regit squash 6f3a^..a2df --execute       # apply
regit squash A^..B,C,D^..E --execute     # several chains at once
regit squash 6f3a^..a2df --llm=ollama/qwen2.5-coder --with-diffs --execute
```

**Range semantics.** `A..B` follows git: it means the commits **after** A through
B — **A itself is excluded** (it is the anchor/parent). To include A, write
`A^..B`. Single refs (`C`) select just that commit. Items are comma-separated.

**Defaults.** The squashed commit's author identity and message default to the
**first (oldest)** commit of the chain; its date defaults to the **last (newest)**
commit. With an LLM configured (`--llm`/`--agent`), the message is an LLM summary
of the chain instead (messages-only by default; `--with-diffs` adds the diffs).
Singletons (a selected commit with no selected neighbor) are ignored. Squashing
across a merge commit is refused. File contents are byte-for-byte unchanged.

## Commit rewording

`regit reword` regenerates the commit message(s) of one or more existing commits
using an external LLM. It reads the commit's diff with a wide context window
(default 30 lines), asks the LLM to propose ranked title + body alternatives, and
lets you accept or reject each proposal before the branch is rewritten. The same
safety machinery used by every other rewrite operation protects the operation:
a backup ref is created before anything moves, and the change-set verifier proves
every rewritten commit is byte-identical to its original.

```bash
regit reword [<commit-ish>...]     # commits to reword (default: HEAD)
  --llm=ollama/qwen2.5-coder       # or openai/gpt-4o, openrouter/..., lmstudio/...
  --agent=claude                   # drive an agent CLI instead of HTTP
  --agent-args="<flags>"           # replace default agent invocation args
  --llm-base-url=<url>             # override the inferred OpenAI-compatible endpoint
  --prompt="focus on the why"      # short user guidance passed to the LLM
  --context=readme,filestates      # comma-separated context keys to include in prompt
  --context-lines=30               # git diff -U context depth (0 = config default, default: 30)
  --agent-scan                     # agent may also browse repo files read-only
                                   # (Read/Grep/Glob; no exec; requires --agent)
  --execute                        # apply the reword (default: dry-run preview)
  --force                          # allow rewriting already-pushed commits
  --sign                           # re-sign rewritten commits with your configured signing key
  --no-cache                       # ignore cached proposals (force a fresh LLM call)
  --clear-cache                    # delete the reword cache directory and exit
  --repo=<path>                    # path to the git repository (default: current dir)
```

Multiple commit-ishes may be supplied; they are de-duplicated and processed in
root-first history order. In headless mode exactly one variant is requested per
commit (no TUI to choose from); use the TUI's `m` action panel → Reword to browse
multiple ranked alternatives interactively.

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

With `--agent-scan` the agent may additionally browse repository files
read-only for context. The subprocess is launched with `--permission-mode plan`
and an `--allowedTools` list containing only the file-browsing tools
(`Read,Grep,Glob`) — no shell access of any kind — with its working directory
set to the repository root. The commit diff is still gathered by `regit` and
sent in the prompt in both modes. This mode requires `--agent`.

`Read`/`Grep`/`Glob` resolve paths relative to the repository but are **not
hard-confined** to it — Claude Code's allow-list is not a boundary for the file
tools, and true subtree confinement is only achievable with an OS-level sandbox.
As defense-in-depth, regit denies reads of well-known secret stores (`~/.ssh`,
`~/.aws`, `~/.gnupg`, `/etc`, `*.pem`, `id_rsa*`, …) via `--disallowedTools`
(deny rules DO resolve symlinks and `..`). Treat agent-scan as a **trust
boundary, not a sandbox**: only enable it on diffs you trust, and treat any
resulting proposal with the same care as any other LLM output.

### Cache

Reword proposals are cached at `.git/regit/cache/reword/<commitHash>.json`.
The cache key incorporates the diff hash, backend name, context keys, user
hint, and variant count, so any input change produces a cache miss. Use
`--no-cache` to force a fresh LLM call without clearing the cache, or
`--clear-cache` to wipe the entire cache directory.

### Background prefetch pipeline

When rewording multiple commits, `regit` starts generating the next commit's
proposal while you are reading and accepting the current one. The pipeline buffer
depth is controlled by `reword.prefetch` in the config (default: 1 ahead). The
TUI also prefetches the next commit below the cursor when you use the
`m` action panel's Reword action.

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
just split HEAD -- --llm=ollama/qwen2.5-coder   # preview (default; add --execute to apply)
```

The codebase is layered so the interesting logic is pure and testable:

- `internal/strategy` — pure date-distribution engine (no git, no UI)
- `internal/gitio` — the only package that shells out to `git`
- `internal/safety` — guards, backup refs, undo
- `internal/split` — commit-splitting orchestrator (extract → group → build → validate → repair → apply)
- `internal/squash` — commit-squash engine (chain summarization, cache, prompt)
- `internal/reword` — commit-rewording engine (variants, cache, prompt building)
- `internal/redact` — secret-redaction engine (pickaxe scan, line cascade, plan → spec)
- `internal/llm` — LLM backend abstraction (OpenAI-compatible HTTP + agent CLI)
- `internal/promptcache` — the shared LLM-result cache the split/reword/squash caches instantiate
- `internal/config` — YAML + env configuration
- `internal/capability` — the command registry: every action as data + headless Run (CLI/TUI/help project from it)
- `internal/ui` — Bubble Tea terminal interface
- `internal/model` — shared domain types
- `internal/termsafe` — strips terminal escape sequences from untrusted repo/LLM text before display
- `internal/gittest` — hermetic git environment for test fixtures (never reads the host git config)
- `internal/docs` — generated-docs renderer; `just docs-sync` rewrites the marked README/AGENTS.md blocks

## License

MIT © wlame
