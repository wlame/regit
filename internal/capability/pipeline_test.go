package capability

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/gittest"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
)

func TestApplyOrPreviewDryRunDoesNotMutate(t *testing.T) {
	dir, repo := newTestRepo(t, 3)
	_ = dir
	commits, _ := repo.ReadCommits()
	plan := model.NewPlan(commits)
	// Edit the newest commit's author date.
	newDate := commits[len(commits)-1].OrigAuthorDate.Add(24 * time.Hour)
	e, _ := plan.EditFor(commits[len(commits)-1].Hash)
	e.NewAuthorDate = &newDate
	plan.SetBase(len(commits) - 1)
	plan.SetEdit(commits[len(commits)-1].Hash, e)

	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0)}
	before, _ := repo.ReadState()

	// dry-run (execute=false): prints, mutates nothing.
	a := newParsedArgs(map[string]any{}, nil)
	if err := applyOrPreview(ctx, plan, a, func(w io.Writer, _ *model.Plan) {}); err != nil {
		t.Fatalf("dry-run err: %v", err)
	}
	after, _ := repo.ReadState()
	if before.HeadHash != after.HeadHash {
		t.Error("dry-run moved HEAD")
	}

	// execute: mutates and creates a backup.
	a2 := newParsedArgs(map[string]any{"execute": true}, nil)
	if err := applyOrPreview(ctx, plan, a2, func(w io.Writer, _ *model.Plan) {}); err != nil {
		t.Fatalf("execute err: %v", err)
	}
	after2, _ := repo.ReadState()
	if before.HeadHash == after2.HeadHash {
		t.Error("execute did not move HEAD")
	}
	if refs, _ := repo.ListBackupRefs(); len(refs) == 0 {
		t.Error("execute did not create a backup ref")
	}
}

// TestApplyOrPreview_headMovedMapsToGuardExitCode exercises the redate ->
// applyOrPreview -> safety.Apply path headlessly: a plan is built from a
// snapshot of the branch, an out-of-band commit then advances HEAD past the
// plan's base, and --execute must be refused with the H3 guard rather than a
// generic runtime failure. The assertion is discriminating on two fronts: the
// exit code must be exactly 2 (guard), and errors.Is must resolve
// safety.ErrHeadMoved specifically — a change that mapped every apply error to
// exit 2, or one that mapped ErrHeadMoved to exit 3 like any other failure,
// would each fail one of the two checks.
func TestApplyOrPreview_headMovedMapsToGuardExitCode(t *testing.T) {
	dir, repo := newTestRepo(t, 3)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}

	// Build a redate-shaped plan from the pre-move snapshot.
	plan := model.NewPlan(commits)
	newDate := commits[len(commits)-1].OrigAuthorDate.Add(24 * time.Hour)
	e, _ := plan.EditFor(commits[len(commits)-1].Hash)
	e.NewAuthorDate = &newDate
	plan.SetBase(len(commits) - 1)
	plan.SetEdit(commits[len(commits)-1].Hash, e)

	// Out-of-band commit: the branch moves past the plan's base head, exactly
	// the H3 situation ExpectedHead is meant to catch.
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "out-of-band.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "out-of-band.txt")
	runGit("commit", "-q", "-m", "out-of-band commit moving HEAD")

	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0)}
	a := newParsedArgs(map[string]any{"execute": true}, nil)
	applyErr := applyOrPreview(ctx, plan, a, func(w io.Writer, _ *model.Plan) {})
	if applyErr == nil {
		t.Fatal("expected an error when the branch moved since the plan was created")
	}
	if code := ExitCode(applyErr); code != 2 {
		t.Errorf("ExitCode(err) = %d, want 2 (guard)", code)
	}
	if !errors.Is(applyErr, safety.ErrHeadMoved) {
		t.Errorf("expected errors.Is(err, safety.ErrHeadMoved), got: %v", applyErr)
	}
}

