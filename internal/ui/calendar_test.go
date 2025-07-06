package ui

import (
	"strings"
	"testing"
	"time"
)

func TestRenderCalendar_previewShowsNumbers(t *testing.T) {
	cursor := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	counts := map[string]int{"2026-06-15": 3}
	st := newStyles()
	preview := renderCalendar(cursor, counts, map[string]int{}, map[string]bool{}, time.UTC, st, true)
	if !strings.Contains(preview, "3") {
		t.Errorf("preview mode should render the per-day count as a number, got:\n%s", preview)
	}
	tiles := renderCalendar(cursor, counts, map[string]int{}, map[string]bool{}, time.UTC, st, false)
	if strings.Contains(tiles, " 3") {
		t.Errorf("tile mode should not render counts as numbers, got:\n%s", tiles)
	}
}
