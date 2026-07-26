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
	// SyncWarning is non-empty when the post-apply checkout refresh failed. It is
	// a warning rather than an error on purpose: history is already correct and
	// the branch has already moved, so aborting is neither possible nor honest.
	// It carries the exact command to run by hand.
	SyncWarning string
}

// rewriteResult is what an operation's rewrite step hands back to runApply:
// the replayed head (no ref moved yet), the change-set expectations to verify,
// and the first-parent chain lengths for the structural check.
type rewriteResult struct {
	NewHead        string
	Expectations   []model.Expectation
	OldLen, NewLen int
}

// applyOp parameterizes one history-rewriting operation for runApply. The
// apply protocol itself is fixed; an operation contributes only its two
// varying parts as data:
//
//   - Guards: the op-specific pre-checks against the caller's snapshot
//     (merge-commit refusals, pushed-history confirmation, plan validity).
//   - Rewrite: the replay itself, producing new objects WITHOUT moving any
//     ref, plus what the verifier must prove about them.
type applyOp struct {
	Guards  func() error
	Rewrite func(oldHead string) (rewriteResult, error)
	// Sync optionally refreshes the checkout after the branch ref has moved.
	//
	// Tree-preserving operations (redate, split, squash, linearize) leave it nil:
	// their new HEAD carries the identical tree, so the index and working tree are
	// already correct. Content-changing operations (purge, redact) MUST set it —
	// without it the branch points at a tree the checkout no longer matches, which
	// leaves a purged file staged for re-addition and a redacted secret still on
	// disk, and additionally trips regit's own dirty-tree guard on the next run.
	//
	// It lives on the skeleton rather than in the two callers so it participates
	// in the same "one protocol, every operation" guarantee as every other step.
	Sync func(newHead string) error
}