// TestSignMode_failsFastWithoutKey proves --sign fails fast (exit 2, a guard
// error) when the repo has no signing key configured, rather than letting the
// failure surface deep inside a replay's `commit-tree -S` call.
func TestSignMode_failsFastWithoutKey(t *testing.T) {
	_, repo := newTestRepo(t, 1) // fresh repo: no user.signingkey configured
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Now: time.Unix(1000, 0)}
	_, err := signMode(ctx, newParsedArgs(map[string]any{"sign": true}, nil))
	if ExitCode(err) != 2 {
		t.Fatalf("--sign without a key must be a guard error (exit 2); got %v", err)
	}
}

// TestSignMode_resolvesToReSignWithConfiguredKey proves the happy path: with a
// signing key configured, --sign resolves to gitio.ReSign and produces no error.
func TestSignMode_resolvesToReSignWithConfiguredKey(t *testing.T) {
	dir, repo := newTestRepo(t, 1)
	cmd := exec.Command("git", "config", "user.signingkey", "test-key-id")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config user.signingkey: %v\n%s", err, out)
	}
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Now: time.Unix(1000, 0)}
	mode, err := signMode(ctx, newParsedArgs(map[string]any{"sign": true}, nil))
	if err != nil {
		t.Fatalf("signMode: %v", err)
	}
	if mode != gitio.ReSign {
		t.Errorf("signMode = %v, want gitio.ReSign", mode)
	}
}

// TestSignMode_defaultsToDropSignatures proves that omitting --sign entirely
// (the default) always resolves to gitio.DropSignatures, with no preflight
// check of the signing key at all.
func TestSignMode_defaultsToDropSignatures(t *testing.T) {
	_, repo := newTestRepo(t, 1)
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Now: time.Unix(1000, 0)}
	mode, err := signMode(ctx, newParsedArgs(map[string]any{}, nil))
	if err != nil {
		t.Fatalf("signMode: %v", err)
	}
	if mode != gitio.DropSignatures {
		t.Errorf("signMode = %v, want gitio.DropSignatures", mode)
	}
}

// TestWarnDroppedSignatures_printsCountWhenNotSigning proves the preview
// warning counts only the signed commits and includes the --sign hint.
func TestWarnDroppedSignatures_printsCountWhenNotSigning(t *testing.T) {
	var out bytes.Buffer
	commits := []model.Commit{{Hash: "a", IsSigned: true}, {Hash: "b", IsSigned: false}, {Hash: "c", IsSigned: true}}
	warnDroppedSignatures(&out, commits, newParsedArgs(map[string]any{}, nil))
	if !strings.Contains(out.String(), "2 signed commit(s)") {
		t.Errorf("expected a warning mentioning 2 signed commits, got %q", out.String())
	}
	if !strings.Contains(out.String(), "--sign") {
		t.Errorf("expected the warning to mention --sign, got %q", out.String())
	}
}

// TestWarnDroppedSignatures_silentWhenSignPassed proves the warning is
// suppressed once the user already opted into --sign.
func TestWarnDroppedSignatures_silentWhenSignPassed(t *testing.T) {
	var out bytes.Buffer
	commits := []model.Commit{{Hash: "a", IsSigned: true}}
	warnDroppedSignatures(&out, commits, newParsedArgs(map[string]any{"sign": true}, nil))
	if out.Len() != 0 {
		t.Errorf("expected no warning when --sign is passed, got %q", out.String())
	}
}

// TestWarnDroppedSignatures_silentWhenNoneSigned proves the warning stays
// silent when the rewrite touches no signed commits at all.
func TestWarnDroppedSignatures_silentWhenNoneSigned(t *testing.T) {
	var out bytes.Buffer
	commits := []model.Commit{{Hash: "a", IsSigned: false}}
	warnDroppedSignatures(&out, commits, newParsedArgs(map[string]any{}, nil))
	if out.Len() != 0 {
		t.Errorf("expected no warning when no commits are signed, got %q", out.String())
	}
}

