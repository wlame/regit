package safety

import (
	"testing"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// TestApplySplit_recordsAppliedHead and TestApplySquash_recordsAppliedHead
// guard against a future refactor silently dropping the RecordAppliedHead call
// from either non-Apply rewrite path. Manager.Apply's own recording is already
// exercised end-to-end via the capability-layer undo tests (applyRedatePlan +
// AppliedHeadFor), but nothing previously asserted the same round-trip for
// ApplySplit/ApplySquash directly against the safety package's real fixtures.
func TestApplySplit_recordsAppliedHead(t *testing.T) {
	repo, childCommit, parentHash, tail := buildSplitRepo(t)
	mgr := NewManager(repo)
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	oldHead, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	parentTreeHash, err := repo.ResolveRef(parentHash + "^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	subs := buildSubCommits(t, repo, parentTreeHash, childCommit)

	res, err := mgr.ApplySplit(
		childCommit, parentHash, subs, tail, state,
		ApplyOptions{UnixTimestamp: 9999, ExpectedHead: oldHead},
	)
	if err != nil {
		t.Fatalf("ApplySplit: %v", err)
	}

	applied, ok, err := repo.AppliedHeadFor(res.BackupRef)
	if err != nil {
		t.Fatalf("AppliedHeadFor: %v", err)
	}
	if !ok {
		t.Fatal("expected a paired applied ref for ApplySplit's backup, found none")
	}
	if applied != res.NewHead {
		t.Errorf("recorded applied head = %s, want ApplySplit's NewHead %s", applied, res.NewHead)
	}
}

func TestApplySquash_recordsAppliedHead(t *testing.T) {
	repo := newLinearRepo(t, 4) // c0..c3 root-first
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	ch := model.SquashChain{
		Members: []model.Commit{commits[2], commits[3]}, Subject: "Squashed",
		AuthorName: commits[2].AuthorName, AuthorEmail: commits[2].AuthorEmail,
		AuthorDate: commits[3].OrigAuthorDate,
	}
	plan := model.SquashPlan{Chains: []model.SquashChain{ch}}

	mgr := NewManager(repo)
	res, err := mgr.ApplySquash(commits, plan, state,
		ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1717689600, ExpectedHead: state.HeadHash})
	if err != nil {
		t.Fatalf("ApplySquash: %v", err)
	}

	applied, ok, err := repo.AppliedHeadFor(res.BackupRef)
	if err != nil {
		t.Fatalf("AppliedHeadFor: %v", err)
	}
	if !ok {
		t.Fatal("expected a paired applied ref for ApplySquash's backup, found none")
	}
	if applied != res.NewHead {
		t.Errorf("recorded applied head = %s, want ApplySquash's NewHead %s", applied, res.NewHead)
	}
}
