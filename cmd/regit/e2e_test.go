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
