package safety

import (
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

func planWithEdit(pushed bool) *model.Plan {
	c := model.Commit{Hash: "a", IsPushed: pushed}
	p := model.NewPlan([]model.Commit{c})
	d := time.Now()
	p.SetEdit("a", model.Edit{NewAuthorDate: &d})
	return p
}

func TestCheckGuards_dirtyTreeBlocks(t *testing.T) {
	g := CheckGuards(model.RepoState{IsDirty: true}, planWithEdit(false))
	if !g.Blocked {
		t.Fatal("dirty tree should block")
	}
}

func TestCheckGuards_sequenceViolationBlocks(t *testing.T) {
	p := planWithEdit(false)
	p.Violations = []model.SequenceViolation{{ChildHash: "b", ParentHash: "a"}}
	p.Shifted = []string{"b"}
	g := CheckGuards(model.RepoState{}, p)
	if !g.Blocked {
		t.Fatal("sequence violation should block")
	}
	if !strings.Contains(g.BlockReason, "conflicting") {
		t.Errorf("block reason should mention conflicting commits, got %q", g.BlockReason)
	}
}

func TestCheckGuards_pushedNeedsConfirmation(t *testing.T) {
	g := CheckGuards(model.RepoState{Branch: "main"}, planWithEdit(true))
	if g.Blocked {
		t.Fatal("pushed commits should not hard-block")
	}
	if !g.NeedsConfirmation {
		t.Fatal("pushed commits should require confirmation")
	}
	if g.ConfirmWord != "main" {
		t.Errorf("confirm word should be the branch name, got %q", g.ConfirmWord)
	}
}

func TestCheckGuards_pushedButUneditedIsClean(t *testing.T) {
	// A pushed commit that is NOT being edited should not trigger confirmation.
	c := model.Commit{Hash: "a", IsPushed: true}
	p := model.NewPlan([]model.Commit{c}) // no edit set
	g := CheckGuards(model.RepoState{Branch: "main"}, p)
	if g.NeedsConfirmation {
		t.Error("unedited pushed commit should not require confirmation")
	}
}

func TestCheckGuards_cleanPlanPasses(t *testing.T) {
	g := CheckGuards(model.RepoState{Branch: "main"}, planWithEdit(false))
	if g.Blocked || g.NeedsConfirmation {
		t.Errorf("clean plan should pass: %+v", g)
	}
}
