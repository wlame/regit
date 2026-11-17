package safety

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// newPurgeRepo builds a temp repo with one commit per entry in commitFiles,
// writing/overwriting the named files with the given content. Modeled on
// squash_test.go's newLinearRepo, but lets each commit shape its own file set
// so fixtures can mirror gitio's purgeHistoryRepo pattern (a keep-only commit,
// a junk-only commit, a commit touching both).
func newPurgeRepo(t *testing.T, commitFiles []map[string]string) *gitio.Repo {
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
	for i, files := range commitFiles {
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run(nil, "add", ".")
		run(date, "commit", "-q", "-m", fmt.Sprintf("commit %d", i))
	}

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo
}

// purgeThreeCommitRepo builds: c1 adds keep.txt only; c2 adds junk.log only;
// c3 touches both. Purging junk.log must drop c2 (emptied) and rewrite c3
// (partial) while c1 stays byte-identical (frozen prefix).
func purgeThreeCommitRepo(t *testing.T) *gitio.Repo {
	t.Helper()
	return newPurgeRepo(t, []map[string]string{
		{"keep.txt": "v1"},
		{"junk.log": "noise"},
		{"keep.txt": "v2", "junk.log": "more"},
	})
}

func TestApplyPurge_endToEnd_removesPathAndSupportsUndo(t *testing.T) {
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
		ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1717689600, ExpectedHead: oldHead})
	if err != nil {
		t.Fatalf("ApplyPurge: %v", err)
	}

	// A purge declares exactly ONE thing about the checkout: junk.log leaves the
	// INDEX. It deliberately stays on disk as an untracked file, so its worktree
	// entry must survive untouched. Anything else moving is a defect — and today
	// this fails, because nothing refreshes the index at all and junk.log is left
	// staged for re-addition.
	gittest.AssertAppliedCleanly(t, snapBefore, gittest.Snap(t, repo.Root()),
		gittest.Declared{Kind: "index", Op: "removed", Path: "junk.log"})

	if res.NewHead == oldHead {
		t.Error("purge should have produced a new head")
	}
	if !mgr.CanUndo() {
		t.Error("a backup should have been pushed onto the undo stack")
	}

	// The purged path must be gone from the rewritten branch history.
	stillAffected, err := repo.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(stillAffected) != 0 {
		t.Errorf("junk.log should touch no commit after purge, still affected: %v", stillAffected)
	}

	restored, _, err := mgr.Undo(false, 1717689601)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if restored != oldHead {
		t.Errorf("Undo should restore old head %s, got %s", shortHash(oldHead), shortHash(restored))
	}
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if head != oldHead {
		t.Errorf("HEAD after undo = %s, want original %s", shortHash(head), shortHash(oldHead))
	}

	gittest.AssertUndoRestores(t, snapBefore, gittest.Snap(t, repo.Root()))
}

func TestApplyPurge_dirtyTreeBlocks(t *testing.T) {
	repo := purgeThreeCommitRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	state := model.RepoState{WorktreeStatus: model.WorktreeStatus{HasTrackedChanges: true}, Branch: "main", HeadHash: head}
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}
	affected, err := repo.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewManager(repo).ApplyPurge(commits, spec, affected, state,
		ApplyOptions{UnixTimestamp: 1, ExpectedHead: head})
	if err == nil {
		t.Fatal("a dirty tree must block the purge")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("error should mention the uncommitted changes, got: %v", err)
	}
}

// TestApplyPurge_invalidSpecBlocks pins the Guards closure's own validation
// call: an invalid spec (here, no paths at all) must be rejected before any
// history is touched, surfacing PurgeSpec.Validate's own message.
func TestApplyPurge_invalidSpecBlocks(t *testing.T) {
	repo := purgeThreeCommitRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	state := model.RepoState{Branch: "main", HeadHash: head}
	spec := model.PurgeSpec{} // no paths — invalid

	_, err = NewManager(repo).ApplyPurge(commits, spec, nil, state,
		ApplyOptions{UnixTimestamp: 1, ExpectedHead: head})
	if err == nil {
		t.Fatal("an invalid spec must block the purge")
	}
	if !strings.Contains(err.Error(), "no paths given") {
		t.Errorf("error should surface PurgeSpec.Validate's message, got: %v", err)
	}
}

