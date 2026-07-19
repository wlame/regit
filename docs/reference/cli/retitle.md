---
tags:
  - reference
  - cli
---

# retitle

```text
regit retitle [flags]
```

Rewrite commit subjects with a regex across a range. Every commit whose subject matches
`--match` gets the replacement applied; bodies, trees, dates, and authorship stay
untouched. Deterministic and LLM-free — the bulk-edit counterpart to
[`reword`](reword.md).

## Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--match` | VALUE | required | RE2 regex matched against each subject. |
| `--replace` | VALUE | — | Replacement (supports `$1` capture refs). |
| `--commits` | VALUE | all | Rev-range `A..B` or a single ref. |
| `--repo` | VALUE | `.` | Path to the git repository. |
| `--execute` | BOOL | `false` | Apply (default: dry-run preview). |
| `--force` | BOOL | `false` | Allow rewriting already-pushed commits. |
| `--sign` | BOOL | `false` | Re-sign rewritten commits with your configured signing key. |

??? example "Examples"

    Correct:

    ```bash
    # strip a conventional-commit prefix (preview)
    regit retitle --match='^feat: ' --replace=''

    # normalize WIP markers on the last 5 commits
    regit retitle --match='(?i)wip' --replace='WIP' --commits=HEAD~5..HEAD --execute
    ```

## Limitations

- RE2 syntax (no backreferences). Subjects that would become empty are skipped.

## Related pages

- [reword](reword.md)
- [drop-bodies](drop-bodies.md)
- [Use case: polish commit messages](../../use-cases/polish-commit-messages.md)
