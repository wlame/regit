package ui

import (
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/termsafe"
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

	if !m.groupByDate {
		for i := 0; i < n; i++ {
			rows = append(rows, listRow{displayIdx: i})
		}
		return rows
	}

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

// listHeight is the commit list's inner height in rows. It reserves a fixed
// chrome budget for the title bar, tab bar, status line, keybar, and the pane
// borders, and otherwise fills the terminal height so the panes span the screen
// even on a repo with only a few commits. When the command-log footer is
// visible, logPanelHeight additional rows are subtracted from the budget.
func (m Model) listHeight() int {
	if m.height < 16 {
		return 8
	}
	h := m.height - 9 // title + tabs + status + keybar + spacing/borders
	if m.logVisible {
		h -= logPanelHeight
	}
	return h
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

// authorColWidth is the fixed character width of the optional author column. A
// longer name truncates with an ellipsis; a shorter one is right-padded so the
// subjects stay aligned regardless of name length.
const authorColWidth = 12

// authorLabel returns a commit's display author: the author name, or the
// local-part ("username") of the author email when the name is blank. Git
// commits always carry an author, but the name can be empty in odd histories,
// so the email gives a sensible fallback.
func authorLabel(c model.Commit) string {
	if c.AuthorName != "" {
		return c.AuthorName
	}
	if at := strings.IndexByte(c.AuthorEmail, '@'); at > 0 {
		return c.AuthorEmail[:at]
	}
	return c.AuthorEmail
}

// authorCell builds the fixed-width author column (text plus a trailing gap)
// shown when showAuthor is on, and returns the screen columns it consumes. When
// the column is hidden it returns ("", 0) so callers add nothing to the row.
func (m Model) authorCell(c model.Commit) (text string, width int) {
	if !m.showAuthor {
		return "", 0
	}
	name := truncate(termsafe.Line(authorLabel(c)), authorColWidth)
	if pad := authorColWidth - len([]rune(name)); pad > 0 {
		name += strings.Repeat(" ", pad)
	}
	return name + " ", authorColWidth + 1
}

// styleAuthor applies the author color to a non-empty author cell, returning an
// empty string untouched so a hidden column never emits stray escape codes.
func (m Model) styleAuthor(text string) string {
	if text == "" {
		return ""
	}
	return m.st.author.Render(text)
}

// renderCommitRow renders a single commit line. The leading two-column gutter
// reflects the selection mode: a ○/● circle in Set mode, a ┃ band in Range mode,
// or the ▸/▌/space caret in Single/Active mode. Frozen commits are never
// selectable, so they always take the dimmed Single/Active path.
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

	// The optional author column sits between the marker and the subject; its
	// width is subtracted from the subject so the line never wraps.
	auth, authWidth := m.authorCell(c)

	// Fixed columns before the subject: gutter(2) + hash(8) + gap(1) + marker(1) + gap(1).
	const fixed = 13
	subj := truncate(termsafe.Line(m.plan.EffectiveSubject(c)), max(1, inner-fixed-authWidth))

	frozen := m.plan.IsFrozen(len(m.plan.Commits) - 1 - displayIdx)
	isCursor := displayIdx == m.listCursor

	// Global mode dims the whole list — it is context only; focus is elsewhere.
	if m.sel.mode == SelGlobal {
		return m.st.dim.Render(fmt.Sprintf("  %s %s %s%s", short(c.Hash), marker, auth, subj))
	}
	// Set / Range gutters apply only to editable (non-frozen) rows; frozen rows
	// fall through to the dimmed Single/Active path below.
	if !frozen {
		switch m.sel.mode {
		case SelSet:
			return m.renderSetRow(c, isCursor, marker, auth, subj)
		case SelRange:
			return m.renderRangeRow(c, displayIdx, isCursor, marker, auth, subj)
		}
	}

	// ---- Single / Active (and any frozen row) ----
	onDay := calKey != "" && m.plan.EffectiveAuthorDate(c).In(m.loc).Format(dateLayout) == calKey
	prefix := "  "
	switch {
	case isCursor:
		prefix = "▸ "
	case onDay:
		prefix = "▌ "
	}
	plain := fmt.Sprintf("%s%s %s %s%s", prefix, short(c.Hash), marker, auth, subj)
	if frozen {
		return m.st.dim.Render(plain)
	}
	if onDay {
		return m.st.dayHighlight.Render(plain)
	}
	styledPrefix := prefix
	if isCursor {
		styledPrefix = m.st.listCursor.Render("▸ ")
	}
	styledMarker := marker
	if marker != " " {
		styledMarker = m.st.marker.Render(marker)
	}
	return fmt.Sprintf("%s%s %s %s%s", styledPrefix, m.st.hash.Render(short(c.Hash)), styledMarker, m.styleAuthor(auth), subj)
}

// renderSetRow renders a row in Set mode: a ○ (unselected) / ● (selected) gutter,
// then hash, marker, subject. The cursor row's circle takes the cursor color so
// the pick position stays visible while toggling.
func (m Model) renderSetRow(c model.Commit, isCursor bool, marker, auth, subj string) string {
	circle := "○"
	circleStyle := m.st.dim
	if m.sel.isSet(c.Hash) {
		circle, circleStyle = "●", m.st.picked
	}
	if isCursor {
		circleStyle = m.st.listCursor
	}
	styledMarker := marker
	if marker != " " {
		styledMarker = m.st.marker.Render(marker)
	}
	return fmt.Sprintf("%s %s %s %s%s", circleStyle.Render(circle), m.st.hash.Render(short(c.Hash)), styledMarker, m.styleAuthor(auth), subj)
}

// renderRangeRow renders a row in Range visual mode: in-range rows carry a ┃ band
// at the left edge (the cursor row colored); out-of-range rows dim.
func (m Model) renderRangeRow(c model.Commit, displayIdx int, isCursor bool, marker, auth, subj string) string {
	if !m.sel.inRange(displayIdx, m.listCursor) {
		return m.st.dim.Render(fmt.Sprintf("  %s %s %s%s", short(c.Hash), marker, auth, subj))
	}
	bar := m.st.marker.Render("┃")
	if isCursor {
		bar = m.st.listCursor.Render("┃")
	}
	styledMarker := marker
	if marker != " " {
		styledMarker = m.st.marker.Render(marker)
	}
	return fmt.Sprintf("%s %s %s %s%s", bar, m.st.hash.Render(short(c.Hash)), styledMarker, m.styleAuthor(auth), subj)
}
