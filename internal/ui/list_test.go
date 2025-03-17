package ui

import (
	"testing"
	"time"

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
