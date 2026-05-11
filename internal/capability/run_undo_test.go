package capability

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// TestUndoList_rendersBranchAndLegacyMarker: --list must render the branch
// column for a new-style backup (the current branch name) and the
// "(legacy)" marker for a flat ref that predates branch qualification.
func TestUndoList_rendersBranchAndLegacyMarker(t *testing.T) {
	_, repo := newTestRepo(t, 2)
	state, err := repo.ReadState()
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	head, err := repo.ResolveRef("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateBackupRef(state.Branch, 1717689600, head); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRef("refs/regit/backup/123", head, ""); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: &out, Now: time.Unix(1000, 0)}
	if err := runUndo(ctx, newParsedArgs(map[string]any{"list": true}, nil)); err != nil {
		t.Fatalf("undo --list: %v", err)
	}
	if !strings.Contains(out.String(), state.Branch) {
		t.Errorf("expected the current branch name %q in --list output:\n%s", state.Branch, out.String())
	}
	if !strings.Contains(out.String(), "(legacy)") {
		t.Errorf("expected a '(legacy)' marker for the flat ref in --list output:\n%s", out.String())
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

// applyRedatePlan builds and applies a trivial redate (pushes the last
// commit's author date forward two days) through the same applyOrPreview
// pipeline production code uses, so the resulting backup ref carries a real
// paired applied-head ref (recorded by safety.Manager.Apply).
func applyRedatePlan(t *testing.T, ctx *RunContext) {
	t.Helper()
	commits, err := ctx.Repo.ReadCommits()
	if err != nil {
		t.Fatal(err)
	}
	plan := model.NewPlan(commits)
	last := commits[len(commits)-1]
	nd := last.OrigAuthorDate.Add(48 * time.Hour)
	e, _ := plan.EditFor(last.Hash)
	e.NewAuthorDate = &nd
	plan.SetBase(len(commits) - 1)
	plan.SetEdit(last.Hash, e)
	if err := applyOrPreview(ctx, plan, newParsedArgs(map[string]any{"execute": true}, nil), func(io.Writer, *model.Plan) {}); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

// appendCapabilityCommit adds one commit to the repo out-of-band, simulating
// work that landed on the branch after regit's apply (advancing HEAD past the
// backup's recorded applied head).
func appendCapabilityCommit(t *testing.T, dir string) {
	t.Helper()
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
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "extra.txt")
	run("commit", "-q", "-m", "concurrent")
}

// TestRunUndo_createsPreUndoBackup: a normal undo must itself create a backup
// ref of the current tip (so the undo is reversible via `undo --to`), and
// that backup must then show up in `--list`.
func TestRunUndo_createsPreUndoBackup(t *testing.T) {
	_, repo := newTestRepo(t, 3)
	before, _ := repo.ReadState()

	actx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: time.Unix(1000, 0)}
	applyRedatePlan(t, actx)
	refsBefore, _ := repo.ListBackupRefs()

	uctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: time.Unix(2000, 0)}
	if err := runUndo(uctx, newParsedArgs(map[string]any{}, nil)); err != nil {
		t.Fatalf("runUndo: %v", err)
	}
	restored, _ := repo.ReadState()
	if restored.HeadHash != before.HeadHash {
		t.Fatalf("undo did not restore HEAD: got %s want %s", Short(restored.HeadHash), Short(before.HeadHash))
	}

	refsAfter, _ := repo.ListBackupRefs()
	if len(refsAfter) != len(refsBefore)+1 {
		t.Fatalf("undo must create exactly one new (pre-undo) backup ref; had %d, now %d", len(refsBefore), len(refsAfter))
	}

	var out bytes.Buffer
	lctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: &out, Stderr: io.Discard, Now: time.Unix(3000, 0)}
	if err := runUndo(lctx, newParsedArgs(map[string]any{"list": true}, nil)); err != nil {
		t.Fatalf("undo --list: %v", err)
	}
	lines := strings.Count(strings.TrimSpace(out.String()), "\n") + 1
	if lines != len(refsAfter) {
		t.Errorf("--list must show all %d backups (including the pre-undo one), showed %d lines:\n%s",
			len(refsAfter), lines, out.String())
	}
}

