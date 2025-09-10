package ui

import (
	"fmt"

	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
)

// selectedHashSet returns the commit hashes the current selection covers: the Set
// directly, or the inclusive Range span; empty for other modes.
func (m Model) selectedHashSet() map[string]bool {
	out := map[string]bool{}
	switch m.sel.mode {
	case SelSet:
		for h := range m.sel.set {
			out[h] = true
		}
	case SelRange:
		lo, hi := m.sel.rangeBounds(m.listCursor)
		for i := lo; i <= hi; i++ {
			out[m.commitAtDisplayIndex(i).Hash] = true
		}
	}
	return out
}

// affectedChains derives the squash chains (and ignored singletons) from the
// current selection over the root-first plan commits.
func (m Model) affectedChains() ([]model.SquashChain, []string) {
	return model.DetectChains(m.plan.Commits, m.selectedHashSet())
}

// squashChange is the staged squash mutation that drops into the apply pipeline.
// commits and plan are captured at staging time (history cannot change while the
// overlay is open).
type squashChange struct {
	commits []model.Commit
	plan    model.SquashPlan
}

func (c squashChange) title() string {
	squashed := 0
	for _, ch := range c.plan.Chains {
		squashed += len(ch.Members)
	}
	return fmt.Sprintf("Squash %d commit(s) into %d", squashed, len(c.plan.Chains))
}

// guard mirrors safety.Manager.ApplySquash's internal checks so the pipeline can
// block/confirm BEFORE applying. Keeping this in lockstep with ApplySquash means
// the guard never disagrees with what the apply will actually allow.
func (c squashChange) guard(state model.RepoState) safety.Guard {
	for _, ch := range c.plan.Chains {
		for _, mem := range ch.Members {
			if mem.IsMerge {
				return safety.Guard{Blocked: true, BlockReason: "cannot squash across a merge commit"}
			}
		}
	}
	if state.IsDirty {
		return safety.Guard{
			Blocked:     true,
			BlockReason: "working tree has uncommitted changes; commit or stash them first",
		}
	}
	if state.HasPushedCommits() {
		return safety.Guard{
			NeedsConfirmation: true,
			ConfirmWord:       state.Branch,
			ConfirmReason: "some commits are already on the upstream; squashing rewrites " +
				"history and will require a force-push",
		}
	}
	return safety.Guard{}
}

// preview lists one line per chain: "first..last  N → 1  <subject>".
func (c squashChange) preview(st styles) previewModel {
	lines := make([]string, 0, len(c.plan.Chains))
	for _, ch := range c.plan.Chains {
		lines = append(lines, fmt.Sprintf("%s..%s  %d → 1  %s",
			short(ch.First().Hash), short(ch.Last().Hash), len(ch.Members), ch.Subject))
	}
	return previewModel{title: c.title(), lines: lines}
}

func (c squashChange) apply(mgr *safety.Manager, state model.RepoState, opts safety.ApplyOptions) (safety.ApplyResult, error) {
	return mgr.ApplySquash(c.commits, c.plan, state, opts)
}
