package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestRenderCalendar_previewShowsNumbers(t *testing.T) {
	cursor := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	counts := map[string]int{"2026-06-15": 3}
	st := newStyles()
	preview := renderCalendar(cursor, counts, map[string]int{}, map[string]bool{}, map[string]bool{},
		time.UTC, st, true)
	if !strings.Contains(preview, "3") {
		t.Errorf("preview mode should render the per-day count as a number, got:\n%s", preview)
	}
	tiles := renderCalendar(cursor, counts, map[string]int{}, map[string]bool{}, map[string]bool{},
		time.UTC, st, false)
	if strings.Contains(tiles, " 3") {
		t.Errorf("tile mode should not render counts as numbers, got:\n%s", tiles)
	}
}

// TestRenderCalendar_AvailableDaysAreTinted: a day flagged in avail must
// render differently from the same day with no avail map at all, so the
// caller's segment-window highlight is actually visible on the heatmap.
//
// The difference renderCalendar produces here is purely a style (color)
// change, not a content change — go test runs with no attached TTY, and
// lipgloss silently drops all color codes when it can't detect one (see
// termenv's profile auto-detection), which would make withAvail and without
// compare equal regardless of whether the tint logic works. Forcing an
// ANSI256 profile for this test makes Render() actually emit the escape
// codes so the comparison is meaningful.
func TestRenderCalendar_AvailableDaysAreTinted(t *testing.T) {
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(orig)

	st := newStyles()
	cursor := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	avail := map[string]bool{"2026-03-10": true}
	withAvail := renderCalendar(cursor, nil, nil, nil, avail, time.UTC, st, false)
	without := renderCalendar(cursor, nil, nil, nil, nil, time.UTC, st, false)
	if withAvail == without {
		t.Fatal("available-day tint must change the rendering")
	}
}
