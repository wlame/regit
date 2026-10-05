## What this changes

<!-- One or two sentences. Link the issue if there is one. -->

## Why

<!-- The problem being solved, not a restatement of the diff. -->

## Checks

- [ ] `just check` passes (ci + e2e + docs drift + vuln scan + fuzz burst)
- [ ] New behavior has a test that fails without the change
- [ ] Generated doc blocks regenerated with `just docs-sync` if the registry,
      keymap, config, or strategy names changed
- [ ] Commit messages are a single imperative sentence, no prefix, no trailers

## Anything touching history rewriting

<!-- Delete this section if it does not apply. -->

- [ ] The change goes through `internal/gitio` for all git access
- [ ] Any new "must hold at apply time" invariant lives in `Manager.preflight`
      or the `runApply` skeleton, so no operation can skip it
- [ ] Tests prove the repository is unchanged when the operation aborts
