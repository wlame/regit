package gitio

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/gittest"
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
		cmd.Env = gittest.Env(dir,
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

// TestCurrentBranch_errorsWhenDetached pins the guard input every apply
// depends on: a detached HEAD has no branch for a rewrite to move, and that
// must surface as an error rather than an empty (and easily-ignored) name.
func TestCurrentBranch_errorsWhenDetached(t *testing.T) {
	repo := branchTestRepo(t)
	if _, err := repo.git("checkout", "-q", "--detach", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CurrentBranch(); err == nil {
		t.Fatal("CurrentBranch on a detached HEAD should fail, got nil error")
	}
}

// TestSwitchBranch_errorsOnUnknownBranch pins the failure path: a name that
// names no local branch must surface `git switch`'s refusal, not silently
// leave HEAD wherever it already was.
func TestSwitchBranch_errorsOnUnknownBranch(t *testing.T) {
	repo := branchTestRepo(t)
	if err := repo.SwitchBranch("does-not-exist"); err == nil {
		t.Fatal("SwitchBranch to an unknown branch should fail, got nil error")
	}
}

// TestMergeBase_errorsOnUnrelatedHistories pins the documented failure mode:
// two branches with no common ancestor at all (built from separate `init`
// calls, not a fork of one repo) must error rather than return a bogus hash.
func TestMergeBase_errorsOnUnrelatedHistories(t *testing.T) {
	repo := branchTestRepo(t)
	// Build a second, wholly disconnected root commit and graft it in as a
	// branch via update-ref, so it shares no history with main at all.
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir,
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "user.name", "wlame")
	run("config", "user.email", "vvlame@gmail.com")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(dir+"/unrelated.txt", []byte("unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "unrelated root")
	other, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	unrelatedHead, err := other.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Import the unrelated commit's object into repo's own object store, then
	// point a branch at it — merge-base has no common ancestor to find.
	if _, err := repo.git("fetch", dir, "HEAD:refs/heads/unrelated"); err != nil {
		t.Fatalf("importing the unrelated history: %v", err)
	}
	if _, err := repo.MergeBase("main", "unrelated"); err == nil {
		t.Errorf("MergeBase across unrelated histories should fail, got a result (unrelated head %s)",
			unrelatedHead)
	}
}
