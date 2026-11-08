# AGENTS.md — regit codebase guide for LLM coding agents

A quick-start reference for coding agents working inside this repo.
Read this before touching code; update it when the shape of the project changes.

## What regit does

`regit` is a terminal UI for **safely rewriting commit and author dates** on the
current git branch, for **splitting and rewording commits** using an LLM, and for
**purging files or redacting secrets** from that same history.
Four independent feature areas share a common safety/gitio core:

1. **Date spreading** — spread a burst of commits across a calendar using
   configurable strategies (Even Spread, Manual Pin, Rate Fill, …), edit
   messages, and apply the rewrite with strong safety guarantees.
2. **Commit splitting** (`regit split`) — split one existing commit into 2–4
   semantically meaningful commits using an external LLM to group its diff hunks.
3. **Commit rewording** (`regit reword`) — regenerate the commit message of one
   or more commits using an LLM; proposes ranked title + body alternatives, with
   an interactive picker in the TUI (via the `m` action panel) and a headless
   dry-run/`--execute` flow on the CLI (one proposal, previewed by default,
   applied with `--execute` — no interactive prompt).
4. **History hygiene** (`regit purge`, `regit redact`) — branch-scoped,
   preview-first rewrites keyed on repo-wide content rather than an explicit
   commit selection. `purge` removes an exact path (file or directory) from
   every commit that ever touched it, as if it were never added. `redact`
   scans history for a literal secret (`git log -S`), follows each hit's line
   forward through later rotations or renames (the cascade), and lets the
   TUI's occurrence picker veto individual hits before applying — headless
   redacts everything `Scan` found; the matched line is either dropped
   whole or has a regex substring replaced within it, per `--mode`. Both
   verbs apply through the same guarded pipeline as every other rewrite: a
   per-commit change-set verifier proves the transform touched exactly the
   paths it predicted, then a post-rewrite absence check re-scans the new
   head to prove the target is actually gone. Side-branch history is a hard
   block for `purge` (it can never guarantee removal there); for `redact` it
   is only an advisory note printed before the apply — regit rewrites just
   the first-parent chain, so if that side-branch content really is
   reachable, the absence check independently rediscovers it after the
   rewrite and aborts anyway, just later.