// TestRunUndo_advancedHeadRefusesWithoutForce: once the branch advances past
// the head a backup's apply produced (a plain commit lands on top), undo
// without --force must refuse with exit 2 and leave HEAD untouched.
func TestRunUndo_advancedHeadRefusesWithoutForce(t *testing.T) {
	dir, repo := newTestRepo(t, 3)

	actx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: time.Unix(1000, 0)}
	applyRedatePlan(t, actx)
	afterApply, _ := repo.ReadState()

	appendCapabilityCommit(t, dir)
	advanced, _ := repo.ReadState()
	if advanced.HeadHash == afterApply.HeadHash {
		t.Fatal("setup: HEAD did not advance past the apply")
	}

	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: time.Unix(2000, 0)}
	err := runUndo(ctx, newParsedArgs(map[string]any{}, nil))
	if err == nil {
		t.Fatal("undo past an advanced HEAD without --force must refuse")
	}
	if code := ExitCode(err); code != 2 {
		t.Errorf("expected exit code 2 (guard blocked), got %d: %v", code, err)
	}
	still, _ := repo.ReadState()
	if still.HeadHash != advanced.HeadHash {
		t.Errorf("HEAD moved on refusal: %s -> %s", Short(advanced.HeadHash), Short(still.HeadHash))
	}
}

// TestRunUndo_advancedHeadForceUnwinds: --force overrides the advanced-HEAD
// guard and restores the branch to the backup anyway (discarding the newer
// commit).
func TestRunUndo_advancedHeadForceUnwinds(t *testing.T) {
	dir, repo := newTestRepo(t, 3)
	before, _ := repo.ReadState()

	actx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: time.Unix(1000, 0)}
	applyRedatePlan(t, actx)

	appendCapabilityCommit(t, dir)

	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: time.Unix(2000, 0)}
	if err := runUndo(ctx, newParsedArgs(map[string]any{"force": true}, nil)); err != nil {
		t.Fatalf("forced undo should succeed: %v", err)
	}
	restored, _ := repo.ReadState()
	if restored.HeadHash != before.HeadHash {
		t.Errorf("forced undo did not restore the original HEAD: got %s want %s", Short(restored.HeadHash), Short(before.HeadHash))
	}
}

// TestRunUndo_legacyBackupDegradesGuard: a backup with no paired applied ref
// (predates this feature) must never trigger the advanced-HEAD guard, even
// when HEAD has moved on — it still gets the ordinary pre-undo backup, exactly
// as the non-legacy path does (mirrors TestRunUndo_createsPreUndoBackup's ref
// count assertion).
func TestRunUndo_legacyBackupDegradesGuard(t *testing.T) {
	_, repo := newTestRepo(t, 2)
	head, _ := repo.ResolveRef("HEAD")
	if _, err := repo.CreateBackupRef("main", 500, head); err != nil {
		t.Fatal(err)
	}
	// No RecordAppliedHead call: this backup has no paired applied ref.
	refsBefore, _ := repo.ListBackupRefs()

	ctx := &RunContext{Repo: repo, Cfg: config.Default(), Stdout: io.Discard, Stderr: io.Discard, Now: time.Unix(600, 0)}
	if err := runUndo(ctx, newParsedArgs(map[string]any{}, nil)); err != nil {
		t.Fatalf("undo of a legacy (unpaired) backup must not be guard-blocked: %v", err)
	}
	restored, _ := repo.ResolveRef("HEAD")
	if restored != head {
		t.Errorf("undo did not restore HEAD: got %s want %s", Short(restored), Short(head))
	}

	refsAfter, _ := repo.ListBackupRefs()
	if len(refsAfter) != len(refsBefore)+1 {
		t.Fatalf("legacy-path undo must still create exactly one new (pre-undo) backup ref; had %d, now %d",
			len(refsBefore), len(refsAfter))
	}
}
