package gitio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitT runs a git command in dir and fails the test on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// twoCommitRepo creates a repo with a parent commit and a child that changes two
// files, returning the repo plus the parent and child tree hashes.
func twoCommitRepo(t *testing.T) (*Repo, string, string) {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("keep\n"), 0o644)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "parent")
	parent := gitT(t, dir, "rev-parse", "HEAD^{tree}")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("ONE\ntwo\nthree\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("new file\n"), 0o644)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "child")
	child := gitT(t, dir, "rev-parse", "HEAD^{tree}")
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo, parent, child
}

func TestHunks_assignsStableIDsAndFiles(t *testing.T) {
	repo, parent, child := twoCommitRepo(t)
	hs, err := repo.Hunks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) < 2 {
		t.Fatalf("expected >=2 hunks (a.txt edit, c.txt add), got %d", len(hs))
	}
	if hs[0].ID != "H1" {
		t.Fatalf("first hunk id = %q, want H1", hs[0].ID)
	}
	files := map[string]bool{}
	for _, h := range hs {
		files[h.File] = true
		if !strings.HasPrefix(h.Body, "@@") {
			t.Fatalf("hunk %s body should start with @@, got %q", h.ID, h.Body)
		}
		if !strings.Contains(h.FileHeader, "diff --git") {
			t.Fatalf("hunk %s missing file header", h.ID)
		}
	}
	if !files["a.txt"] || !files["c.txt"] {
		t.Fatalf("expected hunks for a.txt and c.txt, got %v", files)
	}
}
