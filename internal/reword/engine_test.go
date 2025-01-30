package reword

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// fakeBackend implements llm.Backend, returning a canned raw response.
type fakeBackend struct {
	raw   string
	calls int
}

func (f *fakeBackend) Name() string { return "fake" }
func (f *fakeBackend) Generate(_ context.Context, _, _ string, _ []byte, validate func(string) error) (string, error) {
	f.calls++
	if err := validate(f.raw); err != nil {
		return "", err
	}
	return f.raw, nil
}

// rewordRepo creates a throwaway git repository with two commits (parent and
// child). It returns the open Repo, the parent's tree hash (for diffing), and
// the child model.Commit.
func rewordRepo(t *testing.T) (*gitio.Repo, string, model.Commit) {
	t.Helper()
	dir := t.TempDir()

	run := func(stdin string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_DATE=2026-01-01T10:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-01-01T10:00:00+00:00",
		)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("", "init", "-q")
	run("", "symbolic-ref", "HEAD", "refs/heads/main")
	run("", "config", "user.name", "wlame")
	run("", "config", "user.email", "vvlame@gmail.com")
	run("", "config", "commit.gpgsign", "false")

	// Parent commit: add a file.
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("", "add", "hello.txt")
	run("", "commit", "-q", "-m", "Initial commit.")

	// Child commit: edit the file.
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello regit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("", "add", "hello.txt")
	run("", "commit", "-q", "-m", "Update greeting.", "-m", "Switch from 'world' to 'regit'.")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// parent tree hash is HEAD~1^{tree}
	parentTree, err := repo.ResolveRef("HEAD~1^{tree}")
	if err != nil {
		t.Fatalf("ResolveRef parent tree: %v", err)
	}

	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatalf("ReadCommits: %v", err)
	}
	if len(commits) < 2 {
		t.Fatalf("expected at least 2 commits, got %d", len(commits))
	}
	child := commits[len(commits)-1] // the most recent commit

	return repo, parentTree, child
}

func TestEngine_generateParsesAndCaches(t *testing.T) {
	repo, parent, child := rewordRepo(t) // helper: parent + child commit, returns repo + parent tree + child commit
	fb := &fakeBackend{raw: `{"variants":[{"title":"New title","body":"why"}]}`}
	eng := NewEngine(repo, fb, NewCache(t.TempDir()))
	opts := Options{Variants: 1, ContextLines: 30, Now: time.Unix(0, 0)}

	r, err := eng.Generate(context.Background(), child, parent, "readme text", opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Variants) != 1 || r.Variants[0].Title != "New title" {
		t.Fatalf("variants = %+v", r.Variants)
	}
	if r.CommitHash != child.Hash {
		t.Fatalf("commit hash = %q", r.CommitHash)
	}
	// Second call with the same inputs should hit the cache (no new backend call).
	before := fb.calls
	if _, err := eng.Generate(context.Background(), child, parent, "readme text", opts); err != nil {
		t.Fatal(err)
	}
	if fb.calls != before {
		t.Fatalf("expected cache hit, backend called again (%d -> %d)", before, fb.calls)
	}
}

func TestEngine_contextOrderInvariant(t *testing.T) {
	repo, parent, child := rewordRepo(t)
	fb := &fakeBackend{raw: `{"variants":[{"title":"Test","body":"body"}]}`}
	eng := NewEngine(repo, fb, NewCache(t.TempDir()))

	// First call with context ["a", "b"].
	opts1 := Options{Variants: 1, ContextLines: 30, Context: []string{"a", "b"}, Now: time.Unix(0, 0)}
	_, err := eng.Generate(context.Background(), child, parent, "readme text", opts1)
	if err != nil {
		t.Fatal(err)
	}
	calls1 := fb.calls

	// Second call with context ["b", "a"] (different order, same logical content).
	// This should hit the cache, so backend call count should not increase.
	opts2 := Options{Variants: 1, ContextLines: 30, Context: []string{"b", "a"}, Now: time.Unix(0, 0)}
	_, err = eng.Generate(context.Background(), child, parent, "readme text", opts2)
	if err != nil {
		t.Fatal(err)
	}
	calls2 := fb.calls

	if calls2 != calls1 {
		t.Fatalf("context order should be invariant: calls went from %d to %d (backend was called again)", calls1, calls2)
	}
}
