package safety

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// buildSplitRepo builds a repo with three commits: parent → child (2 hunks:
// modifies a.txt AND adds c.txt) → tail. Returns the repo, the child commit,
// and the tail slice (for passing to ApplySplit). The child's parent tree hash
// and the child tree hash are embedded in the returned commit objects.
func buildSplitRepo(t *testing.T) (*gitio.Repo, model.Commit, string, []model.Commit) {
	t.Helper()
	dir := t.TempDir()

	// runGit is a local helper that sets minimal git identity and runs a command.
	runGit := func(env []string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir, env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	runGit(nil, "init", "-q")
	runGit(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(nil, "config", "user.name", "wlame")
	runGit(nil, "config", "user.email", "vvlame@gmail.com")
	runGit(nil, "config", "commit.gpgsign", "false")

	stamp := "GIT_AUTHOR_DATE=2026-01-01T10:00:00+00:00"
	stampC := "GIT_COMMITTER_DATE=2026-01-01T10:00:00+00:00"
	dateEnv := []string{stamp, stampC}

	// parent commit: one file
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("keep\n"), 0o644)
	runGit(nil, "add", "-A")
	runGit(dateEnv, "commit", "-q", "-m", "parent")

	// child commit: modifies a.txt AND adds c.txt (guarantees ≥2 hunks)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("ONE\ntwo\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("new file\n"), 0o644)
	runGit(nil, "add", "-A")
	runGit(dateEnv, "commit", "-q", "-m", "child")

	// tail commit: empty (just bumps history by one)
	runGit(dateEnv, "commit", "-q", "--allow-empty", "-m", "tail")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	// commits is root-first: [parent, child, tail]
	if len(commits) != 3 {
		t.Fatalf("expected 3 commits, got %d", len(commits))
	}
	childCommit := commits[1]
	tail := commits[2:]
	return repo, childCommit, commits[0].Hash, tail
}

// buildSubCommits creates two SubCommits for childCommit whose last tree equals
// the child's TreeHash, by splitting the child's hunks: first hunk goes in the
// first sub-commit, all hunks go in the second (making its tree == child's tree).
func buildSubCommits(t *testing.T, repo *gitio.Repo, parentTreeHash string, childCommit model.Commit) []gitio.SubCommit {
	t.Helper()
	hs, err := repo.Hunks(parentTreeHash, childCommit.TreeHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) < 2 {
		t.Fatalf("need ≥2 hunks, got %d", len(hs))
	}

	wt, err := repo.NewWorktree(parentTreeHash)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()

	// Intermediate tree: apply just the first hunk.
	t1, err := repo.BuildTreeFromHunks(parentTreeHash, hs[:1], wt)
	if err != nil {
		t.Fatal(err)
	}
	// Full tree: apply all hunks — must equal the original child tree.
	t2, err := repo.BuildTreeFromHunks(parentTreeHash, hs, wt)
	if err != nil {
		t.Fatal(err)
	}
	if t2 != childCommit.TreeHash {
		t.Fatalf("full rebuild tree %s != child TreeHash %s", t2, childCommit.TreeHash)
	}

	base := childCommit.OrigAuthorDate
	return []gitio.SubCommit{
		{
			Tree:           t1,
			Subject:        "part one",
			AuthorName:     childCommit.AuthorName,
			AuthorEmail:    childCommit.AuthorEmail,
			CommitterName:  childCommit.CommitterName,
			CommitterEmail: childCommit.CommitterEmail,
			AuthorDate:     base,
			CommitDate:     base,
		},
		{
			Tree:           t2,
			Subject:        "part two",
			AuthorName:     childCommit.AuthorName,
			AuthorEmail:    childCommit.AuthorEmail,
			CommitterName:  childCommit.CommitterName,
			CommitterEmail: childCommit.CommitterEmail,
			AuthorDate:     base.Add(time.Second),
			CommitDate:     base.Add(time.Second),
		},
	}
}

// TestApplySplit_refusesMergeCommit verifies that ApplySplit returns an error
// containing "merge" when the commit has IsMerge=true, and that no ref moves.
func TestApplySplit_refusesMergeCommit(t *testing.T) {
	repo := buildRepo(t) // reuse the existing helper from manager_test.go
	mgr := NewManager(repo)
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}

	oldHead, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}

	mergeCommit := model.Commit{
		Hash:    oldHead,
		IsMerge: true,
	}

	// Use two dummy sub-commits so we don't trip the len(subs)<2 guard first.
	subs := []gitio.SubCommit{
		{Tree: "dummy1", Subject: "a"},
		{Tree: "dummy2", Subject: "b"},
	}

	_, err = mgr.ApplySplit(mergeCommit, "parent", subs, nil, state, ApplyOptions{UnixTimestamp: 1, ExpectedHead: oldHead})
	if err == nil {
		t.Fatal("expected an error for merge commit, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "merge") {
		t.Errorf("error should mention 'merge', got: %v", err)
	}

	// HEAD must not have moved.
	newHead, _ := repo.ResolveRef("HEAD")
	if newHead != oldHead {
		t.Errorf("HEAD moved when it should not have: %s -> %s", oldHead, newHead)
	}
}

