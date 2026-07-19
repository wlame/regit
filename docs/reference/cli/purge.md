---
tags:
  - reference
  - cli
---

# purge

```text
regit purge [<path>…] [flags]
```

Remove files or directories from branch history as if never added. Every commit on the
branch is rewritten without the given paths — the branch-scoped, preview-first, undoable
member of the `git filter-repo`/`filter-branch` family. Use `--search` first to find the
exact historical paths (including renamed ancestors) worth purging.

## Positional arguments

| Argument | Default | Description |
|---|---|---|
| `<path>…` | — | Repo-relative file or directory to purge (repeatable). |

## Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--search` | VALUE | — | Read-only: list history paths matching the query, then exit. |
| `--repo` | VALUE | `.` | Path to the git repository. |
| `--execute` | BOOL | `false` | Apply (default: dry-run preview). |
| `--force` | BOOL | `false` | Allow rewriting already-pushed commits. |
| `--sign` | BOOL | `false` | Re-sign rewritten commits with your configured signing key. |

??? example "Examples"

    Correct:

    ```bash
    # find candidate paths in history before purging
    regit purge --search=secret

    # preview removing one file from all history
    regit purge config/secrets.env

    # remove a directory and a file everywhere, applied
    regit purge build/ old.log --execute
    ```

    Incorrect:

    ```bash
    # WRONG: globs are rejected; pass exact paths from --search
    regit purge '*.log'
    ```

!!! warning "Branch-scoped — the old objects survive elsewhere"
    Only the current branch is rewritten: other branches, tags, the reflog, and regit's own
    [backup refs](../../concepts/data-flow.md) still reference the old objects until pruned
    (`git gc`). For whole-repo,
    all-refs scrubbing use `git filter-repo`.

## Limitations

- Blocks if a merge commit sits in the rewritten range, or if the path has history on a
  merged side branch at all (even a since-deleted "pocket" the branch itself never carries
  forward) — see `git filter-repo`.
- Exact repo-relative paths only (no globs); renamed ancestors are separate paths — use
  `--search` to find them.
- The working tree is never touched: a purged path still on disk becomes untracked.
- Commits the purge empties are dropped; commits that were already empty are preserved.

## Related pages

- [redact](redact.md)
- [undo](undo.md)
- [Use case: remove secrets and files](../../use-cases/remove-secrets-from-history.md)
