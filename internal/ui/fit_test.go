package ui

// fit_test.go covers the TUI frame staying inside the terminal: it never
// grows taller or wider than the screen.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// assertFrameFits fails when the rendered frame is taller or wider than the
// terminal. Bubble Tea drops the top lines of a too-tall frame and cuts the
// right side of a too-wide line, so either one hides part of the UI.
func assertFrameFits(t *testing.T, m Model, w, h int) {
	t.Helper()
	lines := strings.Split(m.View(), "\n")
	if len(lines) > h {
		t.Errorf("%dx%d: frame has %d lines", w, h, len(lines))
	}
	for i, line := range lines {
		if lw := lipgloss.Width(line); lw > w {
			t.Errorf("%dx%d: line %d is %d columns wide", w, h, i+1, lw)
			return
		}
	}
}

func TestActionPanel_narrowTerminalFitsHeight(t *testing.T) {
	for _, size := range [][2]int{{60, 16}, {66, 16}, {60, 20}, {66, 20}, {72, 16}} {
		for _, keys := range [][]string{{"m"}, {"L", "m"}} {
			m := resizeTo(newTestModelN(t, 12), size[0], size[1])
			m = send(m, keys...)
			assertFrameFits(t, m, size[0], size[1])
		}
	}
}

func TestSquashWizard_twoColumnLayoutFitsTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{88, 24}, {100, 30}, {140, 40}} {
		m := resizeTo(newTestModelN(t, 12), size[0], size[1])
		m = send(m, "s", "m", "q") // select all, open the panel, run squash
		if m.mode != modeSquash {
			t.Fatalf("expected the squash wizard, got mode %v", m.mode)
		}
		assertFrameFits(t, m, size[0], size[1])
	}
}
