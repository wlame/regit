package ui

import tea "github.com/charmbracelet/bubbletea"

// feature.go is the data-driven action registry. The tab bar and each
// workspace's menu are DERIVED from the tables here rather than hard-coded in the
// renderer or the key dispatcher, following the project's "prefer data over
// control flow" principle: adding or disabling a tab, or adding a feature, is a
// change to a table, not to branching code.

// WorkspaceID identifies one of the fixed category tabs. This set is BOUNDED — it
// never grows with the feature count — so the tab bar can never overflow (spec
// §4.1, req #7). Features live as rows inside a workspace, not as new tabs.
type WorkspaceID int

const (
	WSDates WorkspaceID = iota
	WSRestructure
	WSHistory
	WSAuthorship
	WSSearch
)

// SelectionMode is what a feature needs the user to select before it runs. It is
// declared here as data so the registry stays the single source of truth; the
// commit list's per-mode visual language is wired in Phase 4 (selection models)
// and reads this value. v1 features use only Single and Active.
type SelectionMode int

const (
	SelSingle SelectionMode = iota // one commit (the cursor)
	SelRange                       // a contiguous run of commits
	SelSet                         // an arbitrary set of commits
	SelActive                      // the whole non-frozen range (no per-row pick)
	SelGlobal                      // no commit selection; repo-wide operation
)

// workspaceMeta is one tab definition. The tab bar is rendered by iterating this
// ordered table, so tab order and enablement are data, not code.
type workspaceMeta struct {
	ID      WorkspaceID
	Title   string
	Enabled bool // disabled tabs render dimmed and show a "coming soon" panel
}

// workspaces is the ordered tab list. History/Authorship/Search are present but
// disabled in v1 (stub tabs) so the chassis's extensibility is real and the
// three-zone layout is exercised before those features are built (spec §6.5,
// decision #7).
var workspaces = []workspaceMeta{
	{WSDates, "Dates", true},
	{WSRestructure, "Restructure", true},
	{WSHistory, "History", false},
	{WSAuthorship, "Authorship", false},
	{WSSearch, "Search", false},
}

// Feature is a registry row: a named action that lives inside a workspace. A
// workspace's menu is the features whose Workspace matches, in registry order.
// Run performs the action against the model, reusing the existing action methods;
// holding it as a function value keeps dispatch data-driven instead of a growing
// switch. Run is nil for the Dates "feature" because the Dates workspace is the
// rich calendar/strategy widget, not a menu (it is listed only for completeness).
type Feature struct {
	Name      string
	Workspace WorkspaceID
	Selection SelectionMode
	Run       func(m Model) (tea.Model, tea.Cmd)
}

// features is the single source of truth for the action surface. Restructure
// hosts the v1 history-editing actions; each Run re-invokes an existing method so
// Phase 3 introduces no new mutation path (the safety contract is unchanged).
var features = []Feature{
	{Name: "Date/time spread", Workspace: WSDates, Selection: SelActive, Run: nil},
	{Name: "Split commit", Workspace: WSRestructure, Selection: SelSingle, Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginSplit()
		return model, cmd
	}},
	{Name: "Reword message", Workspace: WSRestructure, Selection: SelSingle, Run: func(m Model) (tea.Model, tea.Cmd) {
		model, cmd, _ := m.beginReword()
		return model, cmd
	}},
	{Name: "Retitle (regex)", Workspace: WSRestructure, Selection: SelActive, Run: func(m Model) (tea.Model, tea.Cmd) {
		m.retitle = newRetitleState()
		m.mode = modeRetitle
		return m, nil
	}},
	{Name: "Drop bodies", Workspace: WSRestructure, Selection: SelActive, Run: func(m Model) (tea.Model, tea.Cmd) {
		m.mode = modeDropBodies
		return m, nil
	}},
}

// featuresFor returns the features owned by a workspace, in registry order. The
// Restructure menu and any future workspace menu are rendered straight from this.
func featuresFor(id WorkspaceID) []Feature {
	var out []Feature
	for _, f := range features {
		if f.Workspace == id {
			out = append(out, f)
		}
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
