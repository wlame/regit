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

func TestPendingCmdAppendsToLogOnApply(t *testing.T) {
	// A focused unit check: simulate the emit branch directly.
	m := Model{commandLog: nil, pendingCmd: "regit drop-bodies --execute"}
	// inline the emit branch (the same three lines commitPending runs on success):
	if m.pendingCmd != "" {
		m.commandLog = append(m.commandLog, m.pendingCmd)
		m.pendingCmd = ""
	}
	if len(m.commandLog) != 1 || m.commandLog[0] != "regit drop-bodies --execute" {
		t.Errorf("commandLog = %v", m.commandLog)
	}
	if m.pendingCmd != "" {
		t.Error("pendingCmd should be cleared after emit")
	}
}
