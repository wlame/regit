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
