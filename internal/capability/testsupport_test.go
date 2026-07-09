package capability

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
)

// farFutureNow is a fixed RunContext.Now for redate tests that run against a
// hardcoded "2026-01-01".."2026-03-01"-shaped date range: the segment engine's
// today-ceiling (strategy.SegmentTargets) clamps every window at Now, so a
// clock fixed before the requested range leaves zero overlap and silently
// stages nothing. Keep this after every such range.
var farFutureNow = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

// newTestRepo builds a throwaway git repo with n commits (one file each) and
// returns its path and an opened gitio.Repo. Tests may shell out to git directly
// to BUILD fixtures; production code still goes through gitio.
func newTestRepo(t *testing.T, n int) (string, *gitio.Repo) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for i := 0; i < n; i++ {
		f := filepath.Join(dir, "f.txt")
		if err := os.WriteFile(f, []byte{byte('a' + i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", "f.txt")
		run("commit", "-q", "-m", "commit "+string(rune('A'+i)))
	}
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return dir, repo
}

// newSplittableCapabilityRepo creates a throwaway git repo with a parent commit
// and a child commit whose diff spans two separate files, producing ≥2 hunks.
// The two-file setup lets split tests exercise the LLM path (ShouldSkipSplit
// passes) with a deterministic diff. Returns the dir and the opened Repo.
func newSplittableCapabilityRepo(t *testing.T) (string, *gitio.Repo) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "commit.gpgsign", "false")

	// Parent commit: two files.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("foo\nbar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "parent")

	// Child commit: modify both files → produces ≥2 hunks in the diff.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("HELLO\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("foo\nBAR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "child: update a and b")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return dir, repo
}
