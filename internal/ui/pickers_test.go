package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPicker_filterAndSelect(t *testing.T) {
	p := newPicker("Branches", []string{"main", "feature/auth", "feature/ui", "release"})

	// Empty filter returns everything.
	if got := len(p.filtered()); got != 4 {
		t.Fatalf("empty filter should return all 4, got %d", got)
	}

	// Typing narrows the list (case-insensitive substring).
	p.filter.SetValue("FEAT")
	got := p.filtered()
	if len(got) != 2 || got[0] != "feature/auth" || got[1] != "feature/ui" {
		t.Fatalf("filter FEAT = %v, want the two feature branches", got)
	}

	// Cursor selects within the filtered set and clamps.
	p.moveCursor(1)
	if sel, ok := p.selected(); !ok || sel != "feature/ui" {
		t.Errorf("selected after one move = %q (ok=%v), want feature/ui", sel, ok)
	}
	p.moveCursor(5) // past the end -> clamp to last
	if sel, _ := p.selected(); sel != "feature/ui" {
		t.Errorf("cursor should clamp to the last filtered item, got %q", sel)
	}
}

func TestPicker_updateKeepsCursorInRange(t *testing.T) {
	p := newPicker("Branches", []string{"main", "feature/auth", "feature/ui"})
	p.filter.SetValue("feature")
	p.moveCursor(1) // cursor on "feature/ui" (index 1 of the filtered pair)

	// Narrow further so only one item remains; the cursor must clamp back.
	p.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/ui")})
	if got := len(p.filtered()); got != 1 {
		t.Fatalf("filter 'feature/ui' should match 1, got %d", got)
	}
	if sel, ok := p.selected(); !ok || sel != "feature/ui" {
		t.Errorf("selected after narrowing = %q (ok=%v), want feature/ui", sel, ok)
	}
}
