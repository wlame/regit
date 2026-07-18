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

	"github.com/wlame/regit/internal/capability"
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

// e2eRedactRepo builds the four-commit rotation fixture the redact e2e test
// exercises: c0 adds cfg.yaml carrying "token: sk-OLD"; c1 adds an unrelated
// other.txt (cfg.yaml rides along unmodified); c2 rotates ONLY the secret
// line to "token: sk-NEW" (other.txt untouched) — a rotation-only commit
// that must collapse structurally once both the introduction and its
// cascade descendant are redacted; c3 edits other.txt again (the rotated
// line rides along, untouched). Mirrors internal/capability's
// redactRotationFiles fixture exactly, adapted to e2eRepo's git conventions
// (single shared timestamp, gittest.Env) so headless behavior observed here
// can be cross-checked against that package's in-process coverage.
func e2eRedactRepo(t *testing.T) string {
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
	commits := []struct {
		subject string
		files   map[string]string
	}{
		{"c0 adds the secret", map[string]string{"cfg.yaml": "a\ntoken: sk-OLD\n"}},
		{"c1 unrelated file", map[string]string{"other.txt": "unrelated-v1\n"}},
		{"c2 rotates the secret", map[string]string{"cfg.yaml": "a\ntoken: sk-NEW\n"}},
		{"c3 unrelated edit", map[string]string{"other.txt": "unrelated-v2\n"}},
	}
	for _, c := range commits {
		for name, content := range c.files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git("add", "-A")
		git("commit", "-q", "-m", c.subject)
	}
	return dir
}

