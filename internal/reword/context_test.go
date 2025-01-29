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

