package ui

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/model"
)

func TestPlanChange_titleAndPreview(t *testing.T) {
	m := newTestModel(t) // 3 commits, none frozen
	// Stage a date edit on the cursor commit so the preview shows a change.
	c := m.commitAtCursor()
	newDate := c.OrigAuthorDate.AddDate(0, 0, 5)
	m.plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &newDate})

	pc := newPlanChange(m.plan, m.loc, nil, nil)
	if !strings.Contains(pc.title(), "1") {
		t.Errorf("title should mention the 1 edited commit, got %q", pc.title())
	}
	pv := pc.preview(m.st)
	if !strings.Contains(pv.title, "→") && !strings.Contains(strings.Join(pv.lines, "\n"), "→") {
		t.Error("preview should show an original → new arrow for the edited commit")
	}
	// Only the changed commit is listed; the other two are summarized.
	if len(pv.lines) != 2 {
		t.Fatalf("preview should list 1 changed commit plus a summary line, got %d:\n%s", len(pv.lines), strings.Join(pv.lines, "\n"))
	}
	if !strings.Contains(pv.lines[1], "2 unchanged commit(s) not shown") {
		t.Errorf("summary line = %q, want the unchanged count", pv.lines[1])
	}
	// Dates come before the subject, so a narrow overlay cuts the subject.
	row := pv.lines[0]
	subjectAt := strings.Index(row, c.Subject[:10])
	if subjectAt < 0 || strings.Index(row, "→") > subjectAt {
		t.Errorf("the date change should come before the subject: %q", row)
	}
}

// TestPlanChange_previewShowsMessageAndAuthorEdits proves the mandatory preview
// spells out non-date edits (title, body, author) instead of rendering them as a
// visually-unchanged row — the gate must show what it gates.
func TestPlanChange_previewShowsMessageAndAuthorEdits(t *testing.T) {
	m := newTestModel(t)
	c := m.commitAtCursor()
	newTitle := "Completely different subject"
	newName := "New Author"
	newEmail := "new@example.com"
	m.plan.SetEdit(c.Hash, model.Edit{
		NewSubject:     &newTitle,
		DropBody:       true,
		NewAuthorName:  &newName,
		NewAuthorEmail: &newEmail,
	})

	pv := newPlanChange(m.plan, m.loc, nil, nil).preview(m.st)
	joined := strings.Join(pv.lines, "\n")
	for _, want := range []string{"Completely different subject", "title:", "body dropped", "New Author", "new@example.com"} {
		if !strings.Contains(joined, want) {
			t.Errorf("preview should surface %q for a message/author edit, got:\n%s", want, joined)
		}
	}
}

func TestPlanChange_guardCleanRepoNotBlocked(t *testing.T) {
	m := newTestModel(t) // clean local repo, branch main, no upstream
	c := m.commitAtCursor()
	nd := c.OrigAuthorDate.AddDate(0, 0, 1)
	m.plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &nd})

	g := newPlanChange(m.plan, m.loc, nil, nil).guard(m.state)
	if g.Blocked {
		t.Errorf("a clean local repo should not be blocked, got %q", g.BlockReason)
	}
}

func TestViewPipeline_footerReflectsMode(t *testing.T) {
	st := newStyles()
	pv := previewModel{title: "Preview", lines: []string{"a", "b", "c"}}
	apply := viewPipeline(pv, 0, 10, true, 96, st)
	if !strings.Contains(apply, "enter apply") {
		t.Error("apply-mode footer should offer enter apply")
	}
	peek := viewPipeline(pv, 0, 10, false, 96, st)
	if strings.Contains(peek, "enter apply") {
		t.Error("peek-mode footer should NOT offer enter apply")
	}
}

