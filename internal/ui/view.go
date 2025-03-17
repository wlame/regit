package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// viewMain composes the main screen: title bar, then the commit list beside
// the right column (calendar over strategy form), then status bar and keybar.
func (m Model) viewMain() string {
	title := m.viewTitle()
	list := m.viewCommitList()
	right := lipgloss.JoinVertical(lipgloss.Left, m.viewCalendarPane(), m.viewStrategyPane())

	// Never let the right column drive the frame past the terminal height:
	// bubbletea tail-clips oversized frames, hiding the title bar.
	if avail := m.height - 3; m.height > 0 && lipgloss.Height(right) > avail {
		right = lipgloss.NewStyle().MaxHeight(max(8, avail)).Render(right)
	}

	var body string
	if dims := computeLayout(m.width, m.height); dims.detailWidth > 0 {
		detail := m.viewDetailPane(dims.detailWidth)
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, detail, right)
	} else {
		body = lipgloss.JoinHorizontal(lipgloss.Top, list, right)
	}
	status := m.viewStatusBar()
	keybar := m.st.keybar.Render(
		"[s]trategy [S]plit [w]reword [e]dit [b]ase [D]rop-bodies [R]etitle [p]review [u]ndo [A]pply [r]eset [tab]focus [?]help [q]uit",
	)

	return lipgloss.JoinVertical(lipgloss.Left, title, body, status, keybar)
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
	return m.st.title.Render(fmt.Sprintf("regit · %s · branch: %s · %s", repoName, branch, count))
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
	body := renderDetail(c, stat, have, m.statErr[c.Hash], m.plan, m.loc, width-2, m.st)
	return m.st.pane.Width(width).Height(m.listHeight() + 1).Render(body)
}

// rightPaneWidth is the lipgloss width (border excluded) shared by the
// calendar and strategy panes: grid gutter + 27 two-char week columns + the
// pane's own padding.
const rightPaneWidth = calGutter + halfCols*cellWidth + 2

// viewCalendarPane renders the half-year heatmap, legend, and selected-day
// info at its natural (compact) height.
func (m Model) viewCalendarPane() string {
	counts := m.dayCounts()
	cursor := m.calCursor.In(m.loc)
	heat := renderCalendar(cursor, counts, m.quotas, m.picked, m.loc, m.st)
	legend := renderLegend(m.st)

	key := cursor.Format(dateLayout)
	dayCount := counts[key]
	quota, hasQuota := m.quotas[key]
	info := fmt.Sprintf("Selected: %s (%s) · %d commit(s)",
		key, cursor.Weekday().String()[:3], dayCount)
	if hasQuota {
		info += fmt.Sprintf(" · quota %d", quota)
	}
	if m.picked[key] {
		info += " · picked"
	}

	seq := m.st.statusOK.Render("✓ sequential")
	if !m.plan.IsValid() {
		seq = m.st.statusErr.Render(fmt.Sprintf("✗ %d out-of-order", len(m.plan.Violations)))
	}

	_, _, half := halfRange(cursor, m.loc)
	content := strings.Join([]string{
		m.st.modalTitle.Render(fmt.Sprintf("%d · %s  [ ◀ ] [ ▶ ]", m.calYear, half)),
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
// parameters. Tab focuses it; enter re-applies with the current values.
func (m Model) viewStrategyPane() string {
	content := m.st.modalTitle.Render("STRATEGY") + "\n" + m.form.view(m.st)

	pane := m.st.pane
	if m.focus == focusParams {
		pane = m.st.paneActive
	}
	// MaxWidth guards against long labels/errors wrapping inside the pane,
	// which would inflate its height.
	return pane.Width(rightPaneWidth).Render(
		lipgloss.NewStyle().MaxWidth(rightPaneWidth - 2).Render(content))
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

func (m Model) viewConfirm() string {
	var b strings.Builder
	b.WriteString(m.st.modalTitle.Render("Confirm rewrite") + "\n\n")
	b.WriteString(m.st.statusWarn.Render(m.confirmGuard.ConfirmReason) + "\n\n")
	b.WriteString(fmt.Sprintf("Type the branch name (%s) to proceed:\n\n", m.st.newDate.Render(m.confirmGuard.ConfirmWord)))
	b.WriteString(m.confirmInput.View() + "\n\n")
	b.WriteString(m.st.help.Render("enter confirm · esc cancel"))
	return m.st.modal.Render(b.String())
}
