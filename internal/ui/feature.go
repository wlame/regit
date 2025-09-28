package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/capability"
)

// feature.go is the data-driven action registry. The tab bar and each
// workspace's menu are DERIVED from the tables here rather than hard-coded in the
// renderer or the key dispatcher, following the project's "prefer data over
// control flow" principle: adding or disabling a tab, or adding a feature, is a
// change to a table, not to branching code.

// WorkspaceID and SelectionMode are type aliases for the canonical definitions in
// internal/capability, which owns these enums as the single source of truth.
// Aliasing (not re-defining) means ui.WorkspaceID and capability.WorkspaceID are
// the same type, so values flow freely between the packages without conversion.
type WorkspaceID = capability.WorkspaceID
type SelectionMode = capability.SelectionMode

// Workspace tab identifiers — mirrors capability's constants so call-sites in
// this package can use the short name (WSDates) without a capability. prefix.
const (
	WSDates       = capability.WSDates
	WSRestructure = capability.WSRestructure
	WSHistory     = capability.WSHistory
	WSAuthorship  = capability.WSAuthorship
	WSSearch      = capability.WSSearch
)

// Selection mode constants — same aliasing rationale as the workspace IDs above.
const (
	SelSingle = capability.SelSingle // one commit (the cursor)
	SelRange  = capability.SelRange  // a contiguous run of commits
	SelSet    = capability.SelSet    // an arbitrary set of commits
	SelActive = capability.SelActive // the whole non-frozen range (no per-row pick)
	SelGlobal = capability.SelGlobal // no commit selection; repo-wide operation
)

// workspaceMeta is one tab definition. The tab bar is rendered by iterating this
// ordered table, so tab order and enablement are data, not code.
type workspaceMeta struct {
	ID      WorkspaceID
	Title   string
	Enabled bool // disabled tabs render dimmed and show a "coming soon" panel
}

// workspaces is the ordered tab list. History/Authorship/Search are present but
// disabled (stub tabs) so the chassis's extensibility is real and the three-zone
// layout is exercised before those features are built.
var workspaces = []workspaceMeta{
	{WSDates, "Dates", true},
	{WSRestructure, "Restructure", true},
	{WSHistory, "History", false},
	{WSAuthorship, "Authorship", true},
	{WSSearch, "Search", false},
}

// featureBinding is the TUI half of a registry command: how it appears in a menu
// (Title) and the closure that runs it against the Model. The capability registry
// owns the command's metadata (its Workspace, Selection, and params); internal/ui
// owns only this presentation-and-invocation half. The two are joined by command
// Name in featuresFor, so the menu is a PROJECTION of capability.Registry() rather
// than a parallel table that could silently drift from it.
type featureBinding struct {
	Title string
	Run   func(m Model) (tea.Model, tea.Cmd)
}

// featureBindings maps a capability command Name to its interactive presentation.
// A registry command with no entry here has no menu presence (e.g. "undo", which
// is a global key, not a menu row). "redate" binds to a nil Run because the Dates
// workspace is the calendar/strategy widget, not a menu — it is listed so the
// Dates tab still reports its one feature. Each Run re-invokes an existing action
// method, so this introduces no new mutation path (the safety contract is unchanged).
var featureBindings = map[string]featureBinding{
	"redate": {Title: "Date/time spread", Run: nil},
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
		m.retitle = newRetitleState()
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
}

// Feature is one menu row: a named action inside a workspace. Name and Run come
// from the TUI binding; Workspace and Selection are carried straight from the
// registry command so the menu cannot disagree with the headless surface.
type Feature struct {
	Name      string
	Workspace WorkspaceID
	Selection SelectionMode
	Run       func(m Model) (tea.Model, tea.Cmd)
}

// featuresFor returns a workspace's menu rows as a projection of the capability
// registry: every registry command whose Workspace matches and which has a TUI
// binding, in registry order. This is the single derivation point — the
// Restructure menu and the Dates feature list both come from here, so adding a
// headless verb with a Workspace and a binding automatically gives it a menu row.
func featuresFor(id WorkspaceID) []Feature {
	var out []Feature
	for _, c := range capability.Registry() {
		if c.Workspace != id {
			continue
		}
		b, ok := featureBindings[c.Name]
		if !ok {
			continue // headless-only command with no interactive menu presence
		}
		out = append(out, Feature{
			Name:      b.Title,
			Workspace: c.Workspace,
			Selection: c.Selection,
			Run:       b.Run,
		})
	}
	return out
}

// workspaceByID returns the tab metadata for an id and whether it exists.
func workspaceByID(id WorkspaceID) (workspaceMeta, bool) {
	for _, w := range workspaces {
		if w.ID == id {
			return w, true
		}
	}
	return workspaceMeta{}, false
}
