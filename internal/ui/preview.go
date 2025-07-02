package ui

import (
	"github.com/wlame/regit/internal/model"
)

// violationSet returns the set of commit hashes involved in any violation.
func violationSet(plan *model.Plan) map[string]bool {
	set := make(map[string]bool)
	for _, v := range plan.Violations {
		set[v.ChildHash] = true
		set[v.ParentHash] = true
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
