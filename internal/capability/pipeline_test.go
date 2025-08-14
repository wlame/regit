package capability

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/model"
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
