package gitio

import (
	"fmt"
	"os"
	"path/filepath"
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

// bigChangeRepo builds a repo whose committed file has 60 lines, then changes a
// single line in the MIDDLE. It returns the repo plus the parent and child tree
// hashes. A wide -U<n> context window must surface more surrounding lines than a
// narrow one, so this gives DiffText enough room to prove the flag is honored.
func bigChangeRepo(t *testing.T) (*Repo, string, string) {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")

	// 60 distinct lines so context expansion is observable around the change.
	const lineCount = 60
	var parentLines strings.Builder
	for i := 1; i <= lineCount; i++ {
		fmt.Fprintf(&parentLines, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(parentLines.String()), 0o644); err != nil {
		t.Fatalf("write big.txt: %v", err)
	}
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "parent")
	parent := gitT(t, dir, "rev-parse", "HEAD^{tree}")

	// Change exactly one line in the middle (line 30) so context can extend both
	// up and down without hitting the start or end of the file.
	const changedLine = lineCount / 2
	var childLines strings.Builder
	for i := 1; i <= lineCount; i++ {
		if i == changedLine {
			fmt.Fprintf(&childLines, "CHANGED %d\n", i)
		} else {
			fmt.Fprintf(&childLines, "line %d\n", i)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(childLines.String()), 0o644); err != nil {
		t.Fatalf("write big.txt: %v", err)
	}
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "child")
	child := gitT(t, dir, "rev-parse", "HEAD^{tree}")

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo, parent, child
}

// TestDiffText_widerContextYieldsMoreLines proves the -U<n> context width is
// honored: a single change in the middle of a 60-line file must produce a longer
// diff with -U30 than with -U1, because the wider window pulls in more
// surrounding (unchanged) lines. A regression that dropped the -U flag would
// collapse both diffs to the same width and fail this assertion.
func TestDiffText_widerContextYieldsMoreLines(t *testing.T) {
	repo, parent, child := bigChangeRepo(t)

	narrow, err := repo.DiffText(parent, child, 1)
	if err != nil {
		t.Fatalf("narrow DiffText: %v", err)
	}
	wide, err := repo.DiffText(parent, child, 30)
	if err != nil {
		t.Fatalf("wide DiffText: %v", err)
	}

	narrowLines := strings.Count(narrow, "\n")
	wideLines := strings.Count(wide, "\n")
	if wideLines <= narrowLines {
		t.Fatalf("wide context (-U30) should yield more lines than narrow (-U1): "+
			"wide=%d narrow=%d\nwide diff:\n%s", wideLines, narrowLines, wide)
	}
}

// TestDiffText_nonPositiveContextFallsBack covers the contextLines <= 0 branch:
// DiffText must fall back to a default width and still return a valid diff
// (containing an @@ hunk header) without error.
func TestDiffText_nonPositiveContextFallsBack(t *testing.T) {
	repo, parent, child := twoCommitRepo(t)
	out, err := repo.DiffText(parent, child, 0)
	if err != nil {
		t.Fatalf("DiffText with contextLines=0: %v", err)
	}
	if !strings.Contains(out, "@@") {
		t.Fatalf("fallback diff missing @@ hunk header:\n%s", out)
	}
}