// TestApplyPurge_nothingToPurgeBlocks pins the "no commits touch these paths"
// guard: a path that never appears anywhere in history must refuse cleanly
// rather than run a rewrite that changes nothing.
func TestApplyPurge_nothingToPurgeBlocks(t *testing.T) {
	repo := purgeThreeCommitRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	state := model.RepoState{Branch: "main", HeadHash: head}
	spec := model.PurgeSpec{Paths: []string{"never-existed.txt"}}
	affected, err := repo.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewManager(repo).ApplyPurge(commits, spec, affected, state,
		ApplyOptions{UnixTimestamp: 1, ExpectedHead: head})
	if err == nil {
		t.Fatal("purging a path with no history should block")
	}
	if !strings.Contains(err.Error(), "nothing to purge") {
		t.Errorf("error should say nothing to purge, got: %v", err)
	}
}

// TestApplyPurge_internalRewriteErrorPropagates pins the Rewrite closure's own
// error path from gitio.ApplyPurge (purge.go:87-89): Guards validates the
// caller-supplied commits against affected/spec, but does not re-verify every
// commit's TreeHash actually resolves in the object store. A stale or
// corrupted snapshot (here, simulated by corrupting one TreeHash after
// ReadCommits) must surface as a rewrite failure — never a silent bad
// rewrite — proving the safety layer does not blindly trust its input.
func TestApplyPurge_internalRewriteErrorPropagates(t *testing.T) {
	repo := purgeThreeCommitRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}
	affected, err := repo.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the affected commit's tree hash so gitio.ApplyPurge's own walk
	// fails trying to read it, after Guards (which never inspects TreeHash)
	// has already passed.
	for i := range commits {
		if affected[commits[i].Hash] {
			commits[i].TreeHash = "0000000000000000000000000000000000dead"
		}
	}

	_, err = NewManager(repo).ApplyPurge(commits, spec, affected, state,
		ApplyOptions{UnixTimestamp: 1, ExpectedHead: state.HeadHash})
	if err == nil {
		t.Fatal("a corrupted commit tree hash should fail the rewrite, got nil error")
	}
}

// TestVerifyPurgeHeadDiff_errorsOnUnresolvableRev pins the outer DiffChangeSet
// failure path: a head that names no commit must propagate as an error
// rather than an empty (and therefore falsely "clean") diff.
func TestVerifyPurgeHeadDiff_errorsOnUnresolvableRev(t *testing.T) {
	repo := purgeThreeCommitRepo(t)
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(repo)
	err = mgr.verifyPurgeHeadDiff(head, "0000000000000000000000000000000000dead", []string{"junk.log"})
	if err == nil {
		t.Fatal("verifyPurgeHeadDiff against an unresolvable rev should fail, got nil error")
	}
}

// TestVerifyPurgeAbsence_errorsOnUnresolvableRev pins verifyPurgeAbsence's own
// PathsReachableFrom failure path, mirroring the head-diff sibling above.
func TestVerifyPurgeAbsence_errorsOnUnresolvableRev(t *testing.T) {
	repo := purgeThreeCommitRepo(t)
	mgr := NewManager(repo)
	err := mgr.verifyPurgeAbsence("0000000000000000000000000000000000dead", []string{"junk.log"})
	if err == nil {
		t.Fatal("verifyPurgeAbsence against an unresolvable rev should fail, got nil error")
	}
}

// TestVerifyPurgeHeadDiff_flagsChangeOutsidePurgedPaths and
// TestVerifyPurgeHeadDiff_flagsNonDeletionUnderPurgedPath call the verifier
// directly (same package, like gitio's guard-input tests) rather than through
// a full ApplyPurge, because gitio.ApplyPurge's own rewrite always produces a
// clean diff — the only way to see verifyPurgeHeadDiff's OWN two failure
// branches is to feed it a hand-built before/after pair that a correct rewrite
// would never actually produce, proving the safety net catches a regression in
// the rewrite it is meant to double-check.

// TestVerifyPurgeHeadDiff_flagsChangeOutsidePurgedPaths builds oldHead/newHead
// where the diff includes a path NOT among the purged paths, and asserts the
// verifier refuses rather than silently accept.
func TestVerifyPurgeHeadDiff_flagsChangeOutsidePurgedPaths(t *testing.T) {
	repo := newPurgeRepo(t, []map[string]string{
		{"one.txt": "a"},
		{"one.txt": "a", "two.txt": "b"}, // adds an UNRELATED file
	})
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	oldHead, newHead := commits[0].Hash, commits[1].Hash

	mgr := NewManager(repo)
	err = mgr.verifyPurgeHeadDiff(oldHead, newHead, []string{"one.txt"})
	if err == nil {
		t.Fatal("a diff touching an unpurged path should fail verification")
	}
	if !strings.Contains(err.Error(), "unexpected change") {
		t.Errorf("error = %q, want it to name the unexpected-change failure", err)
	}
}

