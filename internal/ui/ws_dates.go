package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ws_dates.go is the Dates workspace: the contribution calendar over the
// strategy/params form. Dates is the one "rich" (non-menu) workspace — its
// selection mode is the whole active range — so it keeps the calendar-driven
// scheduling UX that predates the tab chassis.

// viewDatesWorkspace renders zone 3 for the Dates tab: the calendar pane above
// the strategy pane, height-clamped so the right column never drives the frame
// past the terminal (bubbletea tail-clips oversized frames, hiding the title).
func (m Model) viewDatesWorkspace() string {
	right := lipgloss.JoinVertical(lipgloss.Left, m.viewCalendarPane(), m.viewStrategyPane())
	// Clamp to avoid pushing the frame past the terminal height. The tab bar
	// now sits between the title and the body, so the body budget is height-4
	// (title + tabs + status + keybar).
	if avail := m.height - 4; m.height > 0 && lipgloss.Height(right) > avail {
		right = lipgloss.NewStyle().MaxHeight(max(8, avail)).Render(right)
	}
	return right
}

// viewCalendarPane renders the half-year heatmap, legend, and selected-day
// info at its natural (compact) height.
func (m Model) viewCalendarPane() string {
	counts := m.dayCounts()
	cursor := m.calCursor.In(m.loc)
	heat := renderCalendar(cursor, counts, m.quotaStrat().quotas, m.pickedStrat().picked, m.loc, m.st)
	legend := renderLegend(m.st)

	key := cursor.Format(dateLayout)
	dayCount := counts[key]
	quota, hasQuota := m.quotaStrat().quotas[key]
	info := fmt.Sprintf("Selected: %s (%s) · %d commit(s)",
		key, cursor.Weekday().String()[:3], dayCount)
	if hasQuota {
		info += fmt.Sprintf(" · quota %d", quota)
	}
	if m.pickedStrat().picked[key] {
		info += " · picked"
	}

	seq := m.st.statusOK.Render("✓ sequential")
	if !m.plan.IsValid() {
		seq = m.st.statusErr.Render(fmt.Sprintf("✗ %d out-of-order", len(m.plan.Violations)))
	}

	_, _, half := halfRange(cursor, m.loc)
	content := strings.Join([]string{
		m.st.modalTitle.Render(fmt.Sprintf("%d · %s", m.calYear, half)),
		heat,
		"",
		legend,
		info,
		seq,
	}, "\n")

	pane := m.st.pane
	if m.focus == focusCalendar {
		pane = m.st.paneActive
	}
	return pane.Width(rightPaneWidth).Render(content)
}

// viewStrategyPane shows the currently selected strategy with its editable
// parameters. The form's view now renders the carousel as the top row, so the
// pane simply frames the form output. Tab focuses it; enter re-applies.
func (m Model) viewStrategyPane() string {
	// The carousel row in form.view() replaces the old separate title; we keep
	// a compact "STRATEGY" label for the pane title so the panel is still labelled.
	content := m.st.modalTitle.Render("STRATEGY") + "\n" + m.activeStrat().form.view(m.st)

	pane := m.st.pane
	if m.focus == focusParams {
		pane = m.st.paneActive
	}
	// MaxWidth guards against long labels/errors wrapping inside the pane,
	// which would inflate its height.
	return pane.Width(rightPaneWidth).Render(
		lipgloss.NewStyle().MaxWidth(rightPaneWidth - 2).Render(content))
}
