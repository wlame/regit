package gittest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runGit runs one git command in dir under the hermetic Env, failing the test
// on error. It is the single place test fixtures shell out to git so every
// failure is reported the same way.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = Env(dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// writeFile writes content to name inside dir, failing the test on error.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// initRepo builds a two-commit fixture with one tracked file, one gitignored
// file, and one untracked file, so every Snapshot component has something to
// capture and the ignore rule has something to exclude.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	writeFile(t, dir, ".gitignore", "build.out\n")
	writeFile(t, dir, "a.txt", "alpha\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "First.")
	writeFile(t, dir, "build.out", "ignored noise\n")
	writeFile(t, dir, "untracked.txt", "visible\n")
	return dir
}

func TestSnap_capturesHeadRefsIndexAndWorktree(t *testing.T) {
	dir := initRepo(t)
	s := Snap(t, dir)

	if s.Head != "refs/heads/main" {
		t.Errorf("Head = %q, want refs/heads/main", s.Head)
	}
	if s.HeadHash == "" {
		t.Error("HeadHash is empty")
	}
	if _, ok := s.Refs["refs/heads/main"]; !ok {
		t.Errorf("Refs missing refs/heads/main; got %v", s.Refs)
	}
	if _, ok := s.Index["a.txt"]; !ok {
		t.Errorf("Index missing a.txt; got %v", s.Index)
	}
	if _, ok := s.Worktree["a.txt"]; !ok {
		t.Errorf("Worktree missing a.txt; got %v", s.Worktree)
	}
	// Untracked-but-not-ignored files ARE captured: a purge that leaves a file
	// behind must be visible to the oracle.
	if _, ok := s.Worktree["untracked.txt"]; !ok {
		t.Errorf("Worktree missing untracked.txt; got %v", s.Worktree)
	}
	// Gitignored files are NOT captured: build output would otherwise fail the
	// oracle for reasons unrelated to the operation under test.
	if _, ok := s.Worktree["build.out"]; ok {
		t.Errorf("Worktree captured gitignored build.out; got %v", s.Worktree)
	}
}

func TestDiff_identicalSnapshotsAreEmpty(t *testing.T) {
	dir := initRepo(t)
	if d := Snap(t, dir).Diff(Snap(t, dir)); !d.IsEmpty() {
		t.Errorf("Diff of identical snapshots = %s, want empty", d)
	}
}

