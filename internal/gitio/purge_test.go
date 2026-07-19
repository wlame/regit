package gitio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// purgeHistoryRepo builds: c1 adds keep.txt; c2 adds junk.log (ONLY change);
// c3 edits keep.txt; c4 edits junk.log AND keep.txt. Purging junk.log must
// drop c2 (emptied), rewrite c4 (partial), and passthrough c3.
func purgeHistoryRepo(t *testing.T) (*Repo, []model.Commit) {
	t.Helper()
	r, dir := purgeTestRepoEmpty(t) // helper below
	commit := func(msg string, files map[string]string) {
		t.Helper()
		for name, content := range files {
			mustWriteFile(t, dir, name, content)
		}
		gitT(t, dir, "add", ".")
		gitT(t, dir, "commit", "-m", msg)
	}
	commit("c1", map[string]string{"keep.txt": "v1"})
	commit("c2", map[string]string{"junk.log": "noise"})
	commit("c3", map[string]string{"keep.txt": "v2"})
	commit("c4", map[string]string{"junk.log": "more", "keep.txt": "v3"})
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	return r, commits
}

func TestApplyPurge_dropsEmptiedAndRewritesPartial(t *testing.T) {
	r, commits := purgeHistoryRepo(t)
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}
	affected, err := r.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	newHead, remap, err := r.ApplyPurge(commits, spec, affected, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	if len(remap.Dropped) != 1 || remap.Dropped[0] != commits[1].Hash {
		t.Errorf("Dropped = %v, want exactly c2 (%s)", remap.Dropped, short(commits[1].Hash))
	}
	if _, ok := remap.Rewritten[commits[3].Hash]; !ok {
		t.Errorf("c4 should be rewritten (partial change), remap: %+v", remap)
	}
	// c3 never touches junk.log itself, but its tree is a FULL snapshot that
	// still carries junk.log forward unmodified from c2. Because commit trees
	// are full snapshots (not diffs), c3 must also be classified Rewritten —
	// its purged tree genuinely differs from its original tree, even though
	// c3's own parent-diff never mentioned junk.log. (Passthrough is exercised
	// separately by TestApplyPurge_passthroughWhenPathAlreadyAbsentFromTree,
	// where the path was already gone from the tree before the purge ran.)
	if _, ok := remap.Rewritten[commits[2].Hash]; !ok {
		t.Errorf("c3 should be rewritten (junk.log rode along in its snapshot), remap: %+v", remap)
	}
	// The path must be gone from every surviving commit's tree.
	out, err := r.git("log", "--name-only", "--format=%H", newHead)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "junk.log") {
		t.Errorf("junk.log still present in rewritten history:\n%s", out)
	}
	// c1 is before the first affected commit: hash preserved byte-identically.
	if !strings.Contains(out, commits[0].Hash) {
		t.Errorf("frozen prefix commit c1 (%s) must keep its hash", short(commits[0].Hash))
	}
}

func TestApplyPurge_rootDropReparentsChildAsRoot(t *testing.T) {
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "junk.log", "noise")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "root adds only junk")
	mustWriteFile(t, dir, "keep.txt", "v1")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "second")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}
	affected, err := r.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	newHead, remap, err := r.ApplyPurge(commits, spec, affected, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	if len(remap.Dropped) != 1 {
		t.Fatalf("root should be dropped, got %v", remap.Dropped)
	}
	parents, err := r.git("rev-list", "--parents", "-n", "1", newHead)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.Fields(parents)) != 1 {
		t.Errorf("new head should be a parentless root, rev-list --parents: %q", parents)
	}
}

func TestApplyPurge_everythingDroppedReturnsEmptyHead(t *testing.T) {
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "junk.log", "noise")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "only junk")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}
	affected := map[string]bool{commits[0].Hash: true}
	newHead, _, err := r.ApplyPurge(commits, spec, affected, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	if newHead != "" {
		t.Errorf("all-dropped purge must return empty newHead, got %s", newHead)
	}
}

// TestApplyPurge_frozenAncestorSuffixDropReturnsAncestorHash covers the
// controller addition from Task 3's review: newHead == "" must mean ONLY
// "every commit dropped from root". When a frozen (untouched) commit sits
// before the affected suffix and that entire suffix is dropped, the walk must
// return the frozen ancestor's ORIGINAL hash, not "" and not an error.
func TestApplyPurge_frozenAncestorSuffixDropReturnsAncestorHash(t *testing.T) {
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "keep.txt", "v1")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c1 keep only")
	mustWriteFile(t, dir, "junk.log", "noise")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c2 adds only junk")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}
	affected, err := r.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 1 || !affected[commits[1].Hash] {
		t.Fatalf("expected only c2 affected, got %v", affected)
	}
	newHead, remap, err := r.ApplyPurge(commits, spec, affected, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	if newHead != commits[0].Hash {
		t.Errorf("newHead = %s, want frozen ancestor c1's original hash %s",
			short(newHead), short(commits[0].Hash))
	}
	if len(remap.Dropped) != 1 || remap.Dropped[0] != commits[1].Hash {
		t.Errorf("Dropped = %v, want exactly c2 (%s)", remap.Dropped, short(commits[1].Hash))
	}
}

