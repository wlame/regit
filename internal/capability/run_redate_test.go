package capability

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
)

func TestRedateEvenSpreadDryRun(t *testing.T) {
	_, repo := newTestRepo(t, 4)
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1_700_000_000, 0)}
	a, err := Parse(mustLookup(t, "redate"), []string{
		"--strategy=Even-Spread", "--time=Working-hours",
		"--start=2026-01-01", "--end=2026-03-01",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	before, _ := repo.ReadState()
	if err := runRedate(ctx, a); err != nil {
		t.Fatalf("runRedate: %v", err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("dry-run redate moved HEAD")
	}
	if !bytes.Contains(out.Bytes(), []byte("->")) {
		t.Errorf("expected a date plan:\n%s", out.String())
	}
}

func TestRedateManualPinRequiresPin(t *testing.T) {
	if _, err := Parse(mustLookup(t, "redate"), []string{"--strategy=Manual-Pin"}); err == nil {
		t.Error("Manual-Pin without --pin should error")
	}
}

// TestRedateParams_jitterZeroCannotOverrideConfig documents that --jitter-min=0 is
// treated as absent and cannot zero a configured jitter (a.Int 0 == unset; a zero
// jitter is a no-op anyway).
func TestRedateParams_jitterZeroDisablesJitter(t *testing.T) {
	cfg := config.Default()
	cfg.JitterMinutes = 15
	ctx := &RunContext{Cfg: cfg, Now: time.Unix(1000, 0)}
	// An explicit --jitter-min=0 disables jitter, overriding the config default.
	p, err := redateParams(ctx, newParsedArgs(map[string]any{"jitter-min": 0}, nil))
	if err != nil {
		t.Fatalf("redateParams: %v", err)
	}
	if p.JitterMin != 0 {
		t.Errorf("JitterMin = %d, want 0 (--jitter-min=0 disables jitter)", p.JitterMin)
	}
	// Omitting --jitter-min still falls back to the config default.
	p2, err := redateParams(ctx, newParsedArgs(map[string]any{}, nil))
	if err != nil {
		t.Fatalf("redateParams: %v", err)
	}
	if p2.JitterMin != 15 {
		t.Errorf("omitted jitter-min: JitterMin = %d, want config default 15", p2.JitterMin)
	}
}

// TestRedateParams_invalidLunchClockIsSilentlyDisabled documents that an invalid
// lunch clock is swallowed (the gap is disabled), unlike workday-start/end which
// propagate the parse error.
func TestRedateParams_invalidLunchClockIsSilentlyDisabled(t *testing.T) {
	ctx := &RunContext{Cfg: config.Default(), Now: time.Unix(1000, 0)}
	p, err := redateParams(ctx, newParsedArgs(map[string]any{"lunch-start": "99:99"}, nil))
	if err != nil {
		t.Fatalf("redateParams should not error on a bad lunch clock: %v", err)
	}
	if p.LunchStart != 0 {
		t.Errorf("LunchStart = %d, want 0 (invalid lunch clock should disable the gap)", p.LunchStart)
	}
}

func TestRedateExecuteEmitsVerificationLine(t *testing.T) {
	_, repo := newTestRepo(t, 4)
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1_700_000_000, 0)}
	a, err := Parse(mustLookup(t, "redate"), []string{
		"--strategy=Even-Spread", "--time=Working-hours",
		"--start=2026-01-01", "--end=2026-03-01", "--execute",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := runRedate(ctx, a); err != nil {
		t.Fatalf("runRedate: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("verified")) || !bytes.Contains(out.Bytes(), []byte("0 paths changed")) {
		t.Errorf("expected a verification line in output:\n%s", out.String())
	}
}

// redateTestRepoWithMerge builds a throwaway repo whose main-branch history
// (the line ReadCommits walks via --first-parent) contains a real merge
// commit: a -> b on main, with "feature" diverging from a and merged back into
// main with --no-ff so the merge keeps both parents. ReadCommits then returns
// [a, b, merge] with merge.IsMerge == true (2 parents), giving runRedate's
// merge gate a genuine merge commit inside plan.Active() to trip on.
func redateTestRepoWithMerge(t *testing.T) (string, *gitio.Repo) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(name string) {
		f := filepath.Join(dir, name+".txt")
		if err := os.WriteFile(f, []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-q", "-m", "commit "+name)
	}
	run("init", "-q", "-b", "main")
	run("config", "commit.gpgsign", "false")
	commit("a")
	run("branch", "feature")
	commit("b") // main: a -> b
	run("switch", "feature")
	commit("c") // feature: a -> c
	run("switch", "main")
	run("merge", "--no-ff", "--no-edit", "feature") // main: a -> b -> merge(parents b, c)

	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return dir, repo
}

// parseRedate parses redate flags through the real registry (Lookup + Parse),
// exactly like every other redate test, so the test never risks drifting from
// how the CLI itself parses --strategy/--execute/etc.
func parseRedate(t *testing.T, args ...string) ParsedArgs {
	t.Helper()
	a, err := Parse(mustLookup(t, "redate"), args)
	if err != nil {
		t.Fatalf("parse redate args %v: %v", args, err)
	}
	return a
}

// TestRunRedate_refusesMergeWithoutAllowMerges proves the merge gate: applying
// a redate whose active range contains a merge commit is refused with the
// guard exit code (2) unless --allow-merges is passed, since the rewrite
// leaves the merge's non-first-parent side branch on its original dates
// (Replay only remaps the first parent — see gitio.Repo.Replay).
func TestRunRedate_refusesMergeWithoutAllowMerges(t *testing.T) {
	_, repo := redateTestRepoWithMerge(t)
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: time.Unix(1_700_000_000, 0)}

	err := runRedate(ctx, parseRedate(t, "--strategy=Even-Spread", "--start=2026-01-01", "--end=2026-02-01", "--execute"))
	if ExitCode(err) != 2 {
		t.Fatalf("expected guard exit 2 for a merge without --allow-merges, got err=%v code=%d", err, ExitCode(err))
	}

	// With --allow-merges the gate passes (may still succeed or hit a later step).
	err = runRedate(ctx, parseRedate(t, "--strategy=Even-Spread", "--start=2026-01-01", "--end=2026-02-01", "--execute", "--allow-merges"))
	if ExitCode(err) == 2 {
		t.Fatalf("with --allow-merges the merge gate must not block; got %v", err)
	}
}

// TestRedatePreview_warnsAboutMerges proves the dry-run preview surfaces the
// same merge warning, so a user sees it before ever touching --execute.
func TestRedatePreview_warnsAboutMerges(t *testing.T) {
	_, repo := redateTestRepoWithMerge(t)
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1_700_000_000, 0)}

	if err := runRedate(ctx, parseRedate(t, "--strategy=Even-Spread", "--start=2026-01-01", "--end=2026-02-01")); err != nil {
		t.Fatalf("runRedate dry-run: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("merge commit")) {
		t.Errorf("expected dry-run preview to warn about merge commits:\n%s", out.String())
	}
}
