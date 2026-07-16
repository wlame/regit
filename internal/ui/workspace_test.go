package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestViewAuthorsPane_listsContributorsAndLightsCursorAuthor(t *testing.T) {
	// Pin a color profile so the styled labels actually emit escape codes
	// regardless of the test host's TTY, matching contributors_render_test.go.
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(orig)

	m := twoAuthorModel(t) // cursor at index 0 = newest commit = "alice" (h cccc… is newest)
	pane := m.viewAuthorsPane(12)
	plain := stripANSI(pane)
	if !strings.Contains(plain, "AUTHORS") {
		t.Fatalf("panel should have an AUTHORS title:\n%s", plain)
	}
	if !strings.Contains(plain, "alice") || !strings.Contains(plain, "bob") {
		t.Errorf("panel should list both contributors:\n%s", plain)
	}
	// The lit (cursor author) line uses the ● glyph; others use ·.
	if !strings.Contains(plain, "●") {
		t.Errorf("cursor author should be marked with ●:\n%s", plain)
	}
}

func TestViewAuthorsPane_tooSmallBudgetRendersNothing(t *testing.T) {
	m := twoAuthorModel(t)
	if got := m.viewAuthorsPane(2); got != "" {
		t.Errorf("budget below minAuthorsPaneHeight must render nothing, got %q", got)
	}
}

// TestViewFlowZone_defaultStateShowsAuthorsPane is a regression test for the
// AUTHORS pane never rendering with no flow open: viewDetailPane used to claim
// the whole zone budget (matched 1:1 to the list pane's height), leaving only
// ~2 rows for AUTHORS at any terminal size — always short of what it needs to
// render anything. At a generously sized terminal the zone has plenty of slack
// to shrink the detail pane and still seat AUTHORS below it.
func TestViewFlowZone_defaultStateShowsAuthorsPane(t *testing.T) {
	m := twoAuthorModel(t)
	m.width, m.height = 160, 55
	if m.flow != flowNone {
		t.Fatalf("expected the default no-flow state, got flow=%v", m.flow)
	}
	out := m.viewFlowZone()
	if !strings.Contains(out, "AUTHORS") {
		t.Fatalf("default state at 160x55 should seat the AUTHORS pane below the detail pane:\n%s", out)
	}
}

// TestViewAuthorsPane_widthClampsToFlowColumnInNarrowRedate is a regression
// test for the AUTHORS pane overflowing the flow column in the redate flow at
// ~100-140 column terminals: it rendered at a fixed rightPaneWidth (62 outer
// columns) regardless of how narrow computeLayout actually budgeted the flow
// zone. viewMain's whole-frame MaxWidth clamp (view.go) hides the symptom at
// the composed-line level — every line still fits the terminal — so this
// checks the pane's own rendered width against its column budget (flowWidth
// plus the pane's own border, the same outer/inner split listWidth and
// flowWidth already use), the same way the truncated right border is actually
// visible on screen. It must clamp down exactly like the calendar and
// strategy panes do (ws_dates.go).
func TestViewAuthorsPane_widthClampsToFlowColumnInNarrowRedate(t *testing.T) {
	m := twoAuthorModel(t)
	m.width, m.height = 100, 40
	m.enterRedateFlow()

	flowColumnBudget := computeLayout(m.width, m.height).flowWidth + 2 // +border
	pane := m.viewAuthorsPane(12)
	if pane == "" {
		t.Fatal("expected a non-empty AUTHORS pane at budget 12")
	}
	for i, line := range strings.Split(pane, "\n") {
		if w := lipgloss.Width(line); w > flowColumnBudget {
			t.Errorf("width %d: AUTHORS pane line %d has display width %d > flow column budget %d:\n%q",
				m.width, i, w, flowColumnBudget, line)
		}
	}
}
