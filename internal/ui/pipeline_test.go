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
