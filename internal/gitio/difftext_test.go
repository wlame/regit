package gitio

import (
	"strings"
	"testing"
)

func TestDiffText_includesContextAndPaths(t *testing.T) {
	repo, parent, child := twoCommitRepo(t) // existing helper (parent + child)
	out, err := repo.DiffText(parent, child, 30)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "diff --git") || !strings.Contains(out, "@@") {
		t.Fatalf("diff text missing markers:\n%s", out)
	}
}