// runApply is the single guarded apply skeleton every history rewrite runs
// through: guards → preflight → backup → rewrite → verify (content +
// structure) → move branch (compare-and-swap) → record applied head. Apply,
// ApplySplit, and ApplySquash used to each hand-roll this sequence — which is
// exactly how the shallow-clone block was once forgotten on two of the three
// paths. Now a step added here (or reordered) holds for every operation by
// construction.
func (m *Manager) runApply(op applyOp, opts ApplyOptions) (ApplyResult, error) {
	if err := op.Guards(); err != nil {
		return ApplyResult{}, err
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

	// Backup the current tip before touching anything.
	backupRef, err := m.repo.CreateBackupRef(branch, opts.UnixTimestamp, oldHead)
	if err != nil {
		return ApplyResult{}, err
	}

	// Replay history. New objects are created but no ref moves yet, so any
	// failure below leaves the repository byte-for-byte unchanged (only an
	// unreferenced backup ref and dangling objects remain).
	res, err := op.Rewrite(oldHead)
	if err != nil {
		return ApplyResult{}, err
	}

	// Prove the rewrite changed exactly what the operation promised (content)
	// and preserved history's shape (chain length + boundary parent) before
	// committing to the new history.
	verification, err := m.verify(res.Expectations)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := m.verifyChainStructure(oldHead, res.NewHead, res.OldLen, res.NewLen); err != nil {
		return ApplyResult{}, err
	}

	// Move the branch, using compare-and-swap so a concurrent change aborts us.
	if err := m.repo.UpdateRef(gitio.BranchRef(branch), res.NewHead, oldHead); err != nil {
		return ApplyResult{}, fmt.Errorf("move branch %s: %w", branch, err)
	}

	// Refresh the checkout for operations whose new HEAD carries a different
	// tree. Deliberately non-fatal: the branch has moved and history is correct,
	// so a failure here is a stale checkout, not a corrupt repository.
	var syncWarning string
	if op.Sync != nil {
		if err := op.Sync(res.NewHead); err != nil {
			syncWarning = fmt.Sprintf(
				"warning: could not refresh the working tree (%v); run: git read-tree --reset -u %s",
				err, res.NewHead)
		}
	}

	// Record the head this apply produced, paired with the backup, so a later
	// undo can detect whether the branch has since advanced past this point.
	// Non-fatal: on failure the advanced-HEAD guard simply degrades to off for
	// this backup (undo still creates its pre-undo backup).
	_ = m.repo.RecordAppliedHead(backupRef, res.NewHead)

	m.undoStack = append(m.undoStack, backupRef)
	return ApplyResult{
		BackupRef:    backupRef,
		OldHead:      oldHead,
		NewHead:      res.NewHead,
		Verification: verification,
		SyncWarning:  syncWarning,
	}, nil
}

// Apply runs the full guarded rewrite for the plan against the given state. It
// returns an error (and changes nothing) if a guard blocks the operation or
// confirmation is required but absent.
func (m *Manager) Apply(plan *model.Plan, state model.RepoState, opts ApplyOptions) (ApplyResult, error) {
	return m.runApply(applyOp{
		Guards: func() error {
			guard := CheckGuards(state, plan)
			if guard.Blocked {
				return fmt.Errorf("rewrite blocked: %s", guard.BlockReason)
			}
			if guard.NeedsConfirmation && !opts.Confirmed {
				return fmt.Errorf("confirmation required: %s", guard.ConfirmReason)
			}
			if !plan.HasChanges() {
				return fmt.Errorf("nothing to apply: no commits were edited")
			}
			return nil
		},
		Rewrite: func(string) (rewriteResult, error) {
			newHead, remap, err := m.repo.Replay(plan, opts.Sign)
			if err != nil {
				return rewriteResult{}, err
			}
			// Every rewritten commit must be content-identical (empty
			// expectations ⇒ tree-hash equality) and the chain keeps its
			// length 1:1.
			return rewriteResult{
				NewHead:      newHead,
				Expectations: emptyExpectations(remap),
				OldLen:       len(remap),
				NewLen:       len(remap),
			}, nil
		},
	}, opts)
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
	return m.runApply(applyOp{
		Guards: func() error {
			if commit.IsMerge {
				return fmt.Errorf("cannot split a merge commit %s", shortHash(commit.Hash))
			}
			if state.IsDirty {
				return fmt.Errorf("rewrite blocked: working tree is dirty")
			}
			if len(subs) < 2 {
				return fmt.Errorf("nothing to split: fewer than two sub-commits")
			}
			// Pushed commits require the caller to have obtained explicit
			// confirmation. The rewrite set is the target commit plus its tail:
			// everything Replay-style re-hashing actually touches (the
			// sub-commits themselves are new objects with no prior existence,
			// so only the ORIGINAL commit and its downstream tail can already
			// be pushed).
			if model.AnyPushed(append([]model.Commit{commit}, tail...)) && !opts.Confirmed {
				return fmt.Errorf("confirmation required: commits are already pushed")
			}
			// Local invariant: the last sub-commit must reproduce the original
			// tree so the history remains self-consistent after the split.
			if subs[len(subs)-1].Tree != commit.TreeHash {
				return fmt.Errorf("refusing split: last sub-commit tree %s != original commit tree %s",
					subs[len(subs)-1].Tree, commit.TreeHash)
			}
			return nil
		},
		Rewrite: func(string) (rewriteResult, error) {
			newHead, splitRemap, err := m.repo.ApplySplit(parentHash, subs, tail, opts.Sign)
			if err != nil {
				return rewriteResult{}, err
			}
			// Global invariant: the boundary pair (original commit → last
			// sub-commit Cₖ) must be content-identical, as must every replayed
			// tail pair. Structurally, the old chain held the target plus its
			// tail; the new one holds len(subs) sub-commits plus the same tail.
			return rewriteResult{
				NewHead: newHead,
				Expectations: append([]model.Expectation{
					{OldCommit: commit.Hash, NewCommit: splitRemap.LastSub},
				}, emptyExpectations(splitRemap.Tail)...),
				OldLen: 1 + len(tail),
				NewLen: len(subs) + len(tail),
			}, nil
		},
	}, opts)
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
// returns the hash the branch was reset to, plus a non-fatal warning (empty on
// success) when the post-move checkout refresh below could not be completed —
// mirroring runApply's ApplyResult.SyncWarning so a stale checkout is never
// silently dropped on the undo path either.
//
// This is THE single implementation of undo — the session-stack Undo below and
// the headless `regit undo` both funnel through it, so "only the Manager moves
// branch refs" actually holds and the two guard sets cannot drift.
func (m *Manager) Restore(backupRef string, force bool, unixTS int64) (string, string, error) {
	branch, err := m.repo.CurrentBranch()
	if err != nil {
		return "", "", err
	}
	if b := gitio.BackupRefBranch(backupRef); b != "" && b != branch {
		return "", "", fmt.Errorf(
			"%w: %s was taken on branch %s; you are on %s — check out %s to undo it",
			ErrBackupOtherBranch, backupRef, b, branch, b)
	}
	target, err := m.repo.ResolveRef(backupRef)
	if err != nil {
		return "", "", fmt.Errorf("resolve backup %s: %w", backupRef, err)
	}
	current, err := m.repo.ResolveRef("HEAD")
	if err != nil {
		return "", "", err
	}
	// Whether the backup's tree equals the current HEAD's tree is the gate for
	// the checkout refresh below: identical trees prove nothing on disk or in
	// the index needs to change, so skipping is provably correct rather than a
	// guess. This is what makes the gate op-agnostic — Restore still does not
	// need to know whether it is undoing a redate or a purge (see the sync
	// block's own comment) — while also confining the refresh to exactly the
	// operations that actually changed content, instead of running it
	// unconditionally on every undo. A read failure here must not abort the
	// undo, since the ref move below IS the undo and always proceeds; it falls
	// back to treesEqual=false, i.e. the pre-fix behavior of always attempting
	// the sync, so a content-changing operation is never silently left
	// unsynced just because a tree lookup happened to fail.
	treesEqual := false
	if trees, terr := m.repo.CommitTrees([]string{current, target}); terr == nil {
		treesEqual = trees[current] == trees[target]
	}
	// Advanced-HEAD guard: if this backup recorded the head its apply produced,
	// and the branch no longer points there, work has happened since — undoing
	// would unwind it. Refuse unless forced. A legacy backup (no paired applied
	// ref) reports ok=false, so it degrades to the old behavior: no guard. A
	// read FAILURE, by contrast, degrades CLOSED: we cannot prove the unwind is
	// safe, so it too requires force.
	applied, ok, aerr := m.repo.AppliedHeadFor(backupRef)
	if aerr != nil && !force {
		return "", "", fmt.Errorf(
			"%w: cannot check the advanced-HEAD guard (%v); re-run with --force to unwind anyway",
			ErrUndoHeadMoved, aerr)
	}
	if ok && applied != current && !force {
		return "", "", fmt.Errorf(
			"%w (apply produced %s, branch is now %s); re-run with --force to unwind it",
			ErrUndoHeadMoved, shortHash(applied), shortHash(current))
	}
	// Pre-undo backup: save the current tip so the undo is itself reversible.
	if _, err := m.repo.CreateBackupRef(branch, unixTS, current); err != nil {
		return "", "", fmt.Errorf("pre-undo backup: %w", err)
	}
	// Read BEFORE the ref moves, while `git status` still compares against the
	// head being left: this answers "does the user have real uncommitted work
	// right now", which is what must never be destroyed below. A dirty tree here
	// is never a reason to refuse the undo itself — the ref move is the undo, and
	// it always proceeds — it only narrows how much of the checkout refresh
	// after it is safe to attempt.
	dirty, dirtyErr := m.repo.IsDirty()
	cleanBeforeMove := dirtyErr == nil && !dirty
	if err := m.repo.UpdateRef(gitio.BranchRef(branch), target, current); err != nil {
		return "", "", fmt.Errorf("reset branch to backup: %w", err)
	}
	// update-ref alone never reverses a content-changing apply's checkout Sync
	// (see runApply): without this, undoing a purge leaves a path missing from
	// the index even though the branch is back on the commit that carries it,
	// and undoing a redact leaves the redacted text sitting in the working tree
	// even though the branch is back before the redaction. Restore does not know
	// which operation produced this backup, so it cannot pick the matching
	// SyncIndexToTree/SyncIndexAndWorktreeToTree policy the way runApply does —
	// but it does not need to: treesEqual (computed above, before the ref moved)
	// already tells it whether there is anything to reverse at all. When the
	// trees match — every tree-preserving op's undo (redate, reword, split,
	// squash, linearize) — the sync is skipped outright: `read-tree --reset`
	// replaces the WHOLE index, not just the paths an operation touched, so
	// running it "just in case" on a dirty tree silently discards unrelated
	// staged changes that have nothing to do with the undo. Only when the trees
	// differ (purge, redact) does the sync run, and only then do the two arms
	// below matter: `git read-tree` without `-u` only ever writes the index, so
	// it is safe unconditionally, and is exactly what purge's undo needs (the
	// purged path was never touched on disk, only in the index). The `-u` half,
	// which DOES write worktree files, additionally requires cleanBeforeMove:
	// `git read-tree -u` has no "would overwrite local changes" refusal the way
	// `git checkout` does (verified empirically — it silently clobbers them), so
	// it only runs when the pre-move check proved there was nothing of the
	// user's on disk to lose. Both are non-fatal, matching runApply's Sync: the
	// branch has already moved, which is what undo promises; a stale checkout
	// after that is a follow-up nuisance, never a reason to report the undo
	// itself as failed — but unlike runApply's Sync, the failure is never simply
	// dropped: it comes back as a warning string so a headless `regit undo` (and
	// the TUI's U key) can still tell the operator their checkout may be stale
	// and hand them the exact command to fix it.
	var syncWarning string
	switch {
	case treesEqual:
		// Nothing changed between the two trees, so nothing needs refreshing —
		// touching the index here would only risk clobbering unrelated state.
	case cleanBeforeMove:
		if err := m.repo.SyncIndexAndWorktreeToTree(target); err != nil {
			syncWarning = fmt.Sprintf(
				"warning: could not refresh the working tree (%v); run: git read-tree --reset -u %s",
				err, target)
		}
	default:
		if err := m.repo.SyncIndexToTree(target); err != nil {
			syncWarning = fmt.Sprintf(
				"warning: could not refresh the index (%v); run: git read-tree --reset %s",
				err, target)
		}
	}
	return target, syncWarning, nil
}

// Undo restores the branch to the most recent backup ref this session created
// and pops it off the stack (see Restore for the guard protocol). It returns
// the hash the branch was reset to, plus Restore's sync warning (see Restore)
// unchanged — Undo is a thin wrapper over the stack, not a second place that
// could decide to swallow it. unixTS names the pre-undo backup; it is a
// parameter (not read from the clock) so Undo stays deterministic and
// testable, matching how ApplyOptions.UnixTimestamp is threaded through Apply.
func (m *Manager) Undo(force bool, unixTS int64) (string, string, error) {
	if !m.CanUndo() {
		return "", "", fmt.Errorf("nothing to undo")
	}
	last := m.undoStack[len(m.undoStack)-1]
	target, warning, err := m.Restore(last, force, unixTS)
	if err != nil {
		return "", "", err
	}
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	return target, warning, nil
}
