package ui

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

func TestPlanChange_titleAndPreview(t *testing.T) {
	m := newTestModel(t) // 3 commits, none frozen
	// Stage a date edit on the cursor commit so the preview shows a change.
	c := m.commitAtCursor()
	newDate := c.OrigAuthorDate.AddDate(0, 0, 5)
	m.plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &newDate})

	pc := newPlanChange(m.plan, m.loc)
	if !strings.Contains(pc.title(), "1") {
		t.Errorf("title should mention the 1 edited commit, got %q", pc.title())
	}
	pv := pc.preview(m.st)
	if !strings.Contains(pv.title, "→") && !strings.Contains(strings.Join(pv.lines, "\n"), "→") {
		t.Error("preview should show an original → new arrow for the edited commit")
	}
	if len(pv.lines) != len(m.plan.Commits) {
		t.Errorf("preview should have one line per commit (%d), got %d", len(m.plan.Commits), len(pv.lines))
	}
}

func TestPlanChange_guardCleanRepoNotBlocked(t *testing.T) {
	m := newTestModel(t) // clean local repo, branch main, no upstream
	c := m.commitAtCursor()
	nd := c.OrigAuthorDate.AddDate(0, 0, 1)
	m.plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &nd})

	g := newPlanChange(m.plan, m.loc).guard(m.state)
	if g.Blocked {
		t.Errorf("a clean local repo should not be blocked, got %q", g.BlockReason)
	}
}

func TestViewPipeline_footerReflectsMode(t *testing.T) {
	st := newStyles()
	pv := previewModel{title: "Preview", lines: []string{"a", "b", "c"}}
	apply := viewPipeline(pv, 0, 10, true, st)
	if !strings.Contains(apply, "enter apply") {
		t.Error("apply-mode footer should offer enter apply")
	}
	peek := viewPipeline(pv, 0, 10, false, st)
	if strings.Contains(peek, "enter apply") {
		t.Error("peek-mode footer should NOT offer enter apply")
	}
}

func TestSplitChange_guardConfirmsPushedCommits(t *testing.T) {
	sc := splitChange{commit: model.Commit{Hash: "abc1234567890000"}}
	// A repo with an upstream has pushed commits, so a split needs the typed
	// branch-name confirmation (it mirrors safety.Manager.ApplySplit).
	pushed := model.RepoState{Branch: "main", HasUpstream: true, UpstreamRef: "origin/main", AheadOfRemote: 1}
	g := sc.guard(pushed)
	if !g.NeedsConfirmation || g.ConfirmWord != "main" {
		t.Errorf("a split in a repo with pushed commits should need confirmation with the branch word, got %+v", g)
	}
	// A purely local repo (no upstream) is not gated.
	local := model.RepoState{Branch: "main"}
	if g2 := sc.guard(local); g2.NeedsConfirmation || g2.Blocked {
		t.Errorf("a purely local split should not be gated, got %+v", g2)
	}
}

func TestSplitChange_guardBlocksDirtyAndMerge(t *testing.T) {
	sc := splitChange{commit: model.Commit{Hash: "abc1234567890000"}}
	if g := sc.guard(model.RepoState{Branch: "main", IsDirty: true}); !g.Blocked {
		t.Error("a dirty working tree should block a split")
	}
	merge := splitChange{commit: model.Commit{Hash: "abc1234567890000", IsMerge: true}}
	if g := merge.guard(model.RepoState{Branch: "main"}); !g.Blocked {
		t.Error("a merge commit should block a split")
	}
}

func TestPlanChange_previewMarksConflictsWithWarning(t *testing.T) {
	m := newTestModelN(t, 4)
	edited := m.plan.Commits[1]
	future := edited.OrigAuthorDate.AddDate(0, 0, 30)
	m.plan.SetEdit(edited.Hash, model.Edit{NewAuthorDate: &future})
	m.recomputeViolations() // populates plan.Shifted (2 commits)

	pv := newPlanChange(m.plan, m.loc).preview(m.st)
	body := strings.Join(pv.lines, "\n")

	if got := strings.Count(body, "⚠"); got != 2 {
		t.Errorf("expected 2 ⚠-marked rows, got %d in:\n%s", got, body)
	}
	if !strings.Contains(pv.title, "2 conflicting") {
		t.Errorf("title should report 2 conflicting, got %q", pv.title)
	}
}
