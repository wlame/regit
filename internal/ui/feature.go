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
	// Desc is a short, static "what this does" phrase shown in grey next to the
	// title in the action panel. It is presentation text (like Title), kept here
	// rather than derived from the registry's Summary because the Summary is a
	// full help sentence — too long for the panel's compact description column.
	Desc string
	// Key is the panel hotkey: pressing this letter while the panel is open
	// runs the row exactly like enter. One lowercase letter, unique across
	// bindings, never a panel navigation key — all pinned by
	// TestFeatureBindings_hotkeysUniqueAndOffReservedKeys.
	Key string
	Run func(m Model) (tea.Model, tea.Cmd)
}

// featureBindings maps a capability command Name to its interactive
// presentation. A registry command with no entry here has no panel presence
// (e.g. "undo", which is a global key, not a panel row). Each Run re-invokes
// an existing action method, so binding a command here introduces no new
// mutation path (the safety contract is unchanged); "redate" opens the redate
// flow (flow.go).
var featureBindings = map[string]featureBinding{
	"redate": {Title: "Date/time spread", Desc: "Spread commit dates across the calendar", Key: "d", Run: func(m Model) (tea.Model, tea.Cmd) {
		m.enterRedateFlow()
		return m, nil
	}},
	"split": {Title: "Split commit", Desc: "Break one commit into several", Key: "s", Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginSplit()
		return model, cmd
	}},
	"reword": {Title: "Reword message", Desc: "Rewrite a commit's message", Key: "w", Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginReword()
		return model, cmd
	}},
	"squash": {Title: "Squash commits", Desc: "Combine consecutive commits into one", Key: "q", Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginSquash()
		return model, cmd
	}},
	"linearize": {Title: "Linearize history", Desc: "Fold merges into a first-parent chain", Key: "l", Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginLinearize()
		return model, cmd
	}},
	"retitle": {Title: "Retitle (regex)", Desc: "Rewrite subjects with a regex rule", Key: "t", Run: func(m Model) (tea.Model, tea.Cmd) {
		m.retitle = newRetitleState(m.layout().inputWidth)
		m.mode = modeRetitle
		return m, nil
	}},
	"drop-bodies": {Title: "Drop bodies", Desc: "Remove message bodies, keep the titles", Key: "b", Run: func(m Model) (tea.Model, tea.Cmd) {
		m.mode = modeDropBodies
		return m, nil
	}},
	"reauthor": {Title: "Change author", Desc: "Rewrite the author / committer identity", Key: "a", Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginReauthor()
		return model, cmd
	}},
	"purge": {Title: "Purge from history", Desc: "Erase a file from all of history", Key: "p", Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginPurge()
		return model, cmd
	}},
	"redact": {Title: "Redact secret", Desc: "Erase a secret line from all of history", Key: "r", Run: func(m Model) (tea.Model, tea.Cmd) {
		m.enterRedactFlow()
		return m, nil
	}},
}
