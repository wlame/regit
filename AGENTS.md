# AGENTS.md — regit codebase guide for LLM coding agents

A quick-start reference for coding agents working inside this repo.
Read this before touching code; update it when the shape of the project changes.

## What regit does

`regit` is a terminal UI for **safely rewriting commit and author dates** on the
current git branch, and for **splitting and rewording commits** using an LLM.
Three independent features share a common safety/gitio core:

1. **Date spreading** — spread a burst of commits across a calendar using
   configurable strategies (Even Spread, Manual Pin, Rate Fill, …), edit
   messages, and apply the rewrite with strong safety guarantees.
2. **Commit splitting** (`regit split`) — split one existing commit into 2–4
   semantically meaningful commits using an external LLM to group its diff hunks.
3. **Commit rewording** (`regit reword`) — regenerate the commit message of one
   or more commits using an LLM; proposes ranked title + body alternatives, with
   an interactive picker in the TUI (`w` key) and a headless Y/n flow on the CLI.

## Core domain types (`internal/model`)

| Type | File | What it is |
|------|------|-----------|
| `model.Commit` | `commit.go` | Immutable snapshot of a git commit (hashes, dates, message, flags). Never mutated — edits produce a separate `Edit`. |
| `model.Plan` | `plan.go` | Collection of `Edit` values validated for date-ordering; the thing `safety.Manager.Apply` consumes. |
| `model.Hunk` | `split.go` | One contiguous region of `diff(parent, commit)`, carrying a stable ID, file header, and patch body. Language-agnostic. |
| `model.HunkGroup` | `split.go` | One future sub-commit: a set of hunk IDs + LLM-generated subject/body/intent/confidence. |
| `model.SplitProposal` | `split.go` | The full ordered plan for splitting one commit: `Hunks`, `Groups`, `ShouldSplit`, and `Reason`. |

`model.ValidateCoverage` enforces the partition invariant (every hunk in exactly
one group, no unknown IDs) before any git object is created.

## Architecture: layered packages

```
cmd/regit/          CLI entry points (main.go, split.go, reword.go)
internal/
  model/            Pure domain types — no imports from other internal packages
  strategy/         Pure date-distribution algorithms — no git, no UI
  gitio/            THE ONLY PACKAGE THAT SHELLS OUT TO git
  safety/           Guards, backup refs, undo stack
  split/            Commit-splitting orchestrator
  reword/           Commit-rewording engine (variants, cache, prompt building)
  llm/              LLM backend abstraction
  config/           YAML + REGIT_* env configuration
  ui/               Bubble Tea TUI (depends on all of the above)
```

The TUI is a bounded tab chassis (internal/ui). Five fixed category tabs —
Dates · Restructure · History · Authorship · Search — sit over a persistent
commit backbone (list + detail). Tabs and per-tab feature menus are DATA: the
`workspaces` table and the `features` registry in feature.go are the single
source of truth, and the tab bar, menus, and key help are derived from them
(add a feature = add a registry row; the action surface grows down, never wide).
`< / >` switch tabs; `tab` focuses the active workspace's widget. Dates is the
rich calendar/strategy workspace; Restructure is a menu that re-invokes the
existing split/reword/retitle/drop-bodies actions; History/Authorship/Search are
disabled stub tabs awaiting later phases.

### What each package does

**`internal/model`** — shared vocabulary. Every other package imports it; it
imports nothing from `internal/`.

**`internal/strategy`** — pure functions that distribute commits across days.
No git, no TUI, no LLM. Fully unit-testable.

**`internal/gitio`** — the only place `git` runs. All git commands go through
`gitio.Repo`. Key methods: `ReadCommits`, `Replay` (date rewrite), `Hunks`
(extract diff hunks), `BuildTreeFromHunks` (apply hunk subset to worktree),
`ApplySplit` (emit sub-commits and replay tail), `VerifyTreesUnchanged`,
`NewWorktree`/`Worktree.Close` (isolated scratch space).
**Never add a git call anywhere else.**

**`internal/safety`** — `Manager` runs the guarded apply protocol:
`CheckGuards` → backup ref → replay → `VerifyTreesUnchanged` → move branch
(compare-and-swap). Also exposes `ApplySplit` for the splitting flow.

