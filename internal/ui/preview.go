package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/wlame/regit/internal/model"
)

// viewPreview renders a scrollable before/after table of every commit's dates.
// Rows that are part of a sequence violation are flagged in red. The table shows
// commits newest-first to match the main list.
func viewPreview(plan *model.Plan, loc *time.Location, offset, visible int, st styles) string {
	violation := violationSet(plan)

	var b strings.Builder
	b.WriteString(st.modalTitle.Render("Preview — original → new") + "\n\n")

	type row struct {
		text    string
		bad     bool
		changed bool
	}
	var rows []row
	// Iterate newest-first for display.
	for i := len(plan.Commits) - 1; i >= 0; i-- {
		c := plan.Commits[i]
		orig := c.OrigAuthorDate.In(loc).Format("2006-01-02 15:04")
		neu := plan.EffectiveAuthorDate(c).In(loc).Format("2006-01-02 15:04")
		_, changed := plan.EditFor(c.Hash)
		arrow := "  =  "
		if changed {
			arrow = " → "
		}
		subj := truncate(c.Subject, 32)
		text := fmt.Sprintf("%s  %-32s %s%s%s", short(c.Hash), subj, orig, arrow, neu)
		rows = append(rows, row{text: text, bad: violation[c.Hash], changed: changed})
	}

	end := offset + visible
	if end > len(rows) {
		end = len(rows)
	}
	if offset > len(rows) {
		offset = len(rows)
	}
	for _, r := range rows[offset:end] {
		switch {
		case r.bad:
			b.WriteString(st.statusErr.Render("✗ "+r.text) + "\n")
		case r.changed:
			b.WriteString(st.newDate.Render("  "+r.text) + "\n")
		default:
			b.WriteString(st.dim.Render("  "+r.text) + "\n")
		}
	}

	summary := fmt.Sprintf("%d edited · %d violations", len(plan.Edits), len(plan.Violations))
	b.WriteString("\n" + st.help.Render(summary+"  ·  ↑/↓ scroll · esc close"))
	return st.modal.Render(b.String())
}

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
