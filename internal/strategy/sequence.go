package strategy

import (
	"time"

	"github.com/wlame/regit/internal/model"
)

// sequence.go upholds regit's hardest invariant: a child commit must never be
// dated before its parent. There are two concerns here:
//
//   - FindViolations (detection): reports every parent/child pair that is out of
//     order, for the preview to highlight and the apply path to block on.
//   - EnforceSequential (correction): rewrites the proposed timestamps so the
//     chain becomes monotonic non-decreasing.
//
// Commits are stored root-first (oldest at index 0), so commit i's parent in the
// linear first-parent history is commit i-1.

// FindViolations returns every place where a commit's proposed time is strictly
// earlier than the previous (parent) commit's proposed time. times[i] is the
// proposed timestamp for commits[i].
func FindViolations(commits []model.Commit, times []time.Time) []model.SequenceViolation {
	var violations []model.SequenceViolation
	for i := 1; i < len(commits); i++ {
		if times[i].Before(times[i-1]) {
			violations = append(violations, model.SequenceViolation{
				ChildHash:  commits[i].Hash,
				ParentHash: commits[i-1].Hash,
				ChildDate:  times[i],
				ParentDate: times[i-1],
			})
		}
	}
	return violations
}

// minGap is the smallest spacing EnforceSequential should put between a parent
// and a child when it has to push a child forward. One second is enough for git
// to record a distinct, correctly-ordered timestamp.
const minGap = time.Second

// EnforceSequential returns a copy of times adjusted so that it is monotonic
// non-decreasing along the parent chain (times[i] >= times[i-1]). It must not
// mutate the input slice.
//
// DESIGN DECISION (your contribution): when commits[i] would land before its
// parent commits[i-1], how should it be corrected? Reasonable policies include:
//
//   - Push the child forward to parent + minGap (keeps earlier commits anchored
//     where the strategy intended; later commits drift forward — the common
//     "respect the start date" choice).
//   - Pull the parent backward to the child's time (anchors the end date instead;
//     can cascade backward through several parents).
//   - Snap to equal (parent == child) when same-second collisions are acceptable.
//
// The trade-off is about WHICH end of the chain stays fixed and how much drift
// you tolerate. The tests in sequence_test.go encode the expected behavior for
// the "push child forward by minGap" policy; implement that here.
func EnforceSequential(commits []model.Commit, times []time.Time) []time.Time {
	// Work on a copy so the caller's proposed timestamps are never mutated; the
	// preview relies on still being able to show the original (possibly invalid)
	// assignment alongside the corrected one.
	out := make([]time.Time, len(times))
	copy(out, times)

	// Policy: "push the child forward". The first commit (index 0) stays exactly
	// where the strategy placed it, which preserves "start here" intent — a
	// spread that was meant to begin on March 1 still begins on March 1, and any
	// unavoidable correction drifts *later* in time rather than earlier.
	//
	// A single left-to-right pass is sufficient. By the time we examine out[i],
	// out[i-1] has already been corrected, so comparing each element only to its
	// immediate predecessor is enough to make the whole chain monotonic. This is
	// what produces the cascade: if several commits each claim an earlier time
	// than the one before, every one gets bumped to "previous + minGap", so the
	// tail ends up at out[0] + k*minGap for k pile-ups in a row.
	//
	// minGap (one second) guarantees a strictly distinct, correctly-ordered
	// timestamp; equal times would technically satisfy "non-decreasing" but would
	// leave same-second collisions that are harder to reason about in git output.
	for i := 1; i < len(out); i++ {
		if out[i].Before(out[i-1]) {
			out[i] = out[i-1].Add(minGap)
		}
	}

	return out
}
