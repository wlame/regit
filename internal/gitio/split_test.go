// internal/gitio/split_test.go
package gitio

import (
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

// TestApplySplit_emptyTailSplittingHEAD covers the common case where the commit
// being split IS the current HEAD (tail is nil/empty). The returned hash must
// preserve the final tree byte-for-byte.
func TestApplySplit_emptyTailSplittingHEAD(t *testing.T) {
	repo, parent, _ := twoCommitRepo(t)
	// HEAD is the child commit; read it so we can reuse its identity/dates.
	commits, err := repo.ReadCommits() // root-first: [parent-commit, child-commit]
	if err != nil {
		t.Fatal(err)
	}
	childCommit := commits[1]

	// Build a single sub-commit whose tree equals the child's tree (all hunks).
	hs, err := repo.Hunks(parent, childCommit.TreeHash)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.NewWorktree(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()
	fullTree, err := repo.BuildTreeFromHunks(parent, hs, wt)
	if err != nil {
		t.Fatal(err)
	}

	subs := []SubCommit{
		{
			Tree:           fullTree,
			Subject:        "single piece",
			AuthorName:     childCommit.AuthorName,
			AuthorEmail:    childCommit.AuthorEmail,
			CommitterName:  childCommit.CommitterName,
			CommitterEmail: childCommit.CommitterEmail,
			AuthorDate:     childCommit.OrigAuthorDate,
			CommitDate:     childCommit.OrigCommitDate,
		},
	}

	oldHead := mustGit(t, repo, "rev-parse", "HEAD")
	// nil tail: we are splitting HEAD itself, no downstream commits to replay.
	newHead, _, err := repo.ApplySplit(childCommit.ParentHashes[0], subs, nil, DropSignatures)
	if err != nil {
		t.Fatalf("ApplySplit with empty tail: %v", err)
	}
	// Final tree must be byte-identical to the original HEAD tree.
	if err := repo.VerifyTreesUnchanged(oldHead, newHead); err != nil {
		t.Fatalf("final tree changed: %v", err)
	}
	// History must still have exactly 2 commits (parent + the single sub-commit).
	count := mustGit(t, repo, "rev-list", "--count", newHead)
	if count != "2" {
		t.Fatalf("commit count = %s, want 2", count)
	}
}

// splitFixtureTwoSubsOneTail builds a repo with a parent commit (a.txt, b.txt),
// a child commit that modifies a.txt and adds c.txt, and one empty tail commit.
// It returns the repo, the parentHash (the original split commit's parent),
// two SubCommits whose combined tree equals the child tree, and the tail slice.
func splitFixtureTwoSubsOneTail(t *testing.T) (repo *Repo, parentHash string, subs []SubCommit, tail []model.Commit) {
	t.Helper()
	repo, parent, child := twoCommitRepo(t)
	// Add a tail commit after the child so we can prove the tail is preserved.
	gitT(t, repo.Root(), "commit", "-q", "--allow-empty", "-m", "tail")

	commits, err := repo.ReadCommits() // root-first
	if err != nil {
		t.Fatal(err)
	}
	// commits: [parent, child, tail]. We split `child` into two pieces using its
	// hunks: H1 (a.txt edit) and the rest.
	childCommit := commits[1]
	tail = commits[2:]
	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.NewWorktree(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()
	t1, err := repo.BuildTreeFromHunks(parent, hs[:1], wt)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := repo.BuildTreeFromHunks(parent, hs, wt) // == child tree
	if err != nil {
		t.Fatal(err)
	}
	base := childCommit.OrigAuthorDate
	subs = []SubCommit{
		{Tree: t1, Subject: "part one", AuthorName: childCommit.AuthorName, AuthorEmail: childCommit.AuthorEmail, CommitterName: childCommit.CommitterName, CommitterEmail: childCommit.CommitterEmail, AuthorDate: base, CommitDate: base},
		{Tree: t2, Subject: "part two", AuthorName: childCommit.AuthorName, AuthorEmail: childCommit.AuthorEmail, CommitterName: childCommit.CommitterName, CommitterEmail: childCommit.CommitterEmail, AuthorDate: base.Add(time.Second), CommitDate: base.Add(time.Second)},
	}
	return repo, childCommit.ParentHashes[0], subs, tail
}

func TestApplySplit_twoSubsPreserveTailAndFinalTree(t *testing.T) {
	repo, parentHash, subs, tail := splitFixtureTwoSubsOneTail(t)

	oldHead := mustGit(t, repo, "rev-parse", "HEAD")
	newHead, _, err := repo.ApplySplit(parentHash, subs, tail, DropSignatures)
	if err != nil {
		t.Fatalf("ApplySplit: %v", err)
	}
	// Final tree must be byte-identical to the original HEAD tree.
	if err := repo.VerifyTreesUnchanged(oldHead, newHead); err != nil {
		t.Fatalf("final tree changed: %v", err)
	}
	// New history must have one extra commit (3 -> 4) and the tail message intact.
	count := mustGit(t, repo, "rev-list", "--count", newHead)
	if count != "4" {
		t.Fatalf("commit count = %s, want 4", count)
	}
	if subj := mustGit(t, repo, "log", "-1", "--format=%s", newHead); subj != "tail" {
		t.Fatalf("tail subject = %q, want \"tail\"", subj)
	}
}

func TestApplySplit_returnsRemapForBoundaryAndTail(t *testing.T) {
	// Reuse the same fixture construction as TestApplySplit_twoSubsPreserveTailAndFinalTree:
	// a parent, a child split into two subs, and one tail commit.
	repo, parentHash, subs, tail := splitFixtureTwoSubsOneTail(t)

	newHead, remap, err := repo.ApplySplit(parentHash, subs, tail, DropSignatures)
	if err != nil {
		t.Fatalf("ApplySplit: %v", err)
	}
	if remap.LastSub == "" {
		t.Error("SplitRemap.LastSub should be the final sub-commit hash")
	}
	if len(remap.Tail) != len(tail) {
		t.Fatalf("Tail has %d entries, want %d", len(remap.Tail), len(tail))
	}
	// The replayed last tail commit must be the returned newHead.
	if remap.Tail[tail[len(tail)-1].Hash] != newHead {
		t.Errorf("Tail[lastTail] = %s, want newHead %s", remap.Tail[tail[len(tail)-1].Hash], newHead)
	}
}
