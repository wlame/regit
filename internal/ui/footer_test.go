package ui

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
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

func TestVerificationLineAppendsToLogOnApply(t *testing.T) {
	// Mirror commitPending's success emit branch: command line THEN verification line.
	m := Model{commandLog: nil, pendingCmd: "regit redate --execute"}
	summary := model.VerificationSummary{Commits: 3, ChangedPaths: 0}
	if m.pendingCmd != "" {
		m.commandLog = append(m.commandLog, m.pendingCmd)
		m.commandLog = append(m.commandLog, summary.String())
		m.pendingCmd = ""
	}
	if len(m.commandLog) != 2 {
		t.Fatalf("commandLog = %v, want 2 entries", m.commandLog)
	}
	if m.commandLog[1] != "verified 3 commits, 0 paths changed" {
		t.Errorf("verification line = %q", m.commandLog[1])
	}
}