// TestApplyPurge_passthroughWhenPathAlreadyAbsentFromTree exercises the
// genuine Passthrough branch: c3 deletes junk.log for real (independent of
// the purge) while also editing keep.txt, and c4 then adds an unrelated file.
// For both, RemovePathsFromTree is a true no-op (the path is already gone
// from their tree), so they must re-emit with their ORIGINAL tree content,
// only reparented past the dropped c2 — the definition of Passthrough.
func TestApplyPurge_passthroughWhenPathAlreadyAbsentFromTree(t *testing.T) {
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "keep.txt", "v1")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c1 keep only")

	mustWriteFile(t, dir, "junk.log", "noise")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c2 adds junk")

	gitT(t, dir, "rm", "-q", "junk.log")
	mustWriteFile(t, dir, "keep.txt", "v2")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c3 removes junk for real, edits keep")

	mustWriteFile(t, dir, "other.txt", "x")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c4 adds unrelated file")

	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}
	affected, err := r.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	newHead, remap, err := r.ApplyPurge(commits, spec, affected, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	if len(remap.Dropped) != 1 || remap.Dropped[0] != commits[1].Hash {
		t.Errorf("Dropped = %v, want exactly c2 (%s)", remap.Dropped, short(commits[1].Hash))
	}
	if _, ok := remap.Passthrough[commits[2].Hash]; !ok {
		t.Errorf("c3 should be passthrough (junk.log already gone from its tree), remap: %+v", remap)
	}
	if _, ok := remap.Passthrough[commits[3].Hash]; !ok {
		t.Errorf("c4 should be passthrough (never had junk.log), remap: %+v", remap)
	}
	if len(remap.Rewritten) != 0 {
		t.Errorf("nothing should need rewriting here, remap: %+v", remap)
	}
	out, err := r.git("log", "--name-only", "--format=%H", newHead)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "junk.log") {
		t.Errorf("junk.log still present in rewritten history:\n%s", out)
	}
}

func TestCommitTouchedFiles_listsFullChangeSetIncludingRoot(t *testing.T) {
	r, commits := purgeHistoryRepo(t)
	files, err := r.CommitTouchedFiles(commits[0].Hash) // root commit needs --root
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "keep.txt" {
		t.Errorf("root touched files = %v, want [keep.txt]", files)
	}
	files, err = r.CommitTouchedFiles(commits[3].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("c4 touched files = %v, want junk.log and keep.txt", files)
	}
}