// signedCommitCapabilityRepo builds a repo with one unsigned commit followed by
// one genuinely SSH-signed commit — the same hermetic setup (no gpg-agent) as
// gitio/sign_test.go's configureSSHSigning, duplicated here because it is
// unexported in that package. Skips gracefully when ssh-keygen is unavailable.
func signedCommitCapabilityRepo(t *testing.T) (string, *gitio.Repo) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	dir, repo := newTestRepo(t, 1) // one unsigned commit to start from

	key := filepath.Join(t.TempDir(), "id")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Skipf("ssh-keygen failed: %v\n%s", err, out)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("config", "gpg.format", "ssh")
	run("config", "user.signingkey", key+".pub")
	// Without an allowed-signers file, git can't verify its own SSH signature
	// and %G? (what IsSigned reads) reports "N" (no signature) even though the
	// commit object does carry a gpgsig header — so this is required, not optional.
	signers := filepath.Join(t.TempDir(), "allowed_signers")
	pub, _ := os.ReadFile(key + ".pub")
	_ = os.WriteFile(signers, []byte("test@regit "+string(pub)), 0o644)
	run("config", "gpg.ssh.allowedSignersFile", signers)
	run("commit", "--allow-empty", "-q", "-S", "-m", "signed commit")

	return dir, repo
}

// TestApplyOrPreview_warnsOnRealSignedCommit closes the loop between IsSigned
// (read from a real git commit's actual %G? signature status) and the preview
// warning: redating a genuinely SSH-signed commit without --sign must print
// the dropped-signature warning, exercised through the real applyOrPreview
// path rather than a synthetic model.Commit{IsSigned: true} fixture.
func TestApplyOrPreview_warnsOnRealSignedCommit(t *testing.T) {
	_, repo := signedCommitCapabilityRepo(t)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	if !commits[len(commits)-1].IsSigned {
		t.Fatal("test setup: newest commit should be signed")
	}

	plan := model.NewPlan(commits)
	for _, c := range commits {
		nd := c.OrigAuthorDate.Add(time.Hour)
		plan.SetEdit(c.Hash, model.Edit{NewAuthorDate: &nd, NewCommitDate: &nd})
	}

	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0)}
	a := newParsedArgs(map[string]any{}, nil) // no --sign
	if err := applyOrPreview(ctx, plan, a, func(io.Writer, *model.Plan) {}); err != nil {
		t.Fatalf("applyOrPreview: %v", err)
	}
	if !strings.Contains(out.String(), "1 signed commit(s) will lose their signatures") {
		t.Errorf("expected the dropped-signature warning, got:\n%s", out.String())
	}
}

// capRepoWithTagInRange builds a throwaway repo with 3 commits and a
// lightweight tag "v1" on the middle commit (inside the range any full-history
// redate would rewrite), returning a RunContext wired to a captured Stdout
// buffer so tests can assert on preview output without touching the real
// filesystem beyond the temp repo.
func capRepoWithTagInRange(t *testing.T) (*RunContext, *bytes.Buffer) {
	t.Helper()
	dir, repo := newTestRepo(t, 3)
	cmd := exec.Command("git", "tag", "v1", "HEAD~1")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git tag: %v\n%s", err, out)
	}
	var buf bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &buf, Stderr: &buf, Now: time.Unix(1_700_000_000, 0)}
	return ctx, &buf
}

// runRedatePreview runs a dry-run (no --execute) Even-Spread redate over the
// whole history, exercising applyOrPreview's preview path — and so
// reportImpact — without mutating the repo.
func runRedatePreview(t *testing.T, ctx *RunContext) {
	t.Helper()
	a := parseRedate(t, "--strategy=Even-Spread", "--time=Working-hours", "--start=2026-01-01", "--end=2026-03-01")
	if err := runRedate(ctx, a); err != nil {
		t.Fatalf("runRedate preview: %v", err)
	}
}

// TestApplyOrPreview_reportsRefImpact proves the pre-apply impact report: a
// tag sitting on a commit inside the rewrite range must show up in the dry-run
// preview, so the user sees the blast radius before ever passing --execute.
func TestApplyOrPreview_reportsRefImpact(t *testing.T) {
	ctx, buf := capRepoWithTagInRange(t)
	runRedatePreview(t, ctx)
	out := buf.String()
	if !strings.Contains(out, "v1") || !strings.Contains(strings.ToLower(out), "reference") {
		t.Errorf("preview should report the tag impact; got:\n%s", out)
	}
}

