package ui

// flow.go models the flow zone's occupant. The right column shows the commit
// detail by default (flowNone); launching a feature that needs an interactive
// stage swaps its widget in for the duration of the flow. Redate is currently
// the only flow; the remaining wizards migrate here over time.
type flowID int

const (
	flowNone   flowID = iota // flow zone shows the commit detail
	flowRedate               // calendar + strategy form (was the Dates tab)
)

// enterRedateFlow opens the redate flow: focus lands on the calendar, and
// with no explicit selection the flow scopes to the whole active range —
// exactly the old Dates-tab default. An explicit Set/Range is preserved and
// becomes the redate target (unselected commits act as anchors).
func (m *Model) enterRedateFlow() {
	m.flow = flowRedate
	m.focus = focusCalendar
	if !m.hasExplicitSelection() {
		m.sel.mode = SelActive
	}
	m.onDatesSelectionChanged()
}

// exitFlow returns the zone to the commit detail. The implicit Active scope
// reverts to Single; an explicit selection is untouched (the invariant:
// navigation never drops a selection). Staged edits survive — R resets them.
func (m *Model) exitFlow() {
	m.flow = flowNone
	m.focus = focusList
	if m.sel.mode == SelActive {
		m.sel.reset()
	}
}

// restoreFlowSelectionScope reapplies the redate flow's implicit selection
// scope after an explicit selection is cleared: Active while the flow is open
// and no explicit selection remains (mirroring enterRedateFlow's own default),
// otherwise left as sel.reset() set it (Single). Shared by every site that
// resets the selection, so the flow's whole-active-range default never
// silently reverts to Single out from under it.
func (m *Model) restoreFlowSelectionScope() {
	if m.flow == flowRedate && !m.hasExplicitSelection() {
		m.sel.mode = SelActive
	}
}
