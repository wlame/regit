// internal/capability/run_linearize.go
package capability

import (
	"fmt"
	"sort"
	"strings"

	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/termsafe"
)

// runLinearize folds every merge on the current branch's first-parent chain
// into an ordinary commit (tree preserved, synthesized message, Co-authored-by
// attribution). Preview by default; --execute applies through the guarded
// skeleton. The whole branch is always in scope (v1: no ranges).
func runLinearize(ctx *RunContext, a ParsedArgs) error {
	repo := ctx.Repo
	commits, err := repo.ReadCommits()
	if err != nil {
		return runtimeErr("read commits: %v", err)
	}
	if len(a.Positional) > 0 {
		return usageErr("linearize takes no arguments; it always covers the whole branch")
	}

	state, err := repo.ReadState()
	if err != nil {
		return runtimeErr("read state: %v", err)
	}

	sideOf := map[string][]model.SideCommit{}
	for _, c := range commits {
		if !c.IsMerge {
			continue
		}
		side, err := repo.SideCommits(c.Hash)
		if err != nil {
			return runtimeErr("side commits of %s: %v", Short(c.Hash), err)
		}
		sideOf[c.Hash] = side
	}
	plan := model.BuildLinearizePlan(commits, sideOf)
	if len(plan.Folds) == 0 {
		fmt.Fprintln(ctx.Stdout, "Nothing to linearize: no merge commits on the branch.")
		return nil
	}

	printLinearizePlan(ctx, plan, state.Branch)
	warnDroppedSignatures(ctx.Stdout, plan.RewrittenCommits(commits), a)
	reportImpact(ctx, ctx.Stdout, plan.RewrittenCommits(commits))

	if !a.Bool("execute") {
		fmt.Fprintln(ctx.Stdout, "\n(dry run — nothing applied; pass --execute to apply)")
		return nil
	}

	if state.IsShallow {
		return guardErr("shallow clone — history is incomplete; run 'git fetch --unshallow' first")
	}
	if state.IsDirty {
		return guardErr("working tree has uncommitted changes; commit or stash them first")
	}
	if model.AnyPushed(plan.RewrittenCommits(commits)) && !a.Bool("force") {
		return guardErr("some commits are already pushed — re-run with --force to rewrite them")
	}

	sm, err := signMode(ctx, a)
	if err != nil {
		return err
	}
	mgr := safety.NewManager(repo)
	res, err := mgr.ApplyLinearize(commits, plan, state, safety.ApplyOptions{
		Sign:          sm,
		UnixTimestamp: ctx.Now.Unix(),
		Confirmed:     a.Bool("force"),
		ExpectedHead:  commits[len(commits)-1].Hash,
	})
	if err != nil {
		return applyGuardErr(err)
	}
	fmt.Fprintf(ctx.Stdout, "\nLinearized %d merge(s). New HEAD %s (backup %s)\n",
		len(plan.Folds), Short(res.NewHead), res.BackupRef)
	fmt.Fprintln(ctx.Stdout, res.Verification.String())
	pushHint(ctx.Stdout, state)
	return nil
}

// printLinearizePlan renders the fold table and, per fold whose side commits
// remain reachable from other refs, a non-blocking advisory naming those refs
// (the redact side-history voice: predict, don't block). History-derived text
// (the synthesized title) is sanitized through termsafe before it reaches the
// terminal, since a hostile commit subject could otherwise smuggle control
// bytes into the output.
func printLinearizePlan(ctx *RunContext, plan model.LinearizePlan, branch string) {
	fmt.Fprintf(ctx.Stdout, "Linearize: %d merge(s) fold into ordinary commits\n\n", len(plan.Folds))
	for _, f := range plan.Folds {
		fmt.Fprintf(ctx.Stdout, "  %s  %-50s  %d commit(s) folded, %d co-author(s)\n",
			Short(f.Merge.Hash), termsafe.Line(f.NewTitle), len(f.Side), len(f.CoAuthors))
	}
	for _, f := range plan.Folds {
		if len(f.Side) == 0 || len(f.Merge.ParentHashes) < 2 {
			continue
		}
		refSet := map[string]bool{}
		for _, parent := range f.Merge.ParentHashes[1:] {
			refs, err := ctx.Repo.RefsContaining(parent, branch)
			if err != nil {
				continue // advisory only; a lookup failure never blocks the preview
			}
			for _, ref := range refs {
				refSet[ref] = true
			}
		}
		if len(refSet) == 0 {
			continue
		}
		refs := make([]string, 0, len(refSet))
		for ref := range refSet {
			refs = append(refs, ref)
		}
		sort.Strings(refs)
		fmt.Fprintf(ctx.Stdout,
			"  note: %d folded commit(s) of %s remain reachable from: %s\n",
			len(f.Side), Short(f.Merge.Hash), strings.Join(refs, ", "))
	}
}
