package safety

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// newLinearRepo builds a temporary git repository with n commits, each adding
// one new file (f0.txt, f1.txt, …). Returns an open *gitio.Repo.
func newLinearRepo(t *testing.T, n int) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if env != nil {
			c.Env = append(os.Environ(), env...)
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
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(nil, "add", ".")
		d := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		run([]string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}, "commit", "-q", "-m", "commit "+name)
	}
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo
}

func TestApplySquash_appliesAndVerifies(t *testing.T) {
	repo := newLinearRepo(t, 4) // c0..c3 root-first
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	ch := model.SquashChain{
		Members: []model.Commit{commits[2], commits[3]}, Subject: "Squashed",
		AuthorName: commits[2].AuthorName, AuthorEmail: commits[2].AuthorEmail,
		AuthorDate: commits[3].OrigAuthorDate,
	}
	plan := model.SquashPlan{Chains: []model.SquashChain{ch}}

	mgr := NewManager(repo)
	res, err := mgr.ApplySquash(commits, plan, state, ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1717689600})
	if err != nil {
		t.Fatalf("ApplySquash: %v", err)
	}
	if res.Verification.Commits == 0 {
		t.Error("verification summary should count at least the boundary pair")
	}
	if !mgr.CanUndo() {
		t.Error("a backup should have been pushed onto the undo stack")
	}
	after, _ := repo.ReadCommits()
	if len(after) != 3 {
		t.Fatalf("history should have 3 commits after squash, got %d", len(after))
	}
}

func TestApplySquash_blocksMergeMember(t *testing.T) {
	repo := newLinearRepo(t, 3)
	commits, _ := repo.ReadCommits()
	state, _ := repo.ReadState()
	commits[1].IsMerge = true // pretend the middle commit is a merge
	plan := model.SquashPlan{Chains: []model.SquashChain{{
		Members: []model.Commit{commits[0], commits[1]}, AuthorDate: time.Time{},
	}}}
	if _, err := NewManager(repo).ApplySquash(commits, plan, state, ApplyOptions{UnixTimestamp: 1}); err == nil {
		t.Error("squashing across a merge commit must be blocked")
	}
}

func TestApplySquash_blocksDirtyTree(t *testing.T) {
	repo := newLinearRepo(t, 3)
	commits, _ := repo.ReadCommits()
	state := model.RepoState{IsDirty: true, Branch: "main"}
	plan := model.SquashPlan{Chains: []model.SquashChain{{
		Members: []model.Commit{commits[0], commits[1]}, AuthorDate: commits[1].OrigAuthorDate,
	}}}
	if _, err := NewManager(repo).ApplySquash(commits, plan, state, ApplyOptions{UnixTimestamp: 1}); err == nil {
		t.Error("a dirty tree must block the squash")
	}
}
