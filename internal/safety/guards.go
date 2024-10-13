// Package safety wraps the rewrite in the protections a destructive history edit
// demands: an automatic backup ref before every apply, pre-flight guards against
// dirty or already-pushed state, and an in-app undo stack.
//
// It builds on internal/gitio for the actual git operations and stays free of
// any UI concerns, returning structured results the TUI renders.
package safety

import (
	"fmt"

	"github.com/wlame/ghist/internal/model"
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
// Order matters: a dirty tree is a hard block regardless of anything else, and a
// plan with sequence violations is a hard block because it would produce
// out-of-order history.
func CheckGuards(state model.RepoState, plan *model.Plan) Guard {
	if state.IsDirty {
		return Guard{
			Blocked:     true,
			BlockReason: "working tree has uncommitted changes; commit or stash them first",
		}
	}
	if !plan.IsValid() {
		return Guard{
			Blocked: true,
			BlockReason: fmt.Sprintf("plan has %d out-of-order commit(s); resolve them before applying",
				len(plan.Violations)),
		}
	}
	if anyPushed(plan) {
		return Guard{
			NeedsConfirmation: true,
			ConfirmWord:       state.Branch,
			ConfirmReason: "some commits already exist on the upstream; rewriting them will " +
				"require a force-push and can disrupt collaborators",
		}
	}
	return Guard{}
}

// anyPushed reports whether the plan would rewrite at least one commit that is
// already present on the upstream branch.
func anyPushed(plan *model.Plan) bool {
	for _, c := range plan.Commits {
		if c.IsPushed {
			if _, edited := plan.EditFor(c.Hash); edited {
				return true
			}
		}
	}
	return false
}
