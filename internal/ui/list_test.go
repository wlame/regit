package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/model"
)

// twoDayModel builds a model whose commits span two distinct author-date days,
// so exactly two date dividers should appear.
func twoDayModel(t *testing.T) Model {
	t.Helper()
	m := newTestModelN(t, 1) // reuse the repo scaffolding; we overwrite the plan
	day1 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	commits := []model.Commit{
		{Hash: "aaaa111111111111111111111111111111111111", Subject: "first", OrigAuthorDate: day1, OrigCommitDate: day1},
		{Hash: "bbbb222222222222222222222222222222222222", Subject: "second", OrigAuthorDate: day1, OrigCommitDate: day1},
		{Hash: "cccc333333333333333333333333333333333333", Subject: "third", OrigAuthorDate: day2, OrigCommitDate: day2},
	}
	m.plan = model.NewPlan(commits)
	m.listCursor = 0
	return m
}

func TestBuildListRows_insertsADividerPerDay(t *testing.T) {
	m := twoDayModel(t)
	rows := m.buildListRows()

	dividers, commitsSeen := 0, 0
	for _, r := range rows {
		if r.isDivider {
			dividers++
		} else {
			commitsSeen++
		}
	}
	if dividers != 2 {
		t.Errorf("two distinct days should yield 2 dividers, got %d", dividers)
	}
	if commitsSeen != 3 {
		t.Errorf("all 3 commits should appear as rows, got %d", commitsSeen)
	}
	// The very first row is a divider (the header for the newest day group).
	if !rows[0].isDivider {
		t.Errorf("first row should be a date divider, got %+v", rows[0])
	}
}

func TestRowOfCommit_findsCursorRow(t *testing.T) {
	m := twoDayModel(t)
	rows := m.buildListRows()
	// Display index 2 (oldest commit) sits below both dividers and two commits.
	got := rowOfCommit(rows, 2)
	if rows[got].isDivider || rows[got].displayIdx != 2 {
		t.Errorf("rowOfCommit(2) landed on %+v", rows[got])
	}
}

// An out-of-range listOffset (one that syncListViewport would normally never
// leave behind) must clamp back into the valid row range at render time rather
// than blanking the pane. This pins the defensive offset clamp in viewCommitList.
//
// The clamp lands on the last row, so the render falls back to the OLDEST
// commit (display index N-1, the bottom of the newest-first list) — the point
// is that a real commit row shows through instead of an empty slice.
func TestViewCommitList_clampsOutOfRangeOffset(t *testing.T) {
	m := newTestModelN(t, 5)
	oldest := m.plan.Commits[0] // root commit = display index N-1 (bottom row)
	hash8 := oldest.Hash[:8]

	// Force the offset well past the end of the row sequence.
	m.listOffset = len(m.buildListRows()) + 5

	out := m.viewCommitList()
	if strings.TrimSpace(out) == "" {
		t.Fatal("an out-of-range offset must not blank the commit pane")
	}
	if !strings.Contains(out, hash8) {
		t.Errorf("clamped render should still show a real commit row (%s)", hash8)
	}
}

func TestBuildListRows_flatWhenGroupingOff(t *testing.T) {
	m := twoDayModel(t)
	m.groupByDate = false
	rows := m.buildListRows()
	for _, r := range rows {
		if r.isDivider {
			t.Fatalf("grouping off should emit no dividers, got %+v", r)
		}
	}
	if len(rows) != 3 {
		t.Errorf("flat list should have one row per commit, got %d", len(rows))
	}
}

func TestScrollMargin_isTwentyPercent(t *testing.T) {
	cases := map[int]int{1: 1, 5: 1, 10: 2, 20: 4, 23: 5}
	for visible, want := range cases {
		if got := scrollMargin(visible); got != want {
			t.Errorf("scrollMargin(%d) = %d, want %d", visible, got, want)
		}
	}
}

func TestScrollOffset_keepsCursorOffTheEdges(t *testing.T) {
	const total, visible = 100, 20 // margin = 4
	// Scrolling down: once the cursor reaches row 30 with the window at top, the
	// offset advances so the cursor sits no closer than the margin to the bottom.
	off := scrollOffset(0, 30, total, visible)
	if off == 0 {
		t.Fatalf("offset should advance once the cursor passes the bottom margin, got %d", off)
	}
	cursorFromTop := 30 - off
	if cursorFromTop > visible-1-scrollMargin(visible) {
		t.Errorf("cursor sits %d rows from top; should stay within the bottom margin", cursorFromTop)
	}

	// Near the top the margin collapses: the offset clamps to 0.
	if got := scrollOffset(0, 2, total, visible); got != 0 {
		t.Errorf("near the top the offset should be 0, got %d", got)
	}
	// Near the bottom the offset clamps so the last row is the final one.
	if got := scrollOffset(0, total-1, total, visible); got != total-visible {
		t.Errorf("at the bottom the offset should clamp to %d, got %d", total-visible, got)
	}
}

func TestListHeight_fillsTerminalRegardlessOfCommitCount(t *testing.T) {
	// 3 commits in a 40-row terminal: the pane must fill the height, not shrink
	// to the commit count (the old clamp returned 8 here).
	m := newTestModelN(t, 3)
	if got, want := m.listHeight(), m.height-9; got != want {
		t.Errorf("listHeight() = %d, want %d (full height, no clamp)", got, want)
	}
}

func TestListHeight_shortTerminalUsesFloor(t *testing.T) {
	m := newTestModelN(t, 3)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 12})
	m = updated.(Model)
	if got := m.listHeight(); got != 8 {
		t.Errorf("listHeight() = %d, want 8 for a short terminal", got)
	}
}
