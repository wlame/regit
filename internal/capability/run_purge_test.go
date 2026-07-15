package capability

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
)

// newPurgeRepo builds a throwaway repo with one commit per entry in
// commitFiles (each a name -> content map written onto the working tree, so
// a later commit can leave an earlier file untouched or overwrite it).
// Modeled on safety/purge_test.go's newPurgeRepo, adapted to this package's
// testsupport_test.go fixture conventions (gittest.Env, one t.TempDir per repo).
func newPurgeRepo(t *testing.T, commitFiles []map[string]string) (*gitio.Repo, string) {
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
	run("init", "-q", "-b", "main")
	for i, files := range commitFiles {
		for name, content := range files {
			full := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run("add", ".")
		run("commit", "-q", "-m", "commit "+strconv.Itoa(i))
	}
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return repo, dir
}

// purgeFixtureFiles is the shared 4-commit history used across most purge
// tests: c0 adds keep.txt; c1 adds junk.log alone (purging must DROP it
// entirely); c2 adds a nested config/secrets.env (lets the unknown-path test
// exercise the "did you mean" suggestion via a missing-directory typo); c3
// touches both keep.txt and junk.log (purging must REWRITE, not drop, it).
func purgeFixtureFiles() []map[string]string {
	return []map[string]string{
		{"keep.txt": "v1"},
		{"junk.log": "noise"},
		{"config/secrets.env": "SECRET=1"},
		{"keep.txt": "v2", "junk.log": "more"},
	}
}

// newPurgeRepoWithPush is newPurgeRepo plus a bare origin: the first
// pushedCount commits are pushed to it in a single push, leaving the rest
// local-only. Modeled on run_squash_test.go's squashTestRepoWithPush, adapted
// to newPurgeRepo's per-commit file-map fixture shape.
func newPurgeRepoWithPush(t *testing.T, commitFiles []map[string]string, pushedCount int) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	origin := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	run("init", "-q", "-b", "main")
	run("remote", "add", "origin", origin)
	for i, files := range commitFiles {
		for name, content := range files {
			full := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run("add", ".")
		run("commit", "-q", "-m", "commit "+strconv.Itoa(i))
		if i+1 == pushedCount {
			run("push", "-q", "-u", "origin", "main")
		}
	}
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return repo
}

func newPurgeCtx(out *bytes.Buffer, repo *gitio.Repo) *RunContext {
	return &RunContext{Repo: repo, Cfg: config.Default(), Stdout: out, Stderr: out, Now: time.Unix(1000, 0)}
}

func TestRunPurge_searchListsMatches(t *testing.T) {
	repo, _ := newPurgeRepo(t, purgeFixtureFiles())
	var out bytes.Buffer
	ctx := newPurgeCtx(&out, repo)
	before, _ := repo.ReadState()

	a, err := Parse(mustLookup(t, "purge"), []string{"--search=junk"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := runPurge(ctx, a); err != nil {
		t.Fatalf("runPurge: %v", err)
	}

	if !strings.Contains(out.String(), "junk.log") {
		t.Errorf("expected search output to list junk.log:\n%s", out.String())
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("--search must not change the repo")
	}
}

func TestRunPurge_dryRunPrintsPreviewAndChangesNothing(t *testing.T) {
	repo, _ := newPurgeRepo(t, purgeFixtureFiles())
	var out bytes.Buffer
	ctx := newPurgeCtx(&out, repo)
	before, _ := repo.ReadState()

	a, err := Parse(mustLookup(t, "purge"), []string{"junk.log"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := runPurge(ctx, a); err != nil {
		t.Fatalf("runPurge: %v", err)
	}

	s := out.String()
	if !strings.Contains(s, "junk.log") {
		t.Errorf("expected the preview to mention junk.log:\n%s", s)
	}
	if !strings.Contains(s, "will be dropped") {
		t.Errorf("expected a drop marker for the junk-only commit:\n%s", s)
	}
	if !strings.Contains(s, "dry run") {
		t.Errorf("expected the dry-run marker:\n%s", s)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("dry run must not move the branch head")
	}
}

func TestRunPurge_executeRemovesPath(t *testing.T) {
	repo, dir := newPurgeRepo(t, purgeFixtureFiles())
	var out bytes.Buffer
	ctx := newPurgeCtx(&out, repo)

	a, err := Parse(mustLookup(t, "purge"), []string{"junk.log", "--execute"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := runPurge(ctx, a); err != nil {
		t.Fatalf("runPurge execute: %v", err)
	}

	cmd := exec.Command("git", "log", "main", "--", "junk.log")
	cmd.Dir = dir
	cmd.Env = gittest.Env(dir)
	logOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v\n%s", err, logOut)
	}
	if len(bytes.TrimSpace(logOut)) != 0 {
		t.Errorf("junk.log should have no history left on main, got:\n%s", logOut)
	}

	s := out.String()
	if !strings.Contains(s, "backup") {
		t.Errorf("expected a backup mention:\n%s", s)
	}
	if !strings.Contains(s, "git gc") {
		t.Errorf("expected the gc hint line:\n%s", s)
	}
}

// TestRunPurge_pushedAffectedCommitRequiresForce: when the purge's rewritten
// suffix (commits[firstAffected:]) includes a commit that is already pushed,
// --execute must be guard-blocked (exit code 2) without --force — the same
// contract run_split.go and run_squash.go already enforce for their own
// rewrite ranges. Before this fix runPurge skipped the pushed pre-check
// entirely and fell through to safety.ApplyPurge's own internal guard, whose
// plain error maps to exit 3 instead of the documented exit 2.
func TestRunPurge_pushedAffectedCommitRequiresForce(t *testing.T) {
	// commit 0 adds keep.txt, commit 1 adds junk.log alone (the first commit
	// purging "junk.log" affects) — push after both, so the rewritten suffix
	// starts at an already-pushed commit.
	repo := newPurgeRepoWithPush(t, purgeFixtureFiles(), 2)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if !commits[1].IsPushed {
		t.Fatal("setup: commit 1 (adds junk.log) should be pushed")
	}

	var out bytes.Buffer
	ctx := newPurgeCtx(&out, repo)
	before, _ := repo.ReadState()

	a, err := Parse(mustLookup(t, "purge"), []string{"junk.log", "--execute"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = runPurge(ctx, a)
	if got := ExitCode(err); got != 2 {
		t.Fatalf("pushed-affected purge without --force exit = %d, want 2 (err=%v)", got, err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("guard-blocked purge must not move HEAD")
	}
}

// TestRunPurge_pushedAffectedCommitWithForceSucceeds is the companion to
// TestRunPurge_pushedAffectedCommitRequiresForce: the identical pushed-history
// setup, but with --force, must go through and actually drop junk.log.
func TestRunPurge_pushedAffectedCommitWithForceSucceeds(t *testing.T) {
	repo := newPurgeRepoWithPush(t, purgeFixtureFiles(), 2)
	var out bytes.Buffer
	ctx := newPurgeCtx(&out, repo)

	a, err := Parse(mustLookup(t, "purge"), []string{"junk.log", "--execute", "--force"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := runPurge(ctx, a); err != nil {
		t.Fatalf("forced purge of pushed history should succeed: %v", err)
	}

	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range commits {
		files, err := repo.CommitTouchedFiles(c.Hash)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if f == "junk.log" {
				t.Errorf("junk.log should still be purged from commit %s after --force", Short(c.Hash))
			}
		}
	}
}

func TestRunPurge_unknownPathIsUsageError(t *testing.T) {
	repo, _ := newPurgeRepo(t, purgeFixtureFiles())
	var out bytes.Buffer
	ctx := newPurgeCtx(&out, repo)

	// "secrets.env" is a plausible typo for "config/secrets.env" (missing the
	// directory prefix); the substring match in purgeSuggestions should surface it.
	a, err := Parse(mustLookup(t, "purge"), []string{"secrets.env"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = runPurge(ctx, a)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("expected a usage error (exit 1), got %v", err)
	}
	if !strings.Contains(err.Error(), "config/secrets.env") {
		t.Errorf("expected the error to suggest config/secrets.env, got: %v", err)
	}
}

func TestRunPurge_noArgsIsUsageError(t *testing.T) {
	repo, _ := newPurgeRepo(t, purgeFixtureFiles())
	var out bytes.Buffer
	ctx := newPurgeCtx(&out, repo)

	a, err := Parse(mustLookup(t, "purge"), nil)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = runPurge(ctx, a)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("expected a usage error (exit 1), got %v", err)
	}
	if !strings.Contains(err.Error(), "--search") {
		t.Errorf("expected the error to mention --search, got: %v", err)
	}
}

// newPurgeRepoWithMergeInPrefix builds the fixture CRITICAL finding 1
// describes (mirroring internal/gitio/purge_test.go's purgeMergeInPrefixRepo
// and internal/safety/purge_test.go's purgeMergeInPrefixRepo): a side branch
// adds junk.log, merges into main untouched further (git simplifies the
// merge out of `rev-list HEAD -- junk.log`), then a later main commit edits
// junk.log directly. newPurgeRepo's per-commit file-map fixtures can't
// express a real branch/merge, so this is built directly.
func newPurgeRepoWithMergeInPrefix(t *testing.T) *gitio.Repo {
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
	run("init", "-q", "-b", "main")
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
		t.Fatalf("open: %v", err)
	}
	return repo
}

// TestRunPurge_mergeInPrefixExitsGuardBlocked is the end-to-end (headless
// CLI) regression test for both CRITICAL finding 1 and IMPORTANT finding 4:
// before either fix, this exact scenario would have gone through
// safety.ApplyPurge's OWN internal merge-in-suffix guard (once the base was
// eventually corrected) and come back as a plain, sentinel-less error that
// applyGuardErr mapped to exit 3 (runtime error) — contradicting the
// registry's documented exit code 2 for a guard block. The new pre-check in
// runPurge, using the same gitio.PurgeBase-corrected base, must catch it
// first and report exit 2, with the branch left untouched.
func TestRunPurge_mergeInPrefixExitsGuardBlocked(t *testing.T) {
	repo := newPurgeRepoWithMergeInPrefix(t)
	var out bytes.Buffer
	ctx := newPurgeCtx(&out, repo)
	before, _ := repo.ReadState()

	a, err := Parse(mustLookup(t, "purge"), []string{"junk.log", "--execute"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = runPurge(ctx, a)
	if got := ExitCode(err); got != 2 {
		t.Fatalf("merge-in-prefix purge exit = %d, want 2 (err=%v)", got, err)
	}
	if !strings.Contains(err.Error(), "merge") {
		t.Errorf("error should mention merge, got: %v", err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("guard-blocked purge must not move HEAD")
	}
}

// TestRunPurge_wouldEmptyBranchExitsGuardBlocked is IMPORTANT finding 4's
// second case: purging the only file in a single-commit repo empties the
// whole branch. Before the fix this fell through to safety.ApplyPurge's
// Rewrite step, whose "purge would remove every commit" error carries no
// sentinel and so mapped to exit 3 via applyGuardErr; the new pre-check
// (reusing BuildPurgePreview's Dropped map) must catch it as a guard block
// (exit 2) instead.
func TestRunPurge_wouldEmptyBranchExitsGuardBlocked(t *testing.T) {
	repo, _ := newPurgeRepo(t, []map[string]string{{"junk.log": "noise"}})
	var out bytes.Buffer
	ctx := newPurgeCtx(&out, repo)
	before, _ := repo.ReadState()

	a, err := Parse(mustLookup(t, "purge"), []string{"junk.log", "--execute"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = runPurge(ctx, a)
	if got := ExitCode(err); got != 2 {
		t.Fatalf("would-empty-branch purge exit = %d, want 2 (err=%v)", got, err)
	}
	if !strings.Contains(err.Error(), "every commit") {
		t.Errorf("error should mention removing every commit, got: %v", err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("guard-blocked purge must not move HEAD")
	}
}

// TestRunPurge_searchSanitizesEscapeSequences is the IMPORTANT finding 2
// regression test: a history path carrying a raw terminal escape sequence
// (here an OSC "set window title" payload terminated by BEL) must never reach
// --search output verbatim. termsafe.Line strips the WHOLE OSC sequence
// (introducer, payload, and terminator alike — see termsafe_test.go's own
// "OSC window-title spoof removed" case), so "PWNED" must be gone entirely
// while the legitimate text on either side of it survives.
func TestRunPurge_searchSanitizesEscapeSequences(t *testing.T) {
	evil := "evil\x1b]0;PWNED\x07.txt"
	repo, _ := newPurgeRepo(t, []map[string]string{{evil: "x"}})
	var out bytes.Buffer
	ctx := newPurgeCtx(&out, repo)

	a, err := Parse(mustLookup(t, "purge"), []string{"--search=evil"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := runPurge(ctx, a); err != nil {
		t.Fatalf("runPurge: %v", err)
	}
	s := out.String()
	if strings.ContainsAny(s, "\x1b\x07") || strings.Contains(s, "PWNED") {
		t.Fatalf("search output leaked the raw OSC payload: %q", s)
	}
	if !strings.Contains(s, "evil.txt") {
		t.Errorf("sanitization should keep the legitimate text around the escape sequence, got: %q", s)
	}
}

// TestPrintPurgePreview_sanitizesCommitSubject is the companion IMPORTANT
// finding 2 test for the OTHER capability-layer site: a commit subject
// carrying a raw escape sequence must not reach the dry-run preview verbatim,
// and (per termsafe's OSC handling) the injected payload itself must be gone,
// not merely its control bytes.
func TestPrintPurgePreview_sanitizesCommitSubject(t *testing.T) {
	var buf bytes.Buffer
	pv := PurgePreview{
		Paths:    []string{"junk.log"},
		Affected: []model.Commit{{Hash: "aaaa1111", Subject: "evil\x1b]0;PWNED\x07 subject"}},
		Dropped:  map[string]bool{"aaaa1111": true},
	}
	printPurgePreview(&buf, pv)
	s := buf.String()
	if strings.ContainsAny(s, "\x1b\x07") || strings.Contains(s, "PWNED") {
		t.Fatalf("preview leaked the raw OSC payload: %q", s)
	}
	if !strings.Contains(s, "evil subject") {
		t.Errorf("sanitization should keep the legitimate text around the escape sequence, got: %q", s)
	}
}

func TestBuildPurgePreview_marksDropsWithoutEnumeratingDirs(t *testing.T) {
	repo, _ := newPurgeRepo(t, []map[string]string{
		{"keep.txt": "v1"},
		{"build/a.log": "x", "build/b.log": "y"},
		{"keep.txt": "v2"},
	})
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	spec := model.PurgeSpec{Paths: []string{"build"}}
	affected, err := repo.AffectedCommits(spec.Paths)
	if err != nil {
		t.Fatal(err)
	}

	pv, err := BuildPurgePreview(repo, commits, spec, affected)
	if err != nil {
		t.Fatal(err)
	}

	if len(pv.Paths) != 1 || pv.Paths[0] != "build" {
		t.Errorf("preview paths = %v, want [build] (the directory only)", pv.Paths)
	}
	dirCommit := commits[1] // the build-only commit
	if !pv.Dropped[dirCommit.Hash] {
		t.Errorf("expected the dir-only commit %s to be marked dropped", Short(dirCommit.Hash))
	}
	if len(pv.Dropped) != 1 {
		t.Errorf("expected exactly one dropped commit, got %d", len(pv.Dropped))
	}
	rendered := fmt.Sprintf("%+v", pv)
	if strings.Contains(rendered, "build/a.log") || strings.Contains(rendered, "build/b.log") {
		t.Errorf("preview must never enumerate directory member files, got: %s", rendered)
	}
}
