---
tags:
  - reference
  - cli
---

# undo

```text
regit undo [flags]
```

Restore the branch to the most recent regit [backup ref](../../concepts/data-flow.md).
Every apply writes a backup of the previous tip first, so `undo` is the escape hatch for
any regit rewrite. It is exempt from
the preview convention and **acts immediately** — there is no `--execute` — but it always
creates a pre-undo backup of the current tip first, so the undo itself can be undone.

## Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--list` | BOOL | `false` | List available backups (read-only). |
| `--to` | VALUE | — | Restore a specific backup (ref name or short hash). |
| `--force` | BOOL | `false` | Unwind even if HEAD moved past the backup (discards newer commits). |
| `--repo` | VALUE | `.` | Path to the git repository. |

??? example "Examples"

    Correct:

    ```bash
    # restore the branch to the latest backup (acts immediately)
    regit undo

    # see available backups before restoring
    regit undo --list

    # unwind even though HEAD advanced past the backup's apply (discards the newer commits)
    regit undo --force
    ```

    Incorrect:

    ```bash
    # BAD: undo takes no --execute; it is exempt from dry-run
    regit undo --execute
    ```

## Limitations

- Exempt from dry-run: restores immediately. The replaced HEAD remains in the reflog.
- Restores only backups taken on the current branch; legacy (pre-branch-qualified) backups
  need an explicit `--to=<ref>`.
- Always creates a pre-undo backup of the current tip first, so the undo itself can be
  undone.
- Refuses (exit 2) when HEAD has advanced past the head the backup's apply produced, unless
  `--force`; backups from before this guard existed have no paired record and are never
  blocked.

## Exit codes

`undo` deviates from the [standard set](index.md#standard-exit-codes):

| Code | Meaning |
|---|---|
| `0` | OK, or nothing to undo |
| `1` | Usage error |
| `2` | Guard blocked (backup from another branch, or HEAD moved past the backup without `--force`) |
| `3` | Restore error |

## Related pages

- [CLI overview](index.md)
- [purge](purge.md)
- [redact](redact.md)
