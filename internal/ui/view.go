package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// viewMain composes the three-pane main screen: title bar, then the commit list
// beside the calendar, then the status bar and keybar.
func (m Model) viewMain() string {
	title := m.viewTitle()
	list := m.viewCommitList()
	cal := m.viewCalendarPane()

	body := lipgloss.JoinHorizontal(lipgloss.Top, list, cal)
	status := m.viewStatusBar()
	keybar := m.st.keybar.Render(
		"[s]trategy [e]dit [p]review [u]ndo [A]pply [r]eset [g]quotas [tab]focus [?]help [q]uit",
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
	return m.st.title.Render(fmt.Sprintf("ghist · %s · branch: %s · %d commits", repoName, branch, len(m.plan.Commits)))
}

// viewCommitList renders the scrollable, newest-first commit list with original
// and (when edited) new dates.
func (m Model) viewCommitList() string {
	width := 44
	height := m.listHeight()

	var b strings.Builder
	header := fmt.Sprintf("COMMITS (%d)", len(m.plan.Commits))
	b.WriteString(m.st.modalTitle.Render(header) + "\n")

	m.ensureCursorVisible(height)
	end := m.listOffset + height
	if end > len(m.plan.Commits) {
		end = len(m.plan.Commits)
	}

	for i := m.listOffset; i < end; i++ {
		c := m.commitAtDisplayIndex(i)
		cursor := "  "
		if i == m.listCursor {
			cursor = m.st.listCursor.Render("▸ ")
		}
		marker := " "
		if e, ok := m.plan.EditFor(c.Hash); ok && e.DropBody {
			marker = m.st.marker.Render("⚑")
		}
		subj := truncate(c.Subject, 32)
		b.WriteString(fmt.Sprintf("%s%s %s %s\n", cursor, short(c.Hash), marker, subj))

		orig := c.OrigAuthorDate.In(m.loc).Format("01-02 15:04")
		if _, ok := m.plan.EditFor(c.Hash); ok {
			neu := m.plan.EffectiveAuthorDate(c).In(m.loc).Format("01-02 15:04")
			b.WriteString(fmt.Sprintf("    %s %s %s\n",
				m.st.origDate.Render(orig), m.st.dim.Render("→"), m.st.newDate.Render(neu)))
		} else {
			b.WriteString("    " + m.st.origDate.Render(orig) + "\n")
		}
	}

	pane := m.st.pane
	if m.focus == focusList {
		pane = m.st.paneActive
	}
	return pane.Width(width).Height(height + 1).Render(b.String())
}

// viewCalendarPane renders the heatmap, legend, and the selected-day info box.
func (m Model) viewCalendarPane() string {
	counts := m.dayCounts()
	heat := renderCalendar(m.calYear, counts, m.calCursor, m.loc, m.st)
	legend := renderLegend(m.st)

	key := m.calCursor.Format("2006-01-02")
	dayCount := counts[key]
	quota, hasQuota := m.quotas[key]
	info := fmt.Sprintf("Selected: %s (%s) · %d commit(s)",
		key, m.calCursor.Weekday().String()[:3], dayCount)
	if hasQuota {
		info += fmt.Sprintf(" · quota %d", quota)
	}

	seq := m.st.statusOK.Render("✓ sequential")
	if !m.plan.IsValid() {
		seq = m.st.statusErr.Render(fmt.Sprintf("✗ %d out-of-order", len(m.plan.Violations)))
	}

	content := strings.Join([]string{
		m.st.modalTitle.Render(fmt.Sprintf("%d  [ ◀ ] [ ▶ ]", m.calYear)),
		"",
		heat,
		"",
		legend,
		"",
		info,
		seq,
	}, "\n")

	pane := m.st.pane
	if m.focus == focusCalendar {
		pane = m.st.paneActive
	}
	return pane.Height(m.listHeight() + 1).Render(content)
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

// listHeight returns how many rows the list pane gets given the terminal size.
// Each commit takes two lines, so this is generous but bounded.
func (m Model) listHeight() int {
	if m.height < 16 {
		return 8
	}
	rows := m.height - 8
	if rows > len(m.plan.Commits)*2 {
		rows = max(8, len(m.plan.Commits)*2)
	}
	return rows
}

// ensureCursorVisible scrolls the list so the cursor stays within the viewport.
func (m *Model) ensureCursorVisible(height int) {
	if m.listCursor < m.listOffset {
		m.listOffset = m.listCursor
	}
	if m.listCursor >= m.listOffset+height {
		m.listOffset = m.listCursor - height + 1
	}
	if m.listOffset < 0 {
		m.listOffset = 0
	}
}
