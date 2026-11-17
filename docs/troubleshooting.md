---
tags:
  - troubleshooting
  - debugging
---

# Troubleshooting

Every regit error is printed to stderr with a `regit: ` prefix, and the process
exit code tells you which *kind* of failure occurred. In the error listings
below, angle-bracket parts like `<branch>` or `<hash>` are filled in with your
actual branch name, commit hash, or path — the surrounding text is exactly what
regit prints.

## Exit codes

Every verb uses the same exit-code contract:

| Code | Meaning |
|------|---------|
| `0` | Success — the preview printed, or the apply completed |
| `1` | Usage error — bad flag, missing required argument, unusable LLM selection |
| `2` | Guard blocked — a safety check refused the operation (shallow clone, dirty tree, moved HEAD, merges in range, pushed history without `--force`, undo guards) |
| `3` | Apply / LLM error — the rewrite or the LLM call itself failed at runtime |

!!! note "Preview is the default"
    Mutating verbs [preview by default](concepts/data-flow.md) and only rewrite with `--execute`. Most
    guard blocks below fire at apply time — a preview (exit 0) can succeed and
    the subsequent `--execute` still be refused (exit 2).

---

## Guard blocks (exit 2)

### Shallow clone

```
regit: refusing to rewrite a shallow clone — history is incomplete; run 'git fetch --unshallow' first
```

That is the wording of the plan verbs (`redate`, `retitle`, `reauthor`,
`drop-bodies`, `reword`); `split`, `squash`, `purge`, `redact`, and `linearize`
report the same condition without the leading clause:

```
regit: shallow clone — history is incomplete; run 'git fetch --unshallow' first
```

Just before the branch moves the condition is re-checked one last time; if the
repository became shallow only in that instant, the refusal reads as follows
and is reported as an apply error (exit 3):

```
regit: rewrite blocked: shallow clone — history is incomplete; run 'git fetch --unshallow' first
```

**Cause**: The repository was cloned with `--depth` (or otherwise grafted).
Rewriting a shallow history is unsafe because the replay would build on an
artificial boundary commit instead of the real history.

**Fix**: Fetch the full history, then retry.

```bash
git fetch --unshallow
```

---

### Dirty working tree

```
regit: working tree has uncommitted changes to tracked files; commit or stash them first
```

A last-instant re-check just before the branch moves reports the same
condition as an apply error (exit 3):

```
regit: rewrite blocked: working tree has uncommitted changes to tracked files; commit or stash them first
```

**Cause**: There are staged or unstaged changes to files git tracks. regit
refuses to rewrite history while those files differ from HEAD.

**Fix**: Commit or stash, then retry.

```bash
git stash        # or: git commit -am "wip"
regit redate --strategy=Even-Spread --start=2026-01-01 --end=2026-03-01 --execute
git stash pop
```

Untracked files do not block most commands: a redate, reword, retitle,
reauthor, squash, split, or linearize keeps every file identical and only moves
the branch pointer. `purge` and `redact` rewrite the checkout, so they also
refuse while untracked files exist:

```
regit: working tree has untracked files and this operation rewrites the checkout; remove them, add them to .gitignore, or run 'git stash -u' first
```

A plain `git stash` leaves untracked files in place; `git stash -u` takes them.
A file that `purge` removed from history stays on disk as an untracked copy, so
it has this effect on the next `purge` or `redact`.

The TUI reads the working tree again each time you apply, so cleaning it in
another terminal is enough; there is no need to restart.

---

### Branch moved since the plan was created (HEAD moved)

```
regit: branch moved since the plan was created: branch <branch> (plan base <hash>, now <hash>); reload and re-stage
```

**Cause**: Between computing the plan/proposal and applying it, the branch tip
changed — a new commit landed, another regit apply ran, or something else moved
the ref. The apply is anchored to the exact head the plan was computed from
(a compare-and-swap check), so a moved head aborts before any ref is touched.

**Fix**: Re-run the command against the current head — the preview and the
apply are recomputed from the new tip.

```bash
regit retitle --match='^feat: ' --replace='' --execute
```

In the TUI, reload the repository view and stage the edit again.

---

### Merge commits in the range

Each history-rewriting verb that cannot cleanly cross a merge refuses with its
own message:

```
regit: range contains <N> merge commit(s); their side branches keep their original dates — pass --allow-merges to proceed
```

```
regit: cannot squash across merge commit <hash>
```

```
regit: cannot purge across merge commit <hash>
```

```
regit: cannot redact across merge commit <hash>
```

**Cause**: regit's replay only remaps a rewritten commit's first parent. For
[`redate`](reference/cli/redate.md), a merge's side branch keeps its original dates —
surprising enough that applying is gated behind an explicit opt-in. For
[`squash`](reference/cli/squash.md), [`purge`](reference/cli/purge.md), and
[`redact`](reference/cli/redact.md), a merge inside the rewritten range is a hard block.
(The `squash` refusal fires inside the guarded apply and is reported as an
apply error — exit 3; the `redate`, `purge`, and `redact` blocks are guard
blocks — exit 2.)

