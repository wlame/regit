package ui

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// TestLinearizeChange_titleAndCommandLines proves the staged linearize change
// summarizes the fold count in its title and carries the CLI-equivalent line
// captured at staging time — mirrors squashChange/splitChange's title() and
// commandLines() contracts (see ui/pipeline.go's pendingChange interface).
func TestLinearizeChange_titleAndCommandLines(t *testing.T) {
	plan := model.LinearizePlan{Folds: []model.LinearizeFold{
		{Merge: model.Commit{Hash: "aaaa"}, NewTitle: "Merge branch a"},
		{Merge: model.Commit{Hash: "bbbb"}, NewTitle: "Merge branch b"},
	}}
	lc := linearizeChange{plan: plan, cmd: "regit linearize"}
	if got := lc.title(); got != "Linearize 2 merge(s)" {
		t.Errorf("title() = %q, want %q", got, "Linearize 2 merge(s)")
	}
	if got := lc.commandLines(); len(got) != 1 || got[0] != "regit linearize" {
		t.Errorf("commandLines() = %v, want [%q]", got, "regit linearize")
	}
}

// TestLinearizeChange_guardMirrorsSafety proves guard() replicates
// safety.Manager.ApplyLinearize's internal checks, in the same order, so the
// pipeline blocks/confirms BEFORE applying instead of disagreeing with what
// the apply will actually allow (see ui/linearize.go's guard doc comment):
// an empty plan and a dirty tree hard-block; a rewrite touching an
// already-pushed commit requires the typed branch-name confirmation instead —
// mirroring squashChange/splitChange's guard.
func TestLinearizeChange_guardMirrorsSafety(t *testing.T) {
	empty := linearizeChange{}
	g := empty.guard(model.RepoState{Branch: "main"})
	if !g.Blocked || !strings.Contains(g.BlockReason, "nothing to linearize") {
		t.Errorf("an empty plan should block with 'nothing to linearize', got %+v", g)
	}

	pushedMerge := model.Commit{Hash: "merge1", IsMerge: true, IsPushed: true}
	pushed := linearizeChange{
		commits: []model.Commit{pushedMerge},
		plan: model.LinearizePlan{
			Folds: []model.LinearizeFold{{Merge: pushedMerge, NewTitle: "Merge x"}},
		},
	}
	g2 := pushed.guard(model.RepoState{Branch: "main"})
	if !g2.NeedsConfirmation || g2.Blocked || g2.ConfirmWord != "main" {
		t.Errorf("a pushed merge commit should need confirmation with the branch word, got %+v", g2)
	}

	// A dirty tree blocks even with folds present and nothing pushed.
	cleanMerge := model.Commit{Hash: "merge2", IsMerge: true}
	dirty := linearizeChange{
		commits: []model.Commit{cleanMerge},
		plan: model.LinearizePlan{
			Folds: []model.LinearizeFold{{Merge: cleanMerge, NewTitle: "Merge y"}},
		},
	}
	if g3 := dirty.guard(model.RepoState{Branch: "main", IsDirty: true}); !g3.Blocked {
		t.Error("a dirty working tree should block a linearize")
	}
}

// TestPanelRow_linearizeEnabledOnlyWithMerges proves the action-panel row
// teaches the selection model the way squash/drop-bodies do (see
// actionpanel.go's panelDetail doc comment): no merge commits in the active
// range disables the row with a reason instead of hiding it; one merge
// enables it with a fold count.
func TestPanelRow_linearizeEnabledOnlyWithMerges(t *testing.T) {
	m := newTestModel(t) // 3 linear commits, no merges
	detail, enabled, reason := panelDetail["linearize"](m)
	if enabled || reason != "no merge commits" {
		t.Errorf("no merges: got detail=%q enabled=%v reason=%q, want disabled with %q",
			detail, enabled, reason, "no merge commits")
	}

	merge := model.Commit{
		Hash: "mergehash", Subject: "Merge topic", IsMerge: true,
		ParentHashes: []string{"p1mergehash", "p2mergehash"},
	}
	commits := append(append([]model.Commit(nil), m.plan.Commits...), merge)
	m.plan = model.NewPlan(commits)

	detail, enabled, reason = panelDetail["linearize"](m)
	if !enabled || detail != "1 merge(s) → fold" || reason != "" {
		t.Errorf("with a merge: got detail=%q enabled=%v reason=%q, want enabled with %q",
			detail, enabled, reason, "1 merge(s) → fold")
	}
}
