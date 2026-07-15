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

// TestCycleWorkspaceKeepsSetSelection: an explicit Set selection must survive
// a tab switch — resetting it on navigation was the old (unwanted) behavior.
func TestCycleWorkspaceKeepsSetSelection(t *testing.T) {
	m := newTestModel(t)
	m.workspace = WSRestructure
	m.applyDefaultSelectionMode()
	h := m.plan.Commits[len(m.plan.Commits)-1].Hash // newest commit, never frozen
	m.sel.toggle(h, false)

	m.nextWorkspace()

	if m.sel.mode != SelSet || !m.sel.isSet(h) {
		t.Fatalf("explicit Set selection lost after workspace switch: mode=%v isSet=%v",
			m.sel.mode, m.sel.isSet(h))
	}
}

// TestCycleWorkspaceKeepsRangeSelection: a confirmed visual Range survives too.
func TestCycleWorkspaceKeepsRangeSelection(t *testing.T) {
	m := newTestModel(t)
	m.workspace = WSRestructure
	m.applyDefaultSelectionMode()
	m.sel.beginVisual(0)
	m.sel.confirmVisual(1)

	m.nextWorkspace()

	if m.sel.mode != SelRange || !m.sel.fixed {
		t.Fatalf("explicit Range selection lost after workspace switch: mode=%v fixed=%v",
			m.sel.mode, m.sel.fixed)
	}
}

// TestCycleWorkspaceWithoutSelectionAppliesDefault: with no explicit selection
// the per-workspace default mode still applies (Dates → whole active range).
func TestCycleWorkspaceWithoutSelectionAppliesDefault(t *testing.T) {
	m := newTestModel(t)
	m.workspace = WSRestructure
	m.applyDefaultSelectionMode()

	for i := 0; m.workspace != WSDates && i < len(workspaces); i++ {
		m.nextWorkspace()
	}

	if m.workspace != WSDates {
		t.Fatal("never reached the Dates workspace")
	}
	if m.sel.mode != SelActive {
		t.Fatalf("mode = %v, want SelActive on Dates with no explicit selection", m.sel.mode)
	}
}