**Fix**:

- `redate` — opt in explicitly if partial coverage of side branches is
  acceptable:

    ```bash
    regit redate --strategy=Even-Spread --start=2026-01-01 --end=2026-03-01 --allow-merges --execute
    ```

- `squash` — pick a range that does not span the merge, or flatten the branch
  first with [`linearize`](reference/cli/linearize.md):

    ```bash
    regit linearize --execute
    regit squash 6f3a^..a2df --execute
    ```

- `purge` / `redact` — run `regit linearize --execute` first, or use
  [git filter-repo](https://github.com/newren/git-filter-repo) for
  full-history (all-refs) rewrites. Note that `purge` also blocks when the path
  has history on a merged side branch:

    ```
    regit: <path> also exists in a merged side branch's history, which regit cannot rewrite — see git filter-repo
    ```

---

### Pushed history requires `--execute --force`

```
regit: some commits already exist on a remote; rewriting them will require a force-push and can disrupt collaborators — re-run with --force to rewrite them
```

That is the plan verbs' wording (`redate`, `retitle`, `reauthor`,
`drop-bodies`, `reword`). `squash`, `purge`, `redact`, and `linearize` print:

```
regit: some commits are already pushed — re-run with --force to rewrite them
```

and `split` names the commit:

```
regit: commit <hash> (or a commit after it) is already pushed — re-run with --force to rewrite it
```

**Cause**: The rewrite range includes commits that already exist on the
upstream. Rewriting them changes their hashes, so publishing the result needs a
force-push — regit demands an explicit acknowledgement. (In the TUI this
surfaces as a typed branch-name confirmation instead.)

**Fix**: Confirm you accept the force-push consequence:

```bash
regit reword HEAD~2 HEAD --llm=openai/gpt-4o --execute --force
```

After the apply, publish with `git push --force-with-lease`.

!!! warning "Coordinate with collaborators"
    Anyone who already pulled the old commits will have to rebase onto the
    rewritten history. Prefer rewriting only unpushed commits when you can.

---

## Undo

### Backup belongs to another branch

```
regit: backup belongs to another branch: <ref> was taken on branch <other>; you are on <branch> — check out <other> to undo it
```

When you name the backup explicitly with `--to`, the same refusal reads:

```
regit: backup <ref> was taken on branch <other>; you are on <branch> — check out <other> to undo it
```

**Cause**: [Backups](reference/cli/undo.md) are branch-qualified
(`refs/regit/backup/<branch>/<ts>-<seq>`). The backup you asked to restore was
taken on a different branch than the one currently checked out; restoring it
here would point *this* branch at *that* branch's old tip.

**Fix**: Switch to the branch the backup belongs to, then undo:

```bash
git checkout <other-branch>
regit undo
```

Use `regit undo --list` to see every backup with its branch.

---

### HEAD moved past the backup

```
regit: HEAD moved past this backup (apply produced <hash>, branch is now <hash>); re-run with --force to unwind it
```

If the guard itself cannot be evaluated, the refusal degrades closed:

```
regit: HEAD moved past this backup: cannot check the advanced-HEAD guard (<reason>); re-run with --force to unwind anyway
```

**Cause**: Work happened after the apply this backup belongs to — another
apply, or plain commits. Restoring the backup would silently discard those
newer commits, so regit refuses unless forced.

**Fix**: If you genuinely want to discard everything after the backup:

```bash
regit undo --force
```

Otherwise pick a newer backup explicitly:

```bash
regit undo --list
regit undo --to=<ref-or-short-hash>
```

!!! tip "Undo is itself reversible"
    Before moving the branch, `regit undo` backs up the *current* tip. If you
    force-unwound too far, `regit undo --list` shows that pre-undo backup and
    `regit undo --to=<ref>` restores it.

---

## LLM (split / reword / squash)

### No LLM configured

```
regit: no LLM configured: pass --llm=provider/model or --agent=<cmd>
```

**Cause**: [`split`](reference/cli/split.md), [`reword`](reference/cli/reword.md), and
`squash` need an [LLM backend](reference/llm-providers.md), and neither the
`--llm` / `--agent` flags nor the `llm:` config block (nor the
`REGIT_LLM_PROVIDER` / `REGIT_LLM_MODEL` env vars) name one. This is a
fail-fast usage error (exit 1) — nothing was contacted or changed.

**Fix**: Pick a backend one of three ways.

=== "Flag"
    ```bash
    regit split HEAD --llm=ollama/qwen2.5-coder
    ```

=== "Config file"
    ```yaml
    # ~/.config/regit/config.yaml
    llm:
      provider: openai
      model: gpt-4o
    ```

