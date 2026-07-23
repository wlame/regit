package safety

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// buildRepo creates a small repo with two clustered commits for manager tests.
func buildRepo(t *testing.T) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir, env...)
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

// TestPreflight_blocksShallowClone proves the shallow-clone block lives in
// preflight, which every apply path (Apply, ApplySplit, ApplySquash) calls — so
// split and squash, which hand-roll their other guards and skip CheckGuards,
// still cannot rewrite a shallow clone's incomplete history.
func TestPreflight_blocksShallowClone(t *testing.T) {
	origin := buildRepo(t)
	shallowDir := t.TempDir()
	cmd := exec.Command("git", "clone", "--depth=1", "file://"+origin.Root(), ".")
	cmd.Dir = shallowDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shallow clone: %v\n%s", err, out)
	}
	repo, err := gitio.Open(shallowDir)
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	err = NewManager(repo).preflight("main", head, ApplyOptions{ExpectedHead: head})
	if err == nil {
		t.Fatal("preflight must block a shallow clone")
	}
	if !strings.Contains(err.Error(), "shallow") {
		t.Errorf("error = %q, want it to mention shallow", err)
	}
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
	restored, _, err := mgr.Undo(false, 2)
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
	if _, _, err := mgr.Undo(false, 1); err == nil {
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
	if _, _, err := mgr.Undo(false, 8); err == nil {
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
		cmd.Env = gittest.Env(dir,
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

// TestManagerRestore_sentinelErrors verifies Restore — the single undo
// implementation both Manager.Undo and headless `regit undo` funnel through —
// reports its guard refusals via errors.Is-able sentinels, which the CLI maps
// to exit code 2.
func TestManagerRestore_sentinelErrors(t *testing.T) {
	repo := buildRepo(t)
	mgr := NewManager(repo)
	state, _ := repo.ReadState()
	oldHead, _ := repo.ResolveRef("HEAD")
	res, err := mgr.Apply(spreadPlan(t, repo), state, ApplyOptions{UnixTimestamp: 1, ExpectedHead: oldHead})
	if err != nil {
		t.Fatal(err)
	}

	// Advance HEAD past the recorded applied head with a plain commit.
	appendCommit(t, repo)

	_, _, err = mgr.Restore(res.BackupRef, false, 2)
	if !errors.Is(err, ErrUndoHeadMoved) {
		t.Fatalf("advanced HEAD should refuse with ErrUndoHeadMoved, got: %v", err)
	}
	// Forced restore proceeds.
	restored, _, err := mgr.Restore(res.BackupRef, true, 3)
	if err != nil {
		t.Fatalf("forced Restore: %v", err)
	}
	if restored != oldHead {
		t.Errorf("restored %s, want %s", shortHash(restored), shortHash(oldHead))
	}

	// Cross-branch refusal: a backup naming another branch.
	otherRef := "refs/regit/backup/other-branch/999-0"
	if err := repo.UpdateRef(otherRef, oldHead, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.Restore(otherRef, false, 4); !errors.Is(err, ErrBackupOtherBranch) {
		t.Fatalf("cross-branch restore should refuse with ErrBackupOtherBranch, got: %v", err)
	}
}

// TestVerifyChainStructure_detectsShapeBugs exercises the structural verifier
// directly: correct lengths over the same boundary pass; a dropped or
// duplicated commit (wrong new length) and a reparented chain (different
// boundary) are caught — cases pairwise tree equality can never notice.
func TestVerifyChainStructure_detectsShapeBugs(t *testing.T) {
	repo := buildRepo(t) // two commits: root + child
	mgr := NewManager(repo)
	head, _ := repo.ResolveRef("HEAD")

	// Identity "rewrite": both chains are the same 1-commit chain over the
	// same parent.
	if err := mgr.verifyChainStructure(head, head, 1, 1); err != nil {
		t.Errorf("identical chains should verify: %v", err)
	}
	// Root case: the full 2-commit history on both sides.
	if err := mgr.verifyChainStructure(head, head, 2, 2); err != nil {
		t.Errorf("rooted chains should verify: %v", err)
	}
	// A remap that lost a commit: claims 2 rewritten commits but the walk
	// roots after 2 entries while the old side expects a boundary below 1.
	if err := mgr.verifyChainStructure(head, head, 1, 2); err == nil {
		t.Error("mismatched chain lengths must fail verification")
	}
	// A chain that claims to extend past the root.
	if err := mgr.verifyChainStructure(head, head, 3, 3); err == nil {
		t.Error("chains shorter than claimed must fail verification")
	}

	// Reparented chain: a new head whose boundary parent differs. Create a
	// sibling commit on top of the ROOT (not head), so walking 1 step from it
	// lands on the root while the old walk lands on head's parent... they
	// differ only if we compare at depth 1 from different heads.
	root, err := repo.FirstParents(head, 2)
	if err != nil || len(root) != 2 {
		t.Fatalf("expected a 2-commit history, got %v (%v)", root, err)
	}
	appendCommit(t, repo) // new head on top of the old head
	newHead, _ := repo.ResolveRef("HEAD")
	// Old side: 1 commit over boundary = old head's parent (the root).
	// New side: 1 commit over boundary = old head. Boundaries differ → fail.
	if err := mgr.verifyChainStructure(head, newHead, 1, 1); err == nil {
		t.Error("differing boundary parents must fail verification")
	}
}

// buildMergeRepo creates a repo whose first-parent history contains a real
// merge commit: c1 → c2 → M(second parent: side) → c3.
func buildMergeRepo(t *testing.T) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir, env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	date := []string{"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00", "GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00"}
	commitFile := func(name, msg string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run(nil, "add", ".")
		run(date, "commit", "-q", "-m", msg)
	}

	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "commit.gpgsign", "false")
	commitFile("c1.txt", "c1")
	run(nil, "checkout", "-q", "-b", "side")
	commitFile("side.txt", "side work")
	run(nil, "checkout", "-q", "main")
	commitFile("c2.txt", "c2")
	run(date, "merge", "--no-ff", "-q", "-m", "merge side", "side")
	commitFile("c3.txt", "c3")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// TestManagerApply_replaysMergeHistory is the merge-commit replay integration
// test: a redate over a first-parent history containing a real merge must
// succeed end to end, rewrite the merge's FIRST parent only (the side branch
// keeps its original commit), preserve every tree, and pass both verifiers.
func TestManagerApply_replaysMergeHistory(t *testing.T) {
	repo := buildMergeRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 4 {
		t.Fatalf("first-parent history = %d commits, want 4 (c1 c2 M c3)", len(commits))
	}
	var mergeOld model.Commit
	for _, c := range commits {
		if c.IsMerge {
			mergeOld = c
		}
	}
	if mergeOld.Hash == "" || len(mergeOld.ParentHashes) != 2 {
		t.Fatalf("fixture lacks a 2-parent merge commit: %+v", mergeOld)
	}
	sideParent := mergeOld.ParentHashes[1]

	plan := model.NewPlan(commits)
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	for i, c := range commits {
		d := base.AddDate(0, 0, i)
		plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &d, NewCommitDate: &d})
	}

	state, _ := repo.ReadState()
	res, err := NewManager(repo).Apply(plan, state, ApplyOptions{UnixTimestamp: 1, ExpectedHead: state.HeadHash})
	if err != nil {
		t.Fatalf("Apply over merge history: %v", err)
	}
	if res.Verification.Commits != 4 {
		t.Errorf("verified %d commits, want 4", res.Verification.Commits)
	}

	after, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 4 {
		t.Fatalf("rewritten first-parent history = %d commits, want 4", len(after))
	}
	var mergeNew model.Commit
	for i, c := range after {
		if c.TreeHash != commits[i].TreeHash {
			t.Errorf("commit %d tree changed: %s -> %s", i, commits[i].TreeHash, c.TreeHash)
		}
		if c.IsMerge {
			mergeNew = c
		}
	}
	if mergeNew.Hash == "" || len(mergeNew.ParentHashes) != 2 {
		t.Fatalf("merge commit lost its second parent after replay: %+v", mergeNew)
	}
	if mergeNew.ParentHashes[1] != sideParent {
		t.Errorf("side branch was rewritten: second parent %s, want original %s",
			shortHash(mergeNew.ParentHashes[1]), shortHash(sideParent))
	}
	if mergeNew.Hash == mergeOld.Hash {
		t.Error("merge commit should have been rewritten (dates changed)")
	}
}
