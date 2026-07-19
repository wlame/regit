---
tags:
  - reference
  - cli
---

# drop-bodies

```text
regit drop-bodies [flags]
```

Strip the body of each commit in a range, keeping titles. Useful when generated or noisy
multi-paragraph messages should collapse to their one-line subjects — for example before
sharing a branch whose bodies were machine-written.

## Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--commits` | VALUE | all | Rev-range `A..B` or a single ref. |
| `--repo` | VALUE | `.` | Path to the git repository. |
| `--execute` | BOOL | `false` | Apply (default: dry-run preview). |
| `--force` | BOOL | `false` | Allow rewriting already-pushed commits. |
| `--sign` | BOOL | `false` | Re-sign rewritten commits with your configured signing key. |

??? example "Examples"

    Correct:

    ```bash
    # preview dropping every commit body on the branch
    regit drop-bodies

    # drop bodies on the last 10 commits
    regit drop-bodies --commits=HEAD~10..HEAD --execute
    ```

## Limitations

- Commits without a body are skipped. Titles are never changed.

## Related pages

- [retitle](retitle.md)
- [reword](reword.md)
- [Use case: polish commit messages](../../use-cases/polish-commit-messages.md)
