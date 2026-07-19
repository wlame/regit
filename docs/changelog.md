---
tags:
  - changelog
---

# Changelog

regit is **pre-release**. There are no tagged versions yet — builds report the
version `dev` (or a `git describe` string when built via `just build`), and the
only tag in the repository is the `before_tui` milestone marker, which is not a
release.

Once versions are tagged, releases will be listed here and on
[GitHub Releases](https://github.com/wlame/regit/releases).

## Unreleased

The current development state includes these feature areas:

- **Date spreading** ([`redate`](reference/cli/redate.md)) — 10 day-assignment strategies
  combined with 7 time-of-day models, applied under regit's safety guarantees.
- **LLM-assisted rewriting** — [`split`](reference/cli/split.md) (one commit into 2–4
  semantic commits), [`reword`](reference/cli/reword.md) (regenerated messages with ranked
  alternatives), and [`squash`](reference/cli/squash.md) (collapse consecutive runs,
  optionally LLM-summarized).
- **History hygiene** — [`purge`](reference/cli/purge.md) (remove a file or directory from
  branch history as if never added) and [`redact`](reference/cli/redact.md) (remove or
  replace a literal secret across history).
- **Supporting verbs** — [`linearize`](reference/cli/linearize.md),
  [`retitle`](reference/cli/retitle.md), [`reauthor`](reference/cli/reauthor.md),
  [`drop-bodies`](reference/cli/drop-bodies.md), [`undo`](reference/cli/undo.md), and
  [`install`/`uninstall`](reference/cli/install.md) for the `git re` integration.

## Related pages

- [About](about.md) — project status and known limitations
- [Getting started](getting-started.md) — install and first run
- [Contributing](contributing.md) — dev setup and release gates
