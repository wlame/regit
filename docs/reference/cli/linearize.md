---
tags:
  - reference
  - cli
---

# linearize

```text
regit linearize [flags]
```

Flatten merge topology into a clean first-parent chain. Every merge on the current branch
is folded into an ordinary commit that lands the side branch's combined changes, so the
history reads as a straight line; the resulting trees are preserved byte for byte. The
default run previews a fold table showing what each merge becomes.

## Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--repo` | VALUE | `.` | Path to the git repository. |
| `--execute` | BOOL | `false` | Apply the linearize (default: dry-run preview). |
| `--force` | BOOL | `false` | Allow rewriting already-pushed commits. |
| `--sign` | BOOL | `false` | Re-sign rewritten commits with your configured signing key. |

??? example "Examples"

    Correct:

    ```bash
    # preview the fold table (dry-run)
    regit linearize

    # fold every merge on the branch into ordinary commits
    regit linearize --execute
    ```

    Incorrect:

    ```bash
    # BAD: linearize takes no range; it always covers the whole branch
    regit linearize HEAD~5..HEAD
    ```

## Limitations

- Folds the whole current branch; per-merge selection and ranges are not supported.
- Side-branch commits stay reachable from other branches/tags that point at them (advisory
  in the preview); the fold only rewrites the current branch.
- Octopus merges fold all non-first parents into the one landing commit.
- Side authorship is preserved as `Co-authored-by` trailers, not as separate commits.
- Previously signed commits lose their signatures unless `--sign` re-signs them.

Exit codes follow the [standard set](index.md#standard-exit-codes); exit `0` also covers
"nothing to linearize".

## Related pages

- [squash](squash.md)
- [undo](undo.md)
- [Configuration](../configuration.md)