// TestVerifyPurgeHeadDiff_flagsNonDeletionUnderPurgedPath builds oldHead/newHead
// where the purged path was MODIFIED rather than deleted, and asserts the
// verifier refuses — a purge that leaves content behind under the path it
// claims to have removed is not a purge.
func TestVerifyPurgeHeadDiff_flagsNonDeletionUnderPurgedPath(t *testing.T) {
	repo := newPurgeRepo(t, []map[string]string{
		{"secret.txt": "a"},
		{"secret.txt": "b"}, // modified, not deleted
	})
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	oldHead, newHead := commits[0].Hash, commits[1].Hash

	mgr := NewManager(repo)
	err = mgr.verifyPurgeHeadDiff(oldHead, newHead, []string{"secret.txt"})
	if err == nil {
		t.Fatal("a purged path that was modified rather than deleted should fail verification")
	}
	if !strings.Contains(err.Error(), "was not deleted") {
		t.Errorf("error = %q, want it to name the not-deleted failure", err)
	}
}

func TestApplyPurge_mergeInSuffixBlocks(t *testing.T) {
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
	// c3 (the last commit, index 2) is in the affected suffix (it touches
	// junk.log); flag it as a merge to trigger the merge-in-suffix guard.
	commits[2].IsMerge = true

	_, err = NewManager(repo).ApplyPurge(commits, spec, affected, state,
		ApplyOptions{UnixTimestamp: 1, ExpectedHead: state.HeadHash})
	if err == nil {
		t.Fatal("purging across a merge commit in the affected suffix must be blocked")
	}
	if !strings.Contains(err.Error(), "merge") {
		t.Errorf("error should mention merge, got: %v", err)
	}
}

// purgeMergeInPrefixRepo mirrors the fixture in internal/gitio/purge_test.go
// (see its doc comment for the empirical git behavior this relies on): a side
// branch adds junk.log, merges into main with no further change to that file
// (so the merge is TREESAME to the side branch on that path and git's own
// history simplification drops it out of `rev-list HEAD -- junk.log`), and a
// later main commit edits junk.log directly. newPurgeRepo's linear
// one-commit-per-entry fixtures can't express a real merge, so this builds
// one directly.
func purgeMergeInPrefixRepo(t *testing.T) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "user.name", "wlame")
	run("config", "user.email", "vvlame@gmail.com")
	run("config", "commit.gpgsign", "false")

	write("keep.txt", "v1")
	run("add", ".")
	run("commit", "-q", "-m", "c0 keep only")
	run("checkout", "-q", "-b", "side")
	write("junk.log", "noise")
	run("add", ".")
	run("commit", "-q", "-m", "SA adds junk only")
	run("checkout", "-q", "main")
	run("merge", "--no-ff", "-q", "-m", "merge side", "side")
	write("junk.log", "noise\nmore")
	run("add", ".")
	run("commit", "-q", "-m", "edit junk directly")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo
}

// TestApplyPurge_mergeInPrefixBlocksCleanly is the safety-layer regression
// test for CRITICAL finding 1: before the fix, the naive rewrite boundary
// landed on the "edit junk directly" commit and never saw the merge commit at
// all (it sat, unexamined, in what the guard believed was an untouched
// prefix) — so the purge would have silently "succeeded" while the merge
// commit's original, unrewritten tree kept junk.log fully reachable. With
// gitio.PurgeBase wired in, the corrected base now includes the merge commit
// in the rewritten suffix, so the existing merge-in-suffix guard fires and
// blocks — the "clean abort" branch of the two acceptable outcomes the task
// describes (block, or fully remove the path; this fixture is expected to
// always hit the guard, since a real merge commit is always IsMerge==true).
func TestApplyPurge_mergeInPrefixBlocksCleanly(t *testing.T) {
	repo := purgeMergeInPrefixRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 || !commits[1].IsMerge {
		t.Fatalf("setup: want [c0, merge, edit] with commits[1] a merge, got %+v", commits)
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
	if affected[commits[1].Hash] {
		t.Fatalf("setup: the merge commit must not be in the naive affected set")
	}
	oldHead := state.HeadHash

	_, err = NewManager(repo).ApplyPurge(commits, spec, affected, state,
		ApplyOptions{UnixTimestamp: 1, ExpectedHead: oldHead})
	if err == nil {
		t.Fatal("a purge whose corrected base pulls in a merge commit must be blocked")
	}
	if !strings.Contains(err.Error(), "merge") {
		t.Errorf("error should mention merge, got: %v", err)
	}
	newHead, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if newHead != oldHead {
		t.Errorf("a guard-blocked purge must not move the branch: %s -> %s", shortHash(oldHead), shortHash(newHead))
	}
}

// purgePocketRepo mirrors the fixture in internal/gitio/purge_test.go's
// purgePocketRepo (see its doc comment for the empirical git behavior this
// relies on): a side branch adds THEN deletes secret.env entirely (net zero
// by merge time — a "pocket"), merges into main with no further change to
// that path, and a later main commit adds secret.env directly for real.
// Unlike purgeMergeInPrefixRepo, the merge's own tree never contains
// secret.env at any point, so gitio.PurgeBase's tree-content walk has no
// reason to pull the merge into the rewritten range at all — this fixture is
// caught ONLY by the new PathsInSideHistory guard, not the pre-existing
// merge-in-suffix one.
func purgePocketRepo(t *testing.T) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	run("config", "user.name", "wlame")
	run("config", "user.email", "vvlame@gmail.com")
	run("config", "commit.gpgsign", "false")

	write("keep.txt", "v1")
	run("add", ".")
	run("commit", "-q", "-m", "c0 keep only")
	run("checkout", "-q", "-b", "side")
	write("secret.env", "s3cr3t")
	run("add", ".")
	run("commit", "-q", "-m", "SA side adds secret")
	run("rm", "-q", "secret.env")
	run("commit", "-q", "-m", "SD side deletes secret")
	run("checkout", "-q", "main")
	run("merge", "--no-ff", "-q", "-m", "merge side (pocket)", "side")
	write("secret.env", "s3cr3t2")
	run("add", ".")
	run("commit", "-q", "-m", "c_edit main adds secret directly")

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo
}

