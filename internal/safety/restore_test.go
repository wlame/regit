package safety

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// Restore's cleanBeforeMove branch (see manager.go) has no direct test: every
// existing undo test either starts from an already-clean tree with nothing to
// dirty, or never inspects the checkout at all. The two tests below drive both
// arms directly against a purge — the simplest content-changing operation,
// since purge's Sync (SyncIndexToTree) deliberately leaves the purged path on
// disk as an untracked file, which is exactly the residue that makes the tree
// dirty right after a genuine apply and forces a test that wants the CLEAN arm
// to clean it up itself first, same as a real operator would.

// TestRestore_cleanTreeRestoresTheWorkingTree drives the cleanBeforeMove=true
// arm: with nothing of the user's on disk, undo must use
// SyncIndexAndWorktreeToTree and put the working tree back exactly as it was
// before the purge, not just the index.
func TestRestore_cleanTreeRestoresTheWorkingTree(t *testing.T) {
	repo := purgeThreeCommitRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}
	affected, err := repo.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	oldHead := state.HeadHash

	snapBefore := gittest.Snap(t, repo.Root())

	mgr := NewManager(repo)
	res, err := mgr.ApplyPurge(commits, spec, affected, state,
		ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1717689800, ExpectedHead: oldHead})
	if err != nil {
		t.Fatalf("ApplyPurge: %v", err)
	}
	if res.NewHead == oldHead {
		t.Fatal("setup: purge should have produced a new head")
	}

	// A purge leaves junk.log on disk as an untracked file (that is the whole
	// point of SyncIndexToTree — see its doc comment), which alone makes the
	// tree dirty. Remove it to reach the genuinely clean state this test needs
	// to exercise: an operator who cleaned up after the purge before undoing.
	if err := os.Remove(filepath.Join(repo.Root(), "junk.log")); err != nil {
		t.Fatal(err)
	}
	dirty, err := repo.IsDirty()
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("setup: the tree must be clean before undo to exercise cleanBeforeMove=true")
	}

	restored, warning, err := mgr.Undo(false, 1717689801)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if warning != "" {
		t.Errorf("unexpected sync warning: %q", warning)
	}
	if restored != oldHead {
		t.Errorf("Undo should restore old head %s, got %s", shortHash(oldHead), shortHash(restored))
	}

	// The strong claim: not merely the index, but the WHOLE working tree —
	// including junk.log's content — is back exactly where it started.
	gittest.AssertUndoRestores(t, snapBefore, gittest.Snap(t, repo.Root()))
}

// TestRestore_dirtyTreeRefreshesTheIndexOnly drives the cleanBeforeMove=false
// arm: with real uncommitted work on disk, undo must fall back to
// SyncIndexToTree — refreshing the index so it agrees with the restored
// branch, but leaving worktree files exactly as the user left them.
//
// The dirt used here is a MODIFIED TRACKED file (keep.txt) at a path that also
// exists in the restore target's tree, not merely an untracked scratch file:
// `git read-tree -u` only ever rewrites paths that are part of the tree it is
// resetting to, so an unrelated untracked file would survive either arm of the
// conditional and prove nothing about which one ran. A locally modified
// tracked file whose path IS in the target tree is the one shape of dirt the
// `-u` arm actually clobbers — confirmed by temporarily forcing manager.go's
// cleanBeforeMove branch to always take the `-u` path and re-running this
// test: it fails, with keep.txt clobbered back to "v2" (see the task's
// mutation-testing record; the inversion was reverted, not committed).
func TestRestore_dirtyTreeRefreshesTheIndexOnly(t *testing.T) {
	repo := purgeThreeCommitRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}
	affected, err := repo.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	oldHead := state.HeadHash

	mgr := NewManager(repo)
	opts := ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1717689900, ExpectedHead: oldHead}
	if _, err := mgr.ApplyPurge(commits, spec, affected, state, opts); err != nil {
		t.Fatalf("ApplyPurge: %v", err)
	}

	const uncommitted = "MY UNCOMMITTED EDIT — must survive undo\n"
	keepPath := filepath.Join(repo.Root(), "keep.txt")
	if err := os.WriteFile(keepPath, []byte(uncommitted), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := repo.IsDirty()
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Fatal("setup: the modified keep.txt must register as a dirty tree")
	}

	restored, warning, err := mgr.Undo(false, 1717689901)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if warning != "" {
		t.Errorf("unexpected sync warning: %q", warning)
	}
	if restored != oldHead {
		t.Errorf("Undo should restore old head %s, got %s", shortHash(oldHead), shortHash(restored))
	}

	// The index must have been refreshed to the restored target: junk.log,
	// which the purge's own Sync had unstaged, is staged again.
	if _, staged := gittest.Snap(t, repo.Root()).Index["junk.log"]; !staged {
		t.Error("index should have been refreshed to include junk.log again")
	}

	// The important part: the user's uncommitted edit on disk must not have
	// been destroyed by the undo, even though the tree was dirty.
	got, err := os.ReadFile(keepPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != uncommitted {
		t.Errorf("keep.txt on disk = %q, want the untouched uncommitted edit %q", got, uncommitted)
	}
}

// TestRestore_treePreservingUndoLeavesUnrelatedStagingAlone is the regression
// test for the bug fixed alongside it: Restore used to run the checkout
// refresh on EVERY undo, including tree-preserving operations (redate here)
// whose backup target shares HEAD's tree byte-for-byte. On a dirty tree that
// took the index-only branch, `git read-tree --reset` replaces the WHOLE
// index — not just the paths the operation touched — so it silently discarded
// unrelated staged changes that have nothing to do with the undo at all. A
// redate never needed the index touched in the first place: its old and new
// trees are identical, so the fix is to skip the sync entirely rather than
// pick a "safer" sync.
func TestRestore_treePreservingUndoLeavesUnrelatedStagingAlone(t *testing.T) {
	repo := buildRepo(t)
	plan := spreadPlan(t, repo)
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	oldHead := state.HeadHash

	mgr := NewManager(repo)
	opts := ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1717689910, ExpectedHead: oldHead}
	res, err := mgr.Apply(plan, state, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.NewHead == oldHead {
		t.Fatal("setup: redate should have produced a new head")
	}

	// Stage a brand-new, wholly unrelated file — standing in for real staged
	// work the user has in progress at undo time, untouched by the redate.
	unrelatedPath := filepath.Join(repo.Root(), "unrelated.txt")
	if err := os.WriteFile(unrelatedPath, []byte("unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	addCmd := exec.Command("git", "add", "unrelated.txt")
	addCmd.Dir = repo.Root()
	addCmd.Env = gittest.Env(repo.Root())
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add unrelated.txt: %v\n%s", err, out)
	}
	dirty, err := repo.IsDirty()
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Fatal("setup: the staged unrelated file must register as a dirty tree")
	}

	restored, warning, err := mgr.Undo(false, 1717689911)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if warning != "" {
		t.Errorf("unexpected sync warning: %q", warning)
	}
	if restored != oldHead {
		t.Errorf("Undo should restore old head %s, got %s", shortHash(oldHead), shortHash(restored))
	}

	// The regression: undoing a tree-preserving operation must never touch the
	// index, so the unrelated staged entry must still be there afterward.
	if _, staged := gittest.Snap(t, repo.Root()).Index["unrelated.txt"]; !staged {
		t.Error("undoing a tree-preserving redate discarded an unrelated staged file")
	}
}
