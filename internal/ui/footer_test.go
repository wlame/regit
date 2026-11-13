package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestCommandLogToggleAndRender(t *testing.T) {
	m := Model{width: 100, height: 40}
	if m.viewCommandLog() != "" {
		t.Error("hidden log should render empty")
	}
	m.logVisible = true
	m.commandLog = []string{"regit redate --strategy=Even-Spread --execute"}
	out := m.viewCommandLog()
	if !strings.Contains(out, "redate --strategy=Even-Spread") {
		t.Errorf("log panel missing entry:\n%s", out)
	}
	if !strings.Contains(out, "command log") {
		t.Errorf("log panel missing header:\n%s", out)
	}
}

// TestCommandLog_neverTallerThanItsBudget: entries that span two lines (a
// wrapped redate command) or are wider than the terminal must not make the
// panel taller than logPanelHeight, or the frame pushes the title off screen.
func TestCommandLog_neverTallerThanItsBudget(t *testing.T) {
	m := Model{width: 60, height: 40, logVisible: true}
	two := "regit redate --strategy=Even-Spread --start=2026-03-02 \\\n  --end=2026-03-13 --execute"
	long := "regit retitle --match=" + strings.Repeat("x", 120) + " --execute"
	m.commandLog = []string{two, "verified 8 commits", two, "regit undo", long, "newest line"}
	out := m.viewCommandLog()
	lines := strings.Split(out, "\n")
	if len(lines) > logPanelHeight {
		t.Errorf("log panel is %d rows, budget %d:\n%s", len(lines), logPanelHeight, out)
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > m.width {
			t.Errorf("row %d is %d cells wide on a %d-column terminal: %q", i, w, m.width, l)
		}
	}
	if !strings.Contains(out, "newest line") {
		t.Errorf("the newest entry must stay visible:\n%s", out)
	}
}

func TestNewestWholeEntries_neverSplitsAnEntry(t *testing.T) {
	entries := []string{"old a\nold b", "mid", "new a\nnew b"}
	got := newestWholeEntries(entries, 4)
	want := []string{"mid", "new a", "new b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("newestWholeEntries = %q, want %q", got, want)
	}
	tall := newestWholeEntries([]string{"1\n2\n3"}, 2)
	if strings.Join(tall, "|") != "2|3" {
		t.Errorf("an entry taller than the budget should keep its last lines, got %q", tall)
	}
}
