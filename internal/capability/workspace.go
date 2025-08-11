package capability

// WorkspaceID identifies a TUI category tab. Moved here from internal/ui so the
// registry (the single source of truth) owns it; internal/ui aliases these.
type WorkspaceID int

const (
	WSDates WorkspaceID = iota
	WSRestructure
	WSHistory
	WSAuthorship
	WSSearch
	WSNone // headless-only / not a menu row (e.g. undo, which is a global key)
)

// SelectionMode is what a feature needs selected before it runs (TUI concern,
// declared here so the registry stays the single source of truth).
type SelectionMode int

const (
	SelSingle SelectionMode = iota
	SelRange
	SelSet
	SelActive
	SelGlobal
)