// TestCommitTouchedFiles_nonASCIIPath guards the -z NUL-delimited parsing
// added to close MINOR 6: without -z, git C-quotes a non-ASCII path (e.g.
// "caf\303\251.txt") in --name-only output, and splitting that literal on
// "\n" would compare the escaped form against the real path and never match
// in the drop-prediction comparisons (model.UnderAnyPath). -z disables the
// quoting entirely, so the parsed path must equal the raw UTF-8 filename.
func TestCommitTouchedFiles_nonASCIIPath(t *testing.T) {
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "café.txt", "x")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "add unicode file")
	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	files, err := r.CommitTouchedFiles(commits[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "café.txt" {
		t.Errorf("touched files = %q, want exactly [\"café.txt\"] (no C-quoting artifacts)", files)
	}
}

// purgeMergeInPrefixRepo builds the fixture CRITICAL finding 1 describes: a
// side branch adds junk.log, gets merged into main with no further change to
// that file (so the merge is TREESAME to the side branch on that path and git
// simplifies it out of `rev-list HEAD -- junk.log`), and a later main commit
// edits junk.log directly. Returns the repo, the first-parent commits
// (root-first: [c0, merge, edit]), and the AffectedCommits set for junk.log.
//
// Empirically (verified against real git before writing this fixture):
// `git rev-list HEAD -- junk.log` lists the edit commit and the side-branch
// commit, but NOT the merge commit — even though the merge commit's own tree
// already contains junk.log. That is exactly the blind spot PurgeBase closes.
//
// The side branch also adds other-side.txt, alongside junk.log, in the SAME
// commit as the rest of this fixture — deliberately, so the merge's tree
// still differs from its parent's after junk.log alone is stripped back out.
// Without that second file, stripping junk.log from the merge's tree would
// collapse it to EXACTLY its parent's tree, and gitio.ApplyPurge would DROP
// the merge outright (see its "tree == prevTree" rule) rather than
// REWRITING it as a genuine merge object — which would silently hide finding
// 2's bug (a rewritten merge's second parent is never remapped; see
// TestApplyPurge_mergeInPrefixLeavesSideParentReachable).
func purgeMergeInPrefixRepo(t *testing.T) (*Repo, []model.Commit, map[string]bool) {
	t.Helper()
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "keep.txt", "v1")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c0 keep only")
	gitT(t, dir, "checkout", "-q", "-b", "side")
	mustWriteFile(t, dir, "junk.log", "noise")
	mustWriteFile(t, dir, "other-side.txt", "side stuff")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "SA adds junk and another file")
	gitT(t, dir, "checkout", "-q", "main")
	gitT(t, dir, "merge", "--no-ff", "-q", "-m", "merge side", "side")
	mustWriteFile(t, dir, "junk.log", "noise\nmore")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "edit junk directly")

	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 || !commits[1].IsMerge {
		t.Fatalf("setup: want [c0, merge, edit] with commits[1] a merge, got %+v", commits)
	}
	affected, err := r.AffectedCommits([]string{"junk.log"})
	if err != nil {
		t.Fatal(err)
	}
	if affected[commits[1].Hash] {
		t.Fatalf("setup: merge commit %s must NOT be in the naive affected set (git's own history "+
			"simplification is what creates the blind spot this fixture exercises)", short(commits[1].Hash))
	}
	if !affected[commits[2].Hash] {
		t.Fatalf("setup: the edit commit must be directly affected")
	}
	return r, commits, affected
}

// TestPurgeBase_extendsPastMergeThatCarriesPathForward is the direct
// regression test for CRITICAL finding 1's fix (a): the naive boundary (the
// first commits[i] flagged in affected) lands on the EDIT commit, skipping
// right over the merge commit whose tree already carries junk.log forward.
// PurgeBase must walk backward past that merge and land on it instead, since
// the merge's own tree still contains the path.
func TestPurgeBase_extendsPastMergeThatCarriesPathForward(t *testing.T) {
	r, commits, affected := purgeMergeInPrefixRepo(t)

	naive := -1
	for i, c := range commits {
		if affected[c.Hash] {
			naive = i
			break
		}
	}
	if naive != 2 {
		t.Fatalf("setup sanity: naive boundary = %d, want 2 (the edit commit)", naive)
	}

	base, err := r.PurgeBase(commits, []string{"junk.log"}, affected)
	if err != nil {
		t.Fatal(err)
	}
	if base != 1 {
		t.Errorf("PurgeBase = %d, want 1 (the merge commit, extended one step back from the naive boundary)", base)
	}
	if !commits[base].IsMerge {
		t.Errorf("PurgeBase landed on commits[%d], which is not the merge commit", base)
	}
}

