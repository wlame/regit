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

// buildRepo creates a small repo with two clustered commits for manager tests.
func buildRepo(t *testing.T) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "user.name", "wlame")
	run(nil, "config", "user.email", "vvlame@gmail.com")
	run(nil, "config", "commit.gpgsign", "false")

	date := []string{"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00", "GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00"}
	for _, name := range []string{"one", "two"} {
		os.WriteFile(filepath.Join(dir, name+".txt"), []byte(name+"\n"), 0o644)
		run(nil, "add", ".")
		run(date, "commit", "-q", "-m", "commit "+name)
	}

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func spreadPlan(t *testing.T, repo *gitio.Repo) *model.Plan {
	t.Helper()
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	plan := model.NewPlan(commits)
	dates := []string{"2026-03-01T10:00:00+00:00", "2026-03-05T10:00:00+00:00"}
	for i, c := range commits {
		d, _ := time.Parse(time.RFC3339, dates[i])
		plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &d, NewCommitDate: &d})
	}
	return plan
}

func TestManagerApply_movesBranchAndBacksUp(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	plan := spreadPlan(t, repo)
	state, _ := repo.ReadState()

	oldHead, _ := repo.ResolveRef("HEAD")
	res, err := mgr.Apply(plan, state, ApplyOptions{UnixTimestamp: 1717689600})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.OldHead != oldHead {
		t.Errorf("OldHead mismatch")
	}
	if res.NewHead == oldHead {
		t.Error("HEAD should have moved")
	}

	// Backup ref must point at the original head.
	backupHash, err := repo.ResolveRef(res.BackupRef)
	if err != nil {
		t.Fatal(err)
	}
	if backupHash != oldHead {
		t.Errorf("backup ref should point at old head")
	}

	// New dates must be in effect.
	commits, _ := repo.ReadCommits()
	if commits[0].OrigAuthorDate.Format("2006-01-02") != "2026-03-01" {
		t.Errorf("first commit date not applied: %s", commits[0].OrigAuthorDate)
	}
}

func TestManagerUndo_restoresOriginalHead(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	state, _ := repo.ReadState()
	oldHead, _ := repo.ResolveRef("HEAD")

	if _, err := mgr.Apply(spreadPlan(t, repo), state, ApplyOptions{UnixTimestamp: 1}); err != nil {
		t.Fatal(err)
	}
	if !mgr.CanUndo() {
		t.Fatal("should be able to undo after apply")
	}
	restored, err := mgr.Undo()
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if restored != oldHead {
		t.Errorf("undo restored %s, want original %s", restored, oldHead)
	}
	if mgr.CanUndo() {
		t.Error("undo stack should be empty after undo")
	}
	now, _ := repo.ResolveRef("HEAD")
	if now != oldHead {
		t.Errorf("HEAD not restored: %s != %s", now, oldHead)
	}
}

func TestManagerApply_blocksDirtyTree(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	plan := spreadPlan(t, repo)
	// Dirty the tree.
	os.WriteFile(filepath.Join(repo.Root(), "one.txt"), []byte("changed\n"), 0o644)
	state, _ := repo.ReadState()
	if _, err := mgr.Apply(plan, state, ApplyOptions{UnixTimestamp: 1}); err == nil {
		t.Error("apply should be blocked on dirty tree")
	}
}

func TestManagerApply_pushedRequiresConfirmation(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	plan := spreadPlan(t, repo)
	// Simulate that the edited commits are already on the upstream.
	for i := range plan.Commits {
		plan.Commits[i].IsPushed = true
	}
	state := model.RepoState{Branch: "main"} // clean tree

	if _, err := mgr.Apply(plan, state, ApplyOptions{UnixTimestamp: 1, Confirmed: false}); err == nil {
		t.Error("pushed commits should require confirmation")
	}
	// With confirmation it should go through.
	if _, err := mgr.Apply(plan, state, ApplyOptions{UnixTimestamp: 2, Confirmed: true}); err != nil {
		t.Errorf("confirmed apply should succeed: %v", err)
	}
}

func TestManagerUndo_withoutApplyErrors(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	if _, err := mgr.Undo(); err == nil {
		t.Error("undo with empty stack should error")
	}
}

func TestManagerApply_noChangesErrors(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	commits, _ := repo.ReadCommits()
	empty := model.NewPlan(commits) // no edits
	state, _ := repo.ReadState()
	if _, err := mgr.Apply(empty, state, ApplyOptions{UnixTimestamp: 1}); err == nil {
		t.Error("apply with no edits should error")
	}
}

// TestManagerApply_sameTimestampKeepsBothBackups: two applies sharing a unix
// timestamp must produce two distinct backup refs — never overwrite (H2).
func TestManagerApply_sameTimestampKeepsBothBackups(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	state, _ := repo.ReadState()
	firstHead, _ := repo.ResolveRef("HEAD")

	res1, err := mgr.Apply(spreadPlan(t, repo), state, ApplyOptions{UnixTimestamp: 42})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	state, _ = repo.ReadState() // head moved; re-snapshot
	res2, err := mgr.Apply(spreadPlan(t, repo), state, ApplyOptions{UnixTimestamp: 42})
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if res1.BackupRef == res2.BackupRef {
		t.Fatalf("backup collision: both applies used %s", res1.BackupRef)
	}
	if got, _ := repo.ResolveRef(res1.BackupRef); got != firstHead {
		t.Errorf("first backup %s now points at %s, want original %s (overwritten!)", res1.BackupRef, got, firstHead)
	}
}
