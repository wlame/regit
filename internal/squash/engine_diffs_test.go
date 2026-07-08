package squash

// engine_diffs_test.go covers the --with-diffs path in Summarize, which
// exercises memberDiffs and parentTreeOf against a real git repository.
// Creating a real repo is necessary because both functions call gitio.Repo
// methods that shell out to git (ResolveRef, Hunks, EmptyTree).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// linearDiffRepo creates a throwaway git repository with n commits (each
// adding a distinct file) and returns the opened Repo and commits root-first.
func linearDiffRepo(t *testing.T, n int) (*gitio.Repo, []model.Commit) {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if env != nil {
			c.Env = gittest.Env(dir, env...)
		}
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "user.name", "wlame")
	run(nil, "config", "user.email", "vvlame@gmail.com")
	run(nil, "config", "commit.gpgsign", "false")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		name := "f" + string(rune('0'+i)) + ".txt"
		content := "content " + name + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		run(nil, "add", ".")
		d := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		run([]string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d},
			"commit", "-q", "-m", "commit "+name)
	}
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("gitio.Open: %v", err)
	}
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatalf("ReadCommits: %v", err)
	}
	return repo, commits
}

// TestSummarize_withDiffsIncludesDiffInPrompt verifies that enabling
// WithDiffs causes memberDiffs to fetch each commit's patch and include it
// in the user message sent to the backend.
func TestSummarize_withDiffsIncludesDiffInPrompt(t *testing.T) {
	repo, commits := linearDiffRepo(t, 2)

	backend := &stubBackend{resp: `{"subject":"Combined","body":""}`}
	eng := NewEngine(repo, backend, NewCache(t.TempDir()))

	ch := model.SquashChain{Members: commits} // chain spanning both commits
	_, err := eng.Summarize(context.Background(), ch, Options{
		WithDiffs: true,
		Now:       time.Unix(0, 0),
	})
	if err != nil {
		t.Fatalf("Summarize with diffs: %v", err)
	}

	// BuildMessages embeds the diff under "  diff:\n".
	if !strings.Contains(backend.lastUser, "diff:") {
		t.Error("user prompt should contain 'diff:' when WithDiffs is true")
	}
	// The added file contents appear as '+content ...' lines in the diff.
	if !strings.Contains(backend.lastUser, "+content") {
		t.Error("user prompt should contain '+content ...' diff lines from the added files")
	}
}

// TestSummarize_withDiffsRootCommitUsesEmptyTree verifies that parentTreeOf
// falls back to the empty-tree hash when the first chain member is the root
// commit (no parents), so the diff correctly shows all lines as additions.
func TestSummarize_withDiffsRootCommitUsesEmptyTree(t *testing.T) {
	repo, commits := linearDiffRepo(t, 2)

	backend := &stubBackend{resp: `{"subject":"Root squash","body":""}`}
	eng := NewEngine(repo, backend, NewCache(t.TempDir()))

	// Include the root commit; parentTreeOf must reach EmptyTree code path.
	ch := model.SquashChain{Members: commits}
	_, err := eng.Summarize(context.Background(), ch, Options{
		WithDiffs: true,
		Now:       time.Unix(0, 0),
	})
	if err != nil {
		t.Fatalf("Summarize with root commit: %v", err)
	}
	// The root commit's diff shows the initial file as fully new ('+content f0.txt').
	if !strings.Contains(backend.lastUser, "+content f0.txt") {
		t.Errorf("root-commit diff should contain '+content f0.txt'; user prompt:\n%s",
			backend.lastUser)
	}
}

// TestSummarize_withDiffsNoCacheInteraction verifies that WithDiffs results
// are cached using a fingerprint that includes the WithDiffs flag, and are not
// mistakenly served from a plain (no-diffs) cache entry.
func TestSummarize_withDiffsNoCacheInteraction(t *testing.T) {
	repo, commits := linearDiffRepo(t, 2)
	cacheDir := t.TempDir()

	backend := &stubBackend{resp: `{"subject":"X","body":""}`}
	eng := NewEngine(repo, backend, NewCache(cacheDir))

	ch := model.SquashChain{Members: commits}
	now := time.Unix(0, 0)

	// First call without diffs; seeds the cache for the no-diffs fingerprint.
	_, err := eng.Summarize(context.Background(), ch, Options{WithDiffs: false, Now: now})
	if err != nil {
		t.Fatalf("first Summarize: %v", err)
	}
	callsAfterFirst := backend.calls

	// Second call with diffs; must miss the no-diffs cache and call the backend.
	_, err = eng.Summarize(context.Background(), ch, Options{WithDiffs: true, Now: now})
	if err != nil {
		t.Fatalf("second Summarize (with diffs): %v", err)
	}
	if backend.calls != callsAfterFirst+1 {
		t.Errorf("WithDiffs=true should produce a different fingerprint and bypass the no-diffs cache; backend calls = %d", backend.calls)
	}
}

// TestSummarize_withDiffs_nonRootFirstMember verifies that memberDiffs
// computes correct diffs when the chain's first member is NOT the repository
// root commit — the parentTreeOf call must resolve the parent's tree hash via
// ResolveRef rather than falling back to EmptyTree.
func TestSummarize_withDiffs_nonRootFirstMember(t *testing.T) {
	// linearDiffRepo builds c0(root),c1,c2.  We form a chain over [c1,c2] so
	// parentTreeOf is called on c1 which HAS a parent (c0).
	repo, commits := linearDiffRepo(t, 3)
	if len(commits) != 3 {
		t.Fatalf("want 3 commits, got %d", len(commits))
	}

	backend := &stubBackend{resp: `{"subject":"Add f1 and f2","body":""}`}
	eng := NewEngine(repo, backend, NewCache(t.TempDir()))

	// Chain from c1 to c2 — c1 has c0 as its parent, not a root commit.
	ch := model.SquashChain{Members: commits[1:]}
	_, err := eng.Summarize(context.Background(), ch, Options{
		WithDiffs: true,
		NoCache:   true,
		Now:       time.Unix(0, 0),
	})
	if err != nil {
		t.Fatalf("Summarize non-root chain with diffs: %v", err)
	}

	// The diff for c1 should show f1.txt being added (c0 has only f0.txt, c1 adds f1.txt).
	if !strings.Contains(backend.lastUser, "f1.txt") {
		t.Errorf("user prompt should contain f1.txt diff; user:\n%s", backend.lastUser)
	}
}