// TestApplyPurge_pocketBlocksCleanly is the safety-layer regression test for
// finding 1 of the second final-review round: the pocket's merge never
// carries secret.env in its own tree (net zero), so it is never pulled into
// the rewritten suffix and the pre-existing merge-in-suffix guard has
// nothing to catch — only the new PathsInSideHistory-backed guard blocks
// this. Asserts a clean abort: an error mentioning the side-branch history,
// and the branch left exactly where it started.
func TestApplyPurge_pocketBlocksCleanly(t *testing.T) {
	repo := purgePocketRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 || !commits[1].IsMerge {
		t.Fatalf("setup: want [c0, merge, edit] with commits[1] a merge, got %+v", commits)
	}
	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.PurgeSpec{Paths: []string{"secret.env"}}
	affected, err := repo.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	oldHead := state.HeadHash

	_, err = NewManager(repo).ApplyPurge(commits, spec, affected, state,
		ApplyOptions{UnixTimestamp: 1, ExpectedHead: oldHead})
	if err == nil {
		t.Fatal("a purge whose target path has pocket history on a merged side branch must be blocked")
	}
	if !strings.Contains(err.Error(), "side branch") {
		t.Errorf("error should mention the side branch, got: %v", err)
	}
	newHead, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if newHead != oldHead {
		t.Errorf("a guard-blocked purge must not move the branch: %s -> %s", shortHash(oldHead), shortHash(newHead))
	}
}

