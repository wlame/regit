package gitio

import (
	"strings"
	"testing"
)

// TestBuildTreeFromHunks_survivesHostileDiffConfig proves the split hunk pipeline
// is immune to a user's diff.noprefix / diff.renames config. Without the pinned
// a/ b/ prefixes, `git apply --index` rejects the prefix-less patch with "header
// lacks filename information"; without --no-renames a rename would collapse into an
// R record the hunk parser cannot reproduce.
func TestBuildTreeFromHunks_survivesHostileDiffConfig(t *testing.T) {
	repo, parent, child := twoCommitRepo(t)
	gitT(t, repo.Root(), "config", "diff.noprefix", "true")
	gitT(t, repo.Root(), "config", "diff.renames", "copies")

	all, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.NewWorktree(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()
	full, err := repo.BuildTreeFromHunks(parent, all, wt)
	if err != nil {
		t.Fatalf("build with hostile diff config: %v", err)
	}
	if full != child {
		t.Fatalf("rebuild tree = %s, want child tree %s", full, child)
	}
}

func TestBuildTreeFromHunks_subsetThenFull(t *testing.T) {
	repo, parent, child := twoCommitRepo(t)
	all, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.NewWorktree(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()

	// Applying ALL hunks must reproduce the child tree exactly.
	full, err := repo.BuildTreeFromHunks(parent, all, wt)
	if err != nil {
		t.Fatalf("build full: %v", err)
	}
	if full != child {
		t.Fatalf("full rebuild tree = %s, want child tree %s", full, child)
	}

	// Applying the empty subset must reproduce the parent tree.
	none, err := repo.BuildTreeFromHunks(parent, nil, wt)
	if err != nil {
		t.Fatalf("build none: %v", err)
	}
	if none != parent {
		t.Fatalf("empty subset tree = %s, want parent tree %s", none, parent)
	}
}

// TestBuildTreeFromHunks_repeatedCallsWithNewFile exercises the repeated-call
// path where a hunk ADDS a new file (not just modifies an existing one). The
// split engine calls BuildTreeFromHunks on the SAME worktree multiple times
// with growing cumulative subsets; without the git-clean fix, the new file
// introduced by an earlier pass stays untracked and causes the next pass to
// fail with "already exists in working directory".
//
// twoCommitRepo creates a child that both modifies a.txt AND adds c.txt, so
// there are at least two hunks; this test verifies that each cumulative prefix
// succeeds and that the final full subset reproduces the child tree exactly.
func TestBuildTreeFromHunks_repeatedCallsWithNewFile(t *testing.T) {
	repo, parent, child := twoCommitRepo(t)
	all, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 2 {
		t.Skipf("need at least 2 hunks for this test, got %d", len(all))
	}

	wt, err := repo.NewWorktree(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()

	// Apply growing cumulative subsets on the SAME worktree. Each call must
	// succeed even if a prior call introduced an untracked file (c.txt).
	var prevTree string
	for i := 1; i <= len(all); i++ {
		subset := all[:i]
		tree, err := repo.BuildTreeFromHunks(parent, subset, wt)
		if err != nil {
			t.Fatalf("BuildTreeFromHunks subset[:d%d]: %v", i, err)
		}

		if i < len(all) {
			// A strict subset must not equal either the parent or the child tree.
			if tree == parent {
				t.Errorf("subset[:d%d] tree == parent tree; expected an intermediate tree", i)
			}
			if tree == child {
				t.Errorf("subset[:d%d] tree == child tree; expected an intermediate tree", i)
			}
			// Each larger subset must produce a different tree than the previous one.
			if prevTree != "" && tree == prevTree {
				t.Errorf("subset[:d%d] tree == subset[:d%d] tree; expected a new tree", i, i-1)
			}
		} else {
			// The full subset must reproduce the child tree exactly.
			if tree != child {
				t.Errorf("full subset tree = %s, want child tree %s", tree, child)
			}
		}
		prevTree = tree
	}
}

// TestBuildTreeFromHunks_errorsOnUnresolvableParentTree pins the reset-failure
// path: a parentTree that doesn't name a real object (e.g. a stale hash from a
// rewrite that has since moved on) must surface as an error, not silently
// build a tree from whatever the worktree happened to contain.
func TestBuildTreeFromHunks_errorsOnUnresolvableParentTree(t *testing.T) {
	repo, parent, _ := twoCommitRepo(t)
	wt, err := repo.NewWorktree(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()

	_, err = repo.BuildTreeFromHunks("0000000000000000000000000000000000dead", nil, wt)
	if err == nil {
		t.Fatal("BuildTreeFromHunks with an unresolvable parent tree should fail, got nil error")
	}
}

// TestBuildTreeFromHunks_errorsOnMismatchedContext pins the apply-failure path:
// hunks whose context lines don't match the worktree's current content (here,
// applied against the CHILD tree instead of the parent they were diffed from)
// must fail through `git apply` rather than silently corrupt the tree.
func TestBuildTreeFromHunks_errorsOnMismatchedContext(t *testing.T) {
	repo, parent, child := twoCommitRepo(t)
	all, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.NewWorktree(child) // wrong baseline on purpose
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()

	_, err = repo.BuildTreeFromHunks(child, all, wt)
	if err == nil {
		t.Fatal("applying hunks whose context no longer matches should fail, got nil error")
	}
	if !strings.Contains(err.Error(), "apply") {
		t.Errorf("error = %q, want it to name the apply step", err)
	}
}
