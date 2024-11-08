package gitio

import "testing"

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
