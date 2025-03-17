package ui

import (
	"fmt"
	"strings"
)

// listRow is one rendered line of the commit list: either a selectable commit
// (identified by its display index, newest = 0) or a non-selectable date
// divider that heads a same-day group. The cursor only ever lands on commits.
type listRow struct {
	isDivider  bool
	label      string // divider text, e.g. "──── 2026-03-02 (Mon) · 3 commits ────"
	displayIdx int    // commit display index when !isDivider
}

// buildListRows produces the full newest-first row sequence, inserting a date
// divider before the first commit of each distinct author-date day. The day a
// commit falls on uses its EFFECTIVE date so pending date edits regroup it live.
func (m Model) buildListRows() []listRow {
	n := len(m.plan.Commits)
	rows := make([]listRow, 0, n+8)

	// Pre-tally each day's commit count for the divider label.
	dayTotals := make(map[string]int, n)
	for i := 0; i < n; i++ {
		c := m.commitAtDisplayIndex(i)
		dayTotals[m.plan.EffectiveAuthorDate(c).In(m.loc).Format(dateLayout)]++
	}

	prevKey := ""
	for i := 0; i < n; i++ {
		c := m.commitAtDisplayIndex(i)
		day := m.plan.EffectiveAuthorDate(c).In(m.loc)
		key := day.Format(dateLayout)
		if key != prevKey {
			label := fmt.Sprintf("──── %s (%s) · %d commit(s) ────",
				key, day.Weekday().String()[:3], dayTotals[key])
			rows = append(rows, listRow{isDivider: true, label: label})
			prevKey = key
		}
		rows = append(rows, listRow{displayIdx: i})
	}
	return rows
}

// rowOfCommit returns the row index holding the given commit display index, or 0
// if absent.
func rowOfCommit(rows []listRow, displayIdx int) int {
	for i, r := range rows {
		if !r.isDivider && r.displayIdx == displayIdx {
			return i
		}
	}
	return 0
}

// scrollMargin is the scroll-off margin: keep the cursor at least this many rows
// from the viewport's top and bottom edges. It is round(0.20 × visible), min 1,
// computed with integer arithmetic ((visible+2)/5 == the rounded value here) to
// avoid a math import.
func scrollMargin(visible int) int {
	return max(1, (visible+2)/5)
}

// scrollOffset returns the new top-of-viewport row index. The viewport scrolls
// only when the cursor enters the top or bottom margin, so the cursor floats in
// the middle band during steady navigation; at the true list ends the margin
// collapses and the offset clamps.
func scrollOffset(prevOffset, cursorRow, totalRows, visible int) int {
	off := prevOffset
	margin := scrollMargin(visible)
	if cursorRow < off+margin {
		off = cursorRow - margin
	}
	if cursorRow > off+visible-1-margin {
		off = cursorRow - visible + 1 + margin
	}
	if maxOff := totalRows - visible; off > maxOff {
		off = maxOff
	}
	if off < 0 {
		off = 0
	}
	return off
}

// syncListViewport recomputes and persists the list's row offset so the cursor
// stays visible. It runs in the Update path (on resize and after the cursor
// moves) — never in View — so scrolling is stable state, not recomputed per frame.
func (m *Model) syncListViewport() {
	rows := m.buildListRows()
	visible := max(1, m.listHeight()-1)
	m.listOffset = scrollOffset(m.listOffset, rowOfCommit(rows, m.listCursor), len(rows), visible)
}

// listHeight returns the list pane's line budget. One line is the COMMITS
// header; the rest hold one row (commit or divider) each. The pane shrinks to
// fit when the whole row sequence is shorter than the available space.
func (m Model) listHeight() int {
	if m.height < 16 {
		return 8
	}
	rows := m.height - 8
	if fit := len(m.buildListRows()) + 1; rows > fit {
		rows = max(8, fit)
	}
	return rows
}

// viewCommitList renders the scrollable, newest-first commit list from the row
// model. Every row is exactly one screen line; subjects truncate so a line can
// never wrap (a wrapped line would inflate the pane height and overflow the UI).
func (m Model) viewCommitList() string {
	width := m.listWidth()
	height := m.listHeight()
	inner := width - 2
	visible := max(1, height-1) // the COMMITS header takes one line

	var b strings.Builder
	b.WriteString(m.st.modalTitle.Render(fmt.Sprintf("COMMITS (%d)", len(m.plan.Commits))) + "\n")

	rows := m.buildListRows()
	offset := m.listOffset
	if offset >= len(rows) {
		offset = max(0, len(rows)-1)
	}
	end := offset + visible
	if end > len(rows) {
		end = len(rows)
	}

	// While the calendar is focused, commits on its cursor day get a whole-row
	// highlight so calendar navigation lights up the list.
	calKey := ""
	if m.focus == focusCalendar {
		calKey = m.calCursor.In(m.loc).Format(dateLayout)
	}

	for _, r := range rows[offset:end] {
		if r.isDivider {
			b.WriteString(m.st.dim.Render(truncate(r.label, inner)) + "\n")
			continue
		}
		b.WriteString(m.renderCommitRow(r.displayIdx, inner, calKey) + "\n")
	}

	pane := m.st.pane
	if m.focus == focusList {
		pane = m.st.paneActive
	}
	return pane.Width(width).Height(height + 1).Render(b.String())
}

// renderCommitRow renders a single commit line: cursor, short hash, an edit
// marker, and the (effective) subject. The per-row date was dropped in Phase 1 —
// the day is conveyed by the divider above and the full dates by the detail panel.
func (m Model) renderCommitRow(displayIdx, inner int, calKey string) string {
	c := m.commitAtDisplayIndex(displayIdx)
	edit, edited := m.plan.EditFor(c.Hash)

	marker := " "
	switch {
	case edited && edit.DropBody:
		marker = "⚑"
	case edited:
		marker = "✎"
	}

	// Fixed columns before the subject: cursor(2) + hash(8) + gap(1) + marker(1) + gap(1).
	const fixed = 13
	subj := truncate(m.plan.EffectiveSubject(c), max(1, inner-fixed))

	onDay := calKey != "" && m.plan.EffectiveAuthorDate(c).In(m.loc).Format(dateLayout) == calKey
	prefix := "  "
	switch {
	case displayIdx == m.listCursor:
		prefix = "▸ "
	case onDay:
		prefix = "▌ "
	}

	plain := fmt.Sprintf("%s%s %s %s", prefix, short(c.Hash), marker, subj)

	// Frozen commits (before the base) render dimmed and are never editable.
	if m.plan.IsFrozen(len(m.plan.Commits) - 1 - displayIdx) {
		return m.st.dim.Render(plain)
	}
	if onDay {
		// One style across the whole row keeps the highlight background continuous.
		return m.st.dayHighlight.Render(plain)
	}

	styledPrefix := prefix
	if displayIdx == m.listCursor {
		styledPrefix = m.st.listCursor.Render("▸ ")
	}
	styledMarker := marker
	if marker != " " {
		styledMarker = m.st.marker.Render(marker)
	}
	return fmt.Sprintf("%s%s %s %s", styledPrefix, m.st.hash.Render(short(c.Hash)), styledMarker, subj)
}