func TestDiff_detectsWorktreeIndexAndRefChanges(t *testing.T) {
	dir := initRepo(t)
	before := Snap(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := before.Diff(Snap(t, dir))
	if d.IsEmpty() {
		t.Fatal("Diff did not detect a working-tree content change")
	}
	if !strings.Contains(d.String(), "a.txt") {
		t.Errorf("Diff = %s, want it to name a.txt", d)
	}
}

func TestAssertAbortedCleanly_toleratesOnlyAddedBackupRefs(t *testing.T) {
	dir := initRepo(t)
	before := Snap(t, dir)

	runGit(t, dir, "update-ref", "refs/regit/backup/main/123-0", "HEAD")
	// Must NOT fail: an aborted apply legitimately leaves its backup ref behind.
	AssertAbortedCleanly(t, before, Snap(t, dir))
}

// recorder captures assertion failures so a test can verify that a violation is
// actually reported, not merely that the happy path stays quiet.
type recorder struct{ msgs []string }

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

// TestAssertAbortedCleanly_rejectsRealMutations is the reject-direction pairing
// for TestAssertAbortedCleanly_toleratesOnlyAddedBackupRefs. That test only
// proves the assertion stays quiet on the one change it should tolerate; this
// one proves it actually fires on everything else. AssertAbortedCleanly is the
// strongest claim regit makes about a failed rewrite (the repository was left
// untouched), so a silent-pass here would be caught by nothing downstream.
// Each subtest starts from a fresh fixture and asserts on the recorder's
// captured message, not merely that it is non-empty, so a failure names the
// offending path or ref and stays diagnosable.
func TestAssertAbortedCleanly_rejectsRealMutations(t *testing.T) {
	t.Run("tracked working-tree file mutated", func(t *testing.T) {
		dir := initRepo(t)
		before := Snap(t, dir)

		writeFile(t, dir, "a.txt", "mutated\n")

		rec := &recorder{}
		AssertAbortedCleanly(rec, before, Snap(t, dir))
		if len(rec.msgs) == 0 {
			t.Fatal("AssertAbortedCleanly reported 0 failures, want the mutated a.txt reported")
		}
		if !strings.Contains(rec.msgs[0], "a.txt") {
			t.Errorf("failure message = %q, want it to name a.txt", rec.msgs[0])
		}
	})

	t.Run("current branch ref moved", func(t *testing.T) {
		dir := initRepo(t)
		before := Snap(t, dir)

		writeFile(t, dir, "a.txt", "moved\n")
		runGit(t, dir, "commit", "-q", "-am", "Moved.")

		rec := &recorder{}
		AssertAbortedCleanly(rec, before, Snap(t, dir))
		if len(rec.msgs) == 0 {
			t.Fatal("AssertAbortedCleanly reported 0 failures, want the moved branch ref reported")
		}
		if !strings.Contains(rec.msgs[0], "refs/heads/main") {
			t.Errorf("failure message = %q, want it to name refs/heads/main", rec.msgs[0])
		}
	})

	t.Run("non-backup ref added", func(t *testing.T) {
		dir := initRepo(t)
		before := Snap(t, dir)

		// An aborted apply must never record an applied ref: safety.runApply
		// calls RecordAppliedHead only after the compare-and-swap succeeds. A ref
		// under this prefix surviving an abort would mean that guard ran too
		// early, so isBackupRefAdded must not tolerate it.
		runGit(t, dir, "update-ref", "refs/regit/applied/main/1-0", "HEAD")

		rec := &recorder{}
		AssertAbortedCleanly(rec, before, Snap(t, dir))
		if len(rec.msgs) == 0 {
			t.Fatal("AssertAbortedCleanly reported 0 failures, want the applied ref reported")
		}
		if !strings.Contains(rec.msgs[0], "refs/regit/applied/main/1-0") {
			t.Errorf("failure message = %q, want it to name refs/regit/applied/main/1-0", rec.msgs[0])
		}
	})
}

// TestAssertAppliedCleanly_allowsBranchMoveAndDeclaredChanges proves the happy
// path stays quiet: a real commit that moves the branch ref and changes one
// tracked file, declared via matching Declared entries for the index and
// worktree changes, records zero failures.
func TestAssertAppliedCleanly_allowsBranchMoveAndDeclaredChanges(t *testing.T) {
	dir := initRepo(t)
	before := Snap(t, dir)

	writeFile(t, dir, "a.txt", "changed\n")
	runGit(t, dir, "commit", "-q", "-am", "Second.")

	rec := &recorder{}
	AssertAppliedCleanly(rec, before, Snap(t, dir),
		Declared{Kind: "index", Op: "changed", Path: "a.txt"},
		Declared{Kind: "worktree", Op: "changed", Path: "a.txt"},
	)
	if len(rec.msgs) != 0 {
		t.Errorf("AssertAppliedCleanly reported %d failure(s), want 0: %v", len(rec.msgs), rec.msgs)
	}
}

// TestAssertAppliedCleanly_rejectsSiblingPathSharingAPrefix is the regression
// test for the substring-matching bug (Finding 1): a declaration of
// "data.txt" must not also swallow an undeclared "data.txt.bak" merely because
// one path is a textual prefix of the other. Under the OLD substring-based
// matchesAny, this test FAILS (the undeclared sibling is silently absorbed by
// the "data.txt" declaration); under exact Kind+Op+Path matching it PASSES,
// because the sibling change is correctly reported as unexpected.
func TestAssertAppliedCleanly_rejectsSiblingPathSharingAPrefix(t *testing.T) {
	dir := initRepo(t)
	before := Snap(t, dir)

	writeFile(t, dir, "data.txt", "one\n")
	writeFile(t, dir, "data.txt.bak", "two\n")
	// Stage only the two files under test: "add -A" would also sweep up
	// initRepo's pre-existing untracked.txt fixture file, adding noise
	// unrelated to what this test demonstrates.
	runGit(t, dir, "add", "data.txt", "data.txt.bak")
	runGit(t, dir, "commit", "-q", "-m", "Third.")

	rec := &recorder{}
	AssertAppliedCleanly(rec, before, Snap(t, dir),
		Declared{Kind: "index", Op: "added", Path: "data.txt"},
		Declared{Kind: "worktree", Op: "added", Path: "data.txt"},
	)
	if len(rec.msgs) == 0 {
		t.Fatal("AssertAppliedCleanly reported 0 failures, want the undeclared data.txt.bak reported")
	}
	if !strings.Contains(rec.msgs[0], "data.txt.bak") {
		t.Errorf("failure message = %q, want it to name data.txt.bak", rec.msgs[0])
	}
}

// TestAssertUndoRestores_toleratesAccumulatedRegitRefs proves the bookkeeping
// waiver in AssertUndoRestores is exercised by a real pass/fail pair rather
// than merely inferred from AssertAppliedCleanly's coverage of the same
// isRegitRefAdded predicate.
func TestAssertUndoRestores_toleratesAccumulatedRegitRefs(t *testing.T) {
	dir := initRepo(t)
	before := Snap(t, dir)

	runGit(t, dir, "update-ref", "refs/regit/backup/main/1-0", "HEAD")
	runGit(t, dir, "update-ref", "refs/regit/applied/main/1-0", "HEAD")

	rec := &recorder{}
	AssertUndoRestores(rec, before, Snap(t, dir))
	if len(rec.msgs) != 0 {
		t.Errorf("AssertUndoRestores reported %d failure(s), want 0: %v", len(rec.msgs), rec.msgs)
	}

	// Unlike an apply, undo tolerates no branch move at all: commit again so
	// refs/heads/main advances, and confirm that IS reported.
	writeFile(t, dir, "a.txt", "moved\n")
	runGit(t, dir, "commit", "-q", "-am", "Moved.")

	rec2 := &recorder{}
	AssertUndoRestores(rec2, before, Snap(t, dir))
	if len(rec2.msgs) == 0 {
		t.Error("AssertUndoRestores reported 0 failures, want the moved branch ref reported")
	}
}

// TestSnap_detectsSymlinkRetargetToByteIdenticalContent is the regression test
// for hashing a symlink's target path (os.Readlink) instead of following it:
// "one.txt" and "two.txt" hold identical bytes, so a content-based hash of the
// link would see no change at all when the link is re-pointed from one to the
// other. Only hashing the target STRING catches the retarget.
func TestSnap_detectsSymlinkRetargetToByteIdenticalContent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	dir := initRepo(t)

	writeFile(t, dir, "one.txt", "same\n")
	writeFile(t, dir, "two.txt", "same\n")
	linkPath := filepath.Join(dir, "link")
	if err := os.Symlink("one.txt", linkPath); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "Add tracked symlink.")
	before := Snap(t, dir)

	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("two.txt", linkPath); err != nil {
		t.Fatal(err)
	}

	d := before.Diff(Snap(t, dir))
	if d.IsEmpty() {
		t.Fatal("Diff did not detect the symlink retarget to byte-identical content")
	}
	if !strings.Contains(d.String(), "link") {
		t.Errorf("Diff = %s, want it to name link", d)
	}
}

// TestSnap_handlesEmptyRepoAndDetachedHead exercises the two sentinel paths
// Snap folds into values instead of treating as errors: a repository with no
// commits yet (HeadHash stays "") and a detached HEAD (Head becomes
// "detached").
func TestSnap_handlesEmptyRepoAndDetachedHead(t *testing.T) {
	t.Run("empty repository", func(t *testing.T) {
		dir := t.TempDir()
		runGit(t, dir, "init", "-q")

		s := Snap(t, dir)
		if s.HeadHash != "" {
			t.Errorf("HeadHash = %q, want empty in a repository with no commits", s.HeadHash)
		}
	})

	t.Run("detached HEAD", func(t *testing.T) {
		dir := initRepo(t)
		runGit(t, dir, "checkout", "-q", "--detach", "HEAD")

		s := Snap(t, dir)
		if s.Head != "detached" {
			t.Errorf("Head = %q, want detached", s.Head)
		}
	})
}
