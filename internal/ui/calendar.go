package ui

import (
	"strings"
	"time"
)

// calendar renders a GitHub-style contribution heatmap for a single year: weeks
// run left-to-right as columns, days of the week run top-to-bottom as seven
// rows, and each cell's color encodes how many commits land on that day.
//
// The grid starts on the Sunday on or before Jan 1 so every column is a full
// week, exactly like GitHub's graph.

const (
	cellFilled = "▇"
	cellEmpty  = "·"
	gridCols   = 53 // up to 53 week-columns cover any year
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

// gridStart returns the Sunday on or before Jan 1 of the given year, in loc.
func gridStart(year int, loc *time.Location) time.Time {
	jan1 := time.Date(year, time.January, 1, 0, 0, 0, 0, loc)
	offset := int(jan1.Weekday()) // Sunday == 0
	return jan1.AddDate(0, 0, -offset)
}

// renderCalendar draws the heatmap for year, using counts keyed by "2006-01-02".
// The cursor day is highlighted. focused tints the active state.
func renderCalendar(year int, counts map[string]int, cursor time.Time, loc *time.Location, st styles) string {
	start := gridStart(year, loc)
	cursorKey := cursor.In(loc).Format("2006-01-02")

	// Build the seven weekday rows.
	rows := make([]strings.Builder, 7)
	dowLabels := [7]string{"   ", "Mon", "   ", "Wed", "   ", "Fri", "   "}
	for r := 0; r < 7; r++ {
		rows[r].WriteString(st.dim.Render(dowLabels[r] + " "))
	}

	for col := 0; col < gridCols; col++ {
		for r := 0; r < 7; r++ {
			day := start.AddDate(0, 0, col*7+r)
			if day.Year() > year {
				rows[r].WriteString(" ")
				continue
			}
			if day.Year() < year {
				rows[r].WriteString(" ")
				continue
			}
			key := day.Format("2006-01-02")
			level := heatLevel(counts[key])
			glyph := cellFilled
			if level == 0 {
				glyph = cellEmpty
			}
			style := st.heatLevels[level]
			if key == cursorKey {
				style = st.heatCursor
				glyph = cellFilled
			}
			rows[r].WriteString(style.Render(glyph))
		}
	}

	// Month header aligned to the week-columns where each month begins.
	header := renderMonthHeader(year, start, loc, st)

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

// renderMonthHeader builds the row of month abbreviations placed above the week
// column where each month's first day falls.
func renderMonthHeader(year int, start time.Time, loc *time.Location, st styles) string {
	// 4-space left gutter to match the weekday-label column ("Mon " == 4 chars).
	const gutter = 4
	line := []byte(strings.Repeat(" ", gutter+gridCols))

	for m := time.January; m <= time.December; m++ {
		first := time.Date(year, m, 1, 0, 0, 0, 0, loc)
		col := int(first.Sub(start).Hours()/24) / 7
		pos := gutter + col
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
