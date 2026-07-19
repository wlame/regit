---
tags:
  - use-cases
  - redate
---

# Spread same-day commits across a calendar

## Scenario

A developer finished a feature in one intense AI-assisted session — or recreated a
repository from scratch, or squashed and replayed old history. Either way, `git log`
now shows 30 commits all stamped today. Before sharing the branch they want the
history to read like the work it actually was: spread over the past few weeks,
committed during working hours.

[`regit redate`](../reference/cli/redate.md) does exactly this: it assigns each commit a
new day using a **strategy**, a new time-of-day using a **time model**, and rewrites the
branch without touching a single file — trees are reused byte-for-byte and
[verified](../concepts/data-flow.md) after the rewrite.

## Prerequisites

- `regit` installed (`go install github.com/wlame/regit/cmd/regit@latest`)
- A clean working tree — the dirty-tree guard blocks applies otherwise
- The commits to redate on the **current branch**

## Complete example

```bash
# 1. Preview: spread everything evenly across June, during working hours
regit redate --strategy=Even-Spread --time=Working-hours --start=2026-06-01 --end=2026-07-01

# 2. Same command plus --execute to apply
regit redate --strategy=Even-Spread --time=Working-hours --start=2026-06-01 --end=2026-07-01 --execute
```

## Walkthrough

### Step 1: Preview the date plan

Run the command without `--execute`. Nothing is written — regit prints the full
old → new date mapping so you can sanity-check it:

```bash
regit redate --strategy=Even-Spread --time=Working-hours --start=2026-06-01 --end=2026-07-01
```

`--strategy=Even-Spread` distributes the commits evenly across the eligible days
between `--start` and `--end` (by default the configured weekdays, Monday–Friday).
`--time=Working-hours` places each commit inside your configured workday
(09:00–18:00 by default, adjustable with `--workday-start=HH:MM` /
`--workday-end=HH:MM` or in the config file).

### Step 2: Check ordering

The plan ends with a `✓ sequential` marker: new dates are strictly monotonic, so
the redated history never shows a commit dated before its parent.

### Step 3: Apply

Append `--execute`. regit takes a [backup ref](../concepts/data-flow.md) first, replays the branch with
`git commit-tree` (reusing the original tree objects), verifies every rewritten
commit is content-identical to its original, and only then moves the branch ref.

## Expected output

Preview (abridged — one row per commit):

```
Date plan (30 commit(s)):
  fc9ad900  2026-07-19 11:00 -> 2026-06-01 13:31
  68e4dc2f  2026-07-19 12:00 -> 2026-06-05 17:30
  1aa390cf  2026-07-19 13:00 -> 2026-06-11 16:53
  ...
  34409b9d  2026-07-19 16:00 -> 2026-07-01 15:47
  ✓ sequential

(dry run — nothing applied; pass --execute to apply)
```

With `--execute`, the same plan is followed by the apply summary:

```
Applied. New HEAD a6d3d8f6 (backup refs/regit/backup/main/1784484349-0)
verified 30 commits, 0 paths changed
```

Exit codes: `0` ok / preview, `1` usage error (e.g. `--strategy=Manual-Pin` without
`--pin`), `2` guard blocked, `3` apply error. A blocked apply looks like this and
exits `2`:

```
regit: working tree has uncommitted changes; commit or stash them first
```

## Variations

### Pin exact days with Manual-Pin

Assign per-day quotas — two commits on June 2nd, one on June 5th, and so on:

```bash
regit redate --strategy=Manual-Pin --pin=2026-06-02=2,2026-06-05=1 --execute
```

`--pin` is required for this strategy; running Manual-Pin without it is rejected
with a usage error.

### Work backwards from a deadline

Make the history end on a specific day and walk backwards from there:

```bash
regit redate --strategy=Backwards-from-Deadline --deadline=2026-06-30 --time=Working-hours
```

### Reproducible randomness

Randomized strategies and time models draw from an RNG. Fix the seed to get the
same plan every run:

```bash
regit redate --strategy=Even-Spread --time=Uniform-random --start=2026-06-01 --end=2026-07-01 --seed=42
```

### Redate only part of the branch

Restrict the rewrite to a range (or a single ref, or a comma-separated list):

```bash
regit redate --strategy=Even-Spread --start=2026-06-01 --end=2026-07-01 --commits=HEAD~10..HEAD
```

### Merge commits

Applying to a range containing a merge commit is refused by default (the preview
still runs, with a warning; `--execute` exits `2`). Pass `--allow-merges=true`
to proceed — the merge commits themselves are redated, but their side branches
keep their original dates.

!!! tip "Prefer picking a strategy interactively"
    Running plain `regit` opens the TUI with an interactive strategy picker,
    per-strategy option forms, and a before/after calendar preview — the same
    engine, applied through the same guarded path. The TUI's log panel even shows
    the equivalent headless command for every action it applies.

If the result is not what you wanted, [`regit undo`](../reference/cli/undo.md) restores
the branch to the backup taken before the apply.

## Related pages

- [redate command reference](../reference/cli/redate.md)
- [undo command reference](../reference/cli/undo.md)
- [Configuration](../reference/configuration.md) — default strategy, time model, workday, weekdays, seed
