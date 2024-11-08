// internal/gitio/split_test.go
package gitio

import (
	"testing"
	"time"
)

func TestApplySplit_twoSubsPreserveTailAndFinalTree(t *testing.T) {
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
	tail := commits[2:]
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
	subs := []SubCommit{
		{Tree: t1, Subject: "part one", AuthorName: childCommit.AuthorName, AuthorEmail: childCommit.AuthorEmail, CommitterName: childCommit.CommitterName, CommitterEmail: childCommit.CommitterEmail, AuthorDate: base, CommitDate: base},
		{Tree: t2, Subject: "part two", AuthorName: childCommit.AuthorName, AuthorEmail: childCommit.AuthorEmail, CommitterName: childCommit.CommitterName, CommitterEmail: childCommit.CommitterEmail, AuthorDate: base.Add(time.Second), CommitDate: base.Add(time.Second)},
	}

	oldHead := mustGit(t, repo, "rev-parse", "HEAD")
	newHead, err := repo.ApplySplit(childCommit.ParentHashes[0], subs, tail, DropSignatures)
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
