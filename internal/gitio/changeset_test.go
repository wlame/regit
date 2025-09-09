package gitio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// changeRepo builds a repo whose second commit modifies a.txt, adds c.txt, and
// deletes b.txt — exercising all three default statuses (M/A/D). It returns the
// repo and the two commit hashes (old, new).
func changeRepo(t *testing.T) (*Repo, string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "user.name", "wlame")
	run("config", "user.email", "vvlame@gmail.com")
	run("config", "commit.gpgsign", "false")

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("keep\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "first")
	old := run("rev-parse", "HEAD")

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("ONE\n"), 0o644) // modify
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("new\n"), 0o644) // add
	os.Remove(filepath.Join(dir, "b.txt"))                            // delete
	run("add", "-A")
	run("commit", "-q", "-m", "second")
	newC := run("rev-parse", "HEAD")

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo, old, newC
}

func TestDiffChangeSet_reportsAddModifyDelete(t *testing.T) {
	repo, old, newC := changeRepo(t)
	cs, err := repo.DiffChangeSet(old, newC)
	if err != nil {
		t.Fatalf("DiffChangeSet: %v", err)
	}
	want := model.NewChangeSet([]model.PathChange{
		{Path: "a.txt", Status: model.Modified},
		{Path: "b.txt", Status: model.Deleted},
		{Path: "c.txt", Status: model.Added},
	})
	if !want.Equal(cs) {
		t.Errorf("change-set mismatch: %v", want.Mismatch(cs))
	}
}

func TestDiffChangeSet_identicalCommitsIsEmpty(t *testing.T) {
	repo, _, newC := changeRepo(t)
	cs, err := repo.DiffChangeSet(newC, newC)
	if err != nil {
		t.Fatalf("DiffChangeSet: %v", err)
	}
	if !cs.IsEmpty() {
		t.Errorf("a commit compared with itself should yield no changes, got %v", cs.Changes)
	}
}

func TestCommitTrees_differentCommitsHaveDifferentTrees(t *testing.T) {
	repo, old, newC := changeRepo(t)
	trees, err := repo.CommitTrees([]string{old, newC})
	if err != nil {
		t.Fatalf("CommitTrees: %v", err)
	}
	if trees[old] == "" || trees[newC] == "" {
		t.Fatalf("missing tree hashes: %v", trees)
	}
	if trees[old] == trees[newC] {
		t.Error("commits with different content must have different tree hashes")
	}
}