// TestApplyPurge_mergeInPrefixLeavesSideParentReachable is the test-honesty
// fix for finding 2 of the second final-review round. This test used to be
// named "...FullyRemovesPath" and asserted gitio.ApplyPurge alone (no merge
// guard — that lives in safety.Manager.ApplyPurge) fully removes junk.log
// even through a merge-in-prefix history. That assertion only ever passed
// because pulling the merge INTO the rewritten range (PurgeBase's fix) does
// not, by itself, close the whole hole: replayParents remaps ONLY a commit's
// FIRST parent (see its own doc comment) — a merge's SECOND parent is never
// touched. So when the merge is REWRITTEN as a genuine merge object (as
// opposed to DROPPED, which erases the whole commit — including its
// second-parent link — see purgeMergeInPrefixRepo's doc comment for why the
// original single-file fixture always hit the DROP path instead), the
// rewritten merge's second parent still points at the ORIGINAL, un-rewritten
// side-branch commit, whose tree still has junk.log. The old test's
// PathsReachableFrom check never caught this because, before finding 1's
// second round, that helper used a plain (non-full-history) rev-list — the
// exact same merge-simplification blind spot PurgeBase itself closes for the
// forward-purge direction — so it never walked into the stale second parent
// to notice the reachable blob.
//
// This is an accepted limitation of the gitio layer, which has no merge
// guard of its own by design: safety.Manager.ApplyPurge's merge-in-suffix
// guard is what actually blocks this case one layer up, BEFORE any rewrite
// is attempted — see TestApplyPurge_mergeInPrefixBlocksCleanly in
// internal/safety/purge_test.go.
func TestApplyPurge_mergeInPrefixLeavesSideParentReachable(t *testing.T) {
	r, commits, affected := purgeMergeInPrefixRepo(t)
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}

	newHead, remap, err := r.ApplyPurge(commits, spec, affected, DropSignatures)
	if err != nil {
		t.Fatalf("ApplyPurge: %v", err)
	}
	if _, ok := remap.Rewritten[commits[1].Hash]; !ok {
		t.Fatalf("setup sanity: the merge commit should be REWRITTEN, not dropped — "+
			"otherwise its stale second parent (what this test is about) never comes into play, remap: %+v", remap)
	}
	// The original merge hash must not survive unchanged...
	out, err := r.git("log", "--format=%H", newHead)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, commits[1].Hash) {
		t.Errorf("the original merge commit hash must not survive unchanged in the rewritten history: %s", out)
	}
	// ...yet junk.log remains reachable via the rewritten merge's untouched
	// second parent (the original, un-rewritten side-branch commit).
	// --full-history is required for PathsReachableFrom to even see this —
	// see its doc comment for why a plain rev-list would miss it too.
	reachable, err := r.PathsReachableFrom(newHead, spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if !reachable {
		t.Errorf("junk.log should still be reachable from %s via the merge's stale second parent — "+
			"gitio.ApplyPurge alone cannot close this; the safety-layer merge guard is what actually blocks it",
			short(newHead))
	}
}

// purgePocketRepo builds the "pocket" fixture from finding 1 of the second
// final-review round: a side branch adds THEN deletes secret.env entirely
// (net zero by merge time — a "pocket"), merges into main with no further
// change to that path (so the merge is TREESAME to BOTH parents, and git's
// default rev-list simplification never even descends into the side
// branch's own history), and a later main commit adds secret.env directly
// for real.
//
// Empirically verified against real git before writing this fixture:
// `git rev-list HEAD -- secret.env` (no --full-history) sees ONLY the edit
// commit; `git rev-list --full-history HEAD -- secret.env` additionally
// sees the side branch's add and delete commits, even though neither the
// merge's tree nor any tree after it ever actually contains secret.env. That
// gap — invisible to a tree-content walk like PurgeBase's, since no
// surviving tree has anything to see — is exactly what PathsInSideHistory
// and PathsReachableFrom's --full-history fix close.
func purgePocketRepo(t *testing.T) (*Repo, []model.Commit, map[string]bool) {
	t.Helper()
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "keep.txt", "v1")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c0 keep only")
	gitT(t, dir, "checkout", "-q", "-b", "side")
	mustWriteFile(t, dir, "secret.env", "s3cr3t")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "SA side adds secret")
	gitT(t, dir, "rm", "-q", "secret.env")
	gitT(t, dir, "commit", "-m", "SD side deletes secret")
	gitT(t, dir, "checkout", "-q", "main")
	gitT(t, dir, "merge", "--no-ff", "-q", "-m", "merge side (pocket)", "side")
	mustWriteFile(t, dir, "secret.env", "s3cr3t2")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c_edit main adds secret directly")

	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 || !commits[1].IsMerge {
		t.Fatalf("setup: want [c0, merge, edit] with commits[1] a merge, got %+v", commits)
	}
	affected, err := r.AffectedCommits([]string{"secret.env"})
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 1 || !affected[commits[2].Hash] {
		t.Fatalf("setup: only the edit commit should be in the naive (non-full-history) affected "+
			"set — the pocket must be entirely invisible to it, got %v", affected)
	}
	return r, commits, affected
}

