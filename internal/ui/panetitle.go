package ui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

// panetitle.go draws btop-style pane titles: the title sits inside the pane's
// top border, and the letter of the key that focuses the pane is highlighted.

// paneFocusKey is one pane focus hotkey: the key, the title whose letter it
// highlights, the focus it sets, and the flow the pane belongs to (flowNone
// for a pane that is always on screen).
type paneFocusKey struct {
	Key   string
	Title string
	Focus focusArea
	Flow  flowID
}

// paneFocusKeys is the pane hotkey table. Each key must be free in every
// context the pane can be focused from, and its letter must occur in Title.
var paneFocusKeys = []paneFocusKey{
	{Key: "o", Title: "COMMITS", Focus: focusList, Flow: flowNone},
	{Key: "c", Title: "CALENDAR", Focus: focusCalendar, Flow: flowRedate},
	{Key: "t", Title: "STRATEGY", Focus: focusParams, Flow: flowRedate},
}

// focusPaneByKey moves focus to the pane bound to key and reports whether it
// did. A key whose pane is not on screen (its flow is closed) does nothing.
func (m *Model) focusPaneByKey(key string) bool {
	for _, p := range paneFocusKeys {
		if p.Key != key || (p.Flow != flowNone && m.flow != p.Flow) {
			continue
		}
		m.focus = p.Focus
		m.quotaTyping = false // a half-typed quota never carries across panes
		m.syncListViewport()
		return true
	}
	return false
}

// paneTitle renders a pane title with the letter of its focus key highlighted.
// suffix follows the title in the same style, e.g. " (12)". The title is
// bright on the focused pane and dim elsewhere.
func (m Model) paneTitle(title, suffix string, focused bool) string {
	base := m.st.dim
	if focused {
		base = m.st.modalTitle
	}
	hot := -1
	for _, p := range paneFocusKeys {
		if p.Title == title {
			hot = strings.IndexRune(title, unicode.ToUpper(rune(p.Key[0])))
		}
	}
	if hot < 0 {
		return base.Render(title + suffix)
	}
	return base.Render(title[:hot]) + m.st.paneHotkey.Render(title[hot:hot+1]) + base.Render(title[hot+1:]+suffix)
}

// withBorderTitle replaces the top border of a pane rendered with style by one
// that carries title: "╭─ TITLE ───╮". A pane too narrow for the title keeps
// its plain border.
func withBorderTitle(rendered string, style lipgloss.Style, title string) string {
	lines := strings.Split(rendered, "\n")
	width := lipgloss.Width(lines[0])
	// The corners, the edge before the title, and one space on each side of it.
	const chrome = 5
	fill := width - chrome - lipgloss.Width(title)
	if fill < 1 {
		return rendered
	}
	border := style.GetBorderStyle()
	edge := lipgloss.NewStyle().Foreground(style.GetBorderTopForeground())
	lines[0] = edge.Render(border.TopLeft+border.Top+" ") + title +
		edge.Render(" "+strings.Repeat(border.Top, fill)+border.TopRight)
	return strings.Join(lines, "\n")
}
