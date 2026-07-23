package gitio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// syncFixture builds a repo with two tracked files and returns it. It reuses
// purgeTestRepo/writeAndCommit from purgetree_test.go rather than adding a
// third fixture builder to the package.
func syncFixture(t *testing.T) *Repo {
	t.Helper()
	repo, dir := purgeTestRepo(t)
	writeAndCommit(t, repo, dir, "keep.txt", "keep\n")
	// Two lines so the redact test can remove one and still assert on content,
	// rather than asserting a file went empty.
	writeAndCommit(t, repo, dir, "gone.txt", "keep this\nsecret=abc\n")
	return repo
}

// commitTreeOnHead writes tree as a new commit on top of HEAD and returns its
// hash, without moving any ref. The test is in-package, so it can use the same
// plumbing the rewrite engine does.
func commitTreeOnHead(t *testing.T, r *Repo, tree string) string {
	t.Helper()
	head, err := r.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := r.gitWithEnvStdin(nil, "Synthetic.", "commit-tree", tree, "-p", head)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

// stagedPaths returns the paths currently in the index.
func stagedPaths(t *testing.T, r *Repo) []string {
	t.Helper()
	out, err := r.git("ls-files")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	return strings.Split(strings.TrimSpace(out), "\n")
}

// TestSyncIndexToTree_refreshesIndexWithoutTouchingDiskOrRefs pins purge's
// policy: staging is cleared, but the file the user still wants stays on disk.
func TestSyncIndexToTree_refreshesIndexWithoutTouchingDiskOrRefs(t *testing.T) {
	repo := syncFixture(t)
	dir := repo.Root()

	// Build a commit whose tree LACKS gone.txt, but DO NOT move the branch to
	// it. The branch stays at its original HEAD. This sets up the condition
	// where SyncIndexToTree must sync the index to a DIFFERENT tree than where
	// the branch points: if the implementation incorrectly used `git reset` (which
	// moves the ref), the branch would move to newHead and the assertion below
	// would fail, proving we use read-tree (which touches only the index).
	headTree, err := repo.git("rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	purged, err := repo.RemovePathsFromTree(headTree, []string{"gone.txt"})
	if err != nil {
		t.Fatal(err)
	}
	newHead := commitTreeOnHead(t, repo, purged)
	oldHead, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.SyncIndexToTree(newHead); err != nil {
		t.Fatalf("SyncIndexToTree: %v", err)
	}

	// The index no longer stages gone.txt...
	for _, p := range stagedPaths(t, repo) {
		if p == "gone.txt" {
			t.Error("gone.txt is still staged after SyncIndexToTree")
		}
	}
	// ...but the file is still on disk, now untracked.
	if _, err := os.Stat(filepath.Join(dir, "gone.txt")); err != nil {
		t.Errorf("SyncIndexToTree deleted gone.txt from disk: %v", err)
	}
	// The branch is deliberately left at oldHead, not newHead. If the sync used
	// `git reset --mixed newHead` (which moves refs), the branch would have moved
	// to newHead. Since it is still at oldHead, we know the sync touches only the
	// index via read-tree, not the branch.
	if got, err := repo.ResolveRef("refs/heads/main"); err != nil {
		t.Fatal(err)
	} else if got != oldHead {
		t.Errorf("branch = %s, want %s (should not move)", short(got),
			short(oldHead))
	}
}

// TestSyncIndexAndWorktreeToTree_rewritesDiskContent pins redact's policy: the
// secret must leave the working tree, not merely history.
func TestSyncIndexAndWorktreeToTree_rewritesDiskContent(t *testing.T) {
	repo := syncFixture(t)
	dir := repo.Root()

	// Build the redacted tree with the production transform itself, so the
	// fixture is a real redact result rather than a hand-rolled approximation
	// that might drop unrelated entries (keep.txt must survive).
	headTree, err := repo.git("rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	newTree, err := repo.RedactBlobsInTree(headTree, model.RedactSpec{
		Mode:    model.RedactRemove,
		Targets: map[string][]string{"gone.txt": {"secret=abc"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if newTree == headTree {
		t.Fatal("RedactBlobsInTree made no change; the fixture is wrong")
	}
	newHead := commitTreeOnHead(t, repo, newTree)
	oldHead, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef("refs/heads/main", newHead, oldHead); err != nil {
		t.Fatal(err)
	}

	if err := repo.SyncIndexAndWorktreeToTree(newHead); err != nil {
		t.Fatalf("SyncIndexAndWorktreeToTree: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "gone.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "keep this\n"; got != want {
		t.Errorf("gone.txt on disk = %q, want %q — the secret must leave the working tree", got, want)
	}
	// The unrelated file must be untouched: -u rewrites only what the tree changed.
	if body, err := os.ReadFile(filepath.Join(dir, "keep.txt")); err != nil {
		t.Errorf("keep.txt was removed by the sync: %v", err)
	} else if string(body) != "keep\n" {
		t.Errorf("keep.txt = %q, want %q", body, "keep\n")
	}

	// Verify the index was also updated, not just the working tree. The method's
	// doc claims it refreshes BOTH. Check that git diff --cached (index vs HEAD)
	// reports no differences, proving the index is in sync with newHead's tree.
	diff, err := repo.git("diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(diff) != "" {
		t.Errorf("index is out of sync with %s; "+
			"git diff --cached reports unstaged paths: %s",
			short(newHead), strings.TrimSpace(diff))
	}
}