**`internal/split`** — orchestrates one split: extract hunks → skip-split
guards → LLM grouping → build intermediate trees → validate/repair loop →
verify final tree == original tree → write cache. The `Engine.Propose` method
returns a `SplitProposal` + per-group tree hashes without moving any ref.

**`internal/reword`** — commit-rewording engine. `Engine.Generate` reads the
commit's diff (or delegates to agent-scan mode), calls the backend, caches
results under `.git/regit/cache/reword/`, and returns ranked `Variant` alternatives
wrapped in a `Result`. `Result.ToEdit` converts the user's `Selection` (TitleIdx,
BodyIdx, NoBody) into a `model.Edit` ready for `safety.Apply`.

**`internal/llm`** — one interface (`Backend`) with two implementations:
`HTTPBackend` (OpenAI-compatible `/v1/chat/completions`) and `AgentBackend`
(drives an agent CLI with `-p` / stdin). `Backend.Generate(ctx, system, user,
schema, validate)` is the single generic call that both the split and reword
engines build on. Split-specific helpers (`SplitRequest`, split `SchemaJSON`)
and reword-specific helpers (`reword.Request`, reword `SchemaJSON`) live in
their respective packages.

**`internal/config`** — loads `$XDG_CONFIG_HOME/regit/config.yaml` and
applies `REGIT_*` environment variable overrides. Env vars always win over
the file. Never read `os.Getenv` anywhere else for configuration values.

**`internal/ui`** — Bubble Tea TUI. Imports all packages above. The `[S]plit`
action uses the same `split.Engine` that `cmd/regit/split.go` uses —
one code path, not two.

## How to build, run, and test

Use `just` (run `just` or `just --list` for all recipes):

```bash
just build                              # compile to dist/regit
just test                               # go test ./...
just test-race                          # tests with race detector + coverage
just ci                                 # full local CI: fmt-check + vet + build + test-race
just run                                # launch TUI on current repo
just split HEAD -- --llm=ollama/qwen2.5-coder --dry-run   # test the splitter
```

Bare go commands also work:

```bash
go build ./...
go test ./... -cover
go vet ./...
```

## Split invariants (the safety contract)

Two invariants are checked before the branch ever moves; either failure aborts
the operation and leaves the repo unchanged:

1. **Local** (`safety.ApplySplit`, before replay): `subs[last].Tree == commit.TreeHash`
   — the last sub-commit's tree equals the original commit's tree. The union of
   sub-commit diffs is identical to the original diff.

2. **Global** (`safety.ApplySplit`, after replay): `VerifyTreesUnchanged(oldHead, newHead)`
   — the final HEAD tree is byte-identical to the original HEAD tree. The only
   structural change to the repository is that commit `C` became `k` commits.

The engine (`split.Engine.Propose`) also enforces this locally before returning:
if `trees[last] != commit.TreeHash` it returns an error rather than a corrupt
proposal.

## Commit-splitting flow (end to end)

```
regit split <commit-ish>
  1. Resolve commit C and its first parent P
  2. Guards: dirty tree? merge commit? (safety package)
  3. Cache lookup at .git/regit/cache/split/<hash>.json
  4. Extract hunks of diff(P, C) with stable IDs (H1, H2, …)
  5. Skip-split guards: too small? single hunk? pure rename? binary-only?
  6. LLM Propose → SplitProposal (groups + messages)
  7. Build cumulative trees T1..Tk in an isolated worktree
     — if --test-cmd: run after each stage; repair loop (≤2 regroups) on failure
  8. Verify Tk == C.TreeHash (invariant 1) → write cache
  9. If --dry-run: print proposal + git commands; stop here
 10. Backup ref → ApplySplit (emit C1..Ck, replay tail) → VerifyTreesUnchanged
     (invariant 2) → move branch (compare-and-swap) → print summary
```

## Gotchas and constraints

- **Never touch the user's working tree.** All intermediate git work happens in
  an isolated worktree created by `gitio.NewWorktree` and cleaned up by
  `Worktree.Close`. The `defer wt.Close()` pattern is mandatory.

- **All git access goes through `internal/gitio`.**  Calling `exec.Command("git",
  ...)` anywhere else will break auditability and testability.

