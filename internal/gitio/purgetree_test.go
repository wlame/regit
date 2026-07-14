package gitio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/gittest"
)

// purgeTestRepo builds a repo with a known layout in one commit:
//
//	keep.txt, junk.log, dir/a.txt, dir/sub/b.txt
func purgeTestRepo(t *testing.T) (*Repo, string) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-b", "main")
	write("keep.txt", "keep")
	write("junk.log", "junk")
	write("dir/a.txt", "a")
	write("dir/sub/b.txt", "b")
	git("add", ".")
	git("commit", "-m", "seed")
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
