package safety

import (
	"fmt"
	"strconv"

	"github.com/wlame/ghist/internal/gitio"
	"github.com/wlame/ghist/internal/model"
)

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
	BackupRef string // ref the previous tip was saved to
	OldHead   string
	NewHead   string
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
	newHead, err := m.repo.Replay(plan, opts.Sign)
	if err != nil {
		return ApplyResult{}, err
	}

	// 3. Prove no file content changed before we commit to the new history.
	if err := m.repo.VerifyTreesUnchanged(oldHead, newHead); err != nil {
		return ApplyResult{}, err
	}

	// 4. Move the branch, using compare-and-swap so a concurrent change aborts us.
	if err := m.repo.UpdateRef(gitio.BranchRef(branch), newHead, oldHead); err != nil {
		return ApplyResult{}, fmt.Errorf("move branch %s: %w", branch, err)
	}

	m.undoStack = append(m.undoStack, backupRef)
	return ApplyResult{BackupRef: backupRef, OldHead: oldHead, NewHead: newHead}, nil
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