- **Commit-message convention**: one imperative sentence, starts with a capital
  letter, ends with a full stop, no conventional-commit prefix (`feat:`, `fix:`,
  …), no trailer (`Co-Authored-By:`, `Signed-off-by:`).

- **Config env vars are `REGIT_*`**, not `GHIST_*` (an old name that was renamed).
  The config package is the single place they are read.

- **API keys come from env vars only.** Resolution order: `REGIT_LLM_API_KEY`
  (tool-wide) → `OPENAI_API_KEY` / `OPENROUTER_API_KEY` (provider-standard) →
  empty for local providers (ollama, lmstudio). Never accept a key via a CLI flag.

- **Cache writes are non-fatal.** `split.Cache.Put` errors are swallowed with
  `_ = e.cache.Put(...)` — a cache miss on the next run is the worst outcome.

- **`ValidateCoverage` runs before any git object is created.** If the LLM's
  grouping is invalid (unknown IDs, duplicate assignments, missing hunks) the
  engine rejects it in pure Go before touching the repo.

- **`model.Hunk` fields**: `FileHeader` (the per-file `diff --git…/---/+++` block)
  and `Body` (the `@@ …` hunk line + diff lines). The `Patch()` method combines
  them. The design spec uses different field names; the code uses `FileHeader`/`Body`.

## Where things live

```
cmd/regit/main.go       Subcommand dispatch (split → runSplit; reword → runReword; default → TUI)
cmd/regit/split.go      Flag parsing, backend wiring, orchestration for `regit split`
cmd/regit/reword.go     Flag parsing, backend wiring, pipeline for `regit reword`
internal/model/
  commit.go             Commit, SplitMessage
  edit.go               Edit (pending date/message change)
  plan.go               Plan (validated batch of edits)
  repo.go               RepoState
  split.go              Hunk, HunkGroup, SplitProposal, ValidateCoverage
internal/gitio/
  repo.go               Repo, Open, git shell-out choke point
  read.go               ReadCommits, ReadState
  rewrite.go            Replay, ApplySplit, VerifyTreesUnchanged
  hunks.go              Hunks(), BuildTreeFromHunks(), Worktree
  refs.go               UpdateRef, BackupRefPrefix, BranchRef
internal/split/
  engine.go             Engine, Propose, buildAndValidate, rebuildAll
  cache.go              Cache, CacheEntry (.git/regit/cache/split/)
  size.go               ShouldSkipSplit, EffectiveMaxCommits, Fingerprint
  subcommits.go         BuildSubCommits (groups + trees → []gitio.SubCommit)
internal/reword/
  engine.go             Engine, Generate (normal + agent-scan modes), diffHash
  cache.go              Cache, CacheEntry (.git/regit/cache/reword/)
  types.go              Result, Variant, Selection, DefaultSelection, Options, Request
  prompt.go             BuildMessages, BuildScanMessages, SchemaJSON, ParseVariants
internal/llm/
  backend.go            Backend interface (generic Generate), FewShot
  http.go               HTTPBackend (OpenAI-compatible)
  agent.go              AgentBackend (CLI driver), ScanDefaultArgs
  schema.go             Split-specific SchemaJSON (JSON schema for structured output)
internal/safety/
  manager.go            Manager, Apply, ApplySplit, Undo
  guards.go             CheckGuards, Guard
internal/config/
  config.go             Config, Load, REGIT_* overrides, RewordConfig
internal/ui/
  app.go                Main Bubble Tea model and update loop
  split.go              Split review screen + backend wiring for TUI
  reword.go             Reword variant picker + async Generate cmd for TUI
  feature.go            Data-driven registry: WorkspaceID, SelectionMode, the workspaces table + features registry
  workspace.go          Tab bar render + tab navigation (< / > switch); zone-3 dispatch (viewRightColumn)
  ws_dates.go           Dates workspace widget (calendar + strategy form)
  ws_restructure.go     Restructure feature menu + the disabled stub-tab panel
  keys.go               Data-driven keymap table; generates the help overlay and keybar
  (other .go files)     Calendar, editor, strategy palette, preview, styles
justfile                All dev recipes — prefer these over raw go commands
AGENTS.md               This file
```
