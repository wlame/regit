package safety

import (
	"fmt"
	"strconv"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// ApplySquash collapses each chain in the plan into one commit and replays the
// rest byte-identically, guarded and backed up exactly like Apply/ApplySplit.
// Two guarantees are proven before the branch moves, both via m.verify over
// all-empty expectations:
//
//  1. Each chain's boundary pair (chain.Last -> squashed S) is content-identical
//     (S reuses Last's tree, so this holds by construction).
//  2. Every replayed untouched commit is content-identical to its original.
func (m *Manager) ApplySquash(commits []model.Commit, plan model.SquashPlan, state model.RepoState, opts ApplyOptions) (ApplyResult, error) {
	// --- Guards ---
	if len(plan.Chains) == 0 {
		return ApplyResult{}, fmt.Errorf("nothing to squash: no chains")
	}
	for _, ch := range plan.Chains {
		for _, mem := range ch.Members {
			if mem.IsMerge {
				return ApplyResult{}, fmt.Errorf("cannot squash across merge commit %s", shortHash(mem.Hash))
			}
		}
	}
	if state.IsDirty {
		return ApplyResult{}, fmt.Errorf("rewrite blocked: working tree is dirty")
	}
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

	// --- Backup ---
	backupRef := gitio.BackupRefPrefix + strconv.FormatInt(opts.UnixTimestamp, 10)
	if err := m.repo.UpdateRef(backupRef, oldHead, ""); err != nil {
		return ApplyResult{}, fmt.Errorf("create backup ref: %w", err)
	}

	// --- Replay ---
	newHead, remap, err := m.repo.ApplySquash(commits, plan, opts.Sign)
	if err != nil {
		return ApplyResult{}, err
	}

	// --- Verify: boundary (empty) per chain + empty per replayed commit ---
	exps := emptyExpectations(remap.Boundary)
	exps = append(exps, emptyExpectations(remap.Passthrough)...)
	verification, err := m.verify(exps)
	if err != nil {
		return ApplyResult{}, err
	}

	// --- Move branch (compare-and-swap) ---
	if err := m.repo.UpdateRef(gitio.BranchRef(branch), newHead, oldHead); err != nil {
		return ApplyResult{}, fmt.Errorf("move branch %s: %w", branch, err)
	}

	m.undoStack = append(m.undoStack, backupRef)
	return ApplyResult{BackupRef: backupRef, OldHead: oldHead, NewHead: newHead, Verification: verification}, nil
}
