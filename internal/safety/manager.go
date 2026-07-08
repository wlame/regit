package safety

import (
	"errors"
	"fmt"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// shortHash returns the first 8 characters of a hash, or the whole string if shorter.
func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// Manager orchestrates a safe rewrite: guards -> backup -> replay -> verify ->
// move branch, and keeps the undo stack of backup refs created this session.
type Manager struct {
	repo      *gitio.Repo
	undoStack []string // backup ref names, oldest first; last is most recent
}

// NewManager returns a safety manager bound to a repository.
func NewManager(repo *gitio.Repo) *Manager {
	return &Manager{repo: repo}
}

// ErrHeadMoved marks the H3 refusal: the branch advanced past the plan's base
// head. Headless callers map it to exit code 2 (guard-blocked) via errors.Is.
var ErrHeadMoved = errors.New("branch moved since the plan was created")

// preflight re-checks live repo state at apply time: shallow-clone state, a fresh
// dirty-tree read (the caller's snapshot may be stale), and the ExpectedHead anchor
// against the just-resolved oldHead. Returns an error when the apply must not
// proceed. Every apply path (Apply, ApplySplit, ApplySquash) calls this, so the
// shallow block cannot be forgotten by a path that skips CheckGuards — which is
// exactly how split and squash previously slipped past it.
func (m *Manager) preflight(branch, oldHead string, opts ApplyOptions) error {
	// A shallow clone's history is incomplete: any replay of the tail builds on a
	// grafted, non-real root, so this is the highest-priority block. Re-checked
	// live here (not trusted from a snapshot) for the same reason dirtiness is.
	shallow, err := m.repo.IsShallow()
	if err != nil {
		return fmt.Errorf("re-check shallow state: %w", err)
	}
	if shallow {
		return fmt.Errorf("rewrite blocked: shallow clone — history is incomplete; run 'git fetch --unshallow' first")
	}
	dirty, err := m.repo.IsDirty()
	if err != nil {
		return fmt.Errorf("re-check working tree: %w", err)
	}
	if dirty {
		return fmt.Errorf("rewrite blocked: working tree has uncommitted changes; commit or stash them first")
	}
	if opts.ExpectedHead == "" {
		return fmt.Errorf("programming error: apply without ExpectedHead")
	}
	if oldHead != opts.ExpectedHead {
		return fmt.Errorf("%w: branch %s (plan base %s, now %s); reload and re-stage",
			ErrHeadMoved, branch, shortHash(opts.ExpectedHead), shortHash(oldHead))
	}
	return nil
}

// ApplyOptions parameterizes a single apply.
type ApplyOptions struct {
	Sign gitio.SignMode
	// UnixTimestamp names the backup ref (passed in rather than read from the
	// clock so the operation is deterministic and testable).
	UnixTimestamp int64
	// Confirmed records that the user typed the confirmation word when the guard
	// required it (pushed commits).
	Confirmed bool
	// ExpectedHead is the commit the plan/proposal was computed from (the head
	// of the snapshot the caller read). Mandatory: Apply refuses when it is
	// empty, and aborts — before creating any ref — when the branch has moved
	// away from it (H3). This anchors the compare-and-swap to the user's
	// intent, not to whatever the branch happens to be at apply time.
	ExpectedHead string
}

// ApplyResult reports what happened on success.
type ApplyResult struct {
	BackupRef    string // ref the previous tip was saved to
	OldHead      string
	NewHead      string
	Verification model.VerificationSummary // result of the change-set verifier
}

// Apply runs the full guarded rewrite for the plan against the given state. It
// returns an error (and changes nothing) if a guard blocks the operation or
// confirmation is required but absent.
func (m *Manager) Apply(plan *model.Plan, state model.RepoState, opts ApplyOptions) (ApplyResult, error) {
	guard := CheckGuards(state, plan)
	if guard.Blocked {
		return ApplyResult{}, fmt.Errorf("rewrite blocked: %s", guard.BlockReason)
	}
	if guard.NeedsConfirmation && !opts.Confirmed {
		return ApplyResult{}, fmt.Errorf("confirmation required: %s", guard.ConfirmReason)
	}
	if !plan.HasChanges() {
		return ApplyResult{}, fmt.Errorf("nothing to apply: no commits were edited")
	}

	branch, err := m.repo.CurrentBranch()
	if err != nil {
		return ApplyResult{}, err
	}
	oldHead, err := m.repo.ResolveRef("HEAD")
	if err != nil {
		return ApplyResult{}, err
	}
	if err := m.preflight(branch, oldHead, opts); err != nil {
		return ApplyResult{}, err
	}

	// 1. Backup the current tip before touching anything.
	backupRef, err := m.repo.CreateBackupRef(branch, opts.UnixTimestamp, oldHead)
	if err != nil {
		return ApplyResult{}, err
	}

	// 2. Replay history with the new dates/messages.
	newHead, remap, err := m.repo.Replay(plan, opts.Sign)
	if err != nil {
		return ApplyResult{}, err
	}

	// 3. Prove every rewritten commit changed exactly nothing before we commit to
	//    the new history. (Empty expectations => verified by tree-hash equality.)
	//    The structural check then proves the new chain has the same length and
	//    sits on the same boundary parent — a remap bug that dropped or
	//    reparented commits would pass pairwise tree checks but not this.
	verification, err := m.verify(emptyExpectations(remap))
	if err != nil {
		return ApplyResult{}, err
	}
	if err := m.verifyChainStructure(oldHead, newHead, len(remap), len(remap)); err != nil {
		return ApplyResult{}, err
	}

	// 4. Move the branch, using compare-and-swap so a concurrent change aborts us.
	if err := m.repo.UpdateRef(gitio.BranchRef(branch), newHead, oldHead); err != nil {
		return ApplyResult{}, fmt.Errorf("move branch %s: %w", branch, err)
	}

	// Record the head this apply produced, paired with the backup, so a later
	// undo can detect whether the branch has since advanced past this point.
	// Non-fatal: on failure the advanced-HEAD guard simply degrades to off for
	// this backup (undo still creates its pre-undo backup).
	_ = m.repo.RecordAppliedHead(backupRef, newHead)

	m.undoStack = append(m.undoStack, backupRef)
	return ApplyResult{BackupRef: backupRef, OldHead: oldHead, NewHead: newHead, Verification: verification}, nil
}

// ApplySplit replaces one commit with sub-commits and replays the tail, guarded
// and backed up exactly like Apply. Two hard invariants are checked before the
// branch moves:
//
//  1. The last sub-commit's tree must equal the original commit's tree
//     (caller-supplied structural guarantee).
//  2. Every rewritten pair must be content-identical to its original
//     (end-to-end content guarantee, verified by m.verify over the boundary
//     pair (C → Cₖ) and all replayed tail expectations).
func (m *Manager) ApplySplit(commit model.Commit, parentHash string, subs []gitio.SubCommit, tail []model.Commit, state model.RepoState, opts ApplyOptions) (ApplyResult, error) {
	// --- Guards ---

	if commit.IsMerge {
		return ApplyResult{}, fmt.Errorf("cannot split a merge commit %s", shortHash(commit.Hash))
	}
	if state.IsDirty {
		return ApplyResult{}, fmt.Errorf("rewrite blocked: working tree is dirty")
	}
	if len(subs) < 2 {
		return ApplyResult{}, fmt.Errorf("nothing to split: fewer than two sub-commits")
	}
	// Pushed commits require the caller to have obtained explicit confirmation.
	// The rewrite set is the target commit plus its tail: everything Replay-style
	// re-hashing actually touches (the sub-commits themselves are new objects with
	// no prior existence, so only the ORIGINAL commit and its downstream tail can
	// already be pushed).
	if model.AnyPushed(append([]model.Commit{commit}, tail...)) && !opts.Confirmed {
		return ApplyResult{}, fmt.Errorf("confirmation required: commits are already pushed")
	}

	// --- Pre-flight state ---

	branch, err := m.repo.CurrentBranch()
	if err != nil {
		return ApplyResult{}, err
	}
	oldHead, err := m.repo.ResolveRef("HEAD")
	if err != nil {
		return ApplyResult{}, err
	}
	if err := m.preflight(branch, oldHead, opts); err != nil {
		return ApplyResult{}, err
	}

	// Local invariant: the last sub-commit must reproduce the original tree so
	// that the commit history remains self-consistent after the split.
	if subs[len(subs)-1].Tree != commit.TreeHash {
		return ApplyResult{}, fmt.Errorf("refusing split: last sub-commit tree %s != original commit tree %s",
			subs[len(subs)-1].Tree, commit.TreeHash)
	}

	// --- Backup ---

	backupRef, err := m.repo.CreateBackupRef(branch, opts.UnixTimestamp, oldHead)
	if err != nil {
		return ApplyResult{}, err
	}

	// --- Replay ---

	newHead, splitRemap, err := m.repo.ApplySplit(parentHash, subs, tail, opts.Sign)
	if err != nil {
		return ApplyResult{}, err
	}

	// --- Global invariant: every commit changed exactly what the split promised.
	//     The boundary pair (original commit -> last sub-commit Ck) must be
	//     content-identical, as must every replayed tail pair.
	exps := append([]model.Expectation{
		{OldCommit: commit.Hash, NewCommit: splitRemap.LastSub},
	}, emptyExpectations(splitRemap.Tail)...)
	verification, err := m.verify(exps)
	if err != nil {
		return ApplyResult{}, err
	}
	// Structural check: the old chain held the target plus its tail; the new one
	// must hold exactly len(subs) sub-commits plus the same tail, over the same
	// boundary parent.
	if err := m.verifyChainStructure(oldHead, newHead, 1+len(tail), len(subs)+len(tail)); err != nil {
		return ApplyResult{}, err
	}

	// --- Move branch (compare-and-swap so a concurrent change aborts us) ---

	if err := m.repo.UpdateRef(gitio.BranchRef(branch), newHead, oldHead); err != nil {
		return ApplyResult{}, fmt.Errorf("move branch %s: %w", branch, err)
	}

	// Record the head this apply produced, paired with the backup, so a later
	// undo can detect whether the branch has since advanced past this point.
	// Non-fatal: on failure the advanced-HEAD guard simply degrades to off for
	// this backup (undo still creates its pre-undo backup).
	_ = m.repo.RecordAppliedHead(backupRef, newHead)

	m.undoStack = append(m.undoStack, backupRef)
	return ApplyResult{BackupRef: backupRef, OldHead: oldHead, NewHead: newHead, Verification: verification}, nil
}

// CanUndo reports whether there is a backup to roll back to.
func (m *Manager) CanUndo() bool {
	return len(m.undoStack) > 0
}

// ErrBackupOtherBranch marks a restore refused because the backup belongs to a
// different branch. Headless callers map it to exit code 2 via errors.Is.
var ErrBackupOtherBranch = errors.New("backup belongs to another branch")

// ErrUndoHeadMoved marks a restore refused because the branch advanced past
// the head the backup's apply produced (another apply or a plain commit
// happened since). Headless callers map it to exit code 2 via errors.Is.
var ErrUndoHeadMoved = errors.New("HEAD moved past this backup")

// Restore moves the current branch back to the given backup ref, running the
// full undo protocol: branch-ownership guard, advanced-HEAD guard (unless
// force), a pre-undo backup of the current tip (under unixTS, so the undo is
// itself reversible via `undo --to`), and a compare-and-swap ref move. It
// returns the hash the branch was reset to.
//
// This is THE single implementation of undo — the session-stack Undo below and
// the headless `regit undo` both funnel through it, so "only the Manager moves
// branch refs" actually holds and the two guard sets cannot drift.
func (m *Manager) Restore(backupRef string, force bool, unixTS int64) (string, error) {
	branch, err := m.repo.CurrentBranch()
	if err != nil {
		return "", err
	}
	if b := gitio.BackupRefBranch(backupRef); b != "" && b != branch {
		return "", fmt.Errorf("%w: %s was taken on branch %s; you are on %s — check out %s to undo it",
			ErrBackupOtherBranch, backupRef, b, branch, b)
	}
	target, err := m.repo.ResolveRef(backupRef)
	if err != nil {
		return "", fmt.Errorf("resolve backup %s: %w", backupRef, err)
	}
	current, err := m.repo.ResolveRef("HEAD")
	if err != nil {
		return "", err
	}
	// Advanced-HEAD guard: if this backup recorded the head its apply produced,
	// and the branch no longer points there, work has happened since — undoing
	// would unwind it. Refuse unless forced. A legacy backup (no paired applied
	// ref) reports ok=false, so it degrades to the old behavior: no guard.
	if applied, ok, _ := m.repo.AppliedHeadFor(backupRef); ok && applied != current && !force {
		return "", fmt.Errorf("%w (apply produced %s, branch is now %s); re-run with --force to unwind it",
			ErrUndoHeadMoved, shortHash(applied), shortHash(current))
	}
	// Pre-undo backup: save the current tip so the undo is itself reversible.
	if _, err := m.repo.CreateBackupRef(branch, unixTS, current); err != nil {
		return "", fmt.Errorf("pre-undo backup: %w", err)
	}
	if err := m.repo.UpdateRef(gitio.BranchRef(branch), target, current); err != nil {
		return "", fmt.Errorf("reset branch to backup: %w", err)
	}
	return target, nil
}

// Undo restores the branch to the most recent backup ref this session created
// and pops it off the stack (see Restore for the guard protocol). It returns
// the hash the branch was reset to. unixTS names the pre-undo backup; it is a
// parameter (not read from the clock) so Undo stays deterministic and
// testable, matching how ApplyOptions.UnixTimestamp is threaded through Apply.
func (m *Manager) Undo(force bool, unixTS int64) (string, error) {
	if !m.CanUndo() {
		return "", fmt.Errorf("nothing to undo")
	}
	last := m.undoStack[len(m.undoStack)-1]
	target, err := m.Restore(last, force, unixTS)
	if err != nil {
		return "", err
	}
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	return target, nil
}