// e2eRedactFreshRepo builds a one-commit repo carrying "token: sk-XXX" for
// the --mode=replace e2e test: a clean, single-secret history keeps that
// test's assertion — the exact post-apply blob content at HEAD — unambiguous,
// independent of the rotation fixture's cascade/drop mechanics above.
func e2eRedactFreshRepo(t *testing.T) string {
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
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte("token: sk-XXX\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "add token")
	return dir
}

// TestE2E_Redact drives redact's full lifecycle through the built binary on
// the rotation fixture: a usage error (exit 1), a dry-run preview naming
// both the introduction and its cascade descendant (exit 0, HEAD unmoved), a
// dirty-tree guard block (exit 2), the real --execute --force apply (both
// the original and rotated secret values gone from history, the
// rotation-only commit dropped, the hygiene warning printed), and finally
// undo restoring the original head byte-for-byte. Mirrors
// TestE2E_purgePreviewExecuteUndo's shape for purge.
func TestE2E_Redact(t *testing.T) {
	dir := e2eRedactRepo(t)
	before := headOf(t, dir)

	// 0. Usage error: --mode=replace without --replace-pattern is rejected
	// at Parse time (Required + AppliesIf) before any repo access — exit 1.
	if out, code := runRegit(t, dir, "redact", "--match=sk-OLD", "--mode=replace"); code != 1 {
		t.Errorf("--mode=replace without --replace-pattern: exit %d, want 1\n%s", code, out)
	}

	// 1. Dry-run preview: exit 0, HEAD unmoved, and the output names BOTH
	// the introduction (c0's "token: sk-OLD") and its cascade descendant
	// (c2's rotated "token: sk-NEW"), tracked forward by LineDescendants.
	out, code := runRegit(t, dir, "redact", "--match=sk-OLD")
	if code != 0 {
		t.Fatalf("preview: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "token: sk-OLD") {
		t.Errorf("expected the preview to name the introduction:\n%s", out)
	}
	if !strings.Contains(out, "cascades to") || !strings.Contains(out, "token: sk-NEW") {
		t.Errorf("expected the preview to name the cascaded rotation:\n%s", out)
	}
	if headOf(t, dir) != before {
		t.Fatal("preview moved HEAD")
	}

	// 2. Guard block: a dirty working tree refuses --execute with exit 2,
	// before any rewrite is attempted.
	cfgPath := filepath.Join(dir, "cfg.yaml")
	origCfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code = runRegit(t, dir, "redact", "--match=sk-OLD", "--execute")
	if code != 2 {
		t.Errorf("dirty-tree execute: exit %d, want 2\n%s", code, out)
	}
	if headOf(t, dir) != before {
		t.Error("guard-blocked redact moved HEAD")
	}
	if err := os.WriteFile(cfgPath, origCfg, 0o644); err != nil {
		t.Fatal(err) // restore the working tree before the real apply below
	}

	// 3. --execute --force applies: both the original and rotated secret
	// values are gone from main's history, the rotation-only commit (c2) is
	// dropped structurally, and the hygiene warning prints.
	beforeLog := gitLines(t, dir, "log", "--oneline", "main")
	out, code = runRegit(t, dir, "redact", "--match=sk-OLD", "--execute", "--force")
	if code != 0 {
		t.Fatalf("execute: exit %d\n%s", code, out)
	}
	rewritten := headOf(t, dir)
	if rewritten == before {
		t.Fatal("--execute did not move HEAD")
	}
	if !strings.Contains(out, capability.HygieneWarning) {
		t.Errorf("expected the hygiene warning:\n%s", out)
	}
	if lines := gitLines(t, dir, "log", "--oneline", "-Ssk-OLD", "main"); len(lines) != 0 {
		t.Errorf("sk-OLD should have no history left on main, got: %v", lines)
	}
	if lines := gitLines(t, dir, "log", "--oneline", "-Ssk-NEW", "main"); len(lines) != 0 {
		t.Errorf("sk-NEW (the cascaded rotation) should have no history left on main, got: %v", lines)
	}
	afterLog := gitLines(t, dir, "log", "--oneline", "main")
	if len(afterLog) != len(beforeLog)-1 {
		t.Errorf("main has %d commit(s) after redact, want %d (rotation-only commit dropped)",
			len(afterLog), len(beforeLog)-1)
	}
	for _, ln := range afterLog {
		if strings.Contains(ln, "c2 rotates the secret") {
			t.Errorf("the rotation-only commit should be gone from the log:\n%v", afterLog)
		}
	}

	// 4. undo restores the original head byte-for-byte.
	out, code = runRegit(t, dir, "undo")
	if code != 0 {
		t.Fatalf("undo: exit %d\n%s", code, out)
	}
	if headOf(t, dir) != before {
		t.Errorf("undo did not restore the original head")
	}
}

// TestE2E_Redact_ReplaceMode exercises --mode=replace on a fresh,
// single-secret fixture: the matched substring within the target line is
// rewritten in place (keeping the rest of the line), applied straight
// through --execute --force.
func TestE2E_Redact_ReplaceMode(t *testing.T) {
	dir := e2eRedactFreshRepo(t)

	out, code := runRegit(t, dir,
		"redact", "--match=sk-XXX", "--mode=replace",
		"--replace-pattern=sk-\\w+", "--replace-with=READONLY", "--execute", "--force")
	if code != 0 {
		t.Fatalf("replace execute: exit %d\n%s", code, out)
	}

	got := gitLines(t, dir, "show", "HEAD:cfg.yaml")
	if len(got) != 1 || got[0] != "token: READONLY" {
		t.Errorf("HEAD:cfg.yaml = %v, want [%q]", got, "token: READONLY")
	}
}

// TestE2E_GitReIntegration exercises the full chain with the built binary:
// regit install links git-re on a controlled PATH, git's external-command
// protocol routes `git re` to it, help renders the git re form (via -h — git
// swallows a bare --help for external commands, per-verb --help passes
// through), and `git re uninstall` removes the link again.
func TestE2E_GitReIntegration(t *testing.T) {
	bin := regitBin(t)
	pathDir := t.TempDir()
	if err := os.Symlink(bin, filepath.Join(pathDir, "regit")); err != nil {
		t.Fatal(err)
	}
	// Prepend the temp dir so its regit/git-re win every PATH lookup while
	// git and go stay reachable.
	env := append(os.Environ(), "PATH="+pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	runCmd := func(name string, args ...string) (string, error) {
		cmd := exec.Command(name, args...)
		cmd.Dir = t.TempDir() // not a git repo — install/uninstall must not care
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := runCmd(filepath.Join(pathDir, "regit"), "install")
	if err != nil {
		t.Fatalf("regit install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Installed:") {
		t.Fatalf("install output: %q", out)
	}

	out, err = runCmd("git", "re", "-h")
	if err != nil {
		t.Fatalf("git re -h: %v\n%s", err, out)
	}
	if !strings.Contains(out, "git re <command> [flags]") {
		t.Fatalf("git re -h should teach the git re form:\n%s", out)
	}

	out, err = runCmd("git", "re", "undo", "--help")
	if err != nil {
		t.Fatalf("git re undo --help: %v\n%s", err, out)
	}
	if !strings.Contains(out, "git re undo") {
		t.Fatalf("per-verb help should render the git re form:\n%s", out)
	}

	out, err = runCmd("git", "re", "install")
	if err != nil {
		t.Fatalf("git re install (idempotent): %v\n%s", err, out)
	}
	if !strings.Contains(out, "Already installed") {
		t.Fatalf("second install output: %q", out)
	}

	out, err = runCmd("git", "re", "uninstall")
	if err != nil {
		t.Fatalf("git re uninstall: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Removed") {
		t.Fatalf("uninstall output: %q", out)
	}
	if _, err := os.Lstat(filepath.Join(pathDir, "git-re")); !os.IsNotExist(err) {
		t.Fatalf("git-re still on PATH after uninstall: %v", err)
	}
}