A complete, **registry-driven headless CLI** mirrors the TUI: every action is
also a flat verb, all projected from one capability registry (see "The
capability registry" below). The verbs, in registry order:

<!-- regit:begin verbs-overview -->
`split` · `reword` · `squash` · `linearize` · `redate` · `retitle` · `reauthor` · `drop-bodies` · `purge` · `redact` · `undo` · `install` · `uninstall`
<!-- regit:end verbs-overview -->

## Core domain types (`internal/model`)

| Type | File | What it is |
|------|------|-----------|
| `model.Commit` | `commit.go` | Immutable snapshot of a git commit (hashes, dates, message, flags). Never mutated — edits produce a separate `Edit`. |
| `model.Plan` | `plan.go` | Collection of `Edit` values validated for date-ordering; the thing `safety.Manager.Apply` consumes. |
| `model.Hunk` | `split.go` | One contiguous region of `diff(parent, commit)`, carrying a stable ID, file header, and patch body. Language-agnostic. |
| `model.HunkGroup` | `split.go` | One future sub-commit: a set of hunk IDs + LLM-generated subject/body/intent/confidence. |
| `model.SplitProposal` | `split.go` | The full ordered plan for splitting one commit: `Hunks`, `Groups`, `ShouldSplit`, and `Reason`. |
| `model.ChangeSet` | `changeset.go` | A normalized set of per-path `PathChange`s between two commits; the **empty** set means "byte-identical". Pure `Equal`/`Mismatch` comparator, no git. |
| `model.Expectation` | `changeset.go` | `(oldCommit, newCommit, Expected ChangeSet)` — what an operation promises to change between two commits. The unit the change-set verifier checks (see "Change-set verification"). |

`model.ValidateCoverage` enforces the partition invariant (every hunk in exactly
one group, no unknown IDs) before any git object is created.

## Architecture: layered packages

```
cmd/regit/          CLI entry points (main.go, dispatch.go)
internal/
  model/            Pure domain types — no imports from other internal packages
  termsafe/         Strips terminal escapes from untrusted text before display
  strategy/         Pure date-distribution algorithms — no git, no UI
  gitio/            THE ONLY PACKAGE THAT SHELLS OUT TO git
  safety/           Guards, backup refs, undo stack
  split/            Commit-splitting orchestrator
  squash/           Commit-squash engine (chain summarization, cache, prompt)
  promptcache/      Shared LLM-result cache (split/reword/squash instantiate it)
  reword/           Commit-rewording engine (variants, cache, prompt building)
  llm/              LLM backend abstraction
  config/           YAML + REGIT_* env configuration
  capability/       Single source of truth: every action as a Command row + headless Run.
                    Imports the engine packages; NEVER imports internal/ui.
  ui/               Bubble Tea TUI (depends on all of the above, incl. capability)
```

The TUI is a two-zone chassis (internal/ui) over a persistent commit backbone
(list + detail): a right-hand FLOW ZONE and a bottom `m` ACTION PANEL, both
data-driven projections rather than a fixed tab bar. `viewFlowZone`
(workspace.go) renders the flow zone: by default it shows the commit detail
(`viewDetailPane`); launching an action that needs an interactive stage swaps
its widget in for the flow's duration instead (flow.go). Redate is the only
flow today — `enterRedateFlow` opens the calendar/strategy widget
(`viewDatesWorkspace`, ws_dates.go) and moves focus onto the calendar; `esc`
exits the flow and returns the zone to the detail pane (`exitFlow`); `tab`
cycles focus list → calendar → params → list while a flow is open
(`focusAfterList`, `updateParamsPane`). With no flow open `tab` is a no-op —
`focusAfterList` returns `focusList` again, since the detail pane is not a
focusable target. `o` / `c` / `t` jump straight to the commit list, calendar,
or strategy pane (panetitle.go `paneFocusKeys`; each pane draws its title into
its top border with that letter highlighted), and `[`/`]` and `<`/`>` change
the strategy and time model from the calendar or strategy pane
(`redateHelperKeys`).

Wizards listed in zone.go `zoneWizards` (reword, squash) draw inside the flow
zone as a titled pane beside the list instead of as a centered popup; their
mode still owns every key, the keybar switches to the empty exclusive
`ctxWizard`, and the flow column widens to `zoneWizardMinWidth`.

Every action is launched from the `m` action panel (actionpanel.go), a
full-width bottom region that replaced the old per-tab feature menus. Its rows
are a pure projection of `capability.Registry()` joined to `featureBindings`
(feature.go) by command `Name` — the same drift-proof guarantee the menus had,
now keyed by what is SELECTED rather than which tab is active (add a verb = a
registry row + a `featureBindings` entry). `selectionKind` coarsens the live
selection to cursor/set/range, and `actionCompat` is the (`Command.Selection`
× kind) → allowed lookup table deciding which rows apply to the current
selection; a row that fails that check, or a verb-specific `panelDetail`
predicate (e.g. squash's "select 2+ consecutive commits"), stays visible but
dimmed with its disabled reason shown in place of a detail string — the panel
teaches the selection model instead of hiding actions. Enabled rows also show
the CLI verb they correspond to (the same name `capability.Render` logs), so
the panel doubles as a lookup from UI action to headless command. Navigation
is arrow-first (`↑/↓` move, `enter` runs, a row's `[letter]` hotkey runs it
directly, `esc`/`m` close); rows are windowed
to the terminal height (`panelWindowRows`, capped at `panelMaxRows` = 10)
rather than growing the frame past the screen. The always-visible keybar is
similarly context-filtered: `currentKeyCtx` maps focus state (panel open >
flow open > commit list) to a `keyCtx`, and `keybarHintFor` renders only the
keymap rows tagged for that context, so the hint line mostly tracks what the
current screen honors — but it is a static filter on the keymap table, not a
live audit, so it is not exact both ways: the global `tab` row shows in every
context, including `ctxList`, where it is the no-op described above; and
`currentKeyCtx` keys off the flow rather than focus, so once focus returns to
the list mid-flow (`tab`/`esc` from the strategy pane) the list and selection
keys work again even though the keybar still shows the redate context and
hides them.

The redate flow's widget holds per-strategy state (`m.strats`, selected by `m.stratIdx`):
a `◀ Name ▶` carousel on the strategy pane's first focus row picks the strategy
(`←/→`); there is no separate palette. Date-range strategies start with their date
fields **unset** (greyed placeholders) and are not applicable until filled; the
moment a strategy is *ready* (dates valid, or — for Manual Pin / Picked Days — ≥1
calendar pin) it **stages live** (`stageActive`/`unstageActive`, which only touch
author dates, never message edits) and the calendar previews the distribution **as
per-day numbers** instead of tiles. Calendar digits pin a quota only while Manual
Pin is active; space picks a day only while Picked Days is active. `R` (reset)
clears every strategy's fields and pins; `A` applies the staged distribution.
Staging is selection-scoped: with an explicit Set/Range selection, `stageActive`
redates only `affectedCommits()`, and every unselected commit becomes a fixed
anchor bounding the segments those targets form; with no selection it stages the
whole active range, matching a bare `redate --commits=`'s own default. The
calendar highlights each segment's pickable day window (`segmentWindows`,
`strategy.Windows`) and snaps the cursor onto an in-window day; digit/space picks
and pins are capacity-limited per window, and overflowing one evicts that
window's most-recently placed pick or pin (`pickWithEviction`/`pinWithEviction`)
rather than refusing the new one. `redate --commits=A^..B` (or a comma-separated
list) is the headless equivalent of making an explicit selection before staging.

The commit backbone is the list — capped at 80 columns (layout.go `listCeilWidth`) —
beside a detail panel that stretches to fill the rest of a wide terminal. The
flow column has a per-flow minimum width (layout.go `flowZoneMinWidths`; the
redate calendar needs `rightPaneWidth`), and the list shrinks to make room. When
the list floor plus that minimum does not fit (below 88 columns, or 110 with the
redate flow open; `showRight`) there is one column: the list alone,
or an open flow in the list's place while the flow has focus
(view.go `flowOwnsNarrowBody`); when the flow hands focus back to the list, the
list shows again, and `tab` switches between them (redate only; redact never
gives focus to the list). The
detail's changed-files list is bounded to the pane height: overflow collapses to a
`… +N more files` line and the whole body is hard-clamped (detail.go `clampLines`),
so a many-file commit can never push the frame past the screen. `d` toggles
date-grouping in the list (`[d]ates`); off renders a flat newest-first list. `a`
toggles an author column (`[a]uthor`, off by default) showing each commit's author
name — or the email username when the name is blank; select-all in a selection
lives on `s`. On load the
list freezes (dims) every pushed commit and every commit shared with the base
branch, whichever boundary is newer (app.go `applyAutoBase`; the title shows
`base: pushed` or the branch name). On an
in-sync trunk branch (main/master with everything already pushed) history starts
fully editable instead of freezing all but the newest commit (base.go
`startsUnlocked`); the apply-time pushed-commit confirmation is unchanged, so this
is a convenience default, not a safety relaxation.

**Change-author action:** `reauthor` is a `featureBindings` row ("Change
author") in the `m` action panel — select commits in the list, open the panel,
and run it to rewrite the author (and, via a checkbox checked by default, the
committer) name/email in a modal wizard (`modeReauthor`, reauthor.go). The
field prefills from the oldest target commit (`reauthorTargetCommits`). It
changes only commit headers, never file content (the rewrite reuses each
original tree and verifies it). Headless: `regit reauthor --name=… --email=…
--commits=A..B [--committer=false] [--execute]`.

The commit list has a selection mode (selection.go) that gives each feature the
selection it needs. The user MARKS commits: Space toggles one, `s` marks all,
`n` clears, and `v` marks a range — the band follows the cursor (Range mode)
until `v`/`enter` adds it to the marks (Set mode); `esc` drops a pending band
and keeps earlier marks, then a second `esc` clears them. `markedHashes()` (the
set plus any pending band, frozen commits excluded) is the single source every
consumer reads. Every editable row carries a checkbox beside the ▸ caret
(list.go `checkbox`: ● marked, ● in the marker color inside a pending band, a
dim ● for every row under the redate flow's whole-range scope, ○ otherwise;
frozen rows have none), and the list title says what an action will touch
(`markSummary`: "3 will change" / "all 9 will change" / "all 9 editable").
An "already pushed" divider row (`pushedDividerRow`) sits above the newest
commit on a remote-tracking ref; it is information, while dimming means frozen.
Other modes: Single (▸ cursor), Active (whole non-frozen range,
frozen cone dimmed), Global (list dimmed). The mode defaults to Single everywhere (`newSelection`/`reset`); the
one exception is entering the redate flow with no explicit selection already
made, which scopes it to the whole active range (`SelActive`) instead —
`enterRedateFlow`'s own default, mirrored by a bare `redate --commits=`. An
explicit Set or Range selection, once made, survives every navigation in or
out of a flow (`restoreFlowSelectionScope` reapplies Active whenever the
redate flow is still open and no explicit selection remains). Reword consumes
a Set as multi-reword (walks the picker per selected commit); Squash consumes
a Set/Range to define one or more chains to collapse; the redate flow consumes
an explicit Set/Range as its target (unselected commits become anchors), else
the whole Active range, with `redate --commits=` as the headless equivalent;
frozen commits are never selectable. Global ignores the selection entirely:
`purge` (repo-wide by nature) is its only panel consumer; `undo` also declares
`SelGlobal` but has no `featureBindings` row, staying bound to the `U` global
key instead of the panel.

Bulk actions (Drop bodies, Retitle) and the action panel's affected-commit
count operate on `affectedCommits()` — the marked commits when there are any,
else the whole active range. Drop bodies opens a confirmation popup
(`modeDropBodies`) reporting how many commits would change before any edit is staged.

Every history mutation funnels through one stage → preview → confirm → apply
pipeline (pipeline.go). A feature stages a `pendingChange` (planChange
for date/message edits, applied with `A`; splitChange for a reviewed split, run
from the action panel); the
`modePipeline` overlay shows its mandatory before/after preview; a typed
branch-name confirm gates already-pushed rewrites; then `commitPending` applies it
through `safety.Manager` (backup ref → op → per-commit change-set verify →
branch-ref CAS) and reloads. `p` opens the same preview read-only. The safety
contract is unchanged — the pipeline only makes the preview + confirm mandatory
before the existing apply call.

### What each package does

**`internal/model`** — shared vocabulary. Every other package imports it; it
imports nothing from `internal/`.

**`internal/strategy`** — pure functions that distribute commits across days.
No git, no TUI, no LLM. Fully unit-testable.

**`internal/gitio`** — the only place `git` runs. All git commands go through
`gitio.Repo`. Key methods: `ReadCommits`, `Replay` (date rewrite; returns the
old→new commit remap), `Hunks` (extract diff hunks), `BuildTreeFromHunks` (apply
hunk subset to worktree), `ApplySplit` (emit sub-commits and replay tail; returns
a `SplitRemap`), `ApplySquash` (emit squashed chains and replay tail; returns a
`SquashRemap`), `DiffChangeSet`/`CommitTrees` (per-path change-set and batched
tree-OID reads that feed the verifier), `VerifyTreesUnchanged` (the ∅-case shim),
`NewWorktree`/`Worktree.Close` (isolated scratch space).
**Never add a git call anywhere else.**

**`internal/safety`** — `Manager` runs the guarded apply protocol through ONE
op-parameterized skeleton (`runApply`): guards → `preflight` → backup ref →
rewrite → `verify` (content, see "Change-set verification" below) →
`verifyChainStructure` (shape) → move branch (compare-and-swap) → record
applied head. `Apply`, `ApplySplit`, and `ApplySquash` each contribute only an
`applyOp` (their guards + rewrite closure) — a protocol step added to
`runApply` holds for every operation by construction.

**`internal/split`** — orchestrates one split: extract hunks → skip-split
guards → LLM grouping → build intermediate trees → validate/repair loop →
verify final tree == original tree → write cache. The `Engine.Propose` method
returns a `SplitProposal` + per-group tree hashes without moving any ref.

**`internal/reword`** — commit-rewording engine. `Engine.Generate` always gathers
the commit's diff itself and sends it to the backend; `--agent-scan` only
additionally lets the agent browse repository files read-only (`Read`/`Grep`/`Glob`,
no shell) for extra context — it never replaces the diff regit sends. Results are
cached under `.git/regit/cache/reword/` and returned as ranked `Variant`
alternatives wrapped in a `Result`. `Result.ToEdit` converts the user's
`Selection` (TitleIdx, BodyIdx, NoBody) into a `model.Edit` ready for
`safety.Apply`.

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

**`internal/ui`** — Bubble Tea TUI. Imports all packages above, including
`capability`. The Split action (in the `m` action panel) uses the same
`split.Engine` that the headless `capability` `Run` uses — one code path, not two.

## The capability registry (`internal/capability`)

`internal/capability` is the single source of truth for every non-interactive
action. Each action is a `Command` row (name, typed `Param`s, an optional
`PositionalSpec` describing its positional-argument slot, examples, limits,
and a headless `Run`). The CLI dispatch (`cmd/regit`), the TUI feature menu
(`internal/ui`), the transparency footer, and `--help`/`--llm-help` are all
PROJECTIONS of this table — they cannot drift apart. `PositionalSpec` drives
both the usage/arguments lines in every help surface and positional-arity
validation in `Parse` (a stray positional on a flag-only verb is a usage
error); the `--llm-help` environment section is likewise projected from
`config.EnvOverrideNames()` and the provider API-key table.

- **Layering:** `engines → capability → ui → cmd/regit`. `capability` imports the
  engine packages but **never** `internal/ui`. TUI closures stay in `ui`, keyed by
  command `Name`.
- **Adding a verb = one registry row** in `registry.go` (+ a name-keyed TUI closure
  if it has a TUI presence). Flag parsing, help, the footer line, and the future
  `mcp` tool all derive automatically.
- **The convention lives in one place:** `pipeline.go`'s `applyOrPreview` runs the
  dry-run / `--execute` / `--force` gradient against the existing `safety.Manager`.
- **Footer fidelity** is guaranteed by a round-trip test: `Render` (footer) is the
  inverse of `Parse` (CLI), so a logged line is always one the CLI accepts.

**The verbs** (each a `Command` row with a `Run` in `run_<verb>.go`):

<!-- regit:begin verbs-detail -->
`split` · `reword` · `squash` · `linearize` · `redate` · `retitle` · `reauthor` · `drop-bodies` · `purge` · `redact` · `undo` · `install` · `uninstall`
<!-- regit:end verbs-detail -->

Every mutating verb except `undo`, `install`, and `uninstall` previews by default
and applies with `--execute`, requiring `--execute --force` to rewrite
already-pushed history; a dirty tree or out-of-order plan is a hard block.
`undo`, `install`, and `uninstall` are exempt — they act immediately; `undo
--list` is read-only.
Exit codes: `0` ok/dry-run · `1` usage error · `2` guard-blocked (dirty, pushed
without `--force`) · `3` apply/runtime error. (`split`/`reword` keep deprecated
`--yes`→`--execute` and no-op `--dry-run` aliases, translated by `deprecate.go`.)

**Agent surface:** `regit --llm-help[=text|md|json]` prints a self-contained,
registry-projected doc of every verb; `regit --help` is the terse list; `regit
<verb> --help` shows one verb's flags.

**Transparency footer (TUI):** the `L` key toggles a command-log panel; on each
applied action the TUI renders its exact `regit … --execute` equivalent via
`capability.Render` (from per-verb `argMap` builders in `argmap.go`) and appends it
to `.git/regit/command.log` (best-effort, non-fatal) — so doing a thing in the UI
teaches its headless command.

## How to build, run, and test

Use `just` (run `just` or `just --list` for all recipes):

```bash
just build                              # compile to dist/regit
just test                               # go test ./...
just test-race                          # tests with race detector + coverage
just ci                                 # full local CI: fmt-check + vet + build + test-race + coverage gate
just e2e                                # end-to-end tests against the built binary
just fuzz [time]                        # run every fuzz target (default 20s each)
just check                              # pre-push battery: ci + e2e + docs drift + vuln scan + fuzz burst
just run                                # launch TUI on current repo
just split HEAD --llm=ollama/qwen2.5-coder      # preview a split (dry-run is the default; add --execute to apply)
just redate --start=2026-01-01 --end=2026-03-01      # preview an even date spread
just retitle '^feat: ' ''                # preview a regex subject rewrite
just llm-help                            # agent-facing docs (regit --llm-help)
just commands                            # list every registered verb (regit --help)
just docs-sync                          # regenerate the marked README/AGENTS.md blocks from code
```

Bare go commands also work:

```bash
go build ./...
go test ./... -cover
go vet ./...
```

## Change-set verification (the safety contract)

Every history rewrite is proven content-safe before the branch ref moves: after
replay but **before** the compare-and-swap, `safety.Manager.verify` (verify.go)
asserts that each rewritten commit changed **exactly** what the operation
promised. A failure aborts and leaves the repo byte-for-byte unchanged — the
branch never moved; only an unreferenced backup ref and dangling objects remain.

The mechanism is data-driven. Each operation produces a list of
`model.Expectation{oldCommit, newCommit, Expected}`, and the verifier checks the
**actual** change-set (from git) against the **expected** one for every pair:

- **Empty expectation** (the safe ops — redate / reword / split) means "these two
  commits must be byte-identical", proven for free by comparing tree OIDs
  (`gitio.CommitTrees`): identical tree ⟺ identical files, every path and mode. No
  diff is run unless a mismatch needs a message.
- **Non-empty expectation** (reserved for future content-changing ops — remove a
  file from history, redact a line) is checked against the actual per-path
  change-set from `gitio.DiffChangeSet` (`git diff --raw`, statuses A/M/D; rename
  detection is off), so exactly the declared paths change and nothing else moves.

Content checks are complemented by a STRUCTURAL check
(`safety.Manager.verifyChainStructure`): after replay, both first-parent chains
are walked to prove the new chain has exactly the expected number of commits
and sits on the same untouched boundary parent as the original (or that both
are rooted) — a remap bug that dropped, duplicated, or reparented commits would
pass pairwise tree equality but not this. `gitio.VerifyTreesUnchanged` is now
just the ∅-expectation HEAD case and remains as a thin shim. On success `verify` returns a `model.VerificationSummary` that
both surfaces render (`verified N commits, 0 paths changed` — the TUI command log
and the headless `Applied.` / `Split into …` output).

**Split** folds its two historical invariants into this model; either failure
aborts before the branch moves:

1. **Local** (`safety.ApplySplit`, before replay): `subs[last].Tree ==
   commit.TreeHash` — the last sub-commit reproduces the original commit's tree.
   The engine (`split.Engine.Propose`) enforces the same check before returning,
   rejecting a corrupt proposal in pure Go.
2. **Global** (via `verify`): the boundary expectation `(C → Cₖ, ∅)` plus an ∅
   expectation per replayed tail pair — every commit's snapshot is
   content-identical end to end. `Cₖ` is the last sub-commit; HEAD is whichever
   pair ends the chain. The only structural change is that commit `C` became `k`.

**Squash** reuses the empty-expectation model for verification:

1. **Per-chain** (via `verify`): the boundary expectation `(chain.Last → S, ∅)`
   for each collapsed chain, ensuring the squashed commit `S` is byte-identical
   in content to the last commit of the chain. Tree OID comparison proves this.
2. **Per-tail-pair**: an ∅ expectation for each replayed commit downstream of a
   squashed chain — every snapshot remains content-identical end to end. The only
   structural change is that consecutive commits in each chain became one.

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
  9. Default (no --execute): print proposal + equivalent commands; stop here
 10. With --execute: guard (shallow/dirty/already-pushed → exit 2 unless --force,
     re-checked in Manager.preflight) → backup ref → ApplySplit (emit C1..Ck, replay
     tail) → verify (empty-expectation change-set, invariant 2) → move branch
     (compare-and-swap) → print summary
```

## Commit-squashing flow (end to end)

```
regit squash <range-ish>
  1. Parse range(s) and resolve commits into chains
  2. Guards: dirty tree? merge commit in range? (safety package)
  3. For each chain: cache lookup at .git/regit/cache/squash/<chainHash>.json
  4. Skip-squash guards: singleton chain? (ignored)
  5. LLM Propose (optional) → message summary
  6. Default (no --execute): print plan (N commits → 1) + equivalent commands; stop here
  7. With --execute: guard (dirty/already-pushed → exit 2 unless --force) → backup
     ref → ApplySquash (emit squashed commits per chain, replay tail) → verify
     (empty expectation per chain + tail pairs) → move branch (compare-and-swap)
     → print summary
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
  The config package is the single place they are read. Loading is STRICT:
  unknown YAML keys, an unresolvable timezone, an unparsable workday clock, or an
  inverted workday window are `Load` errors (`Config.Validate`), never silent
  fallbacks. Headless redate defaults its weekday mask from `cfg.Weekdays` (same
  as the TUI); `--weekdays` overrides it.

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
  them. The fields are named `FileHeader`/`Body` in the code.

- **Marked doc blocks are generated.** The regions between `<!-- regit:begin … -->`
  and `<!-- regit:end … -->` in README.md and AGENTS.md are projections of the
  code (see `internal/docs`). Never hand-edit inside the markers — change the
  code (keymap, registry, config table, strategy names) and run `just docs-sync`;
  the drift test fails CI otherwise.

- **Shallow-clone and dirty-tree blocks live in TWO places, by design.**
  `CheckGuards` (guards.go) checks `state.IsShallow` first for the plan-based verbs,
  but split and squash hand-roll their guards and skip `CheckGuards` — so the
  authoritative block is `Manager.preflight` (manager.go), which re-checks shallow +
  dirty LIVE and is called by every apply path (`Apply`, `ApplySplit`, `ApplySquash`).
  Put any new "must hold at apply time" invariant in `preflight` so no path can
  forget it. `redate --execute` additionally refuses a range containing a merge
  commit unless `--allow-merges` is passed (side-branch dates are left unchanged
  either way — the gate is about surprising the user, not correctness).

- **`git log` is parsed as a NUL-delimited stream, not 0x1f/0x1e.** `read.go` uses
  `git log -z` with `%x00` field separators and forces `--encoding=UTF-8` /
  `--no-show-signature`. NUL is the only byte a git object cannot contain, so an
  adversarial commit message can neither truncate a field nor split a record.
  Rewrites store messages as faithful UTF-8 with no `encoding` header (see
  `commitTreeArgs`) — re-labeling already-transcoded bytes was the old mojibake bug.

- **Every git invocation is bounded by a timeout** (`gitTimeout`, 10 min): a
  wedged git or credential/gpg helper turns into a clear error instead of a
  hang. Guard INPUTS fail closed: a `rev-list --remotes` failure aborts
  `ReadCommits` (never "nothing is pushed"), and an `AppliedHeadFor` read error
  makes undo require `--force` (never "guard off").

- **`runGit` scrubs repository-location `GIT_*` env** (`GIT_DIR`, `GIT_INDEX_FILE`,
  `GIT_WORK_TREE`, `GIT_OBJECT_DIRECTORY`, …) before every call, so an exported
  `GIT_INDEX_FILE` cannot make a worktree op clobber the user's real index. It does
  NOT strip `GIT_CONFIG_GLOBAL/SYSTEM` (tests rely on them) or `GIT_AUTHOR_*/
  COMMITTER_*` (callers append them as explicit overrides).

- **Untrusted repo/LLM text is sanitized before display.** `internal/termsafe`
  (`Line`/`Block`) strips ANSI/OSC escape sequences and stray control bytes; every
  render site that emits a commit subject, author, path, body, or LLM subject routes
  through it, so a crafted commit cannot spoof the terminal or write the clipboard.
  Two boundaries sanitize beyond display: `capability.Render` scrubs every argMap
  value (footer + command.log lines carry no control bytes), and split/squash
  subjects are normalized when the commit object is CREATED
  (`gitio.subCommitMessage` — newlines fold, escapes strip, whitespace trims), so
  an LLM reply can never write a multi-line or escape-carrying subject into real
  history.

- **`--sign` re-signs; the default drops signatures after warning.** `pipeline.go`'s
  `signMode` resolves `--sign` to `gitio.ReSign` and fails fast (exit 2) if the
  repo has no `user.signingkey` configured, rather than letting `commit-tree -S`
  fail deep inside a replay. Without `--sign`, `warnDroppedSignatures` prints a
  heads-up in the preview whenever the active range contains already-signed
  commits, but still proceeds — the user opts in to re-signing, not out of
  dropping.

- **Undo has ONE implementation: `safety.Manager.Restore`.** The session-stack
  `Manager.Undo` and the headless `regit undo` both funnel through it (backup
  selection stays in `run_undo.go`; guard sentinels `ErrUndoHeadMoved` /
  `ErrBackupOtherBranch` map to exit 2). Only the Manager moves branch refs.

- **`undo` refuses to unwind past new work unless `--force`.** Each backup ref
  is paired with a `refs/regit/applied/<branch>/<ts>-<seq>` ref (same
  `<ts>-<seq>` suffix as its `refs/regit/backup/...` ref) recording the HEAD
  that apply produced. `Manager.Undo` compares that recorded head to the
  branch's current tip; a mismatch means another apply or a plain commit
  happened since, and undo refuses unless `--force`. A legacy backup with no
  paired applied ref degrades to the old unguarded behavior. Every `Undo` call
  (forced or not) first backs up the current tip, so the undo itself is always
  reversible via `undo --to=<ref>`.

- **Agent-scan's deny-list is defense-in-depth, not a sandbox.** A research
  spike found `--allowedTools` path-scoping does NOT confine `Read`/`Grep`/`Glob`
  — a prompt injected via the scanned diff can still steer them outside the
  repo. `--disallowedTools` deny rules (`ScanDefaultArgs`, `internal/llm/agent.go`)
  are the only real flags-level hard block, and they DO resolve symlinks and `..`.
  Every deny PATH is expanded to a rule for ALL THREE file tools (Read, Grep, Glob)
  — a `Read(p)` deny alone still lets Grep read `p`'s content — and the list covers
  `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.config`, `~/.kube`, `~/.docker`, `/etc`,
  `/proc`, `~/.netrc`, `~/.git-credentials`, `.env`/`.env.*`, `*.pem`, `id_rsa*`,
  `id_ed25519*`, …. The subprocess also runs with a **scrubbed environment**
  (`scrubbedAgentEnv`, dropping `*API_KEY*`/`*TOKEN*`/`*SECRET*`/cloud vars) so
  `Read(/proc/self/environ)` cannot exfiltrate regit's secrets (the `--test-cmd`
  subprocess runs with the same `llm.ScrubbedEnv`), and a user's
  `--agent-args` are APPENDED to (never replace) the protections — an attempt to
  override `--permission-mode`/`--allowedTools`/`--disallowedTools` is refused. Any
  other path stays readable — treat `--agent-scan` as a trust boundary, never a
  perimeter.

## Where things live

```
cmd/regit/main.go       Entry point: --version/--help/--llm-help, verb dispatch over capability.Registry(), else TUI
cmd/regit/dispatch.go   Builds RunContext (config + repo + clock), parses via capability.Parse, calls Command.Run
cmd/docgen/main.go      Dev tool: regenerates the marked doc blocks (just docs-sync / --check)
internal/model/
  commit.go             Commit, SplitMessage
  edit.go               Edit (pending date/message change)
  plan.go               Plan (validated batch of edits)
  repo.go               RepoState
  split.go              Hunk, HunkGroup, SplitProposal, ValidateCoverage
  changeset.go          ChangeSet, PathChange, Expectation, VerificationSummary, Equal/Mismatch
internal/gitio/
  repo.go               Repo, Open, git shell-out choke point
  read.go               ReadCommits, ReadState
  rewrite.go            Replay (→ remap), VerifyTreesUnchanged
  split.go              ApplySplit (→ SplitRemap), SubCommit
  diff.go               Hunks() (diff → stable-ID model.Hunk), DiffText (wide-context diff)
  buildtree.go          BuildTreeFromHunks (selected hunks → tree, restarts from parent)
  worktree.go           Worktree, NewWorktree, EmptyTree (isolated build worktree)
  refs.go               UpdateRef, BackupRefPrefix, BranchRef, AppliedRefPrefix, RecordAppliedHead/AppliedHeadFor
  changeset.go          DiffChangeSet (git diff --raw), CommitTrees (batched tree OIDs)
internal/promptcache/
  cache.go              Generic Cache[T]/Entry[T] + Fingerprint/ContentHash (one impl for all three LLM caches)
internal/split/
  engine.go             Engine, Propose, buildAndValidate, rebuildAll
  cache.go              promptcache instantiation (.git/regit/cache/split/)
  size.go               ShouldSkipSplit, EffectiveMaxCommits, Fingerprint
  subcommits.go         BuildSubCommits (groups + trees → []gitio.SubCommit)
internal/squash/
  engine.go             Engine, Summarize, Proposal, Cache
  cache.go              promptcache instantiation (.git/regit/cache/squash/)
internal/reword/
  engine.go             Engine, Generate (normal + agent-scan modes), diffHash
  cache.go              promptcache instantiation (.git/regit/cache/reword/)
  model.go              Result, Variant, Selection, DefaultSelection, ToEdit (→ model.Edit)
  prompt.go             BuildMessages, BuildScanMessages, SchemaJSON, ParseVariants
internal/redact/
  engine.go             Redact engine: pickaxe scan, line cascade, plan → RedactSpec
internal/llm/
  backend.go            Backend interface (generic Generate), FewShot
  http.go               HTTPBackend (OpenAI-compatible)
  agent.go              AgentBackend (CLI driver), ScanDefaultArgs
  prompt.go             SchemaJSON, BuildMessages, ParseAndValidate, ExtractJSON, ProposeSplit
internal/safety/
  manager.go            Manager, runApply (the single apply skeleton), Apply, ApplySplit, Restore/Undo
  squash.go             Manager.ApplySquash (squashed chains + tail replay, verified like every apply)
  verify.go             Manager.verify, emptyExpectations, verifyError (the change-set verifier)
  guards.go             CheckGuards, Guard
internal/config/
  config.go             Config, Load, REGIT_* overrides, RewordConfig
internal/gittest/
  env.go                Hermetic git env for test fixtures (private HOME, no inherited GIT_*)
  version.go            Git feature probes for fixtures (e.g. SSH-signing support)
internal/docs/
  blocks.go             Generated doc blocks: commands/strategies/env-vars/verbs renderers
  markers.go            SpliceBlocks — replaces content between regit:begin/end marker lines
  files.go              File→blocks table, FindRoot, Sync/Check (cmd/docgen and drift tests use these)
  drift_test.go         Fails CI when README.md/AGENTS.md disagree with the code
internal/capability/
  command.go            Command, Param, Example, ParamType, RunContext, ParsedArgs (+ typed accessors)
  registry.go           Registry() table (single source of truth), Lookup, strategy/time name resolution
  parse.go              Parse + data-driven Validate (Required / Enum / AppliesIf)
  render.go             Render(name, argMap) — exact runnable CLI line, inverse of Parse (footer fidelity)
  help.go               HelpTerse, HelpCommand, LLMHelp(text|md|json)
  pipeline.go           applyOrPreview (dry-run/--execute/--force gate) + CodedError, ExitCode
  argmap.go             Per-verb argMap builders the TUI footer renders (RedateArgMap, RewordArgMap, …)
  deprecate.go          DeprecatedRewrite (--yes→--execute, drop --dry-run) for split/reword
  backend.go commits.go targets.go splitprint.go   Shared LLM-backend wiring, commit lookup, range resolution, split print
  run_*.go              Headless Run impls, one file per verb (generated list below)
internal/ui/
  app.go                Main Bubble Tea model + update loop; commandLog/pendingCmd footer state, appendCommandLog
  split.go              Split review screen + backend wiring for TUI
  reword.go             Reword variant picker + async Generate cmd for TUI
  retitle.go            Retitle (regex) input modal (modeRetitle)
  feature.go            SelectionMode aliases, featureBindings (action panel = projection of capability.Registry())
  flow.go               flowID + enterRedateFlow/exitFlow (the flow zone's occupant)
  workspace.go          viewFlowZone: renders the flow zone (detail by default, active flow's widget otherwise) + the AUTHORS pane
  actionpanel.go        The `m` action panel: panelRows projection, selectionKind/actionCompat, arrow-nav + letter hotkeys, row windowing
  ws_dates.go           Redate flow widget (calendar + strategy form)
  selection.go          Commit-list selection model: marks (Set), a pending range band (Range), Single/Active/Global
  pipeline.go           Apply pipeline: pendingChange interface + planChange/splitChange + the preview overlay
  view.go               Layout incl. viewCommandLog (the L-toggle transparency panel)
  keys.go               Data-driven keymap table ([L]og toggle); generates the help overlay and keybar
  (other .go files)     Calendar, editor, strategy carousel/form, preview, styles
justfile                All dev recipes — prefer these over raw go commands
AGENTS.md               This file
```

<!-- regit:begin runfiles-map -->
`run_*.go` files: `run_split.go` · `run_reword.go` · `run_squash.go` · `run_linearize.go` · `run_redate.go` · `run_retitle.go` · `run_reauthor.go` · `run_dropbodies.go` · `run_purge.go` · `run_redact.go` · `run_undo.go` · `run_install.go` · `run_uninstall.go`
<!-- regit:end runfiles-map -->
