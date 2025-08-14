package capability

import (
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/gitio"
)

// runUndo restores the branch to a backup ref. It is a recovery operation and is
// exempt from the dry-run/--execute convention: it acts immediately. The pre-undo
// HEAD stays reachable via the other backup refs and the reflog.
func runUndo(ctx *RunContext, a ParsedArgs) error {
	refs, err := ctx.Repo.ListBackupRefs()
	if err != nil {
		return runtimeErr("list backups: %v", err)
	}
	if a.Bool("list") {
		if len(refs) == 0 {
			fmt.Fprintln(ctx.Stdout, "no backups")
			return nil
		}
		for _, r := range refs {
			fmt.Fprintf(ctx.Stdout, "%s  %s\n", Short(r.Hash), r.Name)
		}
		return nil
	}
	if len(refs) == 0 {
		return runtimeErr("nothing to undo: no backup refs")
	}
	target := refs[0] // newest first
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
	}
	state, err := ctx.Repo.ReadState()
	if err != nil {
		return runtimeErr("read state: %v", err)
	}
	if err := ctx.Repo.UpdateRef(gitio.BranchRef(state.Branch), target.Hash, state.HeadHash); err != nil {
		return runtimeErr("restore: %v", err)
	}
	fmt.Fprintf(ctx.Stdout, "Restored %s to %s (was %s)\n", state.Branch, Short(target.Hash), Short(state.HeadHash))
	return nil
}