// TestPathsInSideHistory_detectsPocketCommit is the direct regression test
// for finding 1's PathsInSideHistory helper: secret.env has no first-parent
// history of its own beyond the later direct edit, yet the path was also
// added-then-deleted inside the merged-in side branch. That side-branch
// history must be detected even though it never surfaces in AffectedCommits'
// (non-full-history) rev-list, and even though the merge's own tree — the
// only thing PurgeBase's tree-content walk can see — never contains the path
// at all (a pocket nets to zero, so there is nothing for a tree-content
// check to find).
func TestPathsInSideHistory_detectsPocketCommit(t *testing.T) {
	r, commits, _ := purgePocketRepo(t)
	sidePaths, err := r.PathsInSideHistory(commits, []string{"secret.env"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sidePaths) != 1 || sidePaths[0] != "secret.env" {
		t.Errorf("PathsInSideHistory = %v, want exactly [secret.env]", sidePaths)
	}
}

// TestPathsInSideHistory_mergeInPrefixAlsoCounts guards a subtlety: the
// ordinary merge-in-prefix fixture (junk.log added by a side branch, merged
// in, later edited directly on main) is NOT free of side-branch history
// either — the side branch's own "adds junk only" commit is, by definition,
// off the first-parent chain and its diff genuinely touches junk.log. So
// PathsInSideHistory correctly reports junk.log here too, even though this
// case is ALSO (separately) caught by the merge-in-suffix guard once
// PurgeBase pulls the merge itself into the rewritten range — the two guards
// overlap for any path with side-branch origin; only a genuine "pocket"
// (purgePocketRepo) is caught EXCLUSIVELY by this check (see
// TestPathsInSideHistory_detectsPocketCommit).
func TestPathsInSideHistory_mergeInPrefixAlsoCounts(t *testing.T) {
	r, commits, _ := purgeMergeInPrefixRepo(t)
	sidePaths, err := r.PathsInSideHistory(commits, []string{"junk.log"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sidePaths) != 1 || sidePaths[0] != "junk.log" {
		t.Errorf("PathsInSideHistory = %v, want [junk.log] (the side branch's own add commit "+
			"is genuinely off the first-parent chain)", sidePaths)
	}
}

// TestPathsInSideHistory_emptyWhenNoSideHistory is the negative companion:
// a purely linear history with no side branches or merges at all (every
// commit IS the first-parent chain) must report no side paths, for any path.
func TestPathsInSideHistory_emptyWhenNoSideHistory(t *testing.T) {
	r, commits := purgeHistoryRepo(t)
	sidePaths, err := r.PathsInSideHistory(commits, []string{"junk.log"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sidePaths) != 0 {
		t.Errorf("PathsInSideHistory = %v, want none (linear history has no side branches at all)", sidePaths)
	}
}

// TestApplyPurge_preservesPreExistingEmptyCommit is the regression test for
// IMPORTANT finding 3: purge must drop only the commits IT empties. c3 here
// is a `git commit --allow-empty` marker, already empty (tree == parent's
// tree) BEFORE the purge runs; it must survive as a re-emitted commit even
// though, after junk.log is stripped from the chain, its tree also happens to
// equal its (rewritten) new parent's tree — the exact condition the old code
// used, alone, to decide "drop".
func TestApplyPurge_preservesPreExistingEmptyCommit(t *testing.T) {
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "keep.txt", "v1")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c1 keep only")
	mustWriteFile(t, dir, "junk.log", "noise")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c2 adds junk only")
	gitT(t, dir, "commit", "--allow-empty", "-m", "c3 marker (pre-existing empty)")
	mustWriteFile(t, dir, "keep.txt", "v2")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c4 edits keep, unrelated to junk")

	commits, err := r.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}
	affected, err := r.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 1 || !affected[commits[1].Hash] {
		t.Fatalf("setup: expected only c2 affected, got %v", affected)
	}
	if commits[2].TreeHash != commits[1].TreeHash {
		t.Fatalf("setup: c3 (--allow-empty) must share its parent c2's tree")
	}

	newHead, remap, err := r.ApplyPurge(commits, spec, affected, DropSignatures)
	if err != nil {
		t.Fatal(err)
	}
	if len(remap.Dropped) != 1 || remap.Dropped[0] != commits[1].Hash {
		t.Errorf("Dropped = %v, want exactly c2 (%s) — purge should drop only what IT empties",
			remap.Dropped, short(commits[1].Hash))
	}
	subjects, err := r.git("log", "--format=%s", newHead)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(subjects, "c3 marker") {
		t.Errorf("the pre-existing empty marker commit must be preserved in rewritten history:\n%s", subjects)
	}
	count, err := r.git("rev-list", "--count", newHead)
	if err != nil {
		t.Fatal(err)
	}
	// c1, c3 (preserved marker), c4 survive; c2 is dropped.
	if count != "3" {
		t.Errorf("rewritten chain length = %s, want 3 (c2 dropped, c3 preserved)", count)
	}
}

// purgeTestRepoEmpty creates a freshly `git init`ed repo with no commits yet,
// for tests that need full control over the commit sequence. Reuses gitT
// (diff_test.go), the package's existing dir-rooted git-with-gittest.Env
// helper, so this file adds no parallel exec-and-check-error plumbing.
func purgeTestRepoEmpty(t *testing.T) (*Repo, string) {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r, dir
}

// mustWriteFile writes content to name under dir, creating parent directories
// as needed (name may include subdirectories, unlike squash_test.go's flat
// writeFile), failing the test on error.
func mustWriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
