package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// workspace.go renders the fixed category tab bar and handles moving between
// tabs. Both are driven by the workspaces table (feature.go), so the bar and the
// navigation can never disagree with the registry.

// viewTabBar renders the tabs in registry order: the active tab is highlighted,
// disabled (v1-stub) tabs are dimmed, and a trailing hint shows the switch keys.
// The set is bounded, so this single line never overflows regardless of how many
// features exist.
func (m Model) viewTabBar() string {
	sep := m.st.dim.Render(" │ ")
	parts := make([]string, 0, len(workspaces))
	for _, w := range workspaces {
		switch {
		case w.ID == m.workspace:
			parts = append(parts, m.st.selected.Render(" "+w.Title+" "))
		case !w.Enabled:
			parts = append(parts, m.st.dim.Render(w.Title))
		default:
			parts = append(parts, m.st.help.Render(w.Title))
		}
	}
	return strings.Join(parts, sep) + m.st.dim.Render("    < > switch")
}

// nextWorkspace / prevWorkspace move the active tab forward / back in registry
// order, wrapping at the ends. Cycling intentionally lands on disabled stub tabs
// (which then show a "coming soon" panel) so the chassis's full shape is visible.
func (m *Model) nextWorkspace() { m.cycleWorkspace(1) }
func (m *Model) prevWorkspace() { m.cycleWorkspace(-1) }

// cycleWorkspace shifts the active tab by delta (wrapping) and resets focus to
// the commit list and the menu cursor to the top, so the user never lands in a
// zone the new workspace does not render. An EXPLICIT selection (a non-empty
// Set or a Range) is deliberately preserved across the switch — the selection
// is the subject of the user's next action and navigation must never drop it;
// only with no explicit selection does the workspace's default mode apply.
// The idx assignment above lands on the NEW workspace before the call below,
// so onDatesSelectionChanged re-syncs pins/staging when the switch lands on
// Dates and is a no-op for every other destination.
func (m *Model) cycleWorkspace(delta int) {
	n := len(workspaces)
	idx := 0
	for i, w := range workspaces {
		if w.ID == m.workspace {
			idx = i
			break
		}
	}
	idx = (idx + delta + n) % n
	m.workspace = workspaces[idx].ID
	m.focus = focusList
	m.wsMenuCursor = 0
	if !m.hasExplicitSelection() {
		m.sel.reset()
		m.applyDefaultSelectionMode()
	}
	m.onDatesSelectionChanged()
}

// viewRightColumn renders zone 3 — the active workspace's widget with the AUTHORS
// panel below it — and clamps the whole column to the terminal height. Dates
// shows the calendar + strategy form; Restructure and Authorship show their
// shared feature menu; the v1-stub tabs show a "coming soon" panel. The commit
// backbone (list + detail) stays put across tabs; only this column swaps.
//
// The widget gets priority; the AUTHORS panel takes whatever rows remain (and
// is omitted entirely when too few remain), so it never pushes the workspace
// widget or the frame off-screen. The body budget starts at height-4 (title +
// tabs + status + keybar) and, like listHeight (list.go), is further reduced
// when the log footer or the bottom action panel is open — those are new
// regions stacked BELOW this one, so without this deduction this column would
// keep claiming its full un-shrunk share while the frame grew underneath it,
// pushing the whole UI past the terminal instead of the two columns (this one
// and the commit list) shrinking together to make room.
func (m Model) viewRightColumn() string {
	var widget string
	switch m.workspace {
	case WSDates:
		widget = m.viewDatesWorkspace()
	case WSRestructure, WSAuthorship:
		widget = m.viewFeatureMenu()
	default:
		widget = m.viewStubWorkspace()
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

// viewStubWorkspace renders a disabled (not-yet-built) workspace: its name and a
// "coming soon" note. The stub tabs exist so the chassis's extensibility is real
// and the three-zone layout is exercised before the features land.
func (m Model) viewStubWorkspace() string {
	meta, _ := workspaceByID(m.workspace)
	body := m.st.modalTitle.Render(strings.ToUpper(meta.Title)) + "\n\n" +
		m.st.dim.Render("Coming soon.") + "\n" +
		m.st.dim.Render("This tab is part of the chassis;") + "\n" +
		m.st.dim.Render("its features are planned for a later release.")
	return m.st.pane.Width(rightPaneWidth).Render(body)
}
