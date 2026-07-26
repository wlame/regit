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

// TestApplySplit_rootCommit proves a ROOT commit (no parent) can be split: the
// first sub-commit must be emitted parentless. Before the fix, commitSub passed
// `-p ""` unconditionally and `git commit-tree` failed deep in the apply.
func TestApplySplit_rootCommit(t *testing.T) {
	repo, _, _ := twoCommitRepo(t) // root commit + one child (which becomes the tail)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	root, tail := commits[0], commits[1:]

	empty, err := repo.EmptyTree()
	if err != nil {
		t.Fatal(err)
	}
	// Hunks from the empty baseline up to the root's tree, applied in a worktree
	// checked out at the empty tree, reproduce the root tree exactly.
	hs, err := repo.Hunks(empty, root.TreeHash)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.NewWorktree(empty)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()
	fullTree, err := repo.BuildTreeFromHunks(empty, hs, wt)
	if err != nil {
		t.Fatal(err)
	}
	subs := []SubCommit{
		{Tree: fullTree, Subject: "root piece one", AuthorName: root.AuthorName, AuthorEmail: root.AuthorEmail,
			CommitterName: root.CommitterName, CommitterEmail: root.CommitterEmail,
			AuthorDate: root.OrigAuthorDate, CommitDate: root.OrigCommitDate},
		{Tree: fullTree, Subject: "root piece two", AuthorName: root.AuthorName, AuthorEmail: root.AuthorEmail,
			CommitterName: root.CommitterName, CommitterEmail: root.CommitterEmail,
			AuthorDate: root.OrigAuthorDate, CommitDate: root.OrigCommitDate},
	}

	oldHead := mustGit(t, repo, "rev-parse", "HEAD")
	// Empty parentHash: the split target is the root, so the first sub-commit is a
	// new root.
	newHead, remap, err := repo.ApplySplit("", subs, tail, DropSignatures)
	if err != nil {
		t.Fatalf("ApplySplit on root commit: %v", err)
	}
	if err := repo.VerifyTreesUnchanged(oldHead, newHead); err != nil {
		t.Fatalf("final tree changed: %v", err)
	}
	// The rewritten root must itself be parentless.
	firstParents := mustGit(t, repo, "rev-list", "--max-parents=0", newHead)
	if firstParents == "" {
		t.Fatal("rewritten history has no root commit")
	}
	_ = remap
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

// TestSubCommitMessage_normalizesSubject verifies that LLM-supplied subjects are
// normalized when the commit object is CREATED, not just when displayed: an
// embedded newline can no longer produce a multi-line subject in the real
// commit, and escape sequences are stripped. Bodies keep their structure.
func TestSubCommitMessage_normalizesSubject(t *testing.T) {
	cases := []struct {
		name, subject, body, want string
	}{
		{"newline folds", "Add parser\nsneaky second line", "", "Add parser sneaky second line\n"},
		{"escape stripped", "Add\x1b]0;pwned\x07 parser", "", "Add parser\n"},
		{"whitespace trimmed", "  Add parser  ", "", "Add parser\n"},
		{"body preserved", "Add parser", "line one\nline two", "Add parser\n\nline one\nline two\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subCommitMessage(tc.subject, tc.body); got != tc.want {
				t.Errorf("subCommitMessage(%q, %q) = %q, want %q", tc.subject, tc.body, got, tc.want)
			}
		})
	}
}

// TestApplySplit_noSubCommitsErrors pins ApplySplit's own top-level guard,
// independent of safety.Manager's identical-sounding one: called directly
// with zero sub-commits, it must refuse rather than build a chain of nothing.
func TestApplySplit_noSubCommitsErrors(t *testing.T) {
	repo, parentHash, _, _ := splitFixtureTwoSubsOneTail(t)
	_, _, err := repo.ApplySplit(parentHash, nil, nil, DropSignatures)
	if err == nil {
		t.Fatal("ApplySplit with no sub-commits should fail, got nil error")
	}
}
