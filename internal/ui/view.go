package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// viewMain composes the main screen: title bar, the commit list beside the
// flow zone (the active flow's widget, or the commit detail when none is
// open), then the status bar and keybar.
func (m Model) viewMain() string {
	title := m.viewTitle()

	var body string
	switch dims := m.layout(); {
	case !dims.showRight && m.flowOwnsNarrowBody():
		body = m.viewFlowZone()
	case !dims.showRight:
		// Too narrow for two columns and no flow in front: the list alone.
		// Detail is still reachable via the '.' overlay at this width.
		body = m.viewCommitList()
	default:
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.viewCommitList(), m.viewFlowZone())
	}
	status := m.viewStatusBar()
	keybar := m.st.keybar.Render(keybarHintFor(m.currentKeyCtx(), m.width))
	regions := []string{title, body}
	if m.panel.open {
		regions = append(regions, m.viewActionPanel(m.width))
	}
	regions = append(regions, status)
	if log := m.viewCommandLog(); log != "" {
		regions = append(regions, log)
	}
	regions = append(regions, keybar)
	// Hard-clamp every region to the terminal width. The chrome rows (title,
	// status, keybar) are single strings whose natural width can exceed a narrow
	// terminal; without this, JoinVertical pads the whole frame out to the widest
	// row and the UI overflows horizontally. MaxWidth truncates ANSI-aware, per line.
	if m.width > 0 {
		clamp := lipgloss.NewStyle().MaxWidth(m.width)
		for i, r := range regions {
			regions[i] = clamp.Render(r)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, regions...)
}

// flowOwnsNarrowBody reports whether a one-column terminal shows the open flow
// instead of the commit list. The focused pane wins: a flow can hand focus
// back to the list while it stays open (tab or esc from the strategy pane, or
// staging a strategy so A applies it), and list keys must then move a list the
// user can see. tab switches between the two.
func (m Model) flowOwnsNarrowBody() bool {
	return m.flow != flowNone && m.focus != focusList
}

func (m Model) viewTitle() string {
	branch := m.state.Branch
	if branch == "" {
		branch = "(detached)"
	}
	repoName := m.repo.Root()
	if i := strings.LastIndex(repoName, "/"); i >= 0 {
		repoName = repoName[i+1:]
	}
	count := fmt.Sprintf("%d commits", len(m.plan.Commits))
	if m.plan.Base > 0 {
		count = fmt.Sprintf("editing %d/%d commits", len(m.plan.Active()), len(m.plan.Commits))
	}
	base := "none"
	if m.baseBranch != "" {
		base = m.baseBranch
	}
	return m.st.title.Render(fmt.Sprintf("regit · %s · branch: %s ▾ · base: %s ▾ · %s",
		repoName, branch, base, count))
}

// listWidth returns the commit pane's lipgloss width (border excluded). The
// sizing math lives in computeLayout (layout.go) so the list grows to fill a
// wide terminal — up to listCeilWidth — instead of the old fixed 66-column cap.
func (m Model) listWidth() int {
	return m.layout().listWidth
}

// viewDetailPane frames the commit-detail panel for the cursor commit at the
// given width and box content height (border excluded). Callers typically pass
// m.listHeight()+1 to match the list pane's own height; the flow zone's default
// arm instead sizes it via viewDetailPaneForZone so the AUTHORS pane can seat
// below it. The body comes from the pure renderDetail; this method only
// supplies state (the cached stat) and the frame.
func (m Model) viewDetailPane(width, height int) string {
	if len(m.plan.Commits) == 0 {
		return m.st.pane.Width(width).Height(height).Render("")
	}
	c := m.commitAtCursor()
	stat, have := m.statCache[c.Hash]
	body := renderDetail(c, stat, have, m.statErr[c.Hash], m.plan, m.loc, width-2,
		height-1, m.st, m.authorColorStyle(c))
	return m.st.pane.Width(width).Height(height).Render(body)
}

// rightPaneWidth is the lipgloss width (border excluded) shared by the
// calendar and strategy panes: grid gutter + 27 two-char week columns + the
// pane's own padding.
const rightPaneWidth = calGutter + halfCols*cellWidth + 2

// logPanelHeight is the number of terminal rows the command-log footer
// occupies when visible: one header line plus up to 5 recent command lines.
const logPanelHeight = 6

// viewCommandLog renders the bottom transparency panel showing the most recent
// CLI-equivalent commands for applied actions. Returns an empty string when
// the panel is hidden (logVisible is false), so the caller can skip it cleanly.
func (m Model) viewCommandLog() string {
	if !m.logVisible {
		return ""
	}
	header := m.st.dim.Render("⌥ command log — copy to run headlessly  (toggle: [L])")
	body := m.st.dim.Render("  (no actions yet)")
	if n := len(m.commandLog); n > 0 {
		// Show only the most recent entries that fit within the panel height
		// (one row is consumed by the header).
		start := 0
		if n > logPanelHeight-1 {
			start = n - (logPanelHeight - 1)
		}
		body = strings.Join(m.commandLog[start:], "\n")
	}
	return header + "\n" + body
}

func (m Model) viewStatusBar() string {
	if m.statusMsg == "" {
		return m.st.dim.Render(" ")
	}
	switch m.statusKind {
	case statusOK:
		return m.st.statusOK.Render("● " + m.statusMsg)
	case statusWarn:
		return m.st.statusWarn.Render("● " + m.statusMsg)
	case statusErr:
		return m.st.statusErr.Render("● " + m.statusMsg)
	default:
		return m.st.dim.Render(m.statusMsg)
	}
}

// viewDropBodies renders the drop-bodies confirmation modal: how many commits'
// bodies would be removed (titles kept) across the affected scope, plus the
// apply/cancel footer. It mirrors the retitle modal's confirmation style.
func (m Model) viewDropBodies() string {
	var b strings.Builder
	b.WriteString(m.st.modalTitle.Render("Drop commit bodies") + "\n\n")
	scope := "the editable range"
	if m.hasExplicitSelection() {
		scope = "the selection"
	}
	n := m.dropBodiesCount()
	if n == 0 {
		b.WriteString(m.st.dim.Render("No bodies to drop in "+scope) + "\n")
	} else {
		b.WriteString(m.st.statusOK.Render(fmt.Sprintf("Will drop the body of %d commit(s) — titles kept", n)) + "\n")
		b.WriteString(m.st.dim.Render("Across "+scope) + "\n")
	}
	if n == 0 {
		b.WriteString("\n" + m.st.help.Render("esc close"))
	} else {
		b.WriteString("\n" + m.st.help.Render("enter apply · esc cancel"))
	}
	return m.st.modal.Render(b.String())
}

func (m Model) viewConfirm() string {
	var b strings.Builder
	b.WriteString(m.st.modalTitle.Render("Confirm rewrite") + "\n\n")
	b.WriteString(m.st.statusWarn.Render(m.confirmGuard.ConfirmReason) + "\n\n")
	b.WriteString(fmt.Sprintf("Type the branch name (%s) to proceed:\n\n", m.st.newDate.Render(m.confirmGuard.ConfirmWord)))
	b.WriteString(m.confirmInput.View() + "\n\n")
	b.WriteString(m.st.help.Render("enter confirm · esc cancel"))
	return m.st.modal.Render(b.String())
}
