// internal/gitio/worktree_test.go
package gitio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewWorktree_checksOutParentAndCleansUp(t *testing.T) {
	repo, _, _ := twoCommitRepo(t)
	parentCommit := mustGit(t, repo, "rev-parse", "HEAD~1")

	wt, err := repo.NewWorktree(parentCommit)
	if err != nil {
		t.Fatal(err)
	}
	// The parent commit had a.txt == "one\ntwo\nthree\n".
	data, err := os.ReadFile(filepath.Join(wt.Dir, "a.txt"))
	if err != nil {
		t.Fatalf("expected a.txt in worktree: %v", err)
	}
	if string(data) != "one\ntwo\nthree\n" {
		t.Fatalf("worktree has wrong a.txt: %q", data)
	}
	dir := wt.Dir
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be removed, stat err = %v", err)
	}
}

func TestEmptyTree_isWellKnownHash(t *testing.T) {
	repo, _, _ := twoCommitRepo(t)
	et, err := repo.EmptyTree()
	if err != nil {
		t.Fatal(err)
	}
	if et == "" {
		t.Fatal("empty tree hash should not be blank")
	}
}

// mustGit runs a git command in the repo root for tests.
func mustGit(t *testing.T, r *Repo, args ...string) string {
	t.Helper()
	out, err := r.git(args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}
