package gitio

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// branchTestRepo builds a repo whose `main` has two commits and a `feature`
// branch that forked at the first commit, plus a `zzz` branch at the tip (for
// the sort assertion).
func branchTestRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	commit := func(name string) {
		if err := os.WriteFile(dir+"/"+name+".txt", []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-q", "-m", "commit "+name)
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "user.name", "wlame")
	run("config", "user.email", "vvlame@gmail.com")
	run("config", "commit.gpgsign", "false")
	commit("c0")
	run("branch", "feature") // feature points at c0
	commit("c1")             // main advances to c1; feature stays at c0
	run("branch", "zzz")     // zzz at c1

	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo
}

func TestBranches_listsLocalBranchesSorted(t *testing.T) {
	repo := branchTestRepo(t)
	got, err := repo.Branches()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"feature", "main", "zzz"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Branches() = %v, want %v", got, want)
	}
}

func TestMergeBase_findsForkPoint(t *testing.T) {
	repo := branchTestRepo(t)
	got, err := repo.MergeBase("main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	// feature still points at the fork commit c0, so it IS the merge-base.
	want, err := repo.ResolveRef("feature")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("MergeBase(main, feature) = %s, want %s (c0)", got, want)
	}
}

func TestSwitchBranch_movesHead(t *testing.T) {
	repo := branchTestRepo(t)
	if err := repo.SwitchBranch("feature"); err != nil {
		t.Fatalf("SwitchBranch: %v", err)
	}
	cur, err := repo.CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}
	if cur != "feature" {
		t.Errorf("after switch HEAD is on %q, want feature", cur)
	}
}
