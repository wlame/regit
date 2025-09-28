package ui

import (
	"fmt"

	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
)

// reauthorChange is the staged author/committer rewrite that drops into the apply
// pipeline. It wraps a plan built at staging time (history cannot change while the
// wizard is open) plus the set of edited hashes for the preview.
type reauthorChange struct {
	plan    *model.Plan
	targets map[string]bool
}

// newReauthorChange builds the plan: an identity Edit on each target commit, with
// the base set to the oldest edited commit so the rewrite replays from there.
func newReauthorChange(commits []model.Commit, targets map[string]bool, name, email string, committer bool) reauthorChange {
	plan := model.NewPlan(commits)
	edited := map[string]bool{}
	minIdx := len(commits)
	for i, c := range commits {
		if !targets[c.Hash] {
			continue
		}
		e := model.NewIdentityEdit(c, name, email, committer)
		if e.IsEmpty() {
			continue
		}
		plan.Edits[c.Hash] = e
		edited[c.Hash] = true
		if i < minIdx {
			minIdx = i
		}
	}
	if minIdx < len(commits) {
		plan.SetBase(minIdx) // freezes older history; keeps the edits at/after base
	}
	return reauthorChange{plan: plan, targets: edited}
}

func (c reauthorChange) title() string {
	return fmt.Sprintf("Change author on %d commit(s)", len(c.targets))
}

// guard reuses the standard plan guard: dirty tree / out-of-order block, pushed
// history needs the typed confirmation.
func (c reauthorChange) guard(state model.RepoState) safety.Guard {
	return safety.CheckGuards(state, c.plan)
}

// preview lists each edited commit (newest-first) as old -> new author, plus a
// committer line when the committer changes; unedited rows are dimmed.
func (c reauthorChange) preview(st styles) previewModel {
	lines := make([]string, 0, len(c.plan.Commits))
	for i := len(c.plan.Commits) - 1; i >= 0; i-- {
		commit := c.plan.Commits[i]
		e, ok := c.plan.EditFor(commit.Hash)
		text := fmt.Sprintf("%s  %-28s %s <%s>", short(commit.Hash),
			truncate(commit.Subject, 28), commit.AuthorName, commit.AuthorEmail)
		if !ok {
			lines = append(lines, st.dim.Render("  "+text))
			continue
		}
		text = fmt.Sprintf("%s  %-28s %s <%s> → %s <%s>", short(commit.Hash),
			truncate(commit.Subject, 28),
			commit.AuthorName, commit.AuthorEmail,
			e.EffectiveAuthorName(commit), e.EffectiveAuthorEmail(commit))
		lines = append(lines, st.newDate.Render("  "+text))
	}
	return previewModel{title: c.title(), lines: lines}
}

func (c reauthorChange) apply(mgr *safety.Manager, state model.RepoState, opts safety.ApplyOptions) (safety.ApplyResult, error) {
	return mgr.Apply(c.plan, state, opts)
}
