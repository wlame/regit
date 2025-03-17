package ui

import (
	"slices"

	"github.com/wlame/regit/internal/model"
)

// chooseBaseBranch picks the branch whose merge-base with the current branch
// defines the frozen (shared) history cone, following spec §6.4's precedence:
//  1. the current branch's upstream tracking ref, if configured;
//  2. otherwise a local "main" or "master" (other than the current branch);
//  3. otherwise none ("" — nothing is auto-frozen).
func chooseBaseBranch(state model.RepoState, localBranches []string) string {
	if state.HasUpstream && state.UpstreamRef != "" {
		return state.UpstreamRef
	}
	for _, candidate := range []string{"main", "master"} {
		if candidate != state.Branch && slices.Contains(localBranches, candidate) {
			return candidate
		}
	}
	return ""
}

// baseIndexForMergeBase maps a merge-base commit hash to the plan's Base index:
// the storage index just AFTER the merge-base, so the merge-base and everything
// older are frozen. commits are root-first (index 0 = oldest). When the
// merge-base is not on this linear first-parent history, it returns 0 (freeze
// nothing) rather than guessing.
func baseIndexForMergeBase(commits []model.Commit, mergeBaseHash string) int {
	for i, c := range commits {
		if c.Hash == mergeBaseHash {
			return i + 1
		}
	}
	return 0
}