// TestSplitChange_guardConfirmsPushedCommits proves the guard is scoped to the
// split's own rewrite set (target + tail), not to whether the repo has any
// upstream at all: a split whose target is already pushed needs the typed
// branch-name confirmation regardless of RepoState's upstream bookkeeping, and
// a split whose target is unpushed is never gated — even when the RepoState
// says the branch has an upstream elsewhere (model.AnyPushed, mirroring
// safety.Manager.ApplySplit).
func TestSplitChange_guardConfirmsPushedCommits(t *testing.T) {
	pushedTarget := splitChange{commit: model.Commit{Hash: "abc1234567890000", IsPushed: true}}
	g := pushedTarget.guard(model.RepoState{Branch: "main"})
	if !g.NeedsConfirmation || g.ConfirmWord != "main" {
		t.Errorf("a split whose target is pushed should need confirmation with the branch word, got %+v", g)
	}
	// An unpushed target is never gated, even when the repo has an upstream
	// elsewhere — pushed-detection no longer reads RepoState at all.
	unpushedTarget := splitChange{commit: model.Commit{Hash: "abc1234567890000"}}
	upstream := model.RepoState{Branch: "main", HasUpstream: true, UpstreamRef: "origin/main", AheadOfRemote: 1}
	if g2 := unpushedTarget.guard(upstream); g2.NeedsConfirmation || g2.Blocked {
		t.Errorf("a split whose target is unpushed should not be gated, got %+v", g2)
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

// TestStagePlanApply_reportsRefImpact proves the TUI preview overlay surfaces
// the same pre-apply impact report as the headless path (capability.reportImpact):
// a tag sitting on a commit inside the plan's active range must show up in the
// staged planChange's preview body once stagePlanApply builds it.
func TestStagePlanApply_reportsRefImpact(t *testing.T) {
	m := newTestModel(t) // 3 commits, branch main, none frozen
	target := m.plan.Commits[1]
	tagCmd := exec.Command("git", "tag", "v1", target.Hash)
	tagCmd.Dir = m.repo.Root()
	if out, err := tagCmd.CombinedOutput(); err != nil {
		t.Fatalf("git tag: %v\n%s", err, out)
	}
	newDate := target.OrigAuthorDate.AddDate(0, 0, 1)
	m.plan.SetEdit(target.Hash, model.Edit{NewAuthorDate: &newDate})

	updated, _ := m.stagePlanApply(false)
	m2 := updated.(Model)
	pc, ok := m2.pending.(planChange)
	if !ok {
		t.Fatalf("expected pending to be a planChange, got %T", m2.pending)
	}
	body := strings.Join(pc.preview(m2.st).lines, "\n")
	if !strings.Contains(body, "v1") || !strings.Contains(strings.ToLower(body), "orphan") {
		t.Errorf("preview should report the tag impact; got:\n%s", body)
	}
}

// TestRefImpactLines_excludesMovingBranch proves the branch being rewritten
// is never itself reported as an orphan: a repo with no other refs besides
// the current branch must produce no lines even though every commit is in
// the active set.
func TestRefImpactLines_excludesMovingBranch(t *testing.T) {
	m := newTestModel(t)
	if lines := refImpactLines(m.repo, m.plan.Commits, m.state.Branch); lines != nil {
		t.Errorf("expected no impact lines (only the moving branch points here), got %v", lines)
	}
}

func TestPlanChange_previewMarksConflictsWithWarning(t *testing.T) {
	m := newTestModelN(t, 4)
	edited := m.plan.Commits[1]
	future := edited.OrigAuthorDate.AddDate(0, 0, 30)
	m.plan.SetEdit(edited.Hash, model.Edit{NewAuthorDate: &future})
	m.recomputeViolations() // populates plan.Shifted (2 commits)

	pv := newPlanChange(m.plan, m.loc, nil, nil).preview(m.st)
	body := strings.Join(pv.lines, "\n")

	if got := strings.Count(body, "⚠"); got != 2 {
		t.Errorf("expected 2 ⚠-marked rows, got %d in:\n%s", got, body)
	}
	if !strings.Contains(pv.title, "2 conflicting") {
		t.Errorf("title should report 2 conflicting, got %q", pv.title)
	}
}

// TestViewPipelineRendersAtGivenWidth: the modal fills the given width (so a
// short preview no longer renders as a tiny content-hugging box) and long
// lines are clamped instead of overflowing the frame.
func TestViewPipelineRendersAtGivenWidth(t *testing.T) {
	p := previewModel{title: "Test", lines: []string{"short", strings.Repeat("x", 300)}}
	out := viewPipeline(p, 0, 10, false, 96, newStyles())
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > 96+2 { // +2 for the border columns
			t.Fatalf("line width %d exceeds modal width budget", w)
		}
	}
	if w := lipgloss.Width(out); w < 90 {
		t.Fatalf("modal width = %d, want ≈96 (must fill the given width)", w)
	}
}
