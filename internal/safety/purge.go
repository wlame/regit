package safety

import (
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/model"
)

// ApplyPurge removes the spec's paths from branch history through the shared
// guarded apply skeleton: guards → preflight → backup → rewrite → verify →
// move branch. Verification is three-fold: commits that never touched the
// paths end-to-end must re-emit byte-identically (empty expectations over
// remap.Passthrough), the old→new HEAD diff must consist ONLY of deletions
// under the purged paths, and — a final independent safety net — none of the
// purged paths may remain reachable from the new head's own history at all.
//
// commits is the FULL first-parent history (root-first, as ReadCommits
// returns it); affected is the set AffectedCommits computed for spec.Paths.
// gitio.ApplyPurge itself re-emits every commit from the first affected one
// onward (trees are snapshots, so a later commit that never touched the path
// can still carry it forward), so remap.Passthrough holds only the commits
// whose surgery was a true no-op and remap.Rewritten includes those
// carried-forward ones too.
//
// The rewrite base is computed once via gitio.PurgeBase — the same helper
// gitio.ApplyPurge itself calls — so the merge-in-suffix guard below and the
// actual rewrite always agree on where the affected suffix starts. Without
// that shared base, a path introduced by a since-simplified-away merge could
// sit in what this Guards closure believes is an untouched prefix while
// gitio.ApplyPurge (independently re-deriving the same corrected base) pulls
// the merge into its rewritten range — silently completing a purge this
// guard should have blocked.
func (m *Manager) ApplyPurge(commits []model.Commit, spec model.PurgeSpec, affected map[string]bool, state model.RepoState, opts ApplyOptions) (ApplyResult, error) {
	firstAffected, baseErr := m.repo.PurgeBase(commits, spec.Paths, affected)
	return m.runApply(applyOp{
		Guards: func() error {
			if err := spec.Validate(); err != nil {
				return err
			}
			if baseErr != nil {
				return fmt.Errorf("compute purge base: %w", baseErr)
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
			// A path can have history on a merged-in side branch that never
			// touches the first-parent chain commits represents at all — including
			// a "pocket" (added then removed within the side branch, net zero by
			// merge time) that gitio.PurgeBase's tree-content walk cannot see,
			// since no surviving tree ever contains it, so the merge is never
			// pulled into suffix and the check above has nothing to catch. regit
			// only rewrites the first-parent chain, so this can never be purged
			// either way; checking it here (before any rewrite) turns what would
			// otherwise be a late abort in verifyPurgeAbsence into a clear,
			// up-front guard failure. Checked AFTER the merge-in-suffix loop
			// above on purpose: a path that came from a side branch always has
			// that branch's own add commit sitting off the first-parent chain, so
			// this check is a superset of the merge-in-suffix one — ordering it
			// second keeps the merge-in-suffix guard's own error message (naming
			// the specific commit) as the one users see for that already-covered
			// case, reserving this message for cases the other guard can't catch.
			sidePaths, err := m.repo.PathsInSideHistory(commits, spec.Paths)
			if err != nil {
				return fmt.Errorf("check side-branch history: %w", err)
			}
			if len(sidePaths) > 0 {
				return fmt.Errorf("%s also exists in a merged side branch's history, which regit cannot rewrite — see git filter-repo",
					strings.Join(sidePaths, ", "))
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
			if err := m.verifyPurgeAbsence(newHead, spec.Paths); err != nil {
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
		// Purge keeps the file on disk (see SyncIndexToTree): the index must stop
		// staging it, but someone purging a large artifact they still use locally
		// keeps their copy. It becomes an untracked file.
		Sync: m.repo.SyncIndexToTree,
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

// verifyPurgeAbsence is the final, independent safety net: it proves none of
// the purged paths remain reachable from ANY commit newHead's own history can
// reach, run BEFORE the branch ref moves. verifyPurgeHeadDiff already proves
// the tip-to-tip diff is clean, but that alone cannot catch a wrong rewrite
// base that left an untouched ancestor's tree still carrying a purged path
// (see gitio.PurgeBase's doc comment) — that ancestor never shows up in an
// oldHead-to-newHead diff at all if it was equally present (and equally
// untouched) on both sides. Checking reachability from newHead directly turns
// any future regression in the base computation into a clean abort here,
// instead of a rewrite that "succeeds" while quietly leaving the path in
// place.
func (m *Manager) verifyPurgeAbsence(newHead string, paths []string) error {
	reachable, err := m.repo.PathsReachableFrom(newHead, paths)
	if err != nil {
		return fmt.Errorf("verify purge absence: %w", err)
	}
	if reachable {
		return fmt.Errorf("purge verification failed: %s still reachable from the rewritten head",
			strings.Join(paths, ", "))
	}
	return nil
}
