package safety

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// mergeRepo builds: main c1 — side branch (s1 by Alice, s2 by Bob) — merge
// --no-ff into main — c2 on main. Returns the repo and the merge hash.
func mergeRepo(t *testing.T) (*gitio.Repo, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir, env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(name, msg string, extraEnv ...string) {
		run(nil, "add", "-A")
		env := append([]string{
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00",
		}, extraEnv...)
		run(env, "commit", "-q", "--allow-empty", "-m", msg)
		_ = name
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "user.name", "Integrator")
	run(nil, "config", "user.email", "int@example.com")
	run(nil, "config", "commit.gpgsign", "false")
	commit("c1", "Base commit.")
	run(nil, "checkout", "-q", "-b", "feature/login")
	commit("s1", "Add login form.",
		"GIT_AUTHOR_NAME=Alice", "GIT_AUTHOR_EMAIL=alice@example.com")
	commit("s2", "Wire the backend.",
		"GIT_AUTHOR_NAME=Bob", "GIT_AUTHOR_EMAIL=bob@example.com")
	run(nil, "checkout", "-q", "main")
	run([]string{
		"GIT_AUTHOR_DATE=2026-06-05T15:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-06-05T15:00:00+00:00",
	}, "merge", "-q", "--no-ff", "feature/login", "-m", "Merge branch 'feature/login'")
	mergeHash := run(nil, "rev-parse", "HEAD")
	commit("c2", "Follow-up on main.")
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo, mergeHash
}

func TestApplyLinearize_appliesVerifiesAndUndoes(t *testing.T) {
	repo, mergeHash := mergeRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	side, err := repo.SideCommits(mergeHash)
	if err != nil {
		t.Fatal(err)
	}
	plan := model.BuildLinearizePlan(commits,
		map[string][]model.SideCommit{mergeHash: side})
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(repo)
	res, err := mgr.ApplyLinearize(commits, plan, state, ApplyOptions{
		UnixTimestamp: 1789000000,
		ExpectedHead:  commits[len(commits)-1].Hash,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.BackupRef == "" {
		t.Fatal("apply must create a backup ref")
	}
	// Branch moved to a merge-free chain; end tree identical to the old head's.
	git := func(args ...string) string {
		allArgs := append([]string{"-C", repo.Root()}, args...)
		cmd := exec.Command("git", allArgs...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := git("rev-list", "--min-parents=2", "--count", "main"); got != "0" {
		t.Fatalf("branch still has %s merge(s)", got)
	}
	if git("rev-parse", "main^{tree}") != git("rev-parse", res.OldHead+"^{tree}") {
		t.Fatal("end tree must be byte-identical to the original head tree")
	}

	// Undo restores the original head byte-for-byte.
	restored, _, err := mgr.Restore(res.BackupRef, false, 1789000100)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if restored != res.OldHead {
		t.Fatalf("undo restored %s, want %s", restored, res.OldHead)
	}
}

func TestApplyLinearize_emptyPlanAndDirtyTreeRefuse(t *testing.T) {
	repo, mergeHash := mergeRepo(t)
	commits, _ := repo.ReadCommits()
	state, _ := repo.ReadState()
	mgr := NewManager(repo)

	_, err := mgr.ApplyLinearize(commits, model.LinearizePlan{}, state, ApplyOptions{
		UnixTimestamp: 1, ExpectedHead: commits[len(commits)-1].Hash,
	})
	if err == nil || !strings.Contains(err.Error(), "nothing to linearize") {
		t.Fatalf("empty plan must refuse, got %v", err)
	}

	side, _ := repo.SideCommits(mergeHash)
	plan := model.BuildLinearizePlan(commits, map[string][]model.SideCommit{mergeHash: side})
	dirty := state
	dirty.HasTrackedChanges = true
	_, err = mgr.ApplyLinearize(commits, plan, dirty, ApplyOptions{
		UnixTimestamp: 1, ExpectedHead: commits[len(commits)-1].Hash,
	})
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("dirty tree must refuse, got %v", err)
	}
}

// TestApplyLinearize_pushedRequiresConfirmation pins ApplyLinearize's own
// pushed guard: model.AnyPushed(plan.RewrittenCommits(commits)). An
// unconfirmed linearize whose rewritten range touches a pushed commit must be
// refused before any repo state changes, and the same call with
// Confirmed:true must proceed — mirroring squash and split's identical guard.
func TestApplyLinearize_pushedRequiresConfirmation(t *testing.T) {
	repo, mergeHash := mergeRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	side, err := repo.SideCommits(mergeHash)
	if err != nil {
		t.Fatal(err)
	}
	plan := model.BuildLinearizePlan(commits, map[string][]model.SideCommit{mergeHash: side})
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	for i := range commits {
		commits[i].IsPushed = true
	}
	expectedHead := commits[len(commits)-1].Hash

	mgr := NewManager(repo)
	if _, err := mgr.ApplyLinearize(commits, plan, state, ApplyOptions{
		UnixTimestamp: 1, ExpectedHead: expectedHead,
	}); err == nil {
		t.Fatal("an unconfirmed linearize of pushed commits must be refused")
	}

	if _, err := mgr.ApplyLinearize(commits, plan, state, ApplyOptions{
		UnixTimestamp: 2, ExpectedHead: expectedHead, Confirmed: true,
	}); err != nil {
		t.Fatalf("a confirmed linearize of pushed commits should proceed: %v", err)
	}
}
