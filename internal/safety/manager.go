package safety

import (
	"fmt"
	"strconv"

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

// ApplyOptions parameterizes a single apply.
type ApplyOptions struct {
	Sign gitio.SignMode
	// UnixTimestamp names the backup ref (passed in rather than read from the
	// clock so the operation is deterministic and testable).
	UnixTimestamp int64
	// Confirmed records that the user typed the confirmation word when the guard
	// required it (pushed commits).
	Confirmed bool
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

	// 1. Backup the current tip before touching anything.
	backupRef := gitio.BackupRefPrefix + strconv.FormatInt(opts.UnixTimestamp, 10)
	if err := m.repo.UpdateRef(backupRef, oldHead, ""); err != nil {
		return ApplyResult{}, fmt.Errorf("create backup ref: %w", err)
	}

	// 2. Replay history with the new dates/messages.
	newHead, remap, err := m.repo.Replay(plan, opts.Sign)
	if err != nil {
		return ApplyResult{}, err
	}

	// 3. Prove every rewritten commit changed exactly nothing before we commit to
	//    the new history. (Empty expectations => verified by tree-hash equality.)
	verification, err := m.verify(emptyExpectations(remap))
	if err != nil {
		return ApplyResult{}, err
	}

	// 4. Move the branch, using compare-and-swap so a concurrent change aborts us.
	if err := m.repo.UpdateRef(gitio.BranchRef(branch), newHead, oldHead); err != nil {
		return ApplyResult{}, fmt.Errorf("move branch %s: %w", branch, err)
	}

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
	if state.HasPushedCommits() && !opts.Confirmed {
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

	// Local invariant: the last sub-commit must reproduce the original tree so
	// that the commit history remains self-consistent after the split.
	if subs[len(subs)-1].Tree != commit.TreeHash {
		return ApplyResult{}, fmt.Errorf("refusing split: last sub-commit tree %s != original commit tree %s",
			subs[len(subs)-1].Tree, commit.TreeHash)
	}

	// --- Backup ---

	backupRef := gitio.BackupRefPrefix + strconv.FormatInt(opts.UnixTimestamp, 10)
	if err := m.repo.UpdateRef(backupRef, oldHead, ""); err != nil {
		return ApplyResult{}, fmt.Errorf("create backup ref: %w", err)
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

	// --- Move branch (compare-and-swap so a concurrent change aborts us) ---

	if err := m.repo.UpdateRef(gitio.BranchRef(branch), newHead, oldHead); err != nil {
		return ApplyResult{}, fmt.Errorf("move branch %s: %w", branch, err)
	}

	m.undoStack = append(m.undoStack, backupRef)
	return ApplyResult{BackupRef: backupRef, OldHead: oldHead, NewHead: newHead, Verification: verification}, nil
}

// CanUndo reports whether there is a backup to roll back to.
func (m *Manager) CanUndo() bool {
	return len(m.undoStack) > 0
}

// Undo restores the branch to the most recent backup ref and pops it off the
// stack. It returns the hash the branch was reset to.
func (m *Manager) Undo() (string, error) {
	if !m.CanUndo() {
		return "", fmt.Errorf("nothing to undo")
	}
	branch, err := m.repo.CurrentBranch()
	if err != nil {
		return "", err
	}
	last := m.undoStack[len(m.undoStack)-1]
	target, err := m.repo.ResolveRef(last)
	if err != nil {
		return "", fmt.Errorf("resolve backup %s: %w", last, err)
	}
	current, err := m.repo.ResolveRef("HEAD")
	if err != nil {
		return "", err
	}
	if err := m.repo.UpdateRef(gitio.BranchRef(branch), target, current); err != nil {
		return "", fmt.Errorf("reset branch to backup: %w", err)
	}
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	return target, nil
}
