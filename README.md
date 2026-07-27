# regit

**Safely rewrite the commit history of your current git branch** — spread
commit dates across a calendar, split or squash commits, regenerate messages,
fix authorship, and remove leaked files or secrets. Everything runs from an
interactive terminal UI or as plain scriptable commands, on top of one safety
core: file contents never change, and every apply can be undone.

regit was built for the common situation where a burst of work — often
AI-assisted, or a recreated/squashed history — leaves every commit stamped on
the same day. It has since grown into a general branch-history toolkit.

Full documentation: **<https://wlame.github.io/regit/>**

## Features

- **Date spreading** — distribute commits over a date range with 10 day
  strategies × 7 time-of-day models (working hours, jitter, deadlines, …).
- **LLM-assisted rewrites** — split one commit into semantic pieces, regenerate
  commit messages, or squash chains with a summarized message. Opt-in; works
  with local (Ollama, LM Studio) and hosted (OpenAI, OpenRouter) providers, or
  by driving an agent CLI such as Claude Code.
- **History hygiene** — purge files/directories from history as if never
  added; remove or replace a leaked secret across all commits.
- **Cleanup verbs** — linearize merge topology, rewrite subjects by regex,
  fix author identity, strip commit bodies.
- **`git re` integration** — one command registers regit as a native-feeling
  git subcommand.
- **Preview by default** — every mutating command shows its full plan first
  and only applies with `--execute`.
- **Reversible** — a backup ref precedes every rewrite; `regit undo` restores it.

## Commands

Every interactive action has a non-interactive equivalent. Mutating commands
**preview by default**; pass `--execute` to apply, and `--execute --force` to
rewrite already-pushed commits. `undo`, `install`, and `uninstall` are exempt
and act immediately.

<!-- regit:begin commands -->
| Command | What it does |
|---------|--------------|
| `regit split` | Split one commit into 2–4 semantic commits using an LLM. |
| `regit reword` | Regenerate commit message(s) with an LLM (one proposal headless). |
| `regit squash` | Squash consecutive commits into one per chain (LLM-summarized message). |
| `regit linearize` | Flatten merge topology into a clean first-parent chain (trees preserved). |
| `regit redate --strategy=…` | Spread commit/author dates across a calendar using a strategy. |
| `regit retitle --match=…` | Rewrite commit subjects with a regex across a range. |
| `regit reauthor` | Rewrite the author (and by default committer) identity of commits. |
| `regit drop-bodies` | Strip the body of each commit in a range, keeping titles. |
| `regit purge` | Remove files or directories from branch history as if never added. |
| `regit redact --match=…` | Remove or replace a literal secret across branch history. |
| `regit undo` | Restore the branch to the most recent regit backup ref. |
| `regit install` | Register regit as the `git re` subcommand (symlink git-re on PATH). |
| `regit uninstall` | Remove the `git re` integration (the git-re symlink). |
<!-- regit:end commands -->

## Safety model

- **File contents never change.** Rewrites replay history with
  `git commit-tree` reusing each commit's original tree object, so every
  snapshot stays byte-for-byte identical. A per-commit verifier proves this
  before the branch moves and aborts (changing nothing) otherwise.
- **Commits stay in order.** Dates are forced monotonic along the parent
  chain; a child is never dated before its parent.
- **Every apply is reversible.** A backup ref is created before each rewrite;
  `regit undo` restores it.

| Guard | Behavior |
|-------|----------|
| Dirty working tree | apply is **blocked** until you commit or stash |
| Already-pushed commits | apply requires `--force` (the TUI asks you to type the branch name) |
| Shallow clone | apply is **blocked** — run `git fetch --unshallow` first |
| Merge commits in range | apply is **blocked** (redate accepts `--allow-merges`) |
| Signed commits | rewriting drops signatures; regit **warns**, and `--sign` re-signs with your key |
| Backup | `refs/regit/backup/<branch>/<ts>-<seq>` created before each apply, never overwritten |
| Undo past new work | `undo` **refuses** to unwind past newer commits unless `--force`; it backs up the tip first |

Before applying, regit also reports any tags or branches the rewrite would
orphan; afterwards it prints the exact
`git push --force-with-lease <remote> <branch>` to publish the result.

> Rewriting history that others have pulled requires a force-push and can
> disrupt collaborators. Use regit deliberately on shared branches.

## Install

With the Go toolchain:

```bash
go install github.com/wlame/regit/cmd/regit@latest
```

