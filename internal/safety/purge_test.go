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

	mgr := NewManager(repo)
	res, err := mgr.ApplyPurge(commits, spec, affected, state,
		ApplyOptions{Sign: gitio.DropSignatures, UnixTimestamp: 1717689600, ExpectedHead: oldHead})
	if err != nil {
		t.Fatalf("ApplyPurge: %v", err)
	}
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

	restored, err := mgr.Undo(false, 1717689601)
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
	state := model.RepoState{IsDirty: true, Branch: "main", HeadHash: head}
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
	if !strings.Contains(err.Error(), "dirty") {
		t.Errorf("error should mention dirty tree, got: %v", err)
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
