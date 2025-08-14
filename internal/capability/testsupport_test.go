package capability

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wlame/regit/internal/gitio"
)

// newTestRepo builds a throwaway git repo with n commits (one file each) and
// returns its path and an opened gitio.Repo. Tests may shell out to git directly
// to BUILD fixtures; production code still goes through gitio.
func newTestRepo(t *testing.T, n int) (string, *gitio.Repo) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
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
