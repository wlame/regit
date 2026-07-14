package safety

import (
	"fmt"

	"github.com/wlame/regit/internal/model"
)

// ApplyPurge removes the spec's paths from branch history through the shared
// guarded apply skeleton: guards → preflight → backup → rewrite → verify →
// move branch. Verification is two-fold: commits that never touched the paths
// end-to-end must re-emit byte-identically (empty expectations over
// remap.Passthrough), and the old→new HEAD diff must consist ONLY of
// deletions under the purged paths — nothing else may change.
//
// commits is the FULL first-parent history (root-first, as ReadCommits
// returns it); affected is the set AffectedCommits computed for spec.Paths.
// gitio.ApplyPurge itself re-emits every commit from the first affected one
// onward (trees are snapshots, so a later commit that never touched the path
// can still carry it forward), so remap.Passthrough holds only the commits
// whose surgery was a true no-op and remap.Rewritten includes those
// carried-forward ones too.
func (m *Manager) ApplyPurge(commits []model.Commit, spec model.PurgeSpec, affected map[string]bool, state model.RepoState, opts ApplyOptions) (ApplyResult, error) {
	firstAffected := -1
	for i, c := range commits {
		if affected[c.Hash] {
			firstAffected = i
			break
		}
	}
	return m.runApply(applyOp{
		Guards: func() error {
			if err := spec.Validate(); err != nil {
				return err
			}
			if firstAffected < 0 {
				return fmt.Errorf("nothing to purge: no commits touch the given paths")
			}
			suffix := commits[firstAffected:]
			for _, c := range suffix {
				if c.IsMerge {
					return fmt.Errorf("cannot purge across merge commit %s", shortHash(c.Hash))
				}
			}
			if state.IsDirty {
				return fmt.Errorf("rewrite blocked: working tree is dirty")
			}
			if model.AnyPushed(suffix) && !opts.Confirmed {
				return fmt.Errorf("confirmation required: commits are already pushed")
			}
			return nil
		},
		Rewrite: func(oldHead string) (rewriteResult, error) {
			newHead, remap, err := m.repo.ApplyPurge(commits, spec, affected, opts.Sign)
			if err != nil {
				return rewriteResult{}, err
			}
			if newHead == "" {
				return rewriteResult{}, fmt.Errorf("purge would remove every commit on the branch")
			}
			if err := m.verifyPurgeHeadDiff(oldHead, newHead, spec.Paths); err != nil {
				return rewriteResult{}, err
			}
			oldLen := len(commits) - firstAffected
			return rewriteResult{
				NewHead:      newHead,
				Expectations: emptyExpectations(remap.Passthrough),
				OldLen:       oldLen,
				NewLen:       oldLen - len(remap.Dropped),
			}, nil
		},
	}, opts)
}

// verifyPurgeHeadDiff proves the whole rewrite changed exactly what a purge
// may change: every path differing between the old and new head is a deletion
// under one of the purged prefixes.
func (m *Manager) verifyPurgeHeadDiff(oldHead, newHead string, paths []string) error {
	cs, err := m.repo.DiffChangeSet(oldHead, newHead)
	if err != nil {
		return fmt.Errorf("verify purge diff: %w", err)
	}
	for _, ch := range cs.Changes {
		if !model.UnderAnyPath(ch.Path, paths) {
			return fmt.Errorf("purge verification failed: unexpected change to %q", ch.Path)
		}
		if ch.Status != model.Deleted {
			return fmt.Errorf("purge verification failed: %q changed but was not deleted (%s)", ch.Path, ch.Status)
		}
	}
	return nil
}
