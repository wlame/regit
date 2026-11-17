package gitio

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// TestWorktreeStatus_separatesTrackedChangesFromUntrackedFiles pins the guard
// input every apply depends on. The two kinds are reported separately because
// they block different operations: a rewrite that keeps every file identical
// tolerates untracked files, while one that refreshes the checkout needs
// nothing on disk outside history (see model.WorktreeNeed).
func TestWorktreeStatus_separatesTrackedChangesFromUntrackedFiles(t *testing.T) {
	write := func(t *testing.T, dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		dirty func(t *testing.T, repo *Repo, dir string)
		want  model.WorktreeStatus
	}{
		{"clean", func(*testing.T, *Repo, string) {}, model.WorktreeStatus{}},
		{"untracked file", func(t *testing.T, _ *Repo, dir string) {
			write(t, dir, "stray.txt", "x\n")
		}, model.WorktreeStatus{HasUntrackedFiles: true}},
		{"modified tracked file", func(t *testing.T, _ *Repo, dir string) {
			write(t, dir, "alpha.txt", "modified\n")
		}, model.WorktreeStatus{HasTrackedChanges: true}},
		{"staged new file", func(t *testing.T, repo *Repo, dir string) {
			write(t, dir, "staged.txt", "x\n")
			if _, err := repo.git("add", "staged.txt"); err != nil {
				t.Fatal(err)
			}
		}, model.WorktreeStatus{HasTrackedChanges: true}},
		{"both", func(t *testing.T, _ *Repo, dir string) {
			write(t, dir, "alpha.txt", "modified\n")
			write(t, dir, "stray.txt", "x\n")
		}, model.WorktreeStatus{HasTrackedChanges: true, HasUntrackedFiles: true}},
		{"ignored file", func(t *testing.T, repo *Repo, dir string) {
			write(t, dir, ".gitignore", "*.log\n")
			if _, err := repo.git("add", ".gitignore"); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.git("-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "ignore logs"); err != nil {
				t.Fatal(err)
			}
			write(t, dir, "build.log", "x\n")
		}, model.WorktreeStatus{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, dir := testRepo(t)
			tc.dirty(t, repo, dir)

			got, err := repo.WorktreeStatus()

			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("WorktreeStatus = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestWorktreeStatus_errorsOnBrokenRepo pins the error surface: a repo whose
// `git status` cannot run at all (here, a HEAD ref that points at garbage
// instead of a hash) must propagate the failure rather than report a clean
// tree — the guard must fail closed.
func TestWorktreeStatus_errorsOnBrokenRepo(t *testing.T) {
	dir, repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.WorktreeStatus(); err == nil {
		t.Fatal("WorktreeStatus on a repo with a corrupt HEAD should fail, got nil error")
	}
}

// TestFirstParents_walksAndStopsAtTheRoot pins the structural verifier's input.
// A short chain returning fewer than n entries is how verifyChainStructure
// detects a rooted rewrite, so the truncation behavior is load-bearing.
func TestFirstParents_walksAndStopsAtTheRoot(t *testing.T) {
	repo := syncFixture(t)

	chain, err := repo.FirstParents("HEAD", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) == 0 {
		t.Fatal("FirstParents returned nothing")
	}
	if len(chain) >= 10 {
		t.Errorf("FirstParents returned %d entries for a short history, want truncation at the root", len(chain))
	}
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if chain[0] != head {
		t.Errorf("chain[0] = %s, want HEAD %s", chain[0], head)
	}
}

// TestFirstParents_zeroOrNegativeNReturnsNil pins the n<=0 short-circuit: a
// caller that computes a non-positive chain length (e.g. a boundary-only
// rewrite where oldLen is 0) gets an empty result without running git at all.
func TestFirstParents_zeroOrNegativeNReturnsNil(t *testing.T) {
	repo := syncFixture(t)
	for _, n := range []int{0, -1} {
		chain, err := repo.FirstParents("HEAD", n)
		if err != nil {
			t.Errorf("FirstParents(HEAD, %d): unexpected error %v", n, err)
		}
		if chain != nil {
			t.Errorf("FirstParents(HEAD, %d) = %v, want nil", n, chain)
		}
	}
}

// TestFirstParents_errorsOnUnresolvableRev pins the error path: a rev that
// names no commit must surface rev-list's failure rather than an empty chain,
// since verifyChainStructure treats an empty chain as a legitimate root case.
func TestFirstParents_errorsOnUnresolvableRev(t *testing.T) {
	repo := syncFixture(t)
	if _, err := repo.FirstParents("0000000000000000000000000000000000dead", 5); err == nil {
		t.Fatal("FirstParents on an unresolvable rev should fail, got nil error")
	}
}
