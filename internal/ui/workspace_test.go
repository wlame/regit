package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestViewAuthorsPane_listsContributorsAndLightsCursorAuthor(t *testing.T) {
	// Pin a color profile so the styled labels actually emit escape codes
	// regardless of the test host's TTY, matching contributors_render_test.go.
	orig := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(orig)

	m := twoAuthorModel(t) // cursor at index 0 = newest commit = "alice" (h cccc… is newest)
	pane := m.viewAuthorsPane(12)
	plain := stripANSI(pane)
	if !strings.Contains(plain, "AUTHORS") {
		t.Fatalf("panel should have an AUTHORS title:\n%s", plain)
	}
	if !strings.Contains(plain, "alice") || !strings.Contains(plain, "bob") {
		t.Errorf("panel should list both contributors:\n%s", plain)
	}
	// The lit (cursor author) line uses the ● glyph; others use ·.
	if !strings.Contains(plain, "●") {
		t.Errorf("cursor author should be marked with ●:\n%s", plain)
	}
}

func TestViewAuthorsPane_tooSmallBudgetRendersNothing(t *testing.T) {
	m := twoAuthorModel(t)
	if got := m.viewAuthorsPane(2); got != "" {
		t.Errorf("budget below minAuthorsPaneHeight must render nothing, got %q", got)
	}
}
