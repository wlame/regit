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
	res, err := mgr.Apply(plan, state, ApplyOptions{UnixTimestamp: 1717689600, ExpectedHead: oldHead})
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

	if _, err := mgr.Apply(spreadPlan(t, repo), state, ApplyOptions{UnixTimestamp: 1, ExpectedHead: oldHead}); err != nil {
		t.Fatal(err)
	}
	if !mgr.CanUndo() {
		t.Fatal("should be able to undo after apply")
	}
	restored, err := mgr.Undo(false, 2)
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
	if _, err := mgr.Apply(plan, state, ApplyOptions{UnixTimestamp: 1, ExpectedHead: state.HeadHash}); err == nil {
		t.Error("apply should be blocked on dirty tree")
	}
}

// TestManagerApply_freshDirtyCheckCatchesStaleSnapshot: the tree is dirtied
// AFTER the state snapshot was taken (snapshot says clean), so only preflight's
// live IsDirty recheck can block — proving the apply never trusts stale state.
func TestManagerApply_freshDirtyCheckCatchesStaleSnapshot(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	plan := spreadPlan(t, repo)
	state, _ := repo.ReadState()
	if state.IsDirty {
		t.Fatal("precondition: snapshot must be clean")
	}
	// Dirty the tree AFTER the snapshot: the stale state.IsDirty guard cannot fire.
	if err := os.WriteFile(filepath.Join(repo.Root(), "dirty.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := mgr.Apply(plan, state, ApplyOptions{UnixTimestamp: 11, ExpectedHead: state.HeadHash})
	if err == nil {
		t.Fatal("apply must refuse when the live tree is dirty despite a clean snapshot")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("want the dirty-tree message, got: %v", err)
	}
	refs, _ := repo.ListBackupRefs()
	if len(refs) != 0 {
		t.Errorf("refused apply must not create a backup ref, found %v", refs)
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
	head, _ := repo.ResolveRef("HEAD")
	state := model.RepoState{Branch: "main", HeadHash: head} // clean tree

	if _, err := mgr.Apply(plan, state, ApplyOptions{UnixTimestamp: 1, Confirmed: false, ExpectedHead: head}); err == nil {
		t.Error("pushed commits should require confirmation")
	}
	// With confirmation it should go through.
	if _, err := mgr.Apply(plan, state, ApplyOptions{UnixTimestamp: 2, Confirmed: true, ExpectedHead: head}); err != nil {
		t.Errorf("confirmed apply should succeed: %v", err)
	}
}

func TestManagerUndo_withoutApplyErrors(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	if _, err := mgr.Undo(false, 1); err == nil {
		t.Error("undo with empty stack should error")
	}
}

func TestManagerApply_noChangesErrors(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	commits, _ := repo.ReadCommits()
	empty := model.NewPlan(commits) // no edits
	state, _ := repo.ReadState()
	if _, err := mgr.Apply(empty, state, ApplyOptions{UnixTimestamp: 1, ExpectedHead: state.HeadHash}); err == nil {
		t.Error("apply with no edits should error")
	}
}

// TestManagerUndo_refusesCrossBranchRestore: a backup taken on main must not be
// restorable while another branch is checked out (H4).
func TestManagerUndo_refusesCrossBranchRestore(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	state, _ := repo.ReadState()
	if _, err := mgr.Apply(spreadPlan(t, repo), state, ApplyOptions{UnixTimestamp: 7, ExpectedHead: state.HeadHash}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Switch to a new branch; the in-session undo stack still holds main's backup.
	checkout := exec.Command("git", "checkout", "-q", "-b", "feature")
	checkout.Dir = repo.Root()
	if out, err := checkout.CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v\n%s", err, out)
	}
	if _, err := mgr.Undo(false, 8); err == nil {
		t.Fatal("Undo on a different branch must refuse")
	} else if !strings.Contains(err.Error(), "main") || !strings.Contains(err.Error(), "feature") {
		t.Errorf("refusal should name both branches; got: %v", err)
	}
}

// TestManagerApply_sameTimestampKeepsBothBackups: two applies sharing a unix
// timestamp must produce two distinct backup refs — never overwrite (H2).
func TestManagerApply_sameTimestampKeepsBothBackups(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	state, _ := repo.ReadState()
	firstHead, _ := repo.ResolveRef("HEAD")

	res1, err := mgr.Apply(spreadPlan(t, repo), state, ApplyOptions{UnixTimestamp: 42, ExpectedHead: state.HeadHash})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	state, _ = repo.ReadState() // head moved; re-snapshot
	res2, err := mgr.Apply(spreadPlan(t, repo), state, ApplyOptions{UnixTimestamp: 42, ExpectedHead: state.HeadHash})
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

// TestManagerApply_refusesWhenHeadMoved: a commit made after the plan snapshot
// must abort the apply before any backup ref is created (H3).
func TestManagerApply_refusesWhenHeadMoved(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	plan := spreadPlan(t, repo)
	state, _ := repo.ReadState() // snapshot the plan was computed from

	// Simulate another process committing to the branch. Shell out like buildRepo.
	appendCommit(t, repo) // helper below

	_, err := mgr.Apply(plan, state, ApplyOptions{UnixTimestamp: 9, ExpectedHead: state.HeadHash})
	if err == nil {
		t.Fatal("apply with a moved head must refuse")
	}
	if !strings.Contains(err.Error(), "moved since the plan was created") {
		t.Errorf("want the branch-moved message, got: %v", err)
	}
	refs, _ := repo.ListBackupRefs()
	if len(refs) != 0 {
		t.Errorf("refused apply must not create a backup ref, found %v", refs)
	}
}

// TestManagerApply_requiresExpectedHead: an empty anchor is a programming error
// so no call site can silently skip the check.
func TestManagerApply_requiresExpectedHead(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	state, _ := repo.ReadState()
	_, err := mgr.Apply(spreadPlan(t, repo), state, ApplyOptions{UnixTimestamp: 9})
	if err == nil || !strings.Contains(err.Error(), "ExpectedHead") {
		t.Fatalf("empty ExpectedHead must error mentioning ExpectedHead, got %v", err)
	}
}

// appendCommit adds one commit to the repo out-of-band (simulating a concurrent
// writer). Same shell-out style as buildRepo: tests may build fixtures with raw git.
func appendCommit(t *testing.T, repo *gitio.Repo) {
	t.Helper()
	dir := repo.Root()
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
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "extra.txt")
	run("commit", "-q", "-m", "concurrent")
}
