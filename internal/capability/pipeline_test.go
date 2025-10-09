package capability

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
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
		cmd.Env = append(os.Environ(),
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
