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
