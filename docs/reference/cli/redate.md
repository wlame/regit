---
tags:
  - reference
  - cli
---

# redate

```text
regit redate [flags]
```

Spread commit/author dates across a calendar using a strategy. A **day-assignment
strategy** decides which calendar day each commit lands on, and a **time-of-day model**
decides the clock time within that day; commit order and
[file contents never change](../../concepts/data-flow.md).
The default run previews the full old-date → new-date table until `--execute`.

## Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--strategy` | NAME | required | Day-assignment strategy (see [values](#strategy-values)). |
| `--time` | NAME | config | Time-of-day model (see [values](#time-model-values)). |
| `--start` | YYYY-MM-DD | — | First calendar day of the range. |
| `--end` | YYYY-MM-DD | — | Last calendar day of the range. |
| `--weekdays` | A,B | configured weekdays | Eligible weekdays, e.g. `mon,tue,wed`. |
| `--from` | VALUE | all | Freeze history before this rev. |
| `--commits` | VALUE | whole active range | Target commits: `A..B`, a single ref, or a comma-separated list. |
| `--seed` | N | — | RNG seed for randomized strategies. |
| `--link-dates` | BOOL | config | Keep committer date == author date. |
| `--workday-start` | HH:MM | config | Workday start. |
| `--workday-end` | HH:MM | config | Workday end. |
| `--lunch-start` | HH:MM | — | Lunch-gap start. |
| `--lunch-end` | HH:MM | — | Lunch-gap end. |
| `--allow-merges` | BOOL | `false` | Proceed even if the range contains merge commits (their side branches keep their dates). |
| `--repo` | VALUE | `.` | Path to the git repository. |
| `--execute` | BOOL | `false` | Apply (default: dry-run preview). |
| `--force` | BOOL | `false` | Allow rewriting already-pushed commits. |
| `--sign` | BOOL | `false` | Re-sign rewritten commits with your configured signing key. |

### Strategy-specific flags

These flags only apply with the strategy named in the last column (and some are required
by it):

| Flag | Type | Default | Description | Applies with |
|---|---|---|---|---|
| `--pin` | K=N,… | required | `DAY=N` per-day quotas. | `--strategy=Manual-Pin` |
| `--pick` | A,B | required | `DAY,DAY` chosen days. | `--strategy=Picked-Days` |
| `--spread` | N | `0` | 0–100: how uneven the per-day counts are. `0` keeps the strategy's usual split (exactly even for the date-range strategies, a uniform random scatter for Picked Days); higher values give the days random weights, so some get many more commits. With Picked Days every picked day still gets at least one; with the date-range strategies a high spread can leave some days empty. | `--strategy=Picked-Days`, `Even-Spread`, `Working-Days`, `Weekends-Only` |
| `--per-day-min` | N | — | Min commits per active day. | `--strategy=Rate-Fill` |
| `--per-day-max` | N | — | Max commits per active day. | `--strategy=Rate-Fill` |
| `--offset-days` | N | — | Shift every commit by N days. | `--strategy=Offset-Shift` |
| `--offset-hours` | N | — | Shift every commit by N hours. | `--strategy=Offset-Shift` |
| `--anchor` | YYYY-MM-DD | required | New instant for the first commit. | `--strategy=Preserve-Relative` |
| `--scale` | F | `1` | Stretch/compress original spacing. | `--strategy=Preserve-Relative` |
| `--deadline` | YYYY-MM-DD | required | Last day to work backwards from. | `--strategy=Backwards-from-Deadline` |
| `--compress-day` | YYYY-MM-DD | required | Single target day. | `--strategy=Compress` |

### Time-model-specific flags

| Flag | Type | Default | Description | Applies with |
|---|---|---|---|---|
| `--time-fixed` | HH:MM | required | `HH:MM` for the Fixed-time model. | `--time=Fixed-time` |
| `--jitter-min` | N | config | ± minutes for the Jitter model. | `--time=Jitter` |

### Strategy values

`--strategy` accepts: `Even-Spread`, `Manual-Pin`, `Picked-Days`, `Rate-Fill`,
`Working-Days`, `Weekends-Only`, `Offset-Shift`, `Preserve-Relative`,
`Backwards-from-Deadline`, `Compress`.

### Time model values

`--time` accepts: `Preserve-original`, `Working-hours`, `Evening-hours`,
`Non-working-hours`, `Uniform-random`, `Fixed-time`, `Jitter`.

Both flags accept the kebab-case form shown above or the quoted spaced form
(`--strategy='Even Spread'`).

??? example "Examples"

    Correct:

    ```bash
    # preview an even spread across two months
    regit redate --strategy=Even-Spread --time=Working-hours --start=2026-01-01 --end=2026-03-01

    # pin specific days and apply
    regit redate --strategy=Manual-Pin --pin=2026-03-02=2,2026-03-05=1 --execute
    ```

    Incorrect:

    ```bash
    # BAD: Manual-Pin without --pin is rejected — the per-day quotas are required
    regit redate --strategy=Manual-Pin --start=2026-01-01

    # BAD: a range containing a merge commit is refused without --allow-merges
    regit redate --strategy=Even-Spread --start=2026-01-01 --end=2026-03-01 --execute
    ```

## Limitations

- Strategy/time names come from `strategy.Strategies()`/`TimeModels()`; accepts kebab or
  quoted spaced.
- `--start`/`--end` are validated by the engine per strategy, not by `--help`.

## Related pages

- [reauthor](reauthor.md)
- [undo](undo.md)
- [Configuration](../configuration.md)
- [Use case: spread same-day commits](../../use-cases/spread-same-day-commits.md)