=== "Environment"
    ```bash
    export REGIT_LLM_PROVIDER=openai
    export REGIT_LLM_MODEL=gpt-4o
    ```

Hosted providers read their API key from the environment only:
[`REGIT_LLM_API_KEY`](reference/llm-providers.md), falling back to `OPENAI_API_KEY` /
`OPENROUTER_API_KEY`.
Local providers (`ollama`, `lmstudio`) need no key.

---

### Malformed or unknown `--llm` value

```
regit: no model in --llm: use provider/model, e.g. openai/gpt-4o (got provider "<value>")
```

```
regit: unknown LLM provider "<value>"; known: ollama, lmstudio, openai, openrouter (or pass --llm-base-url)
```

**Cause**: `--llm` takes `provider/model`, split on the *first* slash. Passing
just a model name (`--llm=gpt-4o`) parses as a provider with no model; a
provider outside the built-in table has no default endpoint.

**Fix**:

```bash
regit reword HEAD --llm=openai/gpt-4o
# self-hosted / other OpenAI-compatible endpoint:
regit reword HEAD --llm=openai/my-model --llm-base-url=http://localhost:8000/v1
```

---

## Configuration

regit loads config strictly: unknown YAML keys and unusable values are hard
errors at startup, never silent fallbacks. Config errors are printed with a
`load config:` prefix.

### Unknown YAML key

```
regit: load config: parse <path>/config.yaml: yaml: unmarshal errors:
  line <N>: field <key> not found in type config.Config
```

**Cause**: The config file contains a key regit does not know — usually a typo
(e.g. `default_stratgy`). Strict decoding rejects it so a misspelled setting
cannot silently fall back to a default.

**Fix**: Fix or remove the offending key on the reported line. The recognized
top-level keys are listed in the
[configuration reference](reference/configuration.md).

---

### Invalid timezone

```
regit: load config: config: timezone "<value>" is not a recognized IANA zone (or "local")
```

**Cause**: The [`timezone`](reference/configuration.md) key (or `REGIT_TIMEZONE`) is not a valid IANA zone
name and is not the literal `local`.

**Fix**: Use an IANA name or `local`:

```yaml
timezone: Europe/Berlin   # or: local
```

---

### Inverted workday window

```
regit: load config: config: workday_end "<end>" must be after workday_start "<start>"
```

**Cause**: [`workday_end`](reference/configuration.md) is at or before `workday_start`, so
time-of-day models like [Working hours](reference/cli/redate.md) would have an empty
window to place commits in.

**Fix**: Make the window strictly positive:

```yaml
workday_start: "09:00"
workday_end: "18:00"
```

The same values can come from `REGIT_WORKDAY_START` / `REGIT_WORKDAY_END` —
environment overrides are validated identically.

---

## Install (`git re` integration)

### A foreign `git-re` already exists

```
regit: <path>/git-re already exists and does not point at regit; re-run with --force to replace it
```

**Cause**: [`regit install`](reference/cli/install.md) creates a `git-re` symlink next to the
PATH-resolved regit binary so `git re` works as a subcommand. Something else
already occupies that name — a different program, a plain file, or a dangling
symlink — and regit will not overwrite what it cannot prove is its own.

**Fix**: If you are sure the existing `git-re` should be replaced:

```bash
regit install --force
```

Otherwise inspect it first: `ls -l <path>/git-re`.

---

### regit not on PATH

```
regit: regit is not on your $PATH; put the binary on PATH first (e.g. `just install`), then re-run `regit install`
```

**Cause**: The installer places the symlink next to the PATH-resolved `regit`
binary; if `regit` itself is not findable on `$PATH`, there is nowhere valid to
put the shim.

**Fix**:

```bash
just install       # or copy the binary into a directory on $PATH
regit install
```

---

## Diagnostic steps

When something is wrong and you don't have a specific error message:

1. Check the version: `regit --version`
2. Check repository preconditions: `git status` (clean tree?) and
   `git rev-parse --is-shallow-repository` (must be `false`)
3. List available backups: `regit undo --list`
4. Review what regit actually ran: `.git/regit/command.log` records the
   headless-equivalent command for every action applied in the TUI (for plain
   headless runs, the command is whatever you typed — check your shell history)
5. Check the process exit code (`echo $?`) against the table above to classify
   the failure

## Still broken?

[Open an issue](https://github.com/wlame/regit/issues/new) and include:

- Output of `regit --version`
- The full `regit: …` error line and the exit code
- The exact command you ran (or the relevant `.git/regit/command.log` lines)
- Your config file, if you use one (it contains no secrets)

## Related pages

- [undo command reference](reference/cli/undo.md)
- [Configuration reference](reference/configuration.md)
- [LLM providers](reference/llm-providers.md)
- [CLI overview and shared flags](reference/cli/index.md)
