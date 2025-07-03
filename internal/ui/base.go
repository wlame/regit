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

// trunkBranches are the trunk branch names where regit starts fully unlocked when
// the branch is already in sync with its upstream. There is no local-only work to
// protect, so the auto-base's "merge-base == HEAD" result (which freezes all but
// the newest commit) would be unhelpful. Editing pushed commits is still gated by
// the typed branch-name confirmation at apply time, so safety is unaffected.
var trunkBranches = []string{"main", "master"}

// startsUnlocked reports the "trunk + upstream in sync" case: HEAD is on a trunk
// branch whose configured upstream already contains every local commit
// (AheadOfRemote == 0). applyAutoBase consults this first and freezes nothing when
// it is true.
func startsUnlocked(state model.RepoState) bool {
	if !slices.Contains(trunkBranches, state.Branch) {
		return false
	}
	return state.HasUpstream && state.UpstreamRef != "" && state.AheadOfRemote == 0
}
