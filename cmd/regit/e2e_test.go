package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wlame/regit/internal/gittest"
)

// The e2e tests exercise the BUILT binary end to end — exit codes, the
// preview/--execute gradient, undo, and deprecated-flag translation — the
// wiring in main/dispatch that in-process tests cannot reach.

var (
	e2eBuildOnce sync.Once
	e2eBin       string
	e2eBuildErr  error
)

// regitBin builds the regit binary once per test process and returns its path.
func regitBin(t *testing.T) string {
	t.Helper()
	e2eBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "regit-e2e-*")
		if err != nil {
			e2eBuildErr = err
			return
		}
		e2eBin = filepath.Join(dir, "regit")
		cmd := exec.Command("go", "build", "-o", e2eBin, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			e2eBuildErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if e2eBuildErr != nil {
		t.Fatal(e2eBuildErr)
	}
	return e2eBin
}

// e2eRepo builds a throwaway repo with three commits sharing one timestamp.
func e2eRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir,
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "commit.gpgsign", "false")
	for _, n := range []string{"one", "two", "three"} {
		if err := os.WriteFile(filepath.Join(dir, n+".txt"), []byte(n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-q", "-m", "commit "+n)
	}
	return dir
}

// e2eRepoStaggered builds a three-commit repo like e2eRepo, but spread an
// hour apart across the same day instead of sharing one exact timestamp.
// TestE2E_RedateCommitsSubset needs a middle commit whose segment window has
// real width: with e2eRepo's identical timestamps, redating only the middle
// commit pins both the segment's floor (the root anchor's date) and ceil
// (the untargeted newest commit's date) to that same instant, a zero-width
// window no placement can land inside without violating monotonic ordering.
func e2eRepoStaggered(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(when string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir, "GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("2026-06-05T09:00:00+00:00", "init", "-q")
	git("2026-06-05T09:00:00+00:00", "symbolic-ref", "HEAD", "refs/heads/main")
	git("2026-06-05T09:00:00+00:00", "config", "commit.gpgsign", "false")
	commits := []struct{ name, when string }{
		{"one", "2026-06-05T09:00:00+00:00"},
		{"two", "2026-06-05T12:00:00+00:00"},
		{"three", "2026-06-05T15:00:00+00:00"},
	}
	for _, c := range commits {
		path := filepath.Join(dir, c.name+".txt")
		if err := os.WriteFile(path, []byte(c.name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(c.when, "add", "-A")
		git(c.when, "commit", "-q", "-m", "commit "+c.name)
	}
	return dir
}

// runRegit executes the built binary in dir with a hermetic environment
// (private HOME/XDG so the developer's git and regit config never leak in)
// and returns combined output plus the exit code.
func runRegit(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(regitBin(t), args...)
	cmd.Dir = dir
	cmd.Env = gittest.Env(dir, "XDG_CONFIG_HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(out), exitErr.ExitCode()
	}
	t.Fatalf("regit %s: %v\n%s", strings.Join(args, " "), err, out)
	return "", -1
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	cmd.Env = gittest.Env(dir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// gitLines runs a read-only git command in dir and returns its stdout split
// into trimmed, non-empty lines — e.g. "rev-list HEAD" yields one hash per
// line, newest-first.
func gitLines(t *testing.T, dir string, args ...string) []string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gittest.Env(dir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ln != "" {
			lines = append(lines, ln)
		}
	}
	return lines
}

// TestE2E_exitCodeContract exercises the documented exit codes through the
// real binary: 0 for help/dry-run, 1 for usage errors, 2 for guard blocks.
func TestE2E_exitCodeContract(t *testing.T) {
	dir := e2eRepo(t)

	if out, code := runRegit(t, dir, "--help"); code != 0 {
		t.Errorf("--help: exit %d\n%s", code, out)
	}
	if out, code := runRegit(t, dir, "redate"); code != 1 {
		t.Errorf("missing required --strategy: exit %d, want 1\n%s", code, out)
	}
	if out, code := runRegit(t, dir, "redate", "--strategy=Nope"); code != 1 {
		t.Errorf("bad enum: exit %d, want 1\n%s", code, out)
	}
	if out, code := runRegit(t, dir, "redate", "HEAD", "--strategy=Even-Spread"); code != 1 {
		t.Errorf("stray positional: exit %d, want 1\n%s", code, out)
	}

	// Guard block: dirty tree refuses an --execute with exit 2.
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runRegit(t, dir, "redate",
		"--strategy=Even-Spread", "--start=2026-01-05", "--end=2026-02-05", "--execute")
	if code != 2 {
		t.Errorf("dirty-tree execute: exit %d, want 2\n%s", code, out)
	}
}

// TestE2E_previewExecuteUndo drives the core lifecycle through the binary: a
// dry-run changes nothing, --execute rewrites, undo restores.
func TestE2E_previewExecuteUndo(t *testing.T) {
	dir := e2eRepo(t)
	before := headOf(t, dir)
	spread := []string{"redate", "--strategy=Even-Spread", "--start=2026-01-05", "--end=2026-02-05"}

	out, code := runRegit(t, dir, spread...)
	if code != 0 {
		t.Fatalf("dry-run: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Date plan") {
		t.Errorf("dry-run should print the plan:\n%s", out)
	}
	if headOf(t, dir) != before {
		t.Fatal("dry-run moved HEAD")
	}

	out, code = runRegit(t, dir, append(spread, "--execute")...)
	if code != 0 {
		t.Fatalf("execute: exit %d\n%s", code, out)
	}
	rewritten := headOf(t, dir)
	if rewritten == before {
		t.Fatal("--execute did not move HEAD")
	}
	if !strings.Contains(out, "verified") {
		t.Errorf("execute should print the verification summary:\n%s", out)
	}

	out, code = runRegit(t, dir, "undo")
	if code != 0 {
		t.Fatalf("undo: exit %d\n%s", code, out)
	}
	if headOf(t, dir) != before {
		t.Errorf("undo did not restore the original head")
	}
}

// TestE2E_RedateCommitsSubset redates only the middle commit of three and
// verifies the segment engine's anchor guarantee end to end: the older
// (root) anchor's hash stays byte-identical, while the newest commit's hash
// necessarily changes too, since its parent link moved even though it was
// never itself a redate target. rev-list is newest-first, so index 0 is the
// newest commit, index 1 the middle (the target), index 2 the root anchor.
func TestE2E_RedateCommitsSubset(t *testing.T) {
	// e2eRepoStaggered, not e2eRepo: the middle commit's segment window is
	// bounded by its two neighbors (root below, the untargeted newest commit
	// above), so it needs those neighbors on distinct timestamps to have any
	// width at all — see the helper's doc comment.
	dir := e2eRepoStaggered(t) // three commits, 09:00/12:00/15:00 UTC on 2026-06-05
	hashesBefore := gitLines(t, dir, "rev-list", "HEAD")
	if len(hashesBefore) != 3 {
		t.Fatalf("setup: want 3 commits, got %d", len(hashesBefore))
	}
	mid := hashesBefore[1]

	out, code := runRegit(t, dir,
		"redate", "--strategy=Compress", "--compress-day=2026-06-05",
		"--commits="+mid, "--execute")
	if code != 0 {
		t.Fatalf("redate exit %d:\n%s", code, out)
	}

	hashesAfter := gitLines(t, dir, "rev-list", "HEAD")
	if len(hashesAfter) != 3 {
		t.Fatalf("want 3 commits after redate, got %d", len(hashesAfter))
	}
	if hashesAfter[2] != hashesBefore[2] {
		t.Errorf("root anchor rewritten: %s -> %s", hashesBefore[2], hashesAfter[2])
	}
	if hashesAfter[0] == hashesBefore[0] {
		t.Error("newest commit hash unchanged, but its parent (the redated middle commit) moved")
	}

	authorDate := gitLines(t, dir,
		"log", "-1", "--date=format:%Y-%m-%d", "--format=%ad", hashesAfter[1])
	if len(authorDate) != 1 || authorDate[0] != "2026-06-05" {
		t.Errorf("redated middle commit author date = %v, want [2026-06-05]", authorDate)
	}
}

// TestE2E_deprecatedFlagTranslation verifies the legacy --yes spelling is
// translated (with a notice) by the real dispatch path.
func TestE2E_deprecatedFlagTranslation(t *testing.T) {
	dir := e2eRepo(t)
	// The fixture's HEAD is a single-hunk commit, so split stops at the
	// skip-split verdict (exit 0, no LLM needed) — what matters here is that
	// the legacy --yes spelling was accepted and the notice printed.
	out, code := runRegit(t, dir, "split", "HEAD", "--yes")
	if code != 0 {
		t.Fatalf("skip-split verdict should exit 0, got %d\n%s", code, out)
	}
	if !strings.Contains(out, "deprecated") {
		t.Errorf("expected a --yes deprecation notice:\n%s", out)
	}
	if !strings.Contains(out, "Not splitting") {
		t.Errorf("expected the skip-split verdict:\n%s", out)
	}
}

// TestE2E_repoFlag verifies --repo targets a repository other than the cwd.
func TestE2E_repoFlag(t *testing.T) {
	dir := e2eRepo(t)
	elsewhere := t.TempDir()
	out, code := runRegit(t, elsewhere, "undo", "--list", "--repo="+dir)
	if code != 0 {
		t.Fatalf("--repo from another cwd: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "no backups") {
		t.Errorf("expected the empty backup list:\n%s", out)
	}
}

// e2ePurgeRepo builds a three-commit repo for the purge e2e test: c0 adds
// keep.txt, c1 adds junk.log alone (purge must drop that commit entirely),
// c2 touches both keep.txt and junk.log (purge must rewrite, not drop, it).
// Modeled on capability.purgeFixtureFiles, adapted to e2eRepo's git
// conventions (single shared timestamp, gittest.Env).
func e2ePurgeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir,
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	git("config", "commit.gpgsign", "false")
	commitFiles := []map[string]string{
		{"keep.txt": "v1"},
		{"junk.log": "noise"},
		{"keep.txt": "v2", "junk.log": "more"},
	}
	for i, files := range commitFiles {
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git("add", "-A")
		git("commit", "-q", "-m", fmt.Sprintf("commit %d", i))
	}
	return dir
}

// TestE2E_purgePreviewExecuteUndo drives purge's full lifecycle through the
// built binary: --search discovery, dry-run preview, --execute removal, and
// undo — mirroring TestE2E_previewExecuteUndo's redate lifecycle for purge.
func TestE2E_purgePreviewExecuteUndo(t *testing.T) {
	dir := e2ePurgeRepo(t)
	before := headOf(t, dir)

	// 1. --search finds junk.log among history paths, read-only.
	out, code := runRegit(t, dir, "purge", "--search=junk")
	if code != 0 {
		t.Fatalf("search: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "junk.log") {
		t.Errorf("expected --search to list junk.log:\n%s", out)
	}

	// 2. Dry-run preview: the junk-only commit is marked dropped, the
	// dry-run marker prints, and HEAD does not move.
	out, code = runRegit(t, dir, "purge", "junk.log")
	if code != 0 {
		t.Fatalf("dry-run: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "will be dropped") {
		t.Errorf("expected a drop marker for the junk-only commit:\n%s", out)
	}
	if !strings.Contains(out, "dry run") {
		t.Errorf("expected the dry-run marker:\n%s", out)
	}
	if headOf(t, dir) != before {
		t.Fatal("dry-run moved HEAD")
	}

	// 3. --execute rewrites branch history so junk.log's history on main is
	// gone. The backup ref intentionally still reaches the old commits, so
	// the absence proof must be branch-scoped ("log main"), never --all.
	// The working tree is untouched: junk.log stays on disk, now untracked.
	out, code = runRegit(t, dir, "purge", "junk.log", "--execute")
	if code != 0 {
		t.Fatalf("execute: exit %d\n%s", code, out)
	}
	rewritten := headOf(t, dir)
	if rewritten == before {
		t.Fatal("--execute did not move HEAD")
	}
	if lines := gitLines(t, dir, "log", "main", "--", "junk.log"); len(lines) != 0 {
		t.Errorf("junk.log should have no history left on main, got: %v", lines)
	}
	if _, err := os.Stat(filepath.Join(dir, "junk.log")); err != nil {
		t.Errorf("junk.log should still exist on disk (working tree untouched): %v", err)
	}

	// 4. undo restores the pre-purge head. undo takes no --execute — it acts
	// immediately (the registry's undo entry lists "regit undo --execute" as
	// a Bad example: undo is exempt from dry-run).
	out, code = runRegit(t, dir, "undo")
	if code != 0 {
		t.Fatalf("undo: exit %d\n%s", code, out)
	}
	if headOf(t, dir) != before {
		t.Errorf("undo did not restore the original head")
	}
}
