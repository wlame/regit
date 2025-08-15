package ui

import (
	"strings"
	"testing"
)

func TestCommandLogToggleAndRender(t *testing.T) {
	m := Model{width: 100, height: 40}
	if m.viewCommandLog() != "" {
		t.Error("hidden log should render empty")
	}
	m.logVisible = true
	m.commandLog = []string{"regit redate --strategy=Even-Spread --execute"}
	out := m.viewCommandLog()
	if !strings.Contains(out, "redate --strategy=Even-Spread") {
		t.Errorf("log panel missing entry:\n%s", out)
	}
	if !strings.Contains(out, "command log") {
		t.Errorf("log panel missing header:\n%s", out)
	}
}
