---
tags:
  - reference
  - cli
---

# reauthor

```text
regit reauthor [flags]
```

Rewrite the author (and by default committer) identity of commits. Give a new `--name`
and/or `--email` and every targeted commit is rewritten with that identity; omit either
one to keep each commit's existing value. File content is never changed — the original
tree is reused and [verified](../../concepts/data-flow.md).

## Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--name` | VALUE | — | New author name (omit to keep each commit's name). |
| `--email` | VALUE | — | New author email (omit to keep each commit's email). |
| `--committer` | BOOL | `true` | Also set committer identity to the new author. |
| `--commits` | VALUE | all | `A..B` range, a single ref, or a comma-separated list. |
| `--repo` | VALUE | `.` | Path to the git repository. |
| `--execute` | BOOL | `false` | Apply (default: dry-run preview). |
| `--force` | BOOL | `false` | Allow rewriting already-pushed commits. |
| `--sign` | BOOL | `false` | Re-sign rewritten commits with your configured signing key. |

??? example "Examples"

    Correct:

    ```bash
    # preview changing author+committer on a range
    regit reauthor --name=wlame --email=new@x.com --commits=6f3a^..a2df

    # change only emails on the last 3 commits; names preserved
    regit reauthor --email=new@x.com --commits=HEAD~3..HEAD --execute

    # change only the author, leaving the committer untouched
    regit reauthor --name=wlame --committer=false --commits=HEAD --execute
    ```

    Incorrect:

    ```bash
    # BAD: neither --name nor --email given — rejected
    regit reauthor --commits=HEAD --execute
    ```

## Limitations

- At least one of `--name`/`--email` is required.
- `--commits` accepts a comma-separated list so an arbitrary TUI selection round-trips.
- Changes commit hashes for targets and their descendants; pushed history needs `--force`.
- Never changes file content (the original tree is reused and verified).

Exit codes follow the [standard set](index.md#standard-exit-codes); exit `0` also covers
"nothing to change".

## Related pages

- [redate](redate.md)
- [retitle](retitle.md)
- [undo](undo.md)
- [Configuration](../configuration.md)
