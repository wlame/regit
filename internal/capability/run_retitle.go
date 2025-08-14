package capability

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/wlame/regit/internal/model"
)

func runRetitle(ctx *RunContext, a ParsedArgs) error {
	re, err := regexp.Compile(a.String("match"))
	if err != nil {
		return usageErr("invalid --match regex: %v", err)
	}
	repl := a.String("replace")

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
		neu := strings.TrimSpace(re.ReplaceAllString(c.Subject, repl))
		if neu == "" || neu == c.Subject {
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
