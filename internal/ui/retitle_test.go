package ui

import "testing"

func TestNewRetitleStateUsesGivenInputWidth(t *testing.T) {
	s := newRetitleState(64)
	if s.pattern.Width != 64 || s.replace.Width != 64 {
		t.Fatalf("input widths = %d/%d, want 64", s.pattern.Width, s.replace.Width)
	}
}
