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
func purgeMergeInPrefixRepo(t *testing.T) (*Repo, []model.Commit, map[string]bool) {
	t.Helper()
	r, dir := purgeTestRepoEmpty(t)
	mustWriteFile(t, dir, "keep.txt", "v1")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "c0 keep only")
	gitT(t, dir, "checkout", "-q", "-b", "side")
	mustWriteFile(t, dir, "junk.log", "noise")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-m", "SA adds junk only")
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

// TestApplyPurge_mergeInPrefixFullyRemovesPath exercises gitio.ApplyPurge
// directly — the layer with NO merge-in-suffix guard of its own (that guard
// lives in safety.Manager.ApplyPurge) — to prove the corrected base actually
// closes the hole rather than merely computing the right number: once
// PurgeBase pulls the merge commit into the rewritten range, ApplyPurge
// strips junk.log from the merge's tree too, so the path is fully gone from
// the rewritten branch's entire history, not just its tip.
func TestApplyPurge_mergeInPrefixFullyRemovesPath(t *testing.T) {
	r, commits, affected := purgeMergeInPrefixRepo(t)
	spec := model.PurgeSpec{Paths: []string{"junk.log"}}

	newHead, remap, err := r.ApplyPurge(commits, spec, affected, DropSignatures)
	if err != nil {
		t.Fatalf("ApplyPurge: %v", err)
	}
	// Before the fix, only the edit commit would have been rewritten and the
	// merge (frozen, at its original hash) would still carry junk.log.
	out, err := r.git("log", "--format=%H", newHead)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, commits[1].Hash) {
		t.Errorf("the original merge commit hash must not survive unchanged in the rewritten history: %s", out)
	}
	// The definitive check: no commit reachable from newHead may still touch
	// junk.log — this is exactly what safety's verifyPurgeAbsence re-checks
	// at the layer above.
	reachable, err := r.PathsReachableFrom(newHead, spec.Paths)
	if err != nil {
		t.Fatal(err)
	}
	if reachable {
		t.Errorf("junk.log must not be reachable from the rewritten head %s", short(newHead))
	}
	if len(remap.Dropped) == 0 && len(remap.Rewritten) == 0 {
		t.Errorf("expected the merge commit to be either dropped or rewritten, remap: %+v", remap)
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
	gitT(t, dir, "init", "-b", "main")
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
