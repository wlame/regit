# Security policy

regit rewrites git history and is routinely pointed at repositories containing
credentials — `regit redact` exists specifically to remove leaked secrets. Take
reports about it seriously; this document says how to make one and what regit
already claims about its own limits.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting: open the repository's
**Security** tab and choose **Report a vulnerability**. The report stays private
between you and the maintainer until an advisory is published.

Please do not open a public issue for a security problem, and please do not
include a real secret in the report — a redacted excerpt or a synthetic
reproduction is enough.

A useful report contains:

- `regit --version` and `git --version`
- the operating system
- the exact command, with any secret replaced by a placeholder
- what you expected and what happened

Expect an acknowledgement within a week. regit is maintained by one person, so
a fix may take longer than that; you will be told where it stands.

## Supported versions

Only the latest tagged release is supported. Fixes land on `main` and ship in
the next tag.

## What counts as a vulnerability

In scope:

- **Content corruption.** A rewrite that changes file content, drops or
  duplicates commits, or moves a branch ref when it should have aborted. regit's
  central promise is that a rewrite never alters a snapshot and a *failed*
  rewrite leaves the repository unchanged.
- **A failed removal reported as success.** `purge` or `redact` claiming a path
  or secret is gone while it remains reachable from the rewritten history, or
  still present in the index or working tree.
- **Secret disclosure by regit itself.** Writing a credential somewhere it can
  be read by another local user, or transmitting one that was not supplied for
  that purpose.
- **Terminal escape injection.** Repository- or model-derived text reaching the
  terminal without passing through `internal/termsafe`, allowing output
  spoofing, a window-title rewrite, or an OSC-52 clipboard write.
- **Guards failing open.** Any path where the dirty-tree, shallow-clone,
  pushed-history, or a verb's merge-commit guard can be bypassed rather than
  blocking.

## Known limits — not vulnerabilities

These are documented behaviors, not defects. A report about one will be closed
with a pointer here, though an argument that a limit is worse than described is
welcome.

- **`--agent-scan` is a trust boundary, not a sandbox.** When an agent CLI is
  allowed to browse the repository, `--disallowedTools` deny rules block a list
  of well-known secret stores and the subprocess environment is scrubbed, but
  any other reachable path stays readable. Real confinement needs an OS-level
  sandbox, which regit does not provide. See `internal/llm/agent.go`.
- **`--test-cmd` runs arbitrary commands** you supply, by design.
- **The command log and prompt cache can contain secrets.** `regit redact
  --match=<secret>` puts the secret on its own logged command line, and cached
  LLM proposals hold the diffs that produced them. Both are written owner-only
  (`0600` in a `0700` directory) under `.git/regit/`, and the redact verb warns
  that the value also lands in your shell history. Rotate a credential after
  redacting it — removing it from history does not un-leak it.
- **Rewriting published history requires a force-push** and can disrupt
  collaborators. regit warns and requires `--force`; the consequences of using
  it are yours.
- **Only the first-parent chain is rewritten.** Content reachable solely from a
  merged side branch is reported as an advisory (redact) or a hard block
  (purge), never silently removed.
- **Old objects survive until garbage collection.** Every apply writes a backup
  ref, so the pre-rewrite objects stay reachable until you delete the ref and
  run `git gc`. That is what makes `regit undo` work.
