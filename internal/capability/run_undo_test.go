package capability

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/model"
)

func TestUndoRestoresLastBackup(t *testing.T) {
	_, repo := newTestRepo(t, 3)
	commits, _ := repo.ReadCommits()
	before, _ := repo.ReadState()

	// Apply a redate so a backup ref exists and HEAD moves.
	plan := model.NewPlan(commits)
	nd := commits[len(commits)-1].OrigAuthorDate.Add(48 * time.Hour)
	e, _ := plan.EditFor(commits[len(commits)-1].Hash)
	e.NewAuthorDate = &nd
	plan.SetBase(len(commits) - 1)
	plan.SetEdit(commits[len(commits)-1].Hash, e)
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Now: time.Unix(1000, 0)}
	if err := applyOrPreview(ctx, plan, newParsedArgs(map[string]any{"execute": true}, nil), func(w io.Writer, _ *model.Plan) {}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	moved, _ := repo.ReadState()
	if moved.HeadHash == before.HeadHash {
		t.Fatal("setup: HEAD did not move")
	}

	// undo should restore HEAD to the pre-apply hash.
	var out bytes.Buffer
	uctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(2000, 0)}
	if err := runUndo(uctx, newParsedArgs(map[string]any{}, nil)); err != nil {
		t.Fatalf("runUndo: %v", err)
	}
	restored, _ := repo.ReadState()
	if restored.HeadHash != before.HeadHash {
		t.Errorf("undo did not restore HEAD: got %s want %s", Short(restored.HeadHash), Short(before.HeadHash))
	}
}

func TestUndoListReadOnly(t *testing.T) {
	_, repo := newTestRepo(t, 2)
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0)}
	if err := runUndo(ctx, newParsedArgs(map[string]any{"list": true}, nil)); err != nil {
		t.Fatalf("runUndo --list: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("no backups")) {
		t.Errorf("expected 'no backups':\n%s", out.String())
	}
}
