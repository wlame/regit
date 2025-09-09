package safety

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

func TestVerify_emptyExpectationIdenticalTreesPasses(t *testing.T) {
	repo, child, _, _ := buildSplitRepo(t)
	mgr := NewManager(repo)
	sum, err := mgr.verify([]model.Expectation{{OldCommit: child.Hash, NewCommit: child.Hash}})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if sum.Commits != 1 || sum.ChangedPaths != 0 {
		t.Errorf("summary = %+v, want {Commits:1 ChangedPaths:0}", sum)
	}
}

func TestVerify_emptyExpectationButTreesDifferAborts(t *testing.T) {
	repo, child, parentHash, _ := buildSplitRepo(t)
	mgr := NewManager(repo)
	_, err := mgr.verify([]model.Expectation{{OldCommit: parentHash, NewCommit: child.Hash}})
	if err == nil {
		t.Fatal("expected verify to abort: parent and child trees differ")
	}
	if !strings.Contains(err.Error(), "a.txt") {
		t.Errorf("abort message should name the changed file, got: %v", err)
	}
}

func TestVerify_nonEmptyExpectationMatches(t *testing.T) {
	repo, child, parentHash, _ := buildSplitRepo(t)
	mgr := NewManager(repo)
	expected := model.NewChangeSet([]model.PathChange{
		{Path: "a.txt", Status: model.Modified},
		{Path: "c.txt", Status: model.Added},
	})
	sum, err := mgr.verify([]model.Expectation{{OldCommit: parentHash, NewCommit: child.Hash, Expected: expected}})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if sum.ChangedPaths != 2 {
		t.Errorf("ChangedPaths = %d, want 2", sum.ChangedPaths)
	}
}

func TestVerify_nonEmptyExpectationMissingPathAborts(t *testing.T) {
	repo, child, parentHash, _ := buildSplitRepo(t)
	mgr := NewManager(repo)
	expected := model.NewChangeSet([]model.PathChange{{Path: "a.txt", Status: model.Modified}}) // omits c.txt
	_, err := mgr.verify([]model.Expectation{{OldCommit: parentHash, NewCommit: child.Hash, Expected: expected}})
	if err == nil {
		t.Fatal("expected abort: c.txt changed but was not in the expectation")
	}
	if !strings.Contains(err.Error(), "c.txt") {
		t.Errorf("abort message should name the unexpected file, got: %v", err)
	}
}

func TestManagerApply_populatesVerificationSummary(t *testing.T) {
	repo := buildRepo(t) // 2 commits (manager_test.go helper)
	mgr := NewManager(repo)
	plan := spreadPlan(t, repo)
	state, _ := repo.ReadState()
	res, err := mgr.Apply(plan, state, ApplyOptions{UnixTimestamp: 1})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Verification.Commits != 2 {
		t.Errorf("Verification.Commits = %d, want 2", res.Verification.Commits)
	}
	if res.Verification.ChangedPaths != 0 {
		t.Errorf("Verification.ChangedPaths = %d, want 0", res.Verification.ChangedPaths)
	}
}
