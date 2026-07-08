package safety

import (
	"fmt"

	"github.com/wlame/regit/internal/model"
)

// ApplySquash collapses each chain in the plan into one commit and replays the
// rest byte-identically, guarded and backed up exactly like Apply/ApplySplit
// (all three run the runApply skeleton). Two guarantees are proven before the
// branch moves, both via m.verify over all-empty expectations:
//
//  1. Each chain's boundary pair (chain.Last -> squashed S) is content-identical
//     (S reuses Last's tree, so this holds by construction).
//  2. Every replayed untouched commit is content-identical to its original.
func (m *Manager) ApplySquash(commits []model.Commit, plan model.SquashPlan, state model.RepoState, opts ApplyOptions) (ApplyResult, error) {
	return m.runApply(applyOp{
		Guards: func() error {
			if len(plan.Chains) == 0 {
				return fmt.Errorf("nothing to squash: no chains")
			}
			for _, ch := range plan.Chains {
				for _, mem := range ch.Members {
					if mem.IsMerge {
						return fmt.Errorf("cannot squash across merge commit %s", shortHash(mem.Hash))
					}
				}
			}
			if state.IsDirty {
				return fmt.Errorf("rewrite blocked: working tree is dirty")
			}
			// commits is the FULL history (ApplySquash needs it to locate the
			// chains), but gitio.Repo.ApplySquash only ever re-emits the suffix
			// from the earliest chain's first member onward — everything before
			// that keeps its original hash. RewrittenCommits narrows to that
			// suffix so an unrelated, untouched pushed commit earlier in
			// history cannot over-flag this squash.
			if model.AnyPushed(plan.RewrittenCommits(commits)) && !opts.Confirmed {
				return fmt.Errorf("confirmation required: commits are already pushed")
			}
			return nil
		},
		Rewrite: func(string) (rewriteResult, error) {
			newHead, remap, err := m.repo.ApplySquash(commits, plan, opts.Sign)
			if err != nil {
				return rewriteResult{}, err
			}
			// Content: boundary (empty) per chain + empty per replayed commit.
			// Structure: the rewritten suffix loses exactly len(Members)-1
			// commits per collapsed chain and keeps everything else 1:1.
			oldLen := len(plan.RewrittenCommits(commits))
			newLen := oldLen
			for _, ch := range plan.Chains {
				newLen -= len(ch.Members) - 1
			}
			return rewriteResult{
				NewHead:      newHead,
				Expectations: append(emptyExpectations(remap.Boundary), emptyExpectations(remap.Passthrough)...),
				OldLen:       oldLen,
				NewLen:       newLen,
			}, nil
		},
	}, opts)
}
