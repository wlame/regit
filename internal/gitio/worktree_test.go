// internal/gitio/worktree_test.go
package gitio

import (
	"errors"
	"io/fs"
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
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("worktree dir should be removed, stat err = %v", err)
	}
}

// TestNewWorktree_errorsOnUnresolvableObject pins the cat-file guard: an `at`
// that names no object at all (as opposed to the wrong TYPE of object) must
// fail before any worktree is created, not leave a half-built temp dir behind.
func TestNewWorktree_errorsOnUnresolvableObject(t *testing.T) {
	repo, _, _ := twoCommitRepo(t)
	_, err := repo.NewWorktree("0000000000000000000000000000000000dead")
	if err == nil {
		t.Fatal("NewWorktree on an unresolvable object should fail, got nil error")
	}
}

// TestNewWorktree_errorsOnBlobObject pins the commit-ish branch's own failure:
// a blob is a real, resolvable object (so it passes the cat-file check) but is
// not something `git worktree add` can check out, so the worktree-add call
// itself must fail rather than leave a bogus worktree registered.
func TestNewWorktree_errorsOnBlobObject(t *testing.T) {
	repo, _, _ := twoCommitRepo(t)
	blob, err := repo.gitWithEnvStdin(nil, "blob content\n", "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatal(err)
	}
	if blob == "" {
		t.Fatal("hash-object produced an empty hash")
	}
	if objType := mustGit(t, repo, "cat-file", "-t", blob); objType != "blob" {
		t.Fatalf("fixture object type = %q, want blob", objType)
	}
	_, err = repo.NewWorktree(blob)
	if err == nil {
		t.Fatal("NewWorktree on a blob object should fail, got nil error")
	}
}

// TestNewWorktree_errorsWhenHeadIsUnborn pins the tree-hash branch's own
// worktree-add call, which checks out a fresh worktree at HEAD before
// populating it from the requested tree. On a brand new repository with no
// commits yet, HEAD does not resolve, so that step must fail — the empty tree
// object itself always exists (mktree accepts empty stdin) even with no
// commits, so this is a real, reachable state, not a contrived one.
func TestNewWorktree_errorsWhenHeadIsUnborn(t *testing.T) {
	dir, repo := initRepo(t)
	gitT(t, dir, "config", "user.name", "t")
	gitT(t, dir, "config", "user.email", "t@e")
	empty, err := repo.EmptyTree()
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.NewWorktree(empty)
	if err == nil {
		t.Fatal("NewWorktree(emptyTree) on an unborn branch should fail, got nil error")
	}
}

// TestWorktreeClose_errorsWhenAlreadyRemoved pins Close's own error surface:
// if the worktree was already torn down out from under it (e.g. a second
// cleanup path racing the first), `git worktree remove` fails and Close must
// report that rather than silently succeed.
func TestWorktreeClose_errorsWhenAlreadyRemoved(t *testing.T) {
	repo, parent, _ := twoCommitRepo(t)
	wt, err := repo.NewWorktree(parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.git("worktree", "remove", "--force", wt.Dir); err != nil {
		t.Fatalf("pre-removing the worktree: %v", err)
	}
	if err := wt.Close(); err == nil {
		t.Fatal("Close on an already-removed worktree should fail, got nil error")
	}
}

// TestWorktreeClose_isANoOpAfterTheFirstCall pins the documented contract:
// "safe to call once; subsequent calls are no-ops" — a second Close (as a
// deferred cleanup would do after an earlier explicit Close, or a zero-value
// Worktree that was never opened) must return nil rather than try to remove
// an already-gone worktree.
func TestWorktreeClose_isANoOpAfterTheFirstCall(t *testing.T) {
	repo, parent, _ := twoCommitRepo(t)
	wt, err := repo.NewWorktree(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := wt.Close(); err != nil {
		t.Errorf("second Close should be a no-op, got: %v", err)
	}
}

func TestEmptyTree_isWellKnownHash(t *testing.T) {
	repo, _, _ := twoCommitRepo(t)
	et, err := repo.EmptyTree()
	if err != nil {
		t.Fatal(err)
	}
	const wantEmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	if et != wantEmptyTree {
		t.Fatalf("EmptyTree = %q, want %q", et, wantEmptyTree)
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
