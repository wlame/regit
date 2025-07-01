package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ws_restructure.go is the Restructure workspace: a vertical, scrollable menu of
// history-editing features derived from the registry. This is how req #7 is met —
// features grow DOWN as menu rows, never as new tabs, so the action surface is
// unbounded while the tab bar stays fixed. Each row re-invokes an existing action
// method via the feature's Run; Phase 3 adds no new mutation path.

// viewRestructureMenu renders the feature menu for the Restructure tab. The
// highlighted row is shown only while the menu is focused, so the user can see
// what tab they are on without the menu stealing visual focus from the list.
func (m Model) viewRestructureMenu() string {
	feats := featuresFor(WSRestructure)
	var b strings.Builder
	b.WriteString(m.st.modalTitle.Render("RESTRUCTURE") + "\n\n")
	for i, f := range feats {
		if m.focus == focusWorkspace && i == m.wsMenuCursor {
			b.WriteString(m.st.selected.Render("▸ "+f.Name) + "\n")
		} else {
			b.WriteString("  " + m.st.dim.Render(f.Name) + "\n")
		}
	}
	b.WriteString("\n" + m.st.help.Render("enter run · tab back · ↑/↓ move"))

	pane := m.st.pane
	if m.focus == focusWorkspace {
		pane = m.st.paneActive
	}
	return pane.Width(rightPaneWidth).Render(b.String())
}

// updateWorkspaceMenu drives a menu workspace while it is focused: up/down move
// the cursor, enter runs the highlighted feature, tab/esc return to the commit
// list. Any other key falls through to the global action keys, so S/w/D/R/A/etc.
// keep working from the menu too (the menu is an extra surface, not a mode lock).
func (m Model) updateWorkspaceMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	feats := featuresFor(m.workspace)
	switch msg.String() {
	case "tab", "esc":
		m.focus = focusList
		return m, nil
	case "up", "k":
		if m.wsMenuCursor > 0 {
			m.wsMenuCursor--
		}
		return m, nil
	case "down", "j":
		if m.wsMenuCursor < len(feats)-1 {
			m.wsMenuCursor++
		}
		return m, nil
	case "enter":
		if m.wsMenuCursor >= 0 && m.wsMenuCursor < len(feats) {
			return feats[m.wsMenuCursor].Run(m)
		}
		return m, nil
	}
	if model, cmd, ok := m.globalAction(msg); ok {
		return model, cmd
	}
	return m, nil
}
