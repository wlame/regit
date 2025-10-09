package capability

import (
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/gitio"
)

// runUndo restores the branch to a backup ref. It is a recovery operation and is
// exempt from the dry-run/--execute convention: it acts immediately. The pre-undo
// HEAD stays reachable via the reflog.
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
		return runtimeErr("nothing to undo: no backup refs")
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
			if legacy > 0 {
				return runtimeErr("nothing to undo for branch %s (%d legacy backup(s) exist; restore one explicitly with --to=<ref>)",
					state.Branch, legacy)
			}
			return runtimeErr("nothing to undo for branch %s (regit undo --list shows all backups)", state.Branch)
		}
	}

	if err := ctx.Repo.UpdateRef(gitio.BranchRef(state.Branch), target.Hash, state.HeadHash); err != nil {
		return runtimeErr("restore: %v", err)
	}
	fmt.Fprintf(ctx.Stdout, "Restored %s to %s (was %s)\n", state.Branch, Short(target.Hash), Short(state.HeadHash))
	return nil
}
