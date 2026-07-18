// internal/capability/run_linearize.go
package capability

import (
	"fmt"

	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
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
// (the redact side-history voice: predict, don't block).
func printLinearizePlan(ctx *RunContext, plan model.LinearizePlan, branch string) {
	fmt.Fprintf(ctx.Stdout, "Linearize: %d merge(s) fold into ordinary commits\n\n", len(plan.Folds))
	for _, f := range plan.Folds {
		fmt.Fprintf(ctx.Stdout, "  %s  %-50s  %d commit(s) folded, %d co-author(s)\n",
			Short(f.Merge.Hash), f.NewTitle, len(f.Side), len(f.CoAuthors))
	}
	for _, f := range plan.Folds {
		if len(f.Side) == 0 {
			continue
		}
		tip := f.Side[len(f.Side)-1].Hash
		refs, err := ctx.Repo.RefsContaining(tip, branch)
		if err != nil || len(refs) == 0 {
			continue // advisory only; a lookup failure never blocks the preview
		}
		fmt.Fprintf(ctx.Stdout,
			"  note: %d folded commit(s) of %s remain reachable from: %v\n",
			len(f.Side), Short(f.Merge.Hash), refs)
	}
}
