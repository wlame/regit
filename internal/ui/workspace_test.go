package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/wlame/regit/internal/gitio"
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

	flowColumnBudget := m.layout().flowWidth + 2 // +border
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

// TestViewFlowZone_shortDetailLeavesRoomForEveryAuthor covers the AUTHORS pane
// stuck at its minimum ("… +N more") under a detail pane padded with blank
// rows: when the detail content is short, AUTHORS gets the rows it needs.
func TestViewFlowZone_shortDetailLeavesRoomForEveryAuthor(t *testing.T) {
	m := resizeTo(twoAuthorModel(t), 120, 40)
	for _, name := range []string{"carol", "dave", "erin", "frank"} {
		m.contribs = append(m.contribs, contributor{Key: name, Label: name, Count: 1})
	}

	out := stripANSI(m.viewFlowZone())

	if strings.Contains(out, "more") {
		t.Errorf("AUTHORS should list every author when the detail is short:\n%s", out)
	}
	for _, c := range m.contribs {
		if !strings.Contains(out, c.Label) {
			t.Errorf("AUTHORS is missing %q:\n%s", c.Label, out)
		}
	}
	assertFrameFits(t, m, 120, 40)
}

// TestViewFlowZone_manyFilesStillShowsAuthors: a commit with many files has a
// detail pane that could fill the whole column, but its file list shortens
// itself ("… +N more files"), so it yields rows and AUTHORS keeps its share.
func TestViewFlowZone_manyFilesStillShowsAuthors(t *testing.T) {
	m := resizeTo(twoAuthorModel(t), 120, 40)
	for _, name := range []string{"carol", "dave", "erin", "frank"} {
		m.contribs = append(m.contribs, contributor{Key: name, Label: name, Count: 1})
	}
	var files []gitio.FileStat
	for i := range 30 {
		files = append(files, gitio.FileStat{Status: "M", Path: fmt.Sprintf("pkg/file_%02d.go", i), Add: 1, Del: 1})
	}
	m.statCache[m.commitAtCursor().Hash] = gitio.Stat{Files: files, Additions: 30, Deletions: 30}

	out := stripANSI(m.viewFlowZone())

	for _, c := range m.contribs {
		if !strings.Contains(out, c.Label) {
			t.Errorf("AUTHORS is missing %q:\n%s", c.Label, out)
		}
	}
	if !strings.Contains(out, "more files") {
		t.Errorf("the detail file list should shorten itself instead:\n%s", out)
	}
}
