package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/capability"
)

// feature.go is the data-driven action registry: featureBindings maps a
// capability command to its interactive presentation, following the project's
// "prefer data over control flow" principle. The action panel (actionpanel.go)
// is the sole menu surface derived from this table now that the tab/workspace
// chassis is gone; a launched feature either opens a flow (flow.go) or an
// existing overlay/wizard.

// SelectionMode is a type alias for the canonical definition in
// internal/capability, which owns this enum as the single source of truth.
// Aliasing (not re-defining) means ui.SelectionMode and capability.SelectionMode
// are the same type, so values flow freely between the packages without
// conversion.
type SelectionMode = capability.SelectionMode

// Selection mode constants — same aliasing rationale as above.
const (
	SelSingle = capability.SelSingle // one commit (the cursor)
	SelRange  = capability.SelRange  // a contiguous run of commits
	SelSet    = capability.SelSet    // an arbitrary set of commits
	SelActive = capability.SelActive // the whole non-frozen range (no per-row pick)
	SelGlobal = capability.SelGlobal // no commit selection; repo-wide operation
)

// featureBinding is the TUI half of a registry command: how it appears in the
// action panel (Title) and the closure that runs it against the Model. The
// capability registry owns the command's metadata (its Selection and params);
// internal/ui owns only this presentation-and-invocation half. The two are
// joined by command Name in panelRows (actionpanel.go), so the panel is a
// PROJECTION of capability.Registry() rather than a parallel table that could
// silently drift from it.
type featureBinding struct {
	Title string
	Run   func(m Model) (tea.Model, tea.Cmd)
}

// featureBindings maps a capability command Name to its interactive
// presentation. A registry command with no entry here has no panel presence
// (e.g. "undo", which is a global key, not a panel row). Each Run re-invokes
// an existing action method, so binding a command here introduces no new
// mutation path (the safety contract is unchanged); "redate" opens the redate
// flow (flow.go).
var featureBindings = map[string]featureBinding{
	"redate": {Title: "Date/time spread", Run: func(m Model) (tea.Model, tea.Cmd) {
		m.enterRedateFlow()
		return m, nil
	}},
	"split": {Title: "Split commit", Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginSplit()
		return model, cmd
	}},
	"reword": {Title: "Reword message", Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginReword()
		return model, cmd
	}},
	"squash": {Title: "Squash commits", Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginSquash()
		return model, cmd
	}},
	"retitle": {Title: "Retitle (regex)", Run: func(m Model) (tea.Model, tea.Cmd) {
		m.retitle = newRetitleState(computeLayout(m.width, m.height).inputWidth)
		m.mode = modeRetitle
		return m, nil
	}},
	"drop-bodies": {Title: "Drop bodies", Run: func(m Model) (tea.Model, tea.Cmd) {
		m.mode = modeDropBodies
		return m, nil
	}},
	"reauthor": {Title: "Change author", Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginReauthor()
		return model, cmd
	}},
	"purge": {Title: "Purge from history", Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginPurge()
		return model, cmd
	}},
}
