package ui

import (
	"sort"
	"testing"
)

func TestSelection_setToggleAndFrozenRefusal(t *testing.T) {
	s := newSelection()
	s.toggle("aaa", false)
	if s.mode != SelSet || !s.isSet("aaa") {
		t.Fatalf("toggle should enter Set mode and select aaa: mode=%v set=%v", s.mode, s.set)
	}
	s.toggle("aaa", false) // toggling again deselects
	if s.isSet("aaa") {
		t.Error("second toggle should deselect aaa")
	}
	s.toggle("frz", true) // frozen commits are never selectable
	if s.isSet("frz") {
		t.Error("frozen commit must not be selectable")
	}
}

func TestSelection_selectAllAndClear(t *testing.T) {
	s := newSelection()
	s.selectAll([]string{"a", "b", "c"})
	if s.mode != SelSet || len(s.set) != 3 {
		t.Fatalf("selectAll should select all 3 in Set mode, got %d", len(s.set))
	}
	got := s.selectedHashes()
	sort.Strings(got)
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("selectedHashes = %v, want [a b c]", got)
	}
	s.clear()
	if len(s.set) != 0 {
		t.Errorf("clear should empty the set, got %d", len(s.set))
	}
}

func TestSelection_visualRangeBounds(t *testing.T) {
	s := newSelection()
	s.beginVisual(5) // anchor at display index 5
	if s.mode != SelRange || !s.visual || s.anchor != 5 {
		t.Fatalf("beginVisual should enter visual Range at anchor 5, got mode=%v visual=%v anchor=%d", s.mode, s.visual, s.anchor)
	}
	// Cursor moved up to 2: range spans [2,5] regardless of direction.
	if lo, hi := s.rangeBounds(2); lo != 2 || hi != 5 {
		t.Errorf("rangeBounds(2) = [%d,%d], want [2,5]", lo, hi)
	}
	if lo, hi := s.rangeBounds(8); lo != 5 || hi != 8 {
		t.Errorf("rangeBounds(8) = [%d,%d], want [5,8]", lo, hi)
	}
	if !s.inRange(3, 2) || s.inRange(6, 2) {
		t.Error("inRange should include 3 and exclude 6 for range [2,5]")
	}
	// Confirm the range at cursor 2 → span fixed at [2,5].
	s.confirmVisual(2)
	if s.visual {
		t.Error("confirmVisual should stop extending")
	}
	if s.mode != SelRange {
		t.Error("confirmVisual should keep the Range mode/selection")
	}
	// Cursor now moves to 8 for navigation — the FIXED range must NOT drift; it must
	// stay [2,5] regardless of the live cursor.
	if lo, hi := s.rangeBounds(8); lo != 2 || hi != 5 {
		t.Errorf("fixed rangeBounds drifted with cursor: [%d,%d], want [2,5]", lo, hi)
	}
	if lo, hi := s.rangeBounds(0); lo != 2 || hi != 5 {
		t.Errorf("fixed rangeBounds drifted with cursor: [%d,%d], want [2,5]", lo, hi)
	}
}

func TestSelection_reset(t *testing.T) {
	s := newSelection()
	s.toggle("x", false)
	s.beginVisual(3)
	s.reset()
	if s.mode != SelSingle || s.visual || len(s.set) != 0 {
		t.Errorf("reset should return to clean Single, got mode=%v visual=%v set=%d", s.mode, s.visual, len(s.set))
	}
}
