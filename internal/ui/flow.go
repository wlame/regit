package ui

// flow.go models the flow zone's occupant. The right column shows the commit
// detail by default (flowNone); launching a feature that needs an interactive
// stage swaps its widget in for the duration of the flow. Redate and redact
// are today's two flows; the remaining wizards migrate here over time.
type flowID int

const (
	flowNone   flowID = iota // flow zone shows the commit detail
	flowRedate               // calendar + strategy form (was the Dates tab)
	flowRedact               // match/glob → occurrence picker → mode wizard (redact.go)
)

// enterRedateFlow opens the redate flow: focus lands on the calendar, and
// with no explicit selection the flow scopes to the whole active range —
// exactly the old Dates-tab default. An explicit Set/Range is preserved and
// becomes the redate target (unselected commits act as anchors).
func (m *Model) enterRedateFlow() {
	m.flow = flowRedate
	m.focus = focusCalendar
	m.quotaTyping = false // a digit typed before the last exit starts no quota
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
	m.quotaTyping = false
	if m.sel.mode == SelActive {
		m.sel.reset()
	}
}

// enterRedactFlow opens the redact flow at step 1 (match/glob input). Unlike
// enterRedateFlow, this never touches m.sel: redact is SelGlobal/repo-level
// (see the registry row), so it has no notion of "scope to the selection" to
// preserve or default — the commit-list selection is simply left exactly as
// the operator had it, and stays untouched for the whole flow (including
// exitFlow on esc/apply, which only resets the SelActive case redate uses).
//
// m.redactScanSeq is a Model-level field, deliberately NOT reset by the fresh
// redactState this builds: unlike purge (whose scan starts the instant the
// wizard opens), redact's scan only launches once step 1 is submitted (see
// beginRedactScan) — so the generation token has to survive an open step-1
// screen, and re-entering the flow before ever submitting must not let a
// still-in-flight scan from an abandoned EARLIER session collide with a scan
// the new session goes on to start.
func (m *Model) enterRedactFlow() {
	m.redactSt = newRedactState(computeLayout(m.width, m.height).inputWidth)
	m.flow = flowRedact
	m.focus = focusRedact
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
