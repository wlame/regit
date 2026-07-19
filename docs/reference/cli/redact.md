---
tags:
  - reference
  - cli
---

# redact

```text
regit redact [flags]
```

Remove or replace a literal secret across branch history. regit scans every commit for the
`--match` text (a git pickaxe-style literal search, not a regex), then either removes each
matching line entirely (default mode) or replaces just the secret within it
(`--mode=replace`). Hits are followed forward through later history — rotations and
renames of the same line — unless `--no-cascade`.

## Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--match` | VALUE | required | Literal secret text to find (NOT a regex). |
| `--in` | A,B | — | Glob(s) narrowing which paths are scanned. |
| `--mode` | NAME | remove | Remove the whole matched line, or replace within it (`remove`/`replace`). |
| `--replace-pattern` | VALUE | required with `--mode=replace` | RE2 regex matched within each target line. |
| `--replace-with` | VALUE | — | Replacement text (`$1` capture refs; omit to strip the match, keep the line). Only with `--mode=replace`. |
| `--no-cascade` | BOOL | `false` | Do not follow a hit's line forward through later history (rotations, renames). |
| `--repo` | VALUE | `.` | Path to the git repository. |
| `--execute` | BOOL | `false` | Apply (default: dry-run preview). |
| `--force` | BOOL | `false` | Allow rewriting already-pushed commits. |
| `--sign` | BOOL | `false` | Re-sign rewritten commits with your configured signing key. |

??? example "Examples"

    Correct:

    ```bash
    # preview removing every line containing the literal secret (dry-run)
    regit redact --match=sk-live-abc123

    # replace only the secret substring (keeping the rest of the line) using
    # capture-group-capable regex, then apply
    regit redact --match=sk-live-abc123 --mode=replace --replace-pattern='sk-live-\S+' --replace-with=REDACTED --execute
    ```

    Incorrect:

    ```bash
    # BAD: --mode=replace without --replace-pattern is rejected: the rule is required
    regit redact --match=sk-live-abc123 --mode=replace --execute

    # WRONG: --match is a literal substring (git pickaxe -S), not a regex —
    # metacharacters are matched verbatim, not as a wildcard
    regit redact --match='sk-.*'
    ```

!!! warning "Rotate the credential — and mind the branch scope"
    The secret value is written to `.git/regit/command.log` and your shell history by this
    very invocation — rotate the credential after redacting. And like [`purge`](purge.md), only the
    first-parent chain of the current branch is rewritten: a match with history on a merged
    side branch is reported as an advisory note, never redacted from that branch's own
    commits.

## Limitations

- Only the first-parent chain is rewritten; a match with history on a merged side branch is
  reported as an advisory note, never redacted from that branch's own commits.
- Blocks if a merge commit sits in the rewritten range (like `purge`).
- `--match` is matched as a literal substring, never a regex; use `--mode=replace` with
  `--replace-pattern` for regex-based edits within a matched line.
- Headless has no picker: every occurrence the scan finds is redacted, including cascaded
  descendants (rotations/renames) unless `--no-cascade`.

## Related pages

- [purge](purge.md)
- [undo](undo.md)
- [Use case: remove secrets and files](../../use-cases/remove-secrets-from-history.md)
