package capability

import (
	"errors"
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/safety"
)

// runUndo restores the branch to a backup ref. It is a recovery operation and is
// exempt from the dry-run/--execute convention: it acts immediately. The pre-undo
// HEAD stays reachable via the reflog. Before moving the branch, it also backs
// up the current tip (so the undo itself is reversible) and refuses to unwind
// past a HEAD that has advanced beyond the backup's recorded applied head,
// unless --force is set.
func runUndo(ctx *RunContext, a ParsedArgs) error {
	refs, err := ctx.Repo.ListBackupRefs()
	if err != nil {
		return runtimeErr("list backups: %v", err)
	}
	state, err := ctx.Repo.ReadState()
	if err != nil {
		return runtimeErr("read state: %v", err)
	}
	if a.Bool("list") {
		if len(refs) == 0 {
			fmt.Fprintln(ctx.Stdout, "no backups")
			return nil
		}
		for _, r := range refs {
			branch := r.Branch
			if branch == "" {
				branch = "(legacy)"
			}
			fmt.Fprintf(ctx.Stdout, "%s  %-24s %s\n", Short(r.Hash), branch, r.Name)
		}
		return nil
	}
	if len(refs) == 0 {
		// A repo with no backups is a benign no-op, not a runtime failure —
		// matching squash's "nothing to squash" and reauthor's "nothing to
		// change" convention (exit 0).
		fmt.Fprintln(ctx.Stdout, "Nothing to undo: no backup refs.")
		return nil
	}

	var target gitio.BackupRef
	if to := a.String("to"); to != "" {
		found := false
		for _, r := range refs {
			if r.Name == to || strings.HasSuffix(r.Name, "/"+to) || Short(r.Hash) == to {
				target, found = r, true
				break
			}
		}
		if !found {
			return usageErr("backup %q not found (try regit undo --list)", to)
		}
		switch {
		case target.Branch == "":
			// Legacy flat ref: branch ownership cannot be verified. Allowed only
			// via explicit --to, with a warning.
			fmt.Fprintf(ctx.Stderr, "warning: %s is a legacy backup; its branch cannot be verified\n", target.Name)
		case target.Branch != state.Branch:
			return guardErr("backup %s was taken on branch %s; you are on %s — check out %s to undo it",
				target.Name, target.Branch, state.Branch, target.Branch)
		}
	} else {
		// Default: newest backup taken on the CURRENT branch (H4). Legacy refs
		// are never picked implicitly.
		found, legacy := false, 0
		for _, r := range refs {
			if r.Branch == "" {
				legacy++
				continue
			}
			if r.Branch == state.Branch {
				target, found = r, true
				break // refs are newest-first
			}
		}
		if !found {
			// Unlike the empty-repo no-op above, backups DO exist here — they
			// just cannot be picked implicitly (legacy or other-branch refs).
			// That is a guard refusal (exit 2), not a success and not a
			// runtime failure: the user must choose one with --to.
			if legacy > 0 {
				return guardErr("nothing to undo for branch %s (%d legacy backup(s) exist; restore one explicitly with --to=<ref>)",
					state.Branch, legacy)
			}
			return guardErr("nothing to undo for branch %s (regit undo --list shows all backups)", state.Branch)
		}
	}

	// The restore itself — branch guard, advanced-HEAD guard, pre-undo backup,
	// compare-and-swap ref move — is the Manager's single implementation
	// (safety.Manager.Restore); this file only selects WHICH backup to restore.
	// Sentinel errors map the Manager's guard refusals to exit code 2.
	restored, err := safety.NewManager(ctx.Repo).Restore(target.Name, a.Bool("force"), ctx.Now.Unix())
	if err != nil {
		if errors.Is(err, safety.ErrUndoHeadMoved) || errors.Is(err, safety.ErrBackupOtherBranch) {
			return guardErr("%v", err)
		}
		return runtimeErr("restore: %v", err)
	}
	fmt.Fprintf(ctx.Stdout, "Restored %s to %s (was %s)\n", state.Branch, Short(restored), Short(state.HeadHash))
	return nil
}
