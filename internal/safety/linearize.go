package safety

import (
	"fmt"

	"github.com/wlame/regit/internal/model"
)

// ApplyLinearize folds every merge in the plan into an ordinary commit and
// replays the rest byte-identically, guarded and backed up exactly like
// Apply/ApplySplit/ApplySquash (all run the runApply skeleton). Every emitted
// commit — fold or pass-through — reuses an original tree, so the verifier
// proves content identity over ALL-empty expectations, and the chain length is
// unchanged (a 1:1 rewrite), which the structural check pins.
func (m *Manager) ApplyLinearize(
	commits []model.Commit,
	plan model.LinearizePlan,
	state model.RepoState,
	opts ApplyOptions,
) (ApplyResult, error) {
	return m.runApply(applyOp{
		Guards: func() error {
			if len(plan.Folds) == 0 {
				return fmt.Errorf("nothing to linearize: no merge commits in range")
			}
			if reason := state.BlockReason(model.NeedsNoTrackedChanges); reason != "" {
				return fmt.Errorf("rewrite blocked: %s", reason)
			}
			// commits is the FULL history (ApplyLinearize needs it to locate the
			// folds), but gitio.Repo.ApplyLinearize only ever re-emits the suffix
			// from the earliest fold's merge onward — everything before that keeps
			// its original hash. RewrittenCommits narrows to that suffix so an
			// unrelated, untouched pushed commit earlier in history cannot
			// over-flag this linearize.
			if model.AnyPushed(plan.RewrittenCommits(commits)) && !opts.Confirmed {
				return fmt.Errorf("confirmation required: commits are already pushed")
			}
			return nil
		},
		Rewrite: func(string) (rewriteResult, error) {
			newHead, remap, err := m.repo.ApplyLinearize(commits, plan, opts.Sign)
			if err != nil {
				return rewriteResult{}, err
			}
			// 1:1 rewrite: the suffix keeps its length; every pair is
			// content-identical (folds reuse the merge's tree by construction).
			oldLen := len(plan.RewrittenCommits(commits))
			return rewriteResult{
				NewHead:      newHead,
				Expectations: append(emptyExpectations(remap.Folded), emptyExpectations(remap.Passthrough)...),
				OldLen:       oldLen,
				NewLen:       oldLen,
			}, nil
		},
	}, opts)
}
