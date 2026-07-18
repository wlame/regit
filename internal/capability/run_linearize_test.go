package capability

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
)

// linearizeMergeRepo builds: main c1 — side branch feature/login (s1 by Alice,
// s2 by Bob) — merge --no-ff into main — c2 on main. Mirrors
// internal/gitio/linearize_test.go's and internal/safety/linearize_test.go's
// mergeRepo fixture (same shape, same package-local helper convention since
// the type is unexported per package). Returns the repo and the merge hash.
func linearizeMergeRepo(t *testing.T) (*gitio.Repo, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(env []string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir, env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(msg string, extraEnv ...string) {
		run(nil, "add", "-A")
		env := append([]string{
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00",
		}, extraEnv...)
		run(env, "commit", "-q", "--allow-empty", "-m", msg)
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "user.name", "Integrator")
	run(nil, "config", "user.email", "int@example.com")
	run(nil, "config", "commit.gpgsign", "false")
	commit("Base commit.")
	run(nil, "checkout", "-q", "-b", "feature/login")
	commit("Add login form.",
		"GIT_AUTHOR_NAME=Alice", "GIT_AUTHOR_EMAIL=alice@example.com")
	commit("Wire the backend.",
		"GIT_AUTHOR_NAME=Bob", "GIT_AUTHOR_EMAIL=bob@example.com")
	run(nil, "checkout", "-q", "main")
	run([]string{
		"GIT_AUTHOR_DATE=2026-06-05T15:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-06-05T15:00:00+00:00",
	}, "merge", "-q", "--no-ff", "feature/login", "-m", "Merge branch 'feature/login'")
	mergeHash := run(nil, "rev-parse", "HEAD")
	commit("Follow-up on main.")
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo, mergeHash
}

// linearizeMergeRepoPushed is linearizeMergeRepo plus a bare origin: main is
// pushed right after the merge lands (before the follow-up commit c2), so the
// merge itself is IsPushed and the trailing commit stays local-only. Modeled
// on run_squash_test.go's squashTestRepoWithPush / run_purge_test.go's
// newPurgeRepoWithPush — a real bare remote plus `git push`, the convention
// every other capability test uses to fake pushed state (no manual
// refs/remotes/** update-ref anywhere in this package).
func linearizeMergeRepoPushed(t *testing.T) *gitio.Repo {
	t.Helper()
	dir := t.TempDir()
	origin := t.TempDir()
	run := func(env []string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir, env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(msg string, extraEnv ...string) {
		run(nil, "add", "-A")
		env := append([]string{
			"GIT_AUTHOR_DATE=2026-06-05T14:00:00+00:00",
			"GIT_COMMITTER_DATE=2026-06-05T14:00:00+00:00",
		}, extraEnv...)
		run(env, "commit", "-q", "--allow-empty", "-m", msg)
	}
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	run(nil, "init", "-q")
	run(nil, "symbolic-ref", "HEAD", "refs/heads/main")
	run(nil, "config", "user.name", "Integrator")
	run(nil, "config", "user.email", "int@example.com")
	run(nil, "config", "commit.gpgsign", "false")
	run(nil, "remote", "add", "origin", origin)
	commit("Base commit.")
	run(nil, "checkout", "-q", "-b", "feature/login")
	commit("Add login form.",
		"GIT_AUTHOR_NAME=Alice", "GIT_AUTHOR_EMAIL=alice@example.com")
	commit("Wire the backend.",
		"GIT_AUTHOR_NAME=Bob", "GIT_AUTHOR_EMAIL=bob@example.com")
	run(nil, "checkout", "-q", "main")
	run([]string{
		"GIT_AUTHOR_DATE=2026-06-05T15:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-06-05T15:00:00+00:00",
	}, "merge", "-q", "--no-ff", "feature/login", "-m", "Merge branch 'feature/login'")
	// Push right after the merge lands, so the merge itself is IsPushed.
	run(nil, "push", "-q", "-u", "origin", "main")
	commit("Follow-up on main.")
	repo, err := gitio.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func linearizeCtx(out *bytes.Buffer, repo *gitio.Repo) *RunContext {
	return &RunContext{
		Repo: repo, Cfg: config.Config{}, Stdout: out, Stderr: out,
		Now: time.Unix(1717689600, 0),
	}
}

// TestRunLinearize_noMergesIsBenignNoop: a linear (merge-free) branch has
// nothing to fold, so runLinearize is a no-op that reports as much and leaves
// history untouched.
func TestRunLinearize_noMergesIsBenignNoop(t *testing.T) {
	repo, _ := squashTestRepo(t, 3) // reuse the existing linear-repo builder
	var out bytes.Buffer
	ctx := linearizeCtx(&out, repo)

	a := newParsedArgs(map[string]any{}, nil)
	if err := runLinearize(ctx, a); err != nil {
		t.Fatalf("runLinearize: %v", err)
	}
	if !strings.Contains(out.String(), "Nothing to linearize") {
		t.Errorf("expected a no-op message, got:\n%s", out.String())
	}
}

// TestRunLinearize_previewShowsFoldTableAndAdvisory: dry-run (no --execute)
// prints the fold table naming the merge, the synthesized branch-prefixed
// title, the folded-commit count, the side-history advisory (the branch that
// still carries the folded commits), and the dry-run marker — and leaves the
// branch head exactly where it was.
func TestRunLinearize_previewShowsFoldTableAndAdvisory(t *testing.T) {
	repo, mergeHash := linearizeMergeRepo(t)
	before, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	ctx := linearizeCtx(&out, repo)
	a := newParsedArgs(map[string]any{}, nil)
	if err := runLinearize(ctx, a); err != nil {
		t.Fatalf("runLinearize: %v", err)
	}

	s := out.String()
	if !strings.Contains(s, Short(mergeHash)) {
		t.Errorf("expected the merge's short hash %s in the preview, got:\n%s", Short(mergeHash), s)
	}
	if !strings.Contains(s, "login: Add login form.") {
		t.Errorf("expected the synthesized fold title, got:\n%s", s)
	}
	if !strings.Contains(s, "2 commit(s)") {
		t.Errorf("expected the folded-commit count, got:\n%s", s)
	}
	if !strings.Contains(s, "feature/login") {
		t.Errorf("expected the side-history advisory to name feature/login, got:\n%s", s)
	}
	if !strings.Contains(s, "(dry run") {
		t.Errorf("expected the dry-run marker, got:\n%s", s)
	}

	after, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if before.HeadHash != after.HeadHash {
		t.Error("preview must not move the branch head")
	}
}

// TestRunLinearize_executeFoldsAndReportsBackup: --execute applies the fold
// through the guarded safety path, reports the fold count and the backup ref,
// and the resulting branch has zero merge commits.
func TestRunLinearize_executeFoldsAndReportsBackup(t *testing.T) {
	repo, _ := linearizeMergeRepo(t)
	var out bytes.Buffer
	ctx := linearizeCtx(&out, repo)
	a := newParsedArgs(map[string]any{"execute": true}, nil)
	if err := runLinearize(ctx, a); err != nil {
		t.Fatalf("runLinearize --execute: %v", err)
	}

	s := out.String()
	if !strings.Contains(s, "Linearized 1 merge(s)") {
		t.Errorf("expected a fold-count summary, got:\n%s", s)
	}
	if !strings.Contains(s, "backup") {
		t.Errorf("expected a backup mention, got:\n%s", s)
	}

	state, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "rev-list", "--min-parents=2", "--count", state.Branch)
	cmd.Dir = repo.Root()
	cmd.Env = gittest.Env(repo.Root())
	out2, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-list: %v\n%s", err, out2)
	}
	if got := strings.TrimSpace(string(out2)); got != "0" {
		t.Fatalf("branch still has %s merge(s) after linearize --execute", got)
	}
}

// TestRunLinearize_pushedWithoutForceIsGuardErr: when the merge (the earliest
// fold, per model.LinearizePlan.RewrittenCommits) is already pushed,
// --execute without --force must be guard-blocked (exit code 2), leaving
// history untouched.
func TestRunLinearize_pushedWithoutForceIsGuardErr(t *testing.T) {
	repo := linearizeMergeRepoPushed(t)
	before, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	ctx := linearizeCtx(&out, repo)
	a := newParsedArgs(map[string]any{"execute": true}, nil)
	err = runLinearize(ctx, a)
	if got := ExitCode(err); got != 2 {
		t.Fatalf("pushed linearize without --force exit = %d, want 2 (err=%v)", got, err)
	}

	after, err := repo.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if before.HeadHash != after.HeadHash {
		t.Error("guard-blocked linearize must not move the branch head")
	}
}
