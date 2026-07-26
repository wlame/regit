package gitio

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsDirty_countsUntrackedFiles pins the guard input every apply depends on.
// IsDirty runs a bare `git status --porcelain`, which reports untracked files as
// well as modifications — that breadth is what makes the post-apply working-tree
// sync safe, since a clean gate proves nothing on disk is missing from history.
func TestIsDirty_countsUntrackedFiles(t *testing.T) {
	repo := syncFixture(t) // from sync_test.go (Task 5), same package
	dir := repo.Root()

	dirty, err := repo.IsDirty()
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("fixture started dirty")
	}

	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = repo.IsDirty()
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Error("IsDirty = false with an untracked file present, want true")
	}
}

// TestIsDirty_errorsOnBrokenRepo pins IsDirty's own error surface: a repo whose
// `git status` cannot run at all (here, a HEAD ref that points at garbage
// instead of a hash) must propagate the failure rather than report false —
// the guard must fail closed, not silently read as "clean".
func TestIsDirty_errorsOnBrokenRepo(t *testing.T) {
	dir, repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.IsDirty(); err == nil {
		t.Fatal("IsDirty on a repo with a corrupt HEAD should fail, got nil error")
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
