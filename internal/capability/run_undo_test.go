package capability

import (
	"bytes"
	"io"
	"strings"
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

// TestUndoListWithBackupsIsReadOnly: --list exercises the listing path (a backup
// exists) and must not move HEAD.
func TestUndoListWithBackupsIsReadOnly(t *testing.T) {
	_, repo := newTestRepo(t, 3)
	commits, _ := repo.ReadCommits()

	// Create a backup ref by applying a redate.
	plan := model.NewPlan(commits)
	last := commits[len(commits)-1]
	nd := last.OrigAuthorDate.Add(48 * time.Hour)
	e, _ := plan.EditFor(last.Hash)
	e.NewAuthorDate = &nd
	plan.SetBase(len(commits) - 1)
	plan.SetEdit(last.Hash, e)
	actx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: time.Unix(1000, 0)}
	if err := applyOrPreview(actx, plan, newParsedArgs(map[string]any{"execute": true}, nil), func(io.Writer, *model.Plan) {}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	moved, _ := repo.ReadState()

	var out bytes.Buffer
	lctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(2000, 0)}
	if err := runUndo(lctx, newParsedArgs(map[string]any{"list": true}, nil)); err != nil {
		t.Fatalf("undo --list: %v", err)
	}
	after, _ := repo.ReadState()
	if after.HeadHash != moved.HeadHash {
		t.Errorf("undo --list mutated HEAD: %s -> %s", Short(moved.HeadHash), Short(after.HeadHash))
	}
}

// TestRunUndo_defaultsToCurrentBranchBackup: the implicit pick must skip
// backups from other branches (H4). The two backups point at DIFFERENT
// hashes so the assertion is discriminating: picking the wrong (newer, other
// branch) ref would leave HEAD unchanged, while picking main's (older,
// correct) ref moves HEAD to a distinct hash. A filter-less implementation
// that just takes the newest ref would fail this test.
func TestRunUndo_defaultsToCurrentBranchBackup(t *testing.T) {
	_, repo := newTestRepo(t, 2)
	head, _ := repo.ResolveRef("HEAD")
	older, _ := repo.ResolveRef("HEAD~1")
	// "other" branch's backup is newer and points at the CURRENT head (a
	// no-op if wrongly picked). main's backup is older but points at a
	// different hash, so only picking it actually moves HEAD.
	if _, err := repo.CreateBackupRef("other", 200, head); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateBackupRef("main", 100, older); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0)}
	if err := runUndo(ctx, newParsedArgs(map[string]any{}, nil)); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if !strings.Contains(out.String(), "Restored main") {
		t.Errorf("expected restore on main, got: %s", out.String())
	}
	got, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatalf("resolve HEAD: %v", err)
	}
	if got != older {
		t.Errorf("undo restored HEAD to %s, want main's backup target %s (other's newer backup at %s must be skipped)",
			Short(got), Short(older), Short(head))
	}
}

// TestRunUndo_toOtherBranchRefuses: an explicit --to naming a backup taken on
// another branch must refuse with a guard error (exit 2) naming both
// branches, and must not move HEAD.
func TestRunUndo_toOtherBranchRefuses(t *testing.T) {
	_, repo := newTestRepo(t, 2)
	head, _ := repo.ResolveRef("HEAD")
	ref, err := repo.CreateBackupRef("other", 300, head)
	if err != nil {
		t.Fatal(err)
	}
	before, err := repo.ReadState()
	if err != nil {
		t.Fatalf("read state: %v", err)
	}

	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: time.Unix(1000, 0)}
	undoErr := runUndo(ctx, newParsedArgs(map[string]any{"to": ref}, nil))
	if undoErr == nil {
		t.Fatal("expected refusal restoring another branch's backup")
	}
	if !strings.Contains(undoErr.Error(), "other") || !strings.Contains(undoErr.Error(), "main") {
		t.Errorf("expected error mentioning both branch names, got: %v", undoErr)
	}
	if code := ExitCode(undoErr); code != 2 {
		t.Errorf("expected exit code 2 (guard), got %d: %v", code, undoErr)
	}

	after, err := repo.ReadState()
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if after.HeadHash != before.HeadHash {
		t.Errorf("HEAD moved on refusal: %s -> %s", Short(before.HeadHash), Short(after.HeadHash))
	}
}

// TestRunUndo_legacyRefNeedsExplicitTo: legacy flat refs are never picked
// implicitly and warn when restored explicitly.
func TestRunUndo_legacyRefNeedsExplicitTo(t *testing.T) {
	_, repo := newTestRepo(t, 2)
	head, _ := repo.ResolveRef("HEAD")
	if err := repo.UpdateRef("refs/regit/backup/123", head, ""); err != nil {
		t.Fatal(err)
	}

	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: time.Unix(1000, 0)}
	if err := runUndo(ctx, newParsedArgs(map[string]any{}, nil)); err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("implicit undo with only a legacy ref should refuse mentioning legacy; got %v", err)
	}

	var out, errOut bytes.Buffer
	tctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &errOut, Now: time.Unix(2000, 0)}
	if err := runUndo(tctx, newParsedArgs(map[string]any{"to": "123"}, nil)); err != nil {
		t.Fatalf("explicit --to restore: %v", err)
	}
	if !strings.Contains(out.String(), "Restored") {
		t.Errorf("expected restore output, got: %s", out.String())
	}
	if !strings.Contains(errOut.String(), "legacy backup") {
		t.Errorf("expected legacy backup warning on stderr, got: %s", errOut.String())
	}
}
