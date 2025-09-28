package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ws_restructure.go is the Restructure workspace: a vertical, scrollable menu of
// history-editing features derived from the registry. Features grow DOWN as menu
// rows, never as new tabs, so the action surface is unbounded while the tab bar
// stays fixed. Each row re-invokes an existing action method via the feature's
// Run, so the menu adds no new mutation path.

// viewFeatureMenu renders the feature menu for any menu workspace (Restructure,
// Authorship, etc.). The title uses the active workspace's name, so the renderer
// works correctly for both tabs without hard-coding names. The squash special-case
// (showing chain statistics in the title) fires only when the highlighted row's
// name contains "squash", so it remains workspace-agnostic.
func (m Model) viewFeatureMenu() string {
	feats := featuresFor(m.workspace)
	meta, _ := workspaceByID(m.workspace)
	var b strings.Builder
	title := fmt.Sprintf("%s · %d commit(s)", strings.ToUpper(meta.Title), len(m.affectedCommits()))
	if i := m.wsMenuCursor; m.focus == focusWorkspace && i >= 0 && i < len(feats) &&
		strings.Contains(strings.ToLower(feats[i].Name), "squash") {
		chains, ignored := m.affectedChains()
		squashed := 0
		for _, ch := range chains {
			squashed += len(ch.Members)
		}
		title = fmt.Sprintf("%s · %d commit(s) into %d commit(s)", strings.ToUpper(meta.Title), squashed, len(chains))
		if len(ignored) > 0 {
			title += fmt.Sprintf(" · %d ignored", len(ignored))
		}
	}
	b.WriteString(m.st.modalTitle.Render(title) + "\n\n")
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
