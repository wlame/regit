package ui

import (
	"github.com/mattn/go-runewidth"

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
// truncate shortens s to fit width DISPLAY CELLS (not runes), appending an ellipsis
// when it must cut. Measuring by cell is essential: a CJK ideograph or emoji is two
// cells wide, so a rune-count truncation would let such a subject overflow its
// column and wrap, blowing the row (and the whole frame) past the terminal.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	// runewidth.Truncate keeps the result within width cells, tail included.
	return runewidth.Truncate(s, width, "…")
}
