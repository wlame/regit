// internal/gitio/squash_test.go
package gitio

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// linearRepo builds a throwaway repo with n commits (c0..c{n-1}, root-first),
// each adding file fI.txt, one minute apart starting at base. Returns the repo
// and its commits (root-first).
func linearRepo(t *testing.T, n int) (*Repo, []model.Commit) {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		t.Helper()
		c := newGitCmd(dir, env, args...)
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
		writeFile(t, dir, fileName(i), "content "+fileName(i)+"\n")
		run(nil, "add", ".")
		d := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		run([]string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}, "commit", "-q", "-m", "commit "+fileName(i))
	}
	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	return repo, commits
}

func newGitCmd(dir string, env []string, args ...string) *exec.Cmd {
	c := exec.Command("git", args...)
	c.Dir = dir
	if env != nil {
		c.Env = gittest.Env(dir, env...)
	}
	return c
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileName(i int) string { return "f" + string(rune('0'+i)) + ".txt" }

func mustResolve(t *testing.T, r *Repo, ref string) string {
	t.Helper()
	h, err := r.ResolveRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func mustGitOut(t *testing.T, r *Repo, args ...string) string {
	t.Helper()
	out, err := r.git(args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func TestApplySquash_singleChainAtHeadPreservesTree(t *testing.T) {
	repo, commits := linearRepo(t, 4) // c0,c1,c2,c3 (root-first)
	oldHead := mustResolve(t, repo, "HEAD")

	// Squash c2,c3 (the two newest) into one.
	ch := model.SquashChain{
		Members:     []model.Commit{commits[2], commits[3]},
		Subject:     "Squashed tail",
		AuthorName:  commits[2].AuthorName,
		AuthorEmail: commits[2].AuthorEmail,
		AuthorDate:  commits[3].OrigAuthorDate,
	}
	plan := model.SquashPlan{Chains: []model.SquashChain{ch}}

	newHead, remap, err := repo.ApplySquash(commits, plan, DropSignatures)
	if err != nil {
		t.Fatalf("ApplySquash: %v", err)
	}
	if err := repo.VerifyTreesUnchanged(oldHead, newHead); err != nil {
		t.Fatalf("HEAD tree changed: %v", err)
	}
	// 4 commits -> 3 (c2,c3 became one).
	if count := mustGitOut(t, repo, "rev-list", "--count", newHead); count != "3" {
		t.Fatalf("commit count = %s, want 3", count)
	}
	if remap.Boundary[commits[3].Hash] != newHead {
		t.Errorf("boundary[c3] = %s, want newHead %s", remap.Boundary[commits[3].Hash], newHead)
	}
	if subj := mustGitOut(t, repo, "log", "-1", "--format=%s", newHead); subj != "Squashed tail" {
		t.Errorf("squashed subject = %q, want \"Squashed tail\"", subj)
	}
}

func TestApplySquash_multipleChainsWithPassthrough(t *testing.T) {
	repo, commits := linearRepo(t, 6) // c0..c5
	oldHead := mustResolve(t, repo, "HEAD")

	// Chain A: c0,c1. Passthrough: c2. Chain B: c3,c4. Passthrough: c5.
	chA := model.SquashChain{Members: []model.Commit{commits[0], commits[1]}, Subject: "A", AuthorName: "ann", AuthorEmail: "a@x", AuthorDate: commits[1].OrigAuthorDate}
	chB := model.SquashChain{Members: []model.Commit{commits[3], commits[4]}, Subject: "B", AuthorName: "ann", AuthorEmail: "a@x", AuthorDate: commits[4].OrigAuthorDate}
	plan := model.SquashPlan{Chains: []model.SquashChain{chA, chB}}

	newHead, remap, err := repo.ApplySquash(commits, plan, DropSignatures)
	if err != nil {
		t.Fatalf("ApplySquash: %v", err)
	}
	if err := repo.VerifyTreesUnchanged(oldHead, newHead); err != nil {
		t.Fatalf("HEAD tree changed: %v", err)
	}
	// 6 -> 4 (two chains of 2 each collapse, 2 passthrough remain): 6-2 = 4.
	if count := mustGitOut(t, repo, "rev-list", "--count", newHead); count != "4" {
		t.Fatalf("commit count = %s, want 4", count)
	}
	if len(remap.Boundary) != 2 {
		t.Errorf("want 2 boundary entries, got %d", len(remap.Boundary))
	}
	// c2 and c5 are passthrough (re-emitted), so both appear in Passthrough.
	if _, ok := remap.Passthrough[commits[2].Hash]; !ok {
		t.Error("c2 should be a passthrough entry")
	}
	if _, ok := remap.Passthrough[commits[5].Hash]; !ok {
		t.Error("c5 should be a passthrough entry")
	}
}

func TestApplySquash_chainAtRootBecomesRoot(t *testing.T) {
	repo, commits := linearRepo(t, 3) // c0(root),c1,c2
	oldHead := mustResolve(t, repo, "HEAD")

	ch := model.SquashChain{Members: []model.Commit{commits[0], commits[1]}, Subject: "Root squash", AuthorName: "ann", AuthorEmail: "a@x", AuthorDate: commits[1].OrigAuthorDate}
	plan := model.SquashPlan{Chains: []model.SquashChain{ch}}

	newHead, _, err := repo.ApplySquash(commits, plan, DropSignatures)
	if err != nil {
		t.Fatalf("ApplySquash: %v", err)
	}
	if err := repo.VerifyTreesUnchanged(oldHead, newHead); err != nil {
		t.Fatalf("HEAD tree changed: %v", err)
	}
	// 3 -> 2; the oldest commit must be a root (no parents).
	rootHash := mustGitOut(t, repo, "rev-list", "--max-parents=0", newHead)
	parents := mustGitOut(t, repo, "rev-list", "--count", rootHash)
	if parents != "1" {
		t.Errorf("root rev-list count = %s, want 1 (a single root commit)", parents)
	}
}
