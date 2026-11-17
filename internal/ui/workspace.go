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
		return m.viewDetailPane(m.layout().flowWidth, m.listHeight()+1)
	}

	budget := m.flowZoneRows()

	if m.zoneWizardOpen() {
		return m.viewZoneWizard(budget)
	}
	var widget string
	switch m.flow {
	case flowRedate:
		widget = m.viewDatesWorkspace()
	case flowRedact:
		widget = m.viewRedactFlow()
	default:
		widget = m.viewDetailPaneForZone(m.layout().flowWidth, budget)
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

// flowZoneRows is the row budget of the flow zone, borders included: the
// terminal height minus the title, status, and keybar rows, and minus the
// command log and the action panel while they are open.
func (m Model) flowZoneRows() int {
	budget := m.height - 3
	if m.logVisible {
		budget -= logPanelHeight
	}
	if m.panel.open {
		budget -= panelHeightFor(m.panelWindowRows(len(m.panelRows())))
	}
	return max(budget, minListRows)
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
// default (no-flow) arm, leaving the rest of the zone budget to the AUTHORS
// pane below it (viewFlowZone hands AUTHORS whatever this pane does not use).
// Both panes hold a list that shortens itself ("… +N more files" / "… +N
// more"), so neither may starve the other:
//   - AUTHORS is promised a share: all its authors, up to a third of the zone;
//   - the detail pane yields down to that share even when its content is
//     longer, and stretches when AUTHORS cannot use the rows;
//   - the detail pane never drops below detailPaneMinHeight, nor grows taller
//     than the list pane beside it.
func (m Model) viewDetailPaneForZone(width, budget int) string {
	const paneBorderRows = 2
	full := m.listHeight() + 1
	authorsNatural := m.authorsPaneNaturalHeight()
	authorsShare := min(authorsNatural, max(minAuthorsPaneHeight, budget/3))
	detailNeed := max(m.detailContentHeight(width, full), detailPaneMinHeight)

	height := min(detailNeed, budget-paneBorderRows-authorsShare)
	height = max(height, budget-paneBorderRows-authorsNatural)
	height = max(height, detailPaneMinHeight)
	return m.viewDetailPane(width, min(height, full))
}

// detailContentHeight is the pane height (border excluded) the cursor commit's
// detail needs to show without clamping, capped at limit. It mirrors
// viewDetailPane, which gives the body one row less than the pane height.
func (m Model) detailContentHeight(width, limit int) int {
	if len(m.plan.Commits) == 0 {
		return 0
	}
	c := m.commitAtCursor()
	stat, have := m.statCache[c.Hash]
	body := renderDetail(c, stat, have, m.statErr[c.Hash], m.plan, m.loc, width-2,
		limit-1, m.st, m.authorColorStyle(c))
	return lipgloss.Height(body) + 1
}

// authorsPaneNaturalHeight is the AUTHORS pane's height with every author
// listed: the border, the title row, and one row per author.
func (m Model) authorsPaneNaturalHeight() int {
	if len(m.contribs) == 0 {
		return 0
	}
	return 2 /* border */ + 1 /* title */ + len(m.contribs)
}
