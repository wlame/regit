package ui

import (
	"fmt"
	"strings"
	"time"
)

// calendar renders a GitHub-style contribution heatmap for half a year: weeks
// run left-to-right as columns, days of the week run top-to-bottom as seven
// rows. Each cell is two characters wide so a day can show a pinned quota
// number (0-99) in place of its heat glyph.
//
// The grid starts on the Sunday on or before the half-year's first day so
// every column is a full week, exactly like GitHub's graph.

const (
	cellFilled = "▇"
	cellEmpty  = "·"
	cellWidth  = 2  // every day cell is two characters: "▇▇", "··", or "%2d"
	halfCols   = 27 // a half-year spans at most 27 Sunday-aligned week columns
	// 4-space left gutter matching the weekday-label column ("Mon " == 4 chars).
	calGutter = 4
)

// heatLevel maps a commit count to one of five intensity buckets (0..4).
func heatLevel(count int) int {
	switch {
	case count <= 0:
		return 0
	case count == 1:
		return 1
	case count <= 3:
		return 2
	case count <= 6:
		return 3
	default:
		return 4
	}
}

// halfRange returns the inclusive date range and label of the half-year
// containing day: Jan–Jun or Jul–Dec.
func halfRange(day time.Time, loc *time.Location) (start, end time.Time, label string) {
	y := day.Year()
	if day.Month() <= time.June {
		return time.Date(y, time.January, 1, 0, 0, 0, 0, loc),
			time.Date(y, time.June, 30, 23, 59, 59, 0, loc), "Jan–Jun"
	}
	return time.Date(y, time.July, 1, 0, 0, 0, 0, loc),
		time.Date(y, time.December, 31, 23, 59, 59, 0, loc), "Jul–Dec"
}

// sundayOnOrBefore returns the Sunday on or before t.
func sundayOnOrBefore(t time.Time) time.Time {
	return t.AddDate(0, 0, -int(t.Weekday())) // Sunday == 0
}

// renderCalendar draws the half-year heatmap around cursor, using counts and
// quotas keyed by "2006-01-02". Days with a pinned quota show the number
// instead of a heat glyph; the cursor day is highlighted.
func renderCalendar(cursor time.Time, counts, quotas map[string]int, loc *time.Location, st styles) string {
	rangeStart, rangeEnd, _ := halfRange(cursor.In(loc), loc)
	start := sundayOnOrBefore(rangeStart)
	cursorKey := cursor.In(loc).Format(dateLayout)

	// Build the seven weekday rows.
	rows := make([]strings.Builder, 7)
	dowLabels := [7]string{"   ", "Mon", "   ", "Wed", "   ", "Fri", "   "}
	for r := 0; r < 7; r++ {
		rows[r].WriteString(st.dim.Render(dowLabels[r] + " "))
	}

	for col := 0; col < halfCols; col++ {
		for r := 0; r < 7; r++ {
			day := start.AddDate(0, 0, col*7+r)
			if day.Before(rangeStart) || day.After(rangeEnd) {
				rows[r].WriteString(strings.Repeat(" ", cellWidth))
				continue
			}
			key := day.Format(dateLayout)

			cell := strings.Repeat(cellEmpty, cellWidth)
			level := heatLevel(counts[key])
			style := st.heatLevels[level]
			if level > 0 {
				cell = strings.Repeat(cellFilled, cellWidth)
			}
			if q, ok := quotas[key]; ok {
				// A pinned quota replaces the glyph so the number is visible
				// right on the day it applies to.
				cell = fmt.Sprintf("%*d", cellWidth, q)
				style = st.marker
			}
			if key == cursorKey {
				style = st.heatCursor
			}
			rows[r].WriteString(style.Render(cell))
		}
	}

	header := renderMonthHeader(rangeStart, start, loc, st)

	var b strings.Builder
	b.WriteString(header + "\n")
	for r := 0; r < 7; r++ {
		b.WriteString(rows[r].String())
		if r < 6 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// renderMonthHeader builds the row of month abbreviations placed above the
// week column where each of the half-year's six months begins.
func renderMonthHeader(rangeStart, gridStart time.Time, loc *time.Location, st styles) string {
	line := []byte(strings.Repeat(" ", calGutter+halfCols*cellWidth))

	for i := 0; i < 6; i++ {
		first := rangeStart.AddDate(0, i, 0)
		col := int(first.Sub(gridStart).Hours()/24) / 7
		pos := calGutter + col*cellWidth
		label := first.Format("Jan")
		if pos+len(label) <= len(line) && spaceFree(line, pos, len(label)) {
			copy(line[pos:], label)
		}
	}
	return st.dim.Render(string(line))
}

// spaceFree reports whether width bytes from pos are all spaces (so a label can
// be written without overwriting a neighbor).
func spaceFree(line []byte, pos, width int) bool {
	for i := pos; i < pos+width && i < len(line); i++ {
		if line[i] != ' ' {
			return false
		}
	}
	return true
}

// renderLegend draws the "Less ░▒▓█ More" intensity key.
func renderLegend(st styles) string {
	var b strings.Builder
	b.WriteString(st.dim.Render("Less "))
	for level := 0; level < len(st.heatLevels); level++ {
		glyph := cellFilled
		if level == 0 {
			glyph = cellEmpty
		}
		b.WriteString(st.heatLevels[level].Render(glyph))
	}
	b.WriteString(st.dim.Render(" More"))
	return b.String()
}
