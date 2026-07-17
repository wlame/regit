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
	if m.height <= 0 {
		// No size yet (pre-first-WindowSizeMsg): there is no real budget to
		// size the AUTHORS pane against, so just render the widget at its
		// usual (unshrunk) size, matching every other pane pre-size.
		switch m.flow {
		case flowRedate:
			return m.viewDatesWorkspace()
		case flowRedact:
			return m.viewRedactFlow()
		}
		return m.viewDetailPane(computeLayout(m.width, m.height).flowWidth, m.listHeight()+1)
	}

	budget := m.height - 3
	if m.logVisible {
		budget -= logPanelHeight
	}
	if m.panel.open {
		budget -= panelHeightFor(m.panelWindowRows(len(m.panelRows())))
	}
	if budget < minListRows {
		budget = minListRows
	}

	var widget string
	switch m.flow {
	case flowRedate:
		widget = m.viewDatesWorkspace()
	case flowRedact:
		widget = m.viewRedactFlow()
	default:
		widget = m.viewDetailPaneForZone(computeLayout(m.width, m.height).flowWidth, budget)
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

// detailPaneMinHeight is the fewest content rows (border excluded) the
// commit-detail pane keeps when viewDetailPaneForZone shrinks it to seat the
// AUTHORS pane below: enough for the full header block (identity, dates,
// diffstat — 8 rows), a spacer, and the commit subject (renderDetail, detail.go),
// with a little room to spare for a file line or two. Below this floor the
// detail pane keeps the whole zone budget instead, same as before, and AUTHORS
// stays absent — the same clamping philosophy as a short terminal.
const detailPaneMinHeight = 13

// viewDetailPaneForZone sizes the commit-detail pane for the flow zone's
// default (no-flow) arm. Detail keeps priority: it renders at its usual height,
// matched to the list pane (viewDetailPane's normal m.listHeight()+1), unless
// the zone budget has enough slack beyond detailPaneMinHeight to also reserve
// exactly minAuthorsPaneHeight rows for the AUTHORS pane below it — mirroring
// the redate arm, where AUTHORS gets whatever the widget doesn't need — in
// which case detail gives up exactly those rows and no more.
func (m Model) viewDetailPaneForZone(width, budget int) string {
	full := m.listHeight() + 1
	shrunk := budget - 2 /* pane border */ - minAuthorsPaneHeight
	height := full
	if shrunk >= detailPaneMinHeight && shrunk < full {
		height = shrunk
	}
	return m.viewDetailPane(width, height)
}
