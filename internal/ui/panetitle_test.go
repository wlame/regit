package ui

import (
	"strings"
	"testing"
)

func TestPaneFocusKeys_moveFocusBetweenRedatePanes(t *testing.T) {
	m := send(newTestModelN(t, 6), "m", "d") // flow open, focus on the calendar

	steps := []struct {
		key  string
		want focusArea
	}{
		{"t", focusParams},
		{"o", focusList},
		{"c", focusCalendar},
		{"o", focusList},
		{"t", focusParams},
		{"c", focusCalendar},
	}
	for _, s := range steps {
		m = send(m, s.key)
		if m.focus != s.want {
			t.Fatalf("after %q focus = %v, want %v", s.key, m.focus, s.want)
		}
	}
}

func TestPaneFocusKeys_flowPaneKeysDoNothingWithoutTheFlow(t *testing.T) {
	m := newTestModelN(t, 6)

	m = send(m, "c", "t")

	if m.focus != focusList || m.flow != flowNone {
		t.Errorf("c/t without an open flow changed state: focus=%v flow=%v", m.focus, m.flow)
	}
}

func TestPaneTitles_drawnIntoTheTopBorder(t *testing.T) {
	m := send(resizeTo(newTestModelN(t, 6), 120, 40), "m", "d")

	view := stripANSI(m.View())

	for _, title := range []string{"COMMITS (6)", "CALENDAR", "STRATEGY"} {
		if !hasBorderTitle(view, title) {
			t.Errorf("no pane has %q in its top border:\n%s", title, view)
		}
	}
}

// hasBorderTitle reports whether some line of view carries title inside a
// rounded top border ("╭─ TITLE ─…╮").
func hasBorderTitle(view, title string) bool {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "╭─ "+title+" ─") {
			return true
		}
	}
	return false
}

func TestCommitList_titleInBorderGivesTheRowToACommit(t *testing.T) {
	m := resizeTo(newTestModelN(t, 30), 120, 30)

	rows := 0 // commit rows and day dividers: every non-blank line inside the border
	for _, line := range strings.Split(stripANSI(m.viewCommitList()), "\n") {
		inner := strings.Trim(line, "│ ")
		if strings.HasPrefix(line, "│") && inner != "" {
			rows++
		}
		if strings.HasPrefix(line, "│") && strings.Contains(line, "COMMITS (") {
			t.Errorf("the title should be in the border, not a content row: %q", line)
		}
	}

	if rows != m.listHeight() {
		t.Errorf("list shows %d rows, want listHeight()=%d", rows, m.listHeight())
	}
}
