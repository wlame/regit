package reword

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/gitio"
)

// gitCmd runs a git command in dir with identity env vars set so commits work
// in any environment (CI, sandboxed, etc.).
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t",
		"GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@e",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// makeRepoWithREADME builds a minimal git repo that has a single committed
// README.md of the given size and returns an open *gitio.Repo for it.
func makeRepoWithREADME(t *testing.T, readmeSize int) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()

	gitCmd(t, dir, "init", "-q")
	gitCmd(t, dir, "config", "commit.gpgsign", "false")

	content := strings.Repeat("x", readmeSize)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	gitCmd(t, dir, "add", "README.md")
	gitCmd(t, dir, "commit", "-q", "-m", "add readme")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("gitio.Open: %v", err)
	}
	return repo
}

// makeEmptyRepo builds a minimal git repo with no README and returns its *gitio.Repo.
func makeEmptyRepo(t *testing.T) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()

	gitCmd(t, dir, "init", "-q")
	gitCmd(t, dir, "config", "commit.gpgsign", "false")

	// Commit a file so the repo is valid but has no README.
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write other.txt: %v", err)
	}
	gitCmd(t, dir, "add", "other.txt")
	gitCmd(t, dir, "commit", "-q", "-m", "init")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("gitio.Open: %v", err)
	}
	return repo
}

// makeTwoCommitRepo builds a repo with a parent commit and a child that edits a
// file, returning the open *gitio.Repo plus the parent and child tree hashes.
// It reuses the gitCmd helper already in this file.
func makeTwoCommitRepo(t *testing.T) (*gitio.Repo, string, string) {
	t.Helper()
	dir := t.TempDir()

	gitCmd(t, dir, "init", "-q")
	gitCmd(t, dir, "config", "commit.gpgsign", "false")

	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "parent")
	parent := gitCmd(t, dir, "rev-parse", "HEAD^{tree}")

	if err := os.WriteFile(file, []byte("ONE\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("rewrite a.txt: %v", err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "child")
	child := gitCmd(t, dir, "rev-parse", "HEAD^{tree}")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("gitio.Open: %v", err)
	}
	return repo, parent, child
}

// TestGatherDiff_returnsUnifiedDiff guards against signature drift on the thin
// GatherDiff wrapper: it must return a unified diff (containing "diff --git")
// for a real two-commit repo without error.
func TestGatherDiff_returnsUnifiedDiff(t *testing.T) {
	repo, parent, child := makeTwoCommitRepo(t)
	out, err := GatherDiff(repo, parent, child, 30)
	if err != nil {
		t.Fatalf("GatherDiff: %v", err)
	}
	if !strings.Contains(out, "diff --git") {
		t.Fatalf("GatherDiff output missing 'diff --git' marker:\n%s", out)
	}
}

func TestReadREADME_capsSize(t *testing.T) {
	const readmeSize = 5000
	const maxBytes = 1000

	repo := makeRepoWithREADME(t, readmeSize)
	got := ReadREADME(repo, maxBytes)
	if len(got) > maxBytes {
		t.Fatalf("ReadREADME truncation failed: got %d bytes, want <= %d", len(got), maxBytes)
	}
	if len(got) == 0 {
		t.Fatal("ReadREADME returned empty string for a repo that has a README")
	}

	// A repo with no README.md must return "".
	emptyRepo := makeEmptyRepo(t)
	if result := ReadREADME(emptyRepo, maxBytes); result != "" {
		t.Fatalf("ReadREADME should return empty string when no README exists, got %q", result[:min(len(result), 40)])
	}
}

// TestReadmeForContext verifies that ReadmeForContext returns the README when
// the context slice contains "readme" and "" when it does not.
func TestReadmeForContext_includesReadmeWhenKeyPresent(t *testing.T) {
	repo := makeRepoWithREADME(t, 100)

	// "readme" key present — should return non-empty string.
	got := ReadmeForContext(repo, []string{"diff", "readme", "filestates"})
	if got == "" {
		t.Fatal("ReadmeForContext returned empty string when context includes 'readme'")
	}
	if len(got) > READMEMaxBytes {
		t.Fatalf("ReadmeForContext returned %d bytes, want <= %d", len(got), READMEMaxBytes)
	}

	// "readme" key absent — should return "".
	got = ReadmeForContext(repo, []string{"diff", "filestates"})
	if got != "" {
		t.Fatalf("ReadmeForContext should return empty string when 'readme' is not in context, got %q", got[:min(len(got), 40)])
	}

	// Empty context slice — should return "".
	got = ReadmeForContext(repo, nil)
	if got != "" {
		t.Fatalf("ReadmeForContext should return empty string for nil context, got %q", got[:min(len(got), 40)])
	}
}