// TestApplySplit_pushedTargetRequiresConfirmation verifies ApplySplit's own
// pushed guard (model.AnyPushed(target, tail...)): an unconfirmed apply whose
// target is marked pushed must be refused before any repo state changes, and
// the same call with Confirmed:true must proceed. This exercises the manager
// level directly, independent of the capability-layer gate that normally fires
// first in practice.
func TestApplySplit_pushedTargetRequiresConfirmation(t *testing.T) {
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

	childCommit.IsPushed = true // the split target is already on a remote

	if _, err := mgr.ApplySplit(childCommit, parentHash, subs, tail, state,
		ApplyOptions{UnixTimestamp: 1, ExpectedHead: oldHead}); err == nil {
		t.Fatal("an unconfirmed split of a pushed target must be refused")
	}
	newHead, _ := repo.ResolveRef("HEAD")
	if newHead != oldHead {
		t.Errorf("a refused split must not move HEAD: %s -> %s", oldHead, newHead)
	}

	if _, err := mgr.ApplySplit(childCommit, parentHash, subs, tail, state,
		ApplyOptions{UnixTimestamp: 2, Confirmed: true, ExpectedHead: oldHead}); err != nil {
		t.Errorf("a confirmed split of a pushed target should proceed: %v", err)
	}
}

// TestApplySplit_movesBranchAndIsUndoable exercises the happy path: build a
// real parent→child(≥2 hunks)→tail repo, construct two SubCommits whose last
// tree equals the child's tree, call Manager.ApplySplit, and verify:
//   - branch moved to the returned NewHead
//   - commit count increased by one (3 → 4)
//   - VerifyTreesUnchanged(OldHead, NewHead) passes (file content preserved)
//   - Manager.Undo() restores the original head
func TestApplySplit_movesBranchAndIsUndoable(t *testing.T) {
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

	// Build the parent tree hash (needed for Hunks): the parent commit's tree.
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

	// 1. The result must report the old and new head correctly.
	if res.OldHead != oldHead {
		t.Errorf("OldHead = %s, want %s", res.OldHead, oldHead)
	}
	if res.NewHead == oldHead {
		t.Error("NewHead should differ from OldHead after a split")
	}

	// 2. Branch must have moved to NewHead.
	currentHead, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if currentHead != res.NewHead {
		t.Errorf("HEAD = %s, want NewHead %s", currentHead, res.NewHead)
	}

	// 3. Commit count must have increased by one (3 → 4).
	cmd := exec.Command("git", "-C", repo.Root(), "rev-list", "--count", res.NewHead)
	rawCount, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-list --count: %v", err)
	}
	count := strings.TrimSpace(string(rawCount))
	if count != "4" {
		t.Errorf("commit count = %s, want 4 (was 3 + 1 extra sub-commit)", count)
	}

	// 4. File content must be unchanged end-to-end.
	if err := repo.VerifyTreesUnchanged(oldHead, res.NewHead); err != nil {
		t.Errorf("VerifyTreesUnchanged: %v", err)
	}

	// 5. Undo must restore the original head.
	if !mgr.CanUndo() {
		t.Fatal("CanUndo should be true after ApplySplit")
	}
	restored, _, err := mgr.Undo(false, 10000)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if restored != oldHead {
		t.Errorf("Undo restored %s, want original %s", restored, oldHead)
	}
	afterUndo, _ := repo.ResolveRef("HEAD")
	if afterUndo != oldHead {
		t.Errorf("HEAD after Undo = %s, want %s", afterUndo, oldHead)
	}
	if mgr.CanUndo() {
		t.Error("undo stack should be empty after single Undo")
	}
}

// TestApplySplit_refusesWrongLastTree verifies the local-invariant guard:
// when the last sub-commit's tree does not equal the original commit's tree,
// ApplySplit must return an error and must not move HEAD.
func TestApplySplit_refusesWrongLastTree(t *testing.T) {
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

	// Resolve the parent commit's tree so we can use it as the "wrong" tree.
	parentTreeHash, err := repo.ResolveRef(parentHash + "^{tree}")
	if err != nil {
		t.Fatal(err)
	}

	// Build valid sub-commits, then corrupt the last one by replacing its tree
	// with the parent's tree (which is NOT the original commit tree).
	subs := buildSubCommits(t, repo, parentTreeHash, childCommit)
	subs[len(subs)-1].Tree = parentTreeHash // deliberate corruption

	_, err = mgr.ApplySplit(childCommit, parentHash, subs, tail, state, ApplyOptions{UnixTimestamp: 2, ExpectedHead: oldHead})
	if err == nil {
		t.Fatal("expected error when last sub-commit tree != original commit tree")
	}

	// HEAD must not have moved.
	newHead, _ := repo.ResolveRef("HEAD")
	if newHead != oldHead {
		t.Errorf("HEAD moved when it should not have: %s -> %s", oldHead, newHead)
	}
}