// TestReportImpact_excludesMovingBranch proves the branch being rewritten is
// never reported as an orphan: it is the ref that moves WITH the rewrite, not
// one left behind by it. A repo with no other refs besides the current branch
// must produce an empty report even though every commit is in the active set.
func TestReportImpact_excludesMovingBranch(t *testing.T) {
	_, repo := newTestRepo(t, 3)
	commits, err := repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	// reportImpact never reads ctx.Now, so the exact clock value is arbitrary here.
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &buf, Stderr: &buf, Now: time.Unix(1_700_000_000, 0)}
	reportImpact(ctx, &buf, commits)
	if buf.Len() != 0 {
		t.Errorf("reportImpact should exclude the moving branch (refs/heads/main); got:\n%s", buf.String())
	}
}

// capRepoWithUpstream builds a throwaway repo with 3 commits, all pushed to a
// bare origin via `git push -u origin main` — HasUpstream is true and every
// commit is IsPushed, so a full-history redate needs --force AND is the case
// pushHint must fire for. Returns a RunContext wired to a captured Stdout
// buffer, mirroring capRepoWithTagInRange's shape.
func capRepoWithUpstream(t *testing.T) (*RunContext, *bytes.Buffer) {
	t.Helper()
	dir, repo := newTestRepo(t, 3)
	origin := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = gittest.Env(dir,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("remote", "add", "origin", origin)
	run("push", "-q", "-u", "origin", "main")

	var buf bytes.Buffer
	// applyRedate's fixed range ends 2026-03-01; Now must sit after it so the
	// segment engine's today-ceiling (Task 2) doesn't clamp the whole requested
	// window out of the segment and leave nothing to apply.
	ctx := &RunContext{
		Repo: repo, Cfg: config.Default(), Stdout: &buf, Stderr: &buf, Now: farFutureNow,
	}
	return ctx, &buf
}

// capRepoNoRemote builds a throwaway repo with 3 commits and no remote
// configured at all — pushHint's silent counterpart to capRepoWithUpstream:
// HasUpstream is false, so a full-history redate needs no --force and prints
// no push hint.
func capRepoNoRemote(t *testing.T) (*RunContext, *bytes.Buffer) {
	t.Helper()
	_, repo := newTestRepo(t, 3)
	var buf bytes.Buffer
	// See capRepoWithUpstream: Now must sit after applyRedate's fixed range.
	ctx := &RunContext{
		Repo: repo, Cfg: config.Default(), Stdout: &buf, Stderr: &buf, Now: farFutureNow,
	}
	return ctx, &buf
}

// applyRedate runs a real Even-Spread redate over the whole history through
// runRedate, appending extra flags (e.g. "--execute", "--force") onto a fixed
// strategy/date range — parsed through the real registry (parseRedate), same
// as every other redate test, so push-hint tests exercise the exact
// parse -> runRedate -> applyOrPreview path the CLI itself uses.
func applyRedate(t *testing.T, ctx *RunContext, extra ...string) {
	t.Helper()
	args := append([]string{
		"--strategy=Even-Spread", "--time=Working-hours",
		"--start=2026-01-01", "--end=2026-03-01",
	}, extra...)
	if err := runRedate(ctx, parseRedate(t, args...)); err != nil {
		t.Fatalf("runRedate: %v", err)
	}
}

// TestApply_printsPushHintWhenRemoteExists proves that applying a rewrite on a
// branch with an upstream prints the exact force-with-lease command needed to
// publish it — the recovery step a user would otherwise have to know by heart.
func TestApply_printsPushHintWhenRemoteExists(t *testing.T) {
	ctx, buf := capRepoWithUpstream(t) // origin + tracking @{u}
	applyRedate(t, ctx, "--execute", "--force")
	out := buf.String()
	if !strings.Contains(out, "git push --force-with-lease origin") {
		t.Errorf("expected a push hint; got:\n%s", out)
	}
}

// TestApply_noPushHintWithoutRemote proves pushHint stays silent when the
// branch has no upstream at all — there is nothing to publish, so no command
// to suggest.
func TestApply_noPushHintWithoutRemote(t *testing.T) {
	ctx, buf := capRepoNoRemote(t)
	applyRedate(t, ctx, "--execute")
	if strings.Contains(buf.String(), "force-with-lease") {
		t.Errorf("no remote → no push hint; got:\n%s", buf.String())
	}
}
