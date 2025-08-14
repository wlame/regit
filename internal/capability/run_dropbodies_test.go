package capability

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
)

func TestDropBodiesDryRun(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	git("add", ".")
	git("commit", "-q", "-m", "Subject line\n\nA body paragraph.")
	repo, _ := gitio.Open(dir)

	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0)}
	a, _ := Parse(mustLookup(t, "drop-bodies"), nil)
	if err := runDropBodies(ctx, a); err != nil {
		t.Fatalf("runDropBodies: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("drop body")) {
		t.Errorf("expected a drop preview:\n%s", out.String())
	}
}
