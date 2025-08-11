package ui

import (
	"github.com/wlame/regit/internal/model"
)

// shiftedSet returns the set of commit hashes in the plan's out-of-order blast
// radius (the commits the enforcement cascade would move), for the preview to
// flag with a ⚠.
func shiftedSet(plan *model.Plan) map[string]bool {
	set := make(map[string]bool)
	for _, h := range plan.Shifted {
		set[h] = true
	}
	return set
}

// truncate shortens s to width runes, adding an ellipsis when cut.
func truncate(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 1 {
		return string(r[:width])
	}
	return string(r[:width-1]) + "…"
}
