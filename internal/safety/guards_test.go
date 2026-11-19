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
	g := CheckGuards(model.RepoState{WorktreeStatus: model.WorktreeStatus{HasTrackedChanges: true}}, planWithEdit(false))
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
	if !strings.Contains(g.BlockReason, "before its parent") {
		t.Errorf("block reason should say a commit is dated before its parent, got %q", g.BlockReason)
	}
}

// TestCheckGuards_blockMessageCountsViolationsNotShifted proves the block
// message reports len(plan.Violations), not len(plan.Shifted): a violation
// purely between two commits neither of which the enforcement cascade would
// move (e.g. two anchors whose dates are already out of order) leaves
// Shifted empty while Violations is not. Before the fix this rendered the
// nonsensical "plan has 0 commit(s) conflicting" even though the plan was
// genuinely blocked.
func TestCheckGuards_blockMessageCountsViolationsNotShifted(t *testing.T) {
	p := planWithEdit(false)
	p.Violations = []model.SequenceViolation{{ChildHash: "x", ParentHash: "y"}}
	p.Shifted = nil // deliberately empty, unlike the violation count
	g := CheckGuards(model.RepoState{}, p)
	if !g.Blocked {
		t.Fatal("a plan with violations should block regardless of Shifted")
	}
	if !strings.Contains(g.BlockReason, "1 commit(s)") {
		t.Errorf("BlockReason should count the 1 violation, got %q", g.BlockReason)
	}
	if strings.Contains(g.BlockReason, "0 commit(s)") {
		t.Errorf("BlockReason must not report 0 when a violation exists, got %q", g.BlockReason)
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

// TestCheckGuards_activePushedCommitNeedsConfirmationEvenUnedited proves the
// corrected, broader check: a pushed commit inside plan.Active() (the base
// boundary is 0, so nothing is frozen) needs confirmation even with NO
// individual edit recorded for it. This used to read the other way — the old
// anyPushed(plan) required "IsPushed AND has an edit" — but Replay re-emits
// every commit in plan.Active() as a new git object regardless of whether it
// carries its own edit, so an active-but-unedited pushed commit genuinely gets
// rehashed and must be flagged. (Renamed/flipped from
// TestCheckGuards_pushedButUneditedIsClean, which asserted the narrower,
// now-incorrect behavior.)
func TestCheckGuards_activePushedCommitNeedsConfirmationEvenUnedited(t *testing.T) {
	c := model.Commit{Hash: "a", IsPushed: true}
	p := model.NewPlan([]model.Commit{c}) // no edit set, but Base=0 so c is Active()
	g := CheckGuards(model.RepoState{Branch: "main"}, p)
	if !g.NeedsConfirmation {
		t.Error("an active pushed commit should require confirmation even when unedited")
	}
}

func TestCheckGuards_cleanPlanPasses(t *testing.T) {
	g := CheckGuards(model.RepoState{Branch: "main"}, planWithEdit(false))
	if g.Blocked || g.NeedsConfirmation {
		t.Errorf("clean plan should pass: %+v", g)
	}
}

// TestCheckGuards_blocksShallowClone proves a shallow clone hard-blocks the
// rewrite: its history is incomplete (git history-truncated by --depth), so any
// rewrite that touches the boundary would silently orphan commits the clone
// never fetched.
func TestCheckGuards_blocksShallowClone(t *testing.T) {
	g := CheckGuards(model.RepoState{Branch: "main", IsShallow: true}, planWithEdit(false))
	if !g.Blocked {
		t.Fatal("shallow clone must block the rewrite")
	}
	if !strings.Contains(g.BlockReason, "shallow") {
		t.Errorf("BlockReason = %q, want it to mention shallow", g.BlockReason)
	}
}

// TestCheckGuards_shallowReportedBeforeDirty proves the shallow check runs
// FIRST: a repo that is both shallow and dirty must report the shallow block
// (history-incompleteness), not the dirty-tree block, since incomplete history
// is the more fundamental problem — the dirty tree is moot if the rewrite is
// refused outright anyway.
func TestCheckGuards_shallowReportedBeforeDirty(t *testing.T) {
	g := CheckGuards(model.RepoState{IsShallow: true, WorktreeStatus: model.WorktreeStatus{HasTrackedChanges: true}}, planWithEdit(false))
	if !g.Blocked {
		t.Fatal("shallow+dirty must still block")
	}
	if !strings.Contains(g.BlockReason, "shallow") {
		t.Errorf("BlockReason = %q, want shallow reported before dirty", g.BlockReason)
	}
}

// TestCheckGuards_confirmsOnlyWhenRewriteTouchesPushed is the direct two-sided
// proof that CheckGuards is scoped to the rewrite set, not "does an upstream
// exist anywhere on the branch": a plan whose active range is entirely local
// never prompts, and one whose active range reaches a pushed commit always
// does. This is the split/squash/reword-visible correction: those surfaces used
// to over-prompt whenever ANY upstream existed at all (the old, now-deleted
// RepoState.HasPushedCommits), and now only prompt when model.AnyPushed(the
// actual commits being rewritten) is true.
func TestCheckGuards_confirmsOnlyWhenRewriteTouchesPushed(t *testing.T) {
	pushed := model.Commit{Hash: "aaa", IsPushed: true}
	local := model.Commit{Hash: "bbb", IsPushed: false}
	d := time.Now()

	// A plan whose active range is entirely local: no confirmation.
	planLocal := model.NewPlan([]model.Commit{local})
	planLocal.SetEdit("bbb", model.Edit{NewAuthorDate: &d}) // some edit so it's active
	if g := CheckGuards(model.RepoState{Branch: "main"}, planLocal); g.NeedsConfirmation {
		t.Error("local-only rewrite must not need confirmation")
	}

	// A plan whose active range includes a pushed commit: confirmation required.
	planPushed := model.NewPlan([]model.Commit{pushed})
	planPushed.SetEdit("aaa", model.Edit{NewAuthorDate: &d})
	if g := CheckGuards(model.RepoState{Branch: "main"}, planPushed); !g.NeedsConfirmation {
		t.Error("rewrite touching a pushed commit must need confirmation")
	}
}

// TestCheckGuards_blockMessageNamesTheCommits: the user cannot see date order
// in git status, so the message must say which commit sits before which.
func TestCheckGuards_blockMessageNamesTheCommits(t *testing.T) {
	p := planWithEdit(false)
	p.Violations = []model.SequenceViolation{{ChildHash: "c1c1c1c1c1c1", ParentHash: "p2p2p2p2p2p2"}}

	g := CheckGuards(model.RepoState{}, p)

	if !strings.Contains(g.BlockReason, "c1c1c1c1 is dated before its parent p2p2p2p2") {
		t.Errorf("BlockReason should name the pair, got %q", g.BlockReason)
	}
}
