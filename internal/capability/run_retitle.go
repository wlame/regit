package capability

import (
	"fmt"
	"io"
	"regexp"

	"github.com/wlame/regit/internal/model"
)

func runRetitle(ctx *RunContext, a ParsedArgs) error {
	capitalize, ensurePeriod := a.Bool("capitalize"), a.Bool("ensure-period")
	if !a.Has("match") && !capitalize && !ensurePeriod {
		return usageErr("retitle needs at least one of --match, --capitalize, or --ensure-period")
	}
	var re *regexp.Regexp
	if a.Has("match") {
		var err error
		if re, err = regexp.Compile(a.String("match")); err != nil {
			return usageErr("invalid --match regex: %v", err)
		}
	}
	transform := RetitleTransform(re, a.String("replace"), capitalize, ensurePeriod)

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
	skippedEmpty := 0
	for _, c := range targets {
		neu := transform(c.Subject)
		if neu == c.Subject {
			continue
		}
		if neu == "" {
			skippedEmpty++
			continue
		}
		e := model.Edit{}
		s := neu
		e.NewSubject = &s
		edits[c.Hash] = e
		if idxOf[c.Hash] < minIdx {
			minIdx = idxOf[c.Hash]
		}
	}
	if skippedEmpty > 0 {
		fmt.Fprintf(ctx.Stdout, "skipped %d title(s) that would become empty\n", skippedEmpty)
	}
	if len(edits) == 0 {
		fmt.Fprintln(ctx.Stdout, "no titles would change")
		return nil
	}
	plan.SetBase(minIdx)
	for h, e := range edits {
		plan.SetEdit(h, e)
	}
	return applyOrPreview(ctx, plan, a, previewSubjects)
}

func previewSubjects(w io.Writer, plan *model.Plan) {
	fmt.Fprintln(w, "Title changes:")
	for _, c := range plan.Active() {
		if e, ok := plan.EditFor(c.Hash); ok && e.NewSubject != nil {
			fmt.Fprintf(w, "  %s  %q -> %q\n", Short(c.Hash), c.Subject, *e.NewSubject)
		}
	}
}