Or download a prebuilt binary for your platform (Linux, macOS, Windows —
amd64/arm64) from the
[latest release](https://github.com/wlame/regit/releases/latest), rename it to
`regit`, and put it on your `PATH`.

Each release also carries a `SHA256SUMS` file and signed build provenance, so a
downloaded binary can be checked before you run it:

```bash
sha256sum -c SHA256SUMS --ignore-missing        # the file matches the release
gh attestation verify regit --repo wlame/regit  # …and the release built it here
```

The checksum proves the file is intact; the attestation proves it came from
this repository's release workflow rather than from someone who merely produced
a matching checksum file.

Either way regit needs the `git` binary on your `PATH` (and Go 1.24+ only to
build from source). Optionally register it as a git subcommand:

```bash
regit install     # links git-re next to the regit binary on your PATH
git re redact --help
```

`git re <command>` and `regit <command>` are fully equivalent; `regit
uninstall` removes the link. One git quirk: use `git re -h` (not `git re
--help`) for the command list.

## Usage

Run `regit` inside a repository to launch the interactive UI (`regit
--repo=PATH` for another repo), or use the commands directly:

```bash
regit redate --strategy=Even-Spread --time=Working-hours \
  --start=2026-01-01 --end=2026-03-01            # preview the date plan
regit redate --strategy=Even-Spread --time=Working-hours \
  --start=2026-01-01 --end=2026-03-01 --execute  # apply it
regit redate --strategy=Compress --compress-day=2026-06-05 \
  --commits=abc12345,def67890 --execute          # redate only the given commits

regit purge --search=secret   # find files in history worth purging
regit undo                    # restore the pre-rewrite backup

regit --help                  # the command list
regit redate --help           # one command's flags
regit --llm-help              # full agent-facing documentation
```

`--commits` narrows a rewrite to an explicit target set (a range, a single
ref, or a comma-separated list); commits left out act as fixed anchors. Every
action applied in the UI is also logged as its exact headless command to
`.git/regit/command.log`, so you can always reproduce it in a script.

### Date strategies

regit separates **where** a commit lands (day) from **when** during that day
(time), so a small menu yields many combinations.

**Day assignment:**
<!-- regit:begin strategies -->
Even Spread · Manual Pin · Picked Days · Rate Fill · Working Days · Weekends Only · Offset Shift · Preserve-Relative · Backwards-from-Deadline · Compress.
<!-- regit:end strategies -->

**Time of day:**
<!-- regit:begin time-models -->
Preserve original · Working hours · Evening hours · Non-working hours · Uniform random · Fixed time · Jitter.
<!-- regit:end time-models -->

## LLM-assisted commands

`split`, `reword`, and `squash` use an external LLM. They are opt-in — without
an LLM configured every other command works normally. Pick a backend with
`--llm=<provider>/<model>` (providers: `ollama`, `lmstudio`, `openai`,
`openrouter`) or `--agent=<cmd>` to drive a local agent CLI, and set defaults
in the config file so the flag can be omitted.

API keys are read from environment variables only — never flags or config:
`REGIT_LLM_API_KEY` first, then the provider-standard variable
(`OPENAI_API_KEY`, `OPENROUTER_API_KEY`); local providers need no key.

The model only **proposes** structured data (groupings, messages); regit
validates every proposal and performs the git operations itself through the
same verified, backed-up apply path as every other command. Proposals are
cached under `.git/regit/cache/` so repeated runs skip the LLM call.

`regit split` shows the full flag surface these commands share:

```bash
regit split <commit-ish>          # commit to split (default: HEAD)
  --llm=ollama/qwen2.5-coder      # or openai/gpt-4o, openrouter/..., lmstudio/...
  --agent=claude                  # drive an agent CLI instead of HTTP
  --agent-args="-p --output-format json {schema}"
                                  # replace default agent args; {schema} is substituted
  --llm-base-url=<url>            # override the inferred OpenAI-compatible endpoint
  --max-commits=4                 # clamp on resulting commits (auto-scales with diff size)
  --prompt="keep tests separate"  # short user guidance passed to the LLM
  --test-cmd="go test ./..."      # validate each intermediate commit before applying
  --execute                       # apply the split (default: dry-run preview)
  --force                         # allow rewriting already-pushed commits
  --sign                          # re-sign rewritten commits with your configured key
  --no-cache                      # ignore any cached proposal (force a fresh LLM call)
  --clear-cache                   # delete the split cache directory and exit
  --repo=<path>                   # path to the git repository (default: current dir)
```

By default the agent backend receives only the diff regit sends it — it has no
repository access. `regit reword --agent-scan` optionally lets the agent browse
repo files read-only (no shell of any kind); treat that as a trust boundary,
not a sandbox, and see the [documentation](https://wlame.github.io/regit/)
for the full trust model.

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

split:                       # commit-splitting defaults
  max_commits: 4             # clamp on resulting commits per split
  lines_per_commit: 40       # sizing heuristic for the auto max-commit scale
  min_split_lines: 20        # diffs smaller than this are never split
  recursion_depth: 2         # bound on recursive re-splitting
  context: [readme]          # context keys included in the LLM prompt
  repair_attempts: 2         # LLM regroup attempts when --test-cmd fails
  wip_prefix: "WIP: "        # subject prefix for stages that still fail tests
  cache: true                # persist proposals to .git/regit/cache/split/

reword:                      # commit-rewording defaults
  variants: 3                # ranked alternatives per commit (TUI; headless uses 1)
  context: [readme]          # context keys included in the prompt
  context_lines: 30          # git diff -U context depth
  prefetch: 1                # commits generated ahead during multi-reword
  cache: true                # persist proposals to .git/regit/cache/reword/
```

Every setting can be overridden per-run with `REGIT_*` environment variables
(the environment always beats the file):

<!-- regit:begin env-vars -->
`REGIT_TIMEZONE` · `REGIT_WORKDAY_START` · `REGIT_WORKDAY_END` · `REGIT_DEFAULT_STRATEGY` · `REGIT_DEFAULT_TIME_MODEL` · `REGIT_SEED` · `REGIT_LINK_DATES` · `REGIT_LLM_PROVIDER` · `REGIT_LLM_MODEL` · `REGIT_SPLIT_MAX_COMMITS` · `REGIT_REWORD_VARIANTS`
<!-- regit:end env-vars -->

## Development

```bash
just build        # compile to dist/regit
just test         # run the test suite
just ci           # the full CI gates (fmt, vet, build, race tests, coverage)
```

See `just --list` for all recipes and
[the documentation site](https://wlame.github.io/regit/) for architecture,
per-command references, and troubleshooting.

## License

MIT © wlame
