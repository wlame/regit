package split

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
)

// fixedBackend implements llm.Backend, always returning the same proposal.
// The proposal's Hunks field is overwritten with the request's hunks so that
// coverage validation (every hunk assigned exactly once) can pass.
type fixedBackend struct{ p model.SplitProposal }

func (f fixedBackend) Name() string { return "fixed" }
func (f fixedBackend) Propose(_ context.Context, req llm.SplitRequest, _ []byte) (model.SplitProposal, error) {
	// Attach the request's hunk list so the caller sees the right IDs.
	f.p.Hunks = req.Hunks
	return f.p, nil
}

// backendReturning wraps a fixed proposal in a llm.Backend.
func backendReturning(p model.SplitProposal) llm.Backend { return fixedBackend{p: p} }

// idsFrom extracts hunk IDs from a slice of Hunks.
func idsFrom(hs []model.Hunk) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.ID
	}
	return out
}

// gitE runs a git command in dir and fails the test on error, returning trimmed stdout.
func gitE(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-01-01T00:00:00+00:00",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newSplittableRepo creates a throwaway git repo with a parent commit and a
// child commit whose diff spans two separate files (producing ≥ 2 hunks).
// It returns the repo, the parent tree hash, and the child tree hash.
func newSplittableRepo(t *testing.T) (*gitio.Repo, string, string) {
	t.Helper()
	dir := t.TempDir()

	gitE(t, dir, "init", "-q")
	gitE(t, dir, "config", "commit.gpgsign", "false")
	gitE(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")

	// Parent commit: two files.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("foo\nbar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitE(t, dir, "add", "-A")
	gitE(t, dir, "commit", "-q", "-m", "parent")
	parent := gitE(t, dir, "rev-parse", "HEAD^{tree}")

	// Child commit: modify both files in distinct ways → at least 2 hunks.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("HELLO\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("foo\nBAR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitE(t, dir, "add", "-A")
	gitE(t, dir, "commit", "-q", "-m", "child: update a and b")
	child := gitE(t, dir, "rev-parse", "HEAD^{tree}")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("gitio.Open: %v", err)
	}
	return repo, parent, child
}

func TestEngine_proposeBuildsTreesAndVerifiesFinal(t *testing.T) {
	repo, parent, child := newSplittableRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	// ReadCommits returns root-first; the child is index 1.
	childCommit := commits[1]

	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) < 2 {
		t.Fatalf("need ≥ 2 hunks for a meaningful split test, got %d", len(hs))
	}

	// Build a proposal: first hunk in group 1, the rest in group 2.
	prop := model.SplitProposal{
		ShouldSplit: true,
		Hunks:       hs,
		Groups: []model.HunkGroup{
			{Order: 1, HunkIDs: []string{hs[0].ID}, Subject: "first"},
			{Order: 2, HunkIDs: idsFrom(hs[1:]), Subject: "second"},
		},
	}

	eng := NewEngine(repo, backendReturning(prop), NewCache(t.TempDir()))
	opts := Options{
		MaxCommits:     4,
		LinesPerCommit: 1,
		MinSplitLines:  1,
		RepairAttempts: 2,
		WIPPrefix:      "WIP: ",
		Now:            time.Unix(0, 0),
	}

	got, trees, err := eng.Propose(context.Background(), childCommit, parent, "", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 2 {
		t.Fatalf("want 2 built trees, got %d", len(trees))
	}
	// The LAST built tree must equal the original commit tree (the hard invariant).
	if trees[len(trees)-1] != childCommit.TreeHash {
		t.Fatalf("last tree %s != original tree %s", trees[len(trees)-1], childCommit.TreeHash)
	}
	if !got.ShouldSplit || len(got.Groups) != 2 {
		t.Fatalf("unexpected proposal: %+v", got)
	}
}
