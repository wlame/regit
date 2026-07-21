package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo builds a two-commit fixture with one tracked file, one gitignored
// file, and one untracked file, so every Snapshot component has something to
// capture and the ignore rule has something to exclude.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = Env(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("symbolic-ref", "HEAD", "refs/heads/main")
	write(".gitignore", "build.out\n")
	write("a.txt", "alpha\n")
	run("add", "-A")
	run("commit", "-q", "-m", "First.")
	write("build.out", "ignored noise\n")
	write("untracked.txt", "visible\n")
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

	cmd := exec.Command("git", "update-ref", "refs/regit/backup/main/123-0", "HEAD")
	cmd.Dir, cmd.Env = dir, Env(dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v\n%s", err, out)
	}
	// Must NOT fail: an aborted apply legitimately leaves its backup ref behind.
	AssertAbortedCleanly(t, before, Snap(t, dir))
}
