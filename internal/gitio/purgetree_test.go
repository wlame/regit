package gitio

import (
	"strings"
	"testing"
)

// purgeTestRepo builds a repo with a known layout in one commit:
//
//	keep.txt, junk.log, dir/a.txt, dir/sub/b.txt
//
// Built from the package's shared fixture helpers (gitT in diff_test.go,
// mustWriteFile in purge_test.go) instead of a private exec closure.
func purgeTestRepo(t *testing.T) (*Repo, string) {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-b", "main")
	mustWriteFile(t, dir, "keep.txt", "keep")
	mustWriteFile(t, dir, "junk.log", "junk")
	mustWriteFile(t, dir, "dir/a.txt", "a")
	mustWriteFile(t, dir, "dir/sub/b.txt", "b")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "seed")
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r, dir
}

// treePaths lists every blob path in a tree, for assertions.
func treePaths(t *testing.T, r *Repo, tree string) []string {
	t.Helper()
	out, err := r.git("ls-tree", "-r", "--name-only", tree)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func headTree(t *testing.T, r *Repo) string {
	t.Helper()
	tree, err := r.git("rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestRemovePathsFromTree_removesFile(t *testing.T) {
	r, _ := purgeTestRepo(t)
	newTree, err := r.RemovePathsFromTree(headTree(t, r), []string{"junk.log"})
	if err != nil {
		t.Fatal(err)
	}
	got := treePaths(t, r, newTree)
	want := []string{"dir/a.txt", "dir/sub/b.txt", "keep.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tree after removal = %v, want %v", got, want)
	}
}

func TestRemovePathsFromTree_removesDirectoryRecursively(t *testing.T) {
	r, _ := purgeTestRepo(t)
	newTree, err := r.RemovePathsFromTree(headTree(t, r), []string{"dir"})
	if err != nil {
		t.Fatal(err)
	}
	got := treePaths(t, r, newTree)
	want := []string{"junk.log", "keep.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tree after dir removal = %v, want %v", got, want)
	}
}

func TestRemovePathsFromTree_missingPathIsNoop(t *testing.T) {
	r, _ := purgeTestRepo(t)
	orig := headTree(t, r)
	newTree, err := r.RemovePathsFromTree(orig, []string{"nope.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if newTree != orig {
		t.Errorf("removing a missing path must return the identical tree (%s != %s)", newTree, orig)
	}
}

func TestRemovePathsFromTree_everythingYieldsEmptyTree(t *testing.T) {
	r, _ := purgeTestRepo(t)
	newTree, err := r.RemovePathsFromTree(headTree(t, r), []string{"keep.txt", "junk.log", "dir"})
	if err != nil {
		t.Fatal(err)
	}
	if newTree != emptyTreeHash {
		t.Errorf("all-paths removal = %s, want the empty tree %s", newTree, emptyTreeHash)
	}
}

// TestRemovePathsFromTree_noPathsIsNoop asserts the guard added per Task 3's
// review: a bare `git rm` with no pathspec hard-fails, so an empty paths slice
// must short-circuit and return the input tree unchanged rather than reach git.
func TestRemovePathsFromTree_noPathsIsNoop(t *testing.T) {
	r, _ := purgeTestRepo(t)
	orig := headTree(t, r)
	newTree, err := r.RemovePathsFromTree(orig, nil)
	if err != nil {
		t.Fatal(err)
	}
	if newTree != orig {
		t.Errorf("RemovePathsFromTree with no paths = %s, want unchanged %s", newTree, orig)
	}
}

func TestRemovePathsFromTree_leavesUserIndexAlone(t *testing.T) {
	r, dir := purgeTestRepo(t)
	before, err := r.git("status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RemovePathsFromTree(headTree(t, r), []string{"dir"}); err != nil {
		t.Fatal(err)
	}
	after, err := r.git("status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("user index/worktree changed: %q -> %q (dir %s)", before, after, dir)
	}
}
