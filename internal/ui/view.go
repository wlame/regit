package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// viewMain composes the main screen: title bar, the category tab bar, the commit
// backbone (list + detail) beside the active workspace's widget, then the status
// bar and keybar.
func (m Model) viewMain() string {
	title := m.viewTitle()
	tabs := m.viewTabBar()
	list := m.viewCommitList()
	right := m.viewRightColumn()

	var body string
	if dims := computeLayout(m.width, m.height); dims.detailWidth > 0 {
		detail := m.viewDetailPane(dims.detailWidth)
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, detail, right)
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, right)
	}
	status := m.viewStatusBar()
	keybar := m.st.keybar.Render(keybarHint())

	return lipgloss.JoinVertical(lipgloss.Left, title, tabs, body, status, keybar)
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
	return computeLayout(m.width, m.height).listWidth
}

// viewDetailPane frames the commit-detail panel for the cursor commit at the
// given width, matched to the list pane's height. The body comes from the pure
// renderDetail; this method only supplies state (the cached stat) and the frame.
func (m Model) viewDetailPane(width int) string {
	if len(m.plan.Commits) == 0 {
		return m.st.pane.Width(width).Height(m.listHeight() + 1).Render("")
	}
	c := m.commitAtCursor()
	stat, have := m.statCache[c.Hash]
	body := renderDetail(c, stat, have, m.statErr[c.Hash], m.plan, m.loc, width-2, m.listHeight(), m.st)
	return m.st.pane.Width(width).Height(m.listHeight() + 1).Render(body)
}

// rightPaneWidth is the lipgloss width (border excluded) shared by the
// calendar and strategy panes: grid gutter + 27 two-char week columns + the
// pane's own padding.
const rightPaneWidth = calGutter + halfCols*cellWidth + 2

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
	if (m.sel.mode == SelSet && len(m.sel.set) > 0) || m.sel.mode == SelRange {
		scope = "the selection"
	}
	if n := m.dropBodiesCount(); n == 0 {
		b.WriteString(m.st.dim.Render("No bodies to drop in "+scope) + "\n")
	} else {
		b.WriteString(m.st.statusOK.Render(fmt.Sprintf("Will drop the body of %d commit(s) — titles kept", n)) + "\n")
		b.WriteString(m.st.dim.Render("Across "+scope) + "\n")
	}
	b.WriteString("\n" + m.st.help.Render("enter apply · esc cancel"))
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
