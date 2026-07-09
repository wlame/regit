package capability

import (
	"fmt"
	"io"

	"github.com/wlame/regit/internal/model"
)

// runReauthor rewrites the author (and, unless --committer=false, the committer)
// identity of the targeted commits. At least one of --name/--email must be set;
// an omitted field is left unchanged per commit.
func runReauthor(ctx *RunContext, a ParsedArgs) error {
	name := a.String("name")
	email := a.String("email")
	if name == "" && email == "" {
		return usageErr("reauthor needs at least one of --name or --email")
	}

	// --committer defaults to true; the parser does not inject defaults, so apply
	// it explicitly (mirrors how runRedate handles --link-dates).
	committer := true
	if a.Has("committer") {
		committer = a.Bool("committer")
	}

	commits, err := ctx.Repo.ReadCommits()
	if err != nil {
		return runtimeErr("read commits: %v", err)
	}
	targets, err := resolveTargetSet(ctx.Repo, commits, a.String("commits"))
	if err != nil {
		return usageErr("%v", err)
	}
	idxOf := indexByHash(commits)

	plan := model.NewPlan(commits)
	edits := map[string]model.Edit{}
	minIdx := len(commits)
	for _, c := range targets {
		e := model.NewIdentityEdit(c, name, email, committer)
		if e.IsEmpty() {
			continue // identity already matches; nothing to do
		}
		edits[c.Hash] = e
		if idxOf[c.Hash] < minIdx {
			minIdx = idxOf[c.Hash]
		}
	}
	if len(edits) == 0 {
		fmt.Fprintln(ctx.Stdout, "no commits would change")
		return nil
	}
	plan.SetBase(minIdx)
	for h, e := range edits {
		plan.SetEdit(h, e)
	}
	return applyOrPreview(ctx, plan, a, previewAuthors)
}

// previewAuthors lists each changed commit's old → new author (and committer when
// it changes), newest-first to match the TUI backbone.
func previewAuthors(w io.Writer, plan *model.Plan) {
	fmt.Fprintln(w, "Author changes:")
	for i := len(plan.Commits) - 1; i >= 0; i-- {
		c := plan.Commits[i]
		e, ok := plan.EditFor(c.Hash)
		if !ok {
			continue
		}
		fmt.Fprintf(w, "  %s  author %s <%s> -> %s <%s>\n",
			Short(c.Hash),
			c.AuthorName, c.AuthorEmail,
			e.EffectiveAuthorName(c), e.EffectiveAuthorEmail(c))
		if e.NewCommitterName != nil || e.NewCommitterEmail != nil {
			fmt.Fprintf(w, "        committer %s <%s> -> %s <%s>\n",
				c.CommitterName, c.CommitterEmail,
				e.EffectiveCommitterName(c), e.EffectiveCommitterEmail(c))
		}
	}
}
