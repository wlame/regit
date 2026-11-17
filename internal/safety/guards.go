// Package safety wraps the rewrite in the protections a destructive history edit
// demands: an automatic backup ref before every apply, pre-flight guards against
// dirty or already-pushed state, and an in-app undo stack.
//
// It builds on internal/gitio for the actual git operations and stays free of
// any UI concerns, returning structured results the TUI renders.
package safety

import (
	"fmt"

	"github.com/wlame/regit/internal/model"
)

// Guard is the outcome of the pre-flight checks. The TUI uses it to decide
// whether to block the apply outright or to demand a typed confirmation.
type Guard struct {
	// Blocked is true when the apply must not proceed at all (e.g. dirty tree).
	Blocked bool
	// BlockReason explains a hard block, for display.
	BlockReason string
	// NeedsConfirmation is true when the apply is allowed but risky (rewriting
	// commits that already exist on the upstream).
	NeedsConfirmation bool
	// ConfirmWord is what the user must type to proceed past a confirmation
	// (the branch name, mirroring how `git push --force` is usually gated).
	ConfirmWord string
	// ConfirmReason explains why confirmation is required.
	ConfirmReason string
}

// CheckGuards evaluates the repository state and the plan before any rewrite.
// Order matters: a shallow clone is checked FIRST because its history is
// fundamentally incomplete — that problem exists independently of (and takes
// priority over reporting) a dirty tree or any plan issue. After that, a dirty
// tree is a hard block regardless of anything else, and a plan with sequence
// violations is a hard block because it would produce out-of-order history.
func CheckGuards(state model.RepoState, plan *model.Plan) Guard {
	if state.IsShallow {
		return Guard{
			Blocked: true,
			BlockReason: "refusing to rewrite a shallow clone — history is incomplete; " +
				"run 'git fetch --unshallow' first",
		}
	}
	// A plan only changes commit headers and messages, so every file stays
	// identical and untracked files are no obstacle.
	if reason := state.BlockReason(model.NeedsNoTrackedChanges); reason != "" {
		return Guard{Blocked: true, BlockReason: reason}
	}
	if !plan.IsValid() {
		// Count Violations, not Shifted: IsValid gates on Violations being
		// non-empty, but Shifted only lists commits the enforcement cascade
		// would actually move (see Plan.Shifted's doc comment) — a violation
		// with no target caught in its own blast radius (e.g. two anchors
		// whose dates are out of order) leaves Shifted empty while Violations
		// is not, which used to render as the nonsensical "plan has 0
		// commit(s) conflicting".
		return Guard{
			Blocked: true,
			BlockReason: fmt.Sprintf("plan has %d commit(s) conflicting with your edits; resolve them before applying",
				len(plan.Violations)),
		}
	}
	// plan.Active() is the TRUE rewrite set: the rewrite engine (Replay) re-emits
	// every commit from the base boundary onward as a new git object, even one
	// with no individual edit, because its parent hash changes once anything
	// ahead of it in the frozen prefix is rewritten. Checking the full active
	// range (rather than "IsPushed AND individually edited") is what lets
	// redate/split/squash/reword agree on when a rewrite touches a pushed commit
	// it is about to re-hash as a side effect of an ancestor's edit.
	if model.AnyPushed(plan.Active()) {
		return Guard{
			NeedsConfirmation: true,
			ConfirmWord:       state.Branch,
			ConfirmReason: "some commits already exist on a remote; rewriting them will " +
				"require a force-push and can disrupt collaborators",
		}
	}
	return Guard{}
}
