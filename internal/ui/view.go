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

	body := lipgloss.JoinHorizontal(lipgloss.Top, list, right)
	status := m.viewStatusBar()
	keybar := m.st.keybar.Render(
		"[s]trategy [e]dit [b]ase [D]rop-bodies [R]etitle [p]review [u]ndo [A]pply [r]eset [tab]focus [?]help [q]uit",
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

// Fixed columns of a commit entry line, before the subject starts:
// cursor(2) + date(11) + gap(2) + hash(8) + gap(1) + marker(1) + gap(1).
const listEntryFixedCols = 26

// viewCommitList renders the scrollable, newest-first commit list. Every
// commit occupies exactly one line — effective date, short hash, subject —
// with the subject truncated to the pane so lines can never wrap. A wrapped
// line would inflate the entry height and overflow the terminal.
func (m Model) viewCommitList() string {
	width := m.listWidth()
	height := m.listHeight()
	// lipgloss .Width() includes the pane's 1-column padding on each side.
	inner := width - 2
	visible := max(1, height-1) // the COMMITS header takes one line

	var b strings.Builder
	header := fmt.Sprintf("COMMITS (%d)", len(m.plan.Commits))
	b.WriteString(m.st.modalTitle.Render(header) + "\n")

	m.ensureCursorVisible(visible)
	end := m.listOffset + visible
	if end > len(m.plan.Commits) {
		end = len(m.plan.Commits)
	}

	// While the calendar is focused, commits landing on its cursor day get a
	// whole-row highlight so calendar navigation lights up the list.
	calKey := ""
	if m.focus == focusCalendar {
		calKey = m.calCursor.In(m.loc).Format(dateLayout)
	}

	for i := m.listOffset; i < end; i++ {
		c := m.commitAtDisplayIndex(i)

		// Effective date: the original (gray) until an edit assigns a new
		// date (green). The full orig → new mapping lives in the preview.
		edit, edited := m.plan.EditFor(c.Hash)
		date := c.OrigAuthorDate.In(m.loc).Format("01-02 15:04")
		dateStyle := m.st.origDate
		if edited {
			date = m.plan.EffectiveAuthorDate(c).In(m.loc).Format("01-02 15:04")
			dateStyle = m.st.newDate
		}

		marker := " "
		if edited && edit.DropBody {
			marker = "⚑"
		}
		subj := truncate(m.plan.EffectiveSubject(c), inner-listEntryFixedCols)

		onDay := calKey != "" &&
			m.plan.EffectiveAuthorDate(c).In(m.loc).Format(dateLayout) == calKey
		prefix := "  "
		switch {
		case i == m.listCursor:
			prefix = "▸ "
		case onDay:
			prefix = "▌ "
		}

		// Frozen commits (before the base boundary) render dimmed: they will
		// never be edited or rewritten.
		if m.plan.IsFrozen(len(m.plan.Commits) - 1 - i) {
			row := fmt.Sprintf("%s%s  %s %s %s", prefix, date, short(c.Hash), marker, subj)
			b.WriteString(m.st.dim.Render(row) + "\n")
			continue
		}

		if onDay {
			// One style across the whole row keeps the highlight background
			// continuous; per-segment colors would break it into chunks.
			row := fmt.Sprintf("%s%s  %s %s %s", prefix, date, short(c.Hash), marker, subj)
			b.WriteString(m.st.dayHighlight.Render(row) + "\n")
			continue
		}

		if i == m.listCursor {
			prefix = m.st.listCursor.Render("▸ ")
		}
		if marker == "⚑" {
			marker = m.st.marker.Render("⚑")
		}
		b.WriteString(fmt.Sprintf("%s%s  %s %s %s\n",
			prefix, dateStyle.Render(date), m.st.hash.Render(short(c.Hash)), marker, subj))
	}

	pane := m.st.pane
	if m.focus == focusList {
		pane = m.st.paneActive
	}
	return pane.Width(width).Height(height + 1).Render(b.String())
}

// listWidth returns the commit pane's lipgloss width (border excluded):
// 1.5× the original 44 columns when the terminal is wide enough, shrinking
// so the right column (calendar + strategy) stays fully visible beside it.
func (m Model) listWidth() int {
	const preferred, minimum = 66, 44
	if m.width == 0 {
		return preferred
	}
	// Reserve the right column's full footprint plus both panes' borders.
	avail := m.width - rightPaneWidth - 4
	if avail > preferred {
		return preferred
	}
	if avail < minimum {
		return minimum
	}
	return avail
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

// listHeight returns the pane's line budget given the terminal size. It is a
// count of screen lines; one is taken by the header, the rest hold one
// commit entry each.
func (m Model) listHeight() int {
	if m.height < 16 {
		return 8
	}
	rows := m.height - 8
	// Shrink the pane when the whole list fits: header line + 1 per commit.
	if fit := len(m.plan.Commits) + 1; rows > fit {
		rows = max(8, fit)
	}
	return rows
}

// ensureCursorVisible scrolls the list so the cursor stays within the
// viewport. visible is the number of commit entries that fit on screen.
func (m *Model) ensureCursorVisible(visible int) {
	if m.listCursor < m.listOffset {
		m.listOffset = m.listCursor
	}
	if m.listCursor >= m.listOffset+visible {
		m.listOffset = m.listCursor - visible + 1
	}
	if m.listOffset < 0 {
		m.listOffset = 0
	}
}
