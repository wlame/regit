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

// TestCommitTrees_emptyInputReturnsEmptyMapWithoutRunningGit pins the
// zero-commits short-circuit: verify's own call site builds this list from a
// (possibly empty) expectations slice, and an empty batch must resolve to an
// empty result directly rather than invoke `git rev-parse` with no revisions
// at all (which would itself be a usage error).
func TestCommitTrees_emptyInputReturnsEmptyMapWithoutRunningGit(t *testing.T) {
	repo, _, _ := changeRepo(t)
	trees, err := repo.CommitTrees(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 0 {
		t.Errorf("CommitTrees(nil) = %v, want an empty map", trees)
	}
}

// TestCommitTrees_errorsOnUnresolvableCommit pins the failure path verify's
// content check depends on: a hash naming no commit must propagate as an
// error rather than a map missing that entry.
func TestCommitTrees_errorsOnUnresolvableCommit(t *testing.T) {
	repo, _, _ := changeRepo(t)
	if _, err := repo.CommitTrees([]string{"0000000000000000000000000000000000dead"}); err == nil {
		t.Fatal("CommitTrees on an unresolvable commit should fail, got nil error")
	}
}

// TestDiffChangeSet_errorsOnUnresolvableRev pins the outer diff-failure path.
func TestDiffChangeSet_errorsOnUnresolvableRev(t *testing.T) {
	repo, _, newC := changeRepo(t)
	if _, err := repo.DiffChangeSet(newC, "0000000000000000000000000000000000dead"); err == nil {
		t.Fatal("DiffChangeSet against an unresolvable rev should fail, got nil error")
	}
}

// TestStatusFromLetter pins the mapping this codebase's diff parsing depends
// on directly: A/M/D map to their model.ChangeStatus, an empty status string
// is rejected (a malformed --raw record), and any other letter — which can
// only appear if rename/copy detection were ever accidentally turned back on
// — is rejected rather than silently misreported as one of the three known
// statuses.
func TestStatusFromLetter(t *testing.T) {
	cases := []struct {
		in      string
		want    model.ChangeStatus
		wantErr bool
	}{
		{"A", model.Added, false},
		{"M", model.Modified, false},
		{"D", model.Deleted, false},
		{"", 0, true},
		{"R100", 0, true}, // rename: only possible if --no-renames were dropped
	}
	for _, tc := range cases {
		got, err := statusFromLetter(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("statusFromLetter(%q) = %v, nil, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("statusFromLetter(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("statusFromLetter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
