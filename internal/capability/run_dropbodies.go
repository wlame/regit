package capability

import (
	"fmt"
	"io"

	"github.com/wlame/regit/internal/model"
)

func runDropBodies(ctx *RunContext, a ParsedArgs) error {
	commits, err := ctx.Repo.ReadCommits()
	if err != nil {
		return runtimeErr("read commits: %v", err)
	}
	targets, err := resolveTargets(ctx.Repo, commits, a.String("commits"))
	if err != nil {
		return usageErr("%v", err)
	}
	idxOf := indexByHash(commits)

	plan := model.NewPlan(commits)
	edits := map[string]model.Edit{}
	minIdx := len(commits)
	for _, c := range targets {
		if !c.HasBody() {
			continue
		}
		edits[c.Hash] = model.Edit{DropBody: true}
		if idxOf[c.Hash] < minIdx {
			minIdx = idxOf[c.Hash]
		}
	}
	if len(edits) == 0 {
		fmt.Fprintln(ctx.Stdout, "no bodies to drop in range")
		return nil
	}
	plan.SetBase(minIdx)
	for h, e := range edits {
		plan.SetEdit(h, e)
	}
	return applyOrPreview(ctx, plan, a, previewDrops)
}

func previewDrops(w io.Writer, plan *model.Plan) {
	fmt.Fprintln(w, "Body changes:")
	for _, c := range plan.Active() {
		if e, ok := plan.EditFor(c.Hash); ok && e.DropBody {
			fmt.Fprintf(w, "  %s  drop body — keep %q\n", Short(c.Hash), c.Subject)
		}
	}
}
