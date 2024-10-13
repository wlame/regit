# ghist

A terminal UI for **safely rewriting the commit and author dates** of your
current git branch. Built for the common situation where a burst of work — often
AI-assisted, or a recreated/squashed history — leaves every commit stamped on the
same day. `ghist` lets you spread those commits across a calendar with a rich set
of strategies, edit messages, and apply the change with strong safety guarantees.

```
┌ ghist · myrepo · branch: main · 24 commits ────────────────────────────────┐
│ COMMITS (24)                  │ 2026  [ ◀ ] [ ▶ ]                           │
│ ▸ a1b2c3   Fix login bug      │       Mon ░░▓░░ ░░░░░ ▓▓░░░ ░░░░░ ░░░       │
│     06-05 14:22 → 03-02 10:14 │       Wed ░▓░░░ ░░░░░ ░░▓▓▓ ▓░░░░ ░░░       │
│   d4e5f6   Add user model     │       Fri ░░░░░ ░▓▓▓░ ░░░░░ ░░░░░ ███       │
│     06-05 14:25 → 03-02 11:40 │       Less ·▇▇▇▇ More                       │
│   7890ab   Refactor API   ⚑   │       Selected: 2026-03-02 (Mon) · 2 commits│
│     06-05 14:30 → 03-09 09:02 │       ✓ sequential                         │
├───────────────────────────────┴─────────────────────────────────────────────┤
│ ● Even Spread · 24 commits edited · ✓ sequential                            │
│ [s]trategy [e]dit [p]review [u]ndo [A]pply [r]eset [g]quotas [tab]focus [?]  │
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
go install github.com/wlame/ghist/cmd/ghist@latest
```

Requires the `git` binary on your `PATH` and Go 1.26+ to build.

## Usage

Run inside any git repository:

```bash
ghist                 # operate on the repo in the current directory
ghist --repo=/path/to/repo
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
| `A` | apply the rewrite (creates a backup first) |
| `u` | undo the last apply |
| `r` | reset pending edits |
| `?` | help · `q` quit |

## Strategies

`ghist` separates **where** a commit lands (day) from **when** during that day
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
| Backup | `refs/ghist/backup/<unix-ts>` created before each apply |
| Undo | restores the most recent backup from within the TUI |

> Rewriting history that others have pulled requires a force-push and can
> disrupt collaborators. `ghist` warns you, but use it deliberately on shared
> branches.

## Configuration

Optional YAML at `$XDG_CONFIG_HOME/ghist/config.yaml` (defaults shown):

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

Any value can be overridden per-run with `GHIST_*` environment variables
(`GHIST_TIMEZONE`, `GHIST_DEFAULT_STRATEGY`, `GHIST_SEED`, …).

## Development

```bash
go build ./...
go test ./... -cover
```

The codebase is layered so the interesting logic is pure and testable:

- `internal/strategy` — pure date-distribution engine (no git, no UI)
- `internal/gitio` — the only package that shells out to `git`
- `internal/safety` — guards, backup refs, undo
- `internal/config` — YAML + env configuration
- `internal/ui` — Bubble Tea terminal interface
- `internal/model` — shared domain types

## License

MIT © wlame
