---
tags:
  - use-cases
  - purge
  - redact
  - security
---

# Remove secrets and files from history

## Scenario

Three weeks ago someone committed `config/secrets.env` with a live API key in it.
The file was deleted a few commits later — but deletion only removes it from the
tip; every clone still carries the key in history. In the same repository a
`build/` directory was accidentally tracked for a while, bloating the repo.

regit offers two branch-scoped, preview-first tools for this:

- **[`purge`](../reference/cli/purge.md)** removes whole files or directories from branch
  history as if they were never added.
- **[`redact`](../reference/cli/redact.md)** finds a literal secret string across branch history and removes the
  matching lines (or replaces just the secret within them), following the line
  forward through later rotations and renames.

!!! warning "A rewrite is not revocation — rotate the credential regardless"
    Both verbs rewrite **only the current branch**. Other branches, tags, the
    reflog, and regit's own [backup refs](../concepts/data-flow.md) still reference the old objects until git
    prunes them (`git gc`) — and anyone who already pulled has the secret anyway.
    The secret value also lands in `.git/regit/command.log` and your shell history
    through the very invocation that removes it. Treat the key as compromised and
    **rotate it**, whatever the rewrite says. For whole-repo, all-refs scrubbing
    use `git filter-repo`; regit is the branch-scoped, preview-first, undoable
    member of that family.

## Prerequisites

- A clean working tree
- The exact secret string, or the repo-relative path(s) to remove — `purge` takes
  no globs, so use `--search` first to find the paths

## Complete example

```bash
# 1. Find candidate paths anywhere in history (read-only)
regit purge --search=secret

# 2. Purge the file — preview, then apply
regit purge config/secrets.env
regit purge config/secrets.env --execute

# 3. Redact the leaked key everywhere it appears — preview, then apply
regit redact --match=sk-live-abc123
regit redact --match=sk-live-abc123 --execute
```

## Walkthrough

### Step 1: Search history for the paths

```bash
regit purge --search=secret
```

`--search` is read-only: it lists every path in branch history matching the query
and exits — nothing is rewritten.

```
config/secrets.env	(added in 34409b9d)
```

Renamed ancestors are separate paths; search for them too and purge all spellings.

### Step 2: Purge the file

```bash
regit purge config/secrets.env
```

```
Purging 1 path(s):
  config/secrets.env

1 affected commit(s):
  34409b9d  Add deployment config.                             ✖ will be dropped

(dry run — nothing applied; pass --execute to apply)
```

Commits the purge would empty are dropped from the chain (commits that were
already empty are preserved). Re-run with `--execute` to apply. Multiple paths can
be given at once — `regit purge build/ old.log --execute` — but globs like
`'*.log'` are rejected: pass exact repo-relative paths from `--search`. The
working tree is never touched; a purged path still on disk simply becomes
untracked.

### Step 3: Redact the key itself

If the secret also appears inside files you want to keep, use `redact`:

```bash
regit redact --match=sk-live-abc123
```

```
Redacting "sk-live-abc123" (remove the matched line) — 1 occurrence(s):

  config/secrets.env @ 34409b9d
  > API_KEY=sk-live-abc123

1 affected commit(s):
  34409b9d  Add deployment config.                             rewritten

(dry run — nothing applied; pass --execute to apply)
```

`--match` is a **literal substring** (git's pickaxe scan), not a regex — writing
`--match='sk-.*'` would search for those exact characters. In the default
`remove` mode the whole matching line is deleted. redact also follows each hit's
line forward through later history — rotations of the value, file renames — so
descendants of the leaked line are caught too; pass `--no-cascade=true` to
disable that.

### Step 4: Verify and clean up

After `--execute`, the standard apply summary reports the new HEAD and backup
ref. [`regit undo`](../reference/cli/undo.md) restores the pre-rewrite state if the result looks wrong —
which also restores the secret, so decide before force-pushing.

## Expected output

Both verbs preview by default and exit `0`; `--execute` applies and exits `0` on
success. Exit `1` is a usage error (e.g. a glob passed to purge, or
`--mode=replace` without `--replace-pattern`), exit `2` a blocked guard (dirty
tree, a merge commit in the rewritten range, pushed history without `--force`),
exit `3` an apply error.

## Variations

### Replace the secret instead of deleting the line

Keep the line, swap only the key for a placeholder — `--replace-pattern` is an
RE2 regex applied within each matched line, with `$1` capture refs available in
`--replace-with`:

```bash
regit redact --match=sk-live-abc123 --mode=replace --replace-pattern='sk-live-\S+' --replace-with=REDACTED --execute
```

### Narrow the scan to certain paths

```bash
regit redact --match=sk-live-abc123 --in='*.env,config/*'
```

`--in` takes glob(s) limiting which paths are scanned (unlike `purge`, which
takes exact paths only).

### Merge commits and side branches

Both verbs block (exit `2`) if a merge commit sits in the rewritten range, and
purge also blocks when the path has history on a merged side branch. redact
reports secrets found in merged side-branch history as an advisory note — it
rewrites only the first-parent chain, never that side branch's own commits.

## Related pages

- [purge command reference](../reference/cli/purge.md)
- [redact command reference](../reference/cli/redact.md)
- [undo command reference](../reference/cli/undo.md)
- [Troubleshooting](../troubleshooting.md)
