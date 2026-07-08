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
	"github.com/wlame/regit/internal/gittest"
)

// TestDropBodiesZeroEditsNoMutation: with no bodies to drop, even --execute is a
// no-op that prints the empty-range message and never moves HEAD.
func TestDropBodiesZeroEditsNoMutation(t *testing.T) {
	_, repo := newTestRepo(t, 3) // newTestRepo commits have titles only, no bodies
	before, _ := repo.ReadState()
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0)}
	a, _ := Parse(mustLookup(t, "drop-bodies"), []string{"--execute"})
	if err := runDropBodies(ctx, a); err != nil {
		t.Fatalf("runDropBodies: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("no bodies to drop")) {
		t.Errorf("expected the zero-edit message:\n%s", out.String())
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("zero-edit drop-bodies moved HEAD")
	}
}

// TestDropBodiesMixedSkipsBodilessCommits: only commits that actually have a body
// appear in the preview; bodiless commits are skipped.
func TestDropBodiesMixedSkipsBodilessCommits(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = gittest.Env(dir, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("f0", "0")
	git("add", ".")
	git("commit", "-q", "-m", "title only")
	write("f1", "1")
	git("add", ".")
	git("commit", "-q", "-m", "has a body\n\nthe body paragraph")
	write("f2", "2")
	git("add", ".")
	git("commit", "-q", "-m", "another title only")
	repo, _ := gitio.Open(dir)
	commits, _ := repo.ReadCommits() // root-first: [c0, c1, c2]

	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0)}
	a, _ := Parse(mustLookup(t, "drop-bodies"), nil) // dry-run
	if err := runDropBodies(ctx, a); err != nil {
		t.Fatalf("runDropBodies: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte(Short(commits[1].Hash))) {
		t.Errorf("body commit %s should appear in the preview:\n%s", Short(commits[1].Hash), out.String())
	}
	for _, i := range []int{0, 2} {
		if bytes.Contains(out.Bytes(), []byte(Short(commits[i].Hash))) {
			t.Errorf("bodiless commit %s should be skipped:\n%s", Short(commits[i].Hash), out.String())
		}
	}
}

func TestDropBodiesDryRun(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = gittest.Env(dir, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
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