// TestVerifyPurgeAbsence_detectsPocketAfterHypotheticalRewrite is the
// verification-layer test for finding 1: it drives verifyPurgeAbsence
// directly on the head gitio.ApplyPurge (the layer with no pocket-awareness
// of its own) actually produces for the pocket fixture, simulating what
// would happen if the new PathsInSideHistory guard did not exist or ever
// regressed. Because the merge's tree never contains secret.env, PurgeBase
// has no reason to pull it into the rewritten range: gitio.ApplyPurge only
// rewrites the later "c_edit" commit and leaves the merge completely
// untouched, still pointing (via its second parent) at the side branch's own
// SA commit, whose tree still has secret.env. Before PathsReachableFrom
// gained --full-history, this check would have wrongly reported "not
// reachable" for exactly the same merge-simplification reason
// PathsInSideHistory itself has to work around — this proves the fix closes
// that false-pass, independent of the guard added above.
func TestVerifyPurgeAbsence_detectsPocketAfterHypotheticalRewrite(t *testing.T) {
	repo := purgePocketRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.PurgeSpec{Paths: []string{"secret.env"}}
	affected, err := repo.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	// Bypass the new guard entirely: call gitio.ApplyPurge directly, exactly
	// as safety.Manager.ApplyPurge's own Rewrite step does, to get the head a
	// hypothetical unguarded rewrite would produce.
	newHead, _, err := repo.ApplyPurge(commits, spec, affected, gitio.DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(repo)
	if err := mgr.verifyPurgeAbsence(newHead, spec.Paths); err == nil {
		t.Fatal("verifyPurgeAbsence should catch the pocket's secret.env still reachable via the " +
			"merge's untouched second parent, even with no guard involved")
	} else if !strings.Contains(err.Error(), "still reachable") {
		t.Errorf("error should mention reachability, got: %v", err)
	}
}

// TestVerifyPurgeAbsence_detectsPathStillReachable unit-tests the CRITICAL
// finding 1 (b) safety net directly: verifyPurgeAbsence must fire whenever a
// path is still reachable from the given head, independent of how that head
// was produced. This is what "converts any future base bug into a safe
// abort" — even if PurgeBase itself ever regressed, this final check still
// catches a rewrite that left a purged path in place.
func TestVerifyPurgeAbsence_detectsPathStillReachable(t *testing.T) {
	repo := purgeThreeCommitRepo(t) // junk.log is reachable from HEAD here
	mgr := NewManager(repo)
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	err = mgr.verifyPurgeAbsence(head, []string{"junk.log"})
	if err == nil {
		t.Fatal("verifyPurgeAbsence should fail when the path is still reachable from head")
	}
	if !strings.Contains(err.Error(), "still reachable") {
		t.Errorf("error should mention reachability, got: %v", err)
	}
}

// TestVerifyPurgeAbsence_passesWhenPathGone is the companion positive case:
// after a real, successful purge, the new head must pass the check cleanly.
func TestVerifyPurgeAbsence_passesWhenPathGone(t *testing.T) {
	repo := purgeThreeCommitRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}
	affected, err := repo.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	newHead, _, err := repo.ApplyPurge(commits, spec, affected, gitio.DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(repo)
	if err := mgr.verifyPurgeAbsence(newHead, spec.Paths); err != nil {
		t.Errorf("verifyPurgeAbsence should pass after a genuine successful purge, got: %v", err)
	}
}

func TestApplyPurge_pushedNeedsConfirmation(t *testing.T) {
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
	// c2 (index 1) is the first affected commit; flag it pushed.
	commits[1].IsPushed = true
	oldHead := state.HeadHash

	mgr := NewManager(repo)
	_, err = mgr.ApplyPurge(commits, spec, affected, state,
		ApplyOptions{UnixTimestamp: 1, ExpectedHead: oldHead})
	if err == nil {
		t.Fatal("an unconfirmed purge touching a pushed commit must be refused")
	}
	if !strings.Contains(err.Error(), "confirmation") {
		t.Errorf("error should mention confirmation, got: %v", err)
	}
	newHead, _ := repo.ResolveRef("HEAD")
	if newHead != oldHead {
		t.Errorf("a refused purge must not move HEAD: %s -> %s", oldHead, newHead)
	}

	if _, err := mgr.ApplyPurge(commits, spec, affected, state,
		ApplyOptions{UnixTimestamp: 2, Confirmed: true, ExpectedHead: oldHead}); err != nil {
		t.Errorf("a confirmed purge touching a pushed commit should proceed: %v", err)
	}
}

func TestApplyPurge_wouldEmptyBranchBlocks(t *testing.T) {
	repo := newPurgeRepo(t, []map[string]string{{"junk.log": "noise"}})
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

	_, err = NewManager(repo).ApplyPurge(commits, spec, affected, state,
		ApplyOptions{UnixTimestamp: 1, ExpectedHead: state.HeadHash})
	if err == nil {
		t.Fatal("purging the only file in a single-commit repo must be blocked")
	}
	if !strings.Contains(err.Error(), "every commit") {
		t.Errorf("error should mention removing every commit, got: %v", err)
	}
}

// TestApplyPurge_rootDropSurvivesChainVerification exercises the shape the
// brief flagged as a possible risk for verifyChainStructure: purging a path
// that empties the ROOT commit, so the next commit is reparented as the new
// root. OldLen/NewLen are computed from firstAffected (0 here, since the root
// itself is affected), so both chain walks hit their respective roots exactly
// and verifyChainStructure's existing root-vs-root case (oldChain/newChain
// shorter than the +1 lookahead) accepts it without any relaxation.
func TestApplyPurge_rootDropSurvivesChainVerification(t *testing.T) {
	repo := newPurgeRepo(t, []map[string]string{
		{"junk.log": "noise"},
		{"keep.txt": "v1"},
	})
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

	res, err := NewManager(repo).ApplyPurge(commits, spec, affected, state,
		ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1, ExpectedHead: state.HeadHash})
	if err != nil {
		t.Fatalf("ApplyPurge: %v", err)
	}
	parents, err := repo.FirstParents(res.NewHead, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(parents) != 1 {
		t.Errorf("new head should be a parentless root, FirstParents = %v", parents)
	}
}
