package ui

import (
	"github.com/charmbracelet/lipgloss"
)

// workspace.go renders the flow zone: the active flow's widget (or the commit
// detail when no flow is open) with the AUTHORS panel below whatever rows
// remain.

// viewFlowZone renders the right column: the active flow's widget, or the
// commit detail when no flow is open, with the AUTHORS panel below whatever
// rows remain (widget priority, unchanged from the tab era).
func (m Model) viewFlowZone() string {
	var widget string
	switch m.flow {
	case flowRedate:
		widget = m.viewDatesWorkspace()
	default:
		widget = m.viewDetailPane(computeLayout(m.width, m.height).flowWidth)
	}
	if m.height <= 0 {
		return widget // no size yet (pre-first-WindowSizeMsg)
	}

	budget := m.height - 4
	if m.logVisible {
		budget -= logPanelHeight
	}
	if m.panel.open {
		budget -= panelHeightFor(m.panelWindowRows(len(m.panelRows())))
	}
	if budget < minListRows {
		budget = minListRows
	}
	widgetH := lipgloss.Height(widget)
	if widgetH >= budget {
		// No room for the AUTHORS panel: clamp the widget alone, exactly as
		// before. budget is already floored at minListRows above, so clamping
		// to it directly (no separate 8-row floor) lets this column actually
		// shrink when the log/action panel below it needs the room — an extra
		// floor here would silently re-inflate this column past what was
		// deducted for them and reopen the overflow this budget exists to
		// prevent.
		return lipgloss.NewStyle().MaxHeight(budget).Render(widget)
	}
	pane := m.viewAuthorsPane(budget - widgetH)
	col := widget
	if pane != "" {
		col = lipgloss.JoinVertical(lipgloss.Left, widget, pane)
	}
	return lipgloss.NewStyle().MaxHeight(budget).Render(col)
}
