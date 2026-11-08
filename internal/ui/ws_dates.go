package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/strategy"
)

// ws_dates.go is the redate flow's widget: the contribution calendar over the
// strategy/params form. Its selection mode is the whole active range, so it
// keeps the calendar-driven scheduling UX that predates the tab chassis.

// viewDatesWorkspace renders the redate flow's widget: the calendar pane above
// the strategy pane. Height clamping for the whole flow zone (this widget plus
// the AUTHORS panel) is handled by viewFlowZone.
func (m Model) viewDatesWorkspace() string {
	return lipgloss.JoinVertical(lipgloss.Left, m.viewCalendarPane(), m.viewStrategyPane())
}

// viewCalendarPane renders the half-year heatmap, legend, and selected-day
// info at its natural (compact) height. Calendar overlays (quota pins, picked
// days) reflect the ACTIVE strategy's pins so only the relevant strategy's
// choices are visible; non-pin strategies have empty maps → no overlay.
func (m Model) viewCalendarPane() string {
	counts := m.dayCounts()
	cursor := m.calCursor.In(m.loc)
	a := m.activeStrat()

	// avail flags every day inside the current selection's segment windows, so
	// the heatmap tints the pickable range. With no explicit selection the
	// whole active range is available and no tint is drawn (avail stays nil).
	var avail map[string]bool
	var wins []strategy.Window
	if m.hasExplicitSelection() {
		wins = m.segmentWindows()
		avail = map[string]bool{}
		rs, re, _ := halfRange(cursor, m.loc)
		for d := rs; !d.After(re); d = d.AddDate(0, 0, 1) {
			for _, w := range wins {
				if w.ContainsDay(d, m.loc) {
					avail[d.Format(dateLayout)] = true
					break
				}
			}
		}
	}
	heat := renderCalendar(cursor, counts, a.quotas, a.picked, avail, m.loc, m.st, m.strategyStaged())
	legend := renderLegend(m.st)

	key := cursor.Format(dateLayout)
	dayCount := counts[key]
	quota, hasQuota := a.quotas[key]
	info := fmt.Sprintf("Selected: %s (%s) · %d commit(s)",
		key, cursor.Weekday().String()[:3], dayCount)
	if hasQuota {
		info += fmt.Sprintf(" · quota %d", quota)
	}
	if a.picked[key] {
		info += " · picked"
	}
	if segIdxs := strategy.SegmentsForDay(wins, cursor, m.loc); len(wins) > 0 && len(segIdxs) > 0 {
		// The cursor day can belong to at most two adjacent segments (a shared
		// boundary day); segIdxs[0] is the earlier one, matching how pins on a
		// shared day are attributed (see strategy.AttributePins).
		si := segIdxs[0]
		picksInSeg := 0
		for k := range a.picked {
			d, err := time.ParseInLocation(dateLayout, k, m.loc)
			if err == nil && wins[si].ContainsDay(d, m.loc) {
				picksInSeg++
			}
		}
		info += fmt.Sprintf(" · window %d/%d · %d commit(s) · %d picked",
			si+1, len(wins), wins[si].Size, picksInSeg)
	}

	seq := m.st.statusOK.Render("✓ sequential")
	if !m.plan.IsValid() {
		seq = m.st.statusWarn.Render(fmt.Sprintf("⚠ %d conflicting", len(m.plan.Shifted)))
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
	// The calendar no longer dictates the flow zone's width: it renders at its
	// natural width when the column is wide enough, else clamps down to
	// whatever the layout gave the zone (flowWidth, layout.go).
	width := min(rightPaneWidth, m.layout().flowWidth)
	return withBorderTitle(pane.Width(width).Render(content), pane, m.paneTitle("CALENDAR", "", m.focus == focusCalendar))
}

// viewStrategyPane shows the currently selected strategy with its editable
// parameters. The form's view now renders the carousel as the top row, so the
// pane simply frames the form output. Tab focuses it; enter re-applies.
func (m Model) viewStrategyPane() string {
	content := m.activeStrat().form.view(m.st)

	pane := m.st.pane
	if m.focus == focusParams {
		pane = m.st.paneActive
	}
	// The strategy pane shares the calendar's width policy: natural width
	// (rightPaneWidth) when the flow zone is wide enough, else clamped down to
	// it (layout.go's flowWidth).
	width := min(rightPaneWidth, m.layout().flowWidth)
	// MaxWidth guards against long labels/errors wrapping inside the pane,
	// which would inflate its height.
	rendered := pane.Width(width).Render(lipgloss.NewStyle().MaxWidth(width - 2).Render(content))
	return withBorderTitle(rendered, pane, m.paneTitle("STRATEGY", "", m.focus == focusParams))
}
