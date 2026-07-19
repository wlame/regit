---
tags:
  - reference
  - cli
---

# install / uninstall

Register — or remove — regit as the `git re` subcommand. `install` creates a `git-re`
symlink next to the regit binary on PATH, after which `git re <command>` is equivalent to
`regit <command>`; `uninstall` removes it. Both are exempt from the preview convention and
act immediately, and neither ever touches a repository.

## install

```text
regit install [flags]
```

### Flags

| Flag | Type | Default | Description |
|---|---|---|---|
| `--force` | BOOL | `false` | Replace an existing `git-re` that does not point at regit. |

??? example "Examples"

    Correct:

    ```bash
    # link git-re next to the regit binary on PATH (applies immediately)
    regit install

    # replace a foreign git-re file with the regit link
    regit install --force
    ```

    Incorrect:

    ```bash
    # BAD: install takes no --execute; it applies immediately and regit uninstall reverses it
    regit install --execute
    ```

### Limitations

- Writes only the `git-re` symlink in the PATH directory containing regit; never touches
  any repository.
- Windows: creating symlinks may require elevated privileges; no copy/shim fallback is
  provided.
- git intercepts a bare `git re --help` (man-page lookup); use `git re -h` or
  `git re <command> --help`.

### Exit codes

`install` deviates from the [standard set](index.md#standard-exit-codes):

| Code | Meaning |
|---|---|
| `0` | OK, or already installed |
| `1` | Usage error |
| `3` | Install error (regit not on PATH, unwritable directory, or foreign `git-re` without `--force`) |

## uninstall

```text
regit uninstall
```

Removes the `git-re` symlink. Takes no flags.

??? example "Examples"

    ```bash
    # remove the git-re link if it points at this regit binary
    regit uninstall
    ```

### Limitations

- Never deletes a `git-re` that does not resolve to the running regit binary.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | OK, or not installed |
| `1` | Usage error |
| `3` | Uninstall error (foreign `git-re`, or removal failed) |

## Related pages

- [CLI overview](index.md)
- [Configuration](../configuration.md)
- [Getting started](../../getting-started.md)
