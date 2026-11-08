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
	if s.mode != SelRange || s.anchor != 5 {
		t.Fatalf("beginVisual should start a band at anchor 5, got mode=%v anchor=%d", s.mode, s.anchor)
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
	// Ending the band with nothing marked returns to the plain cursor; with
	// marks it leaves the marked set.
	s.endBand()
	if s.mode != SelSingle || s.inRange(3, 2) {
		t.Errorf("endBand with no marks: mode=%v, want Single and no band", s.mode)
	}
	s.set["x"] = true
	s.beginVisual(1)
	s.endBand()
	if s.mode != SelSet {
		t.Errorf("endBand with marks: mode=%v, want Set", s.mode)
	}
}

func TestSelection_reset(t *testing.T) {
	s := newSelection()
	s.toggle("x", false)
	s.beginVisual(3)
	s.reset()
	if s.mode != SelSingle || s.inRange(3, 3) || len(s.set) != 0 {
		t.Errorf("reset should return to clean Single, got mode=%v set=%d", s.mode, len(s.set))
	}
}
