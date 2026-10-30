package ui

import (
	"testing"
	"time"

	"github.com/wlame/regit/internal/model"
)

// TestEnterRedateFlowDefaultsToActiveScope: with no explicit selection the
// redate flow operates on the whole active range (the old Dates default).
func TestEnterRedateFlowDefaultsToActiveScope(t *testing.T) {
	m := newTestModel(t)
	m.enterRedateFlow()
	if m.flow != flowRedate || m.sel.mode != SelActive || m.focus != focusCalendar {
		t.Fatalf("flow=%v selMode=%v focus=%v", m.flow, m.sel.mode, m.focus)
	}
}

// TestEnterRedateFlowKeepsExplicitSelection: an explicit Set survives entering
// the flow and scopes it (spec: selection is the subject).
func TestEnterRedateFlowKeepsExplicitSelection(t *testing.T) {
	m := newTestModel(t)
	h := m.plan.Commits[0].Hash
	m.sel.toggle(h, false)
	m.enterRedateFlow()
	if m.sel.mode != SelSet || !m.sel.isSet(h) {
		t.Fatalf("explicit selection lost entering flow: mode=%v", m.sel.mode)
	}
}

// TestEnterRedateFlowKeepsExplicitRangeSelection: a confirmed visual Range
// survives entering the flow too (Set is covered by
// TestEnterRedateFlowKeepsExplicitSelection above).
func TestEnterRedateFlowKeepsExplicitRangeSelection(t *testing.T) {
	m := newTestModel(t)
	m.sel.beginVisual(0)
	m.sel.confirmVisual(1)

	m.enterRedateFlow()

	if m.sel.mode != SelRange || !m.sel.fixed {
		t.Fatalf("explicit Range selection lost entering the flow: mode=%v fixed=%v",
			m.sel.mode, m.sel.fixed)
	}
}

// TestSelectionChange_withNoFlowOpenKeepsEditorDateEdit: onDatesSelectionChanged
// is a no-op unless the redate flow is open, so changing the commit selection
// (e.g. selecting all) with no flow open must never discard a date edit staged
// from the per-commit editor — the old tab chassis guarded the same invariant
// via the workspace check.
func TestSelectionChange_withNoFlowOpenKeepsEditorDateEdit(t *testing.T) {
	m := newTestModel(t)
	newDate := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)
	m.plan.Edits[m.plan.Commits[0].Hash] = model.Edit{NewAuthorDate: &newDate}

	m = send(m, "s") // select all active commits

	e, ok := m.plan.Edits[m.plan.Commits[0].Hash]
	if !ok || e.NewAuthorDate == nil || !e.NewAuthorDate.Equal(newDate) {
		t.Fatalf("editor-staged date edit lost after a selection change with no flow open: %+v", e)
	}
}

// TestExitFlowRestoresDetailAndKeepsEdits: leaving the flow returns the zone
// to detail, restores Single mode from the flow's implicit Active scope, and
// never discards staged edits (they reset via R, not via navigation).
func TestExitFlowRestoresDetailAndKeepsEdits(t *testing.T) {
	m := newTestModel(t)
	m.enterRedateFlow()
	d := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)
	m.plan.Edits[m.plan.Commits[0].Hash] = model.Edit{NewAuthorDate: &d}

	m.exitFlow()

	if m.flow != flowNone || m.focus != focusList || m.sel.mode != SelSingle {
		t.Fatalf("flow=%v focus=%v selMode=%v", m.flow, m.focus, m.sel.mode)
	}
	if e := m.plan.Edits[m.plan.Commits[0].Hash]; e.NewAuthorDate == nil {
		t.Fatal("staged edit lost on flow exit")
	}
}

// TestExitFlow_keepsExplicitSetSelection: an explicit Set selection is left
// untouched by exitFlow — only the flow's own implicit Active scope reverts
// (the invariant: navigation never drops an explicit selection). Complements
// TestExitFlowRestoresDetailAndKeepsEdits, which only exercises the implicit
// (no-selection) case.
func TestExitFlow_keepsExplicitSetSelection(t *testing.T) {
	m := newTestModel(t)
	h := m.plan.Commits[0].Hash
	m.sel.toggle(h, false)
	m.enterRedateFlow()

	m.exitFlow()

	if m.sel.mode != SelSet || !m.sel.isSet(h) {
		t.Fatalf("explicit Set selection lost on flow exit: mode=%v", m.sel.mode)
	}
	if m.flow != flowNone || m.focus != focusList {
		t.Fatalf("flow=%v focus=%v", m.flow, m.focus)
	}
}

// TestFlowExit_viaEscFromCalendarFocus drives the real key path (Update ->
// updateMain) instead of calling exitFlow directly, proving the esc-exits-flow
// case in updateMain's switch actually fires when the calendar is focused.
func TestFlowExit_viaEscFromCalendarFocus(t *testing.T) {
	m := newTestModel(t)
	m.enterRedateFlow()
	if m.focus != focusCalendar {
		t.Fatalf("setup: expected calendar focus, got %v", m.focus)
	}

	m = send(m, "esc")

	if m.flow != flowNone || m.focus != focusList {
		t.Fatalf("esc from calendar should exit the flow: flow=%v focus=%v", m.flow, m.focus)
	}
}

// TestFlowExit_viaEscTwiceFromListWithExplicitSet: with the list focused and
// an explicit Set selection, the layering documented in updateMain applies —
// the FIRST esc is consumed by listSelectionKey (cancels the selection, back
// to the flow's Active default); only the SECOND esc, once no explicit
// selection remains, reaches updateMain's own esc case and exits the flow.
func TestFlowExit_viaEscTwiceFromListWithExplicitSet(t *testing.T) {
	m := newTestModel(t)
	h := m.plan.Commits[0].Hash
	m.sel.toggle(h, false) // explicit Set, before entering the flow
	m.enterRedateFlow()
	m.focus = focusList // tab back to the list without leaving the flow

	m = send(m, "esc")
	if m.flow != flowRedate {
		t.Fatalf("first esc should not exit the flow yet, got flow=%v", m.flow)
	}
	if m.sel.mode != SelActive {
		t.Fatalf("first esc should cancel the Set back to the flow's Active default, got %v", m.sel.mode)
	}

	m = send(m, "esc")
	if m.flow != flowNone || m.focus != focusList {
		t.Fatalf("second esc should exit the flow: flow=%v focus=%v", m.flow, m.focus)
	}
}

// TestReload_keepsOpenFlowsActiveScope: reload() must not silently drop the
// redate flow's implicit Active scope back to Single underneath an open flow —
// restoreFlowSelectionScope (not the deleted applyDefaultSelectionMode) is what
// keeps it alive across a reload.
func TestReload_keepsOpenFlowsActiveScope(t *testing.T) {
	m := newTestModelN(t, 3)
	m.enterRedateFlow() // no explicit selection -> Active
	if m.sel.mode != SelActive {
		t.Fatalf("setup: expected Active scope, got %v", m.sel.mode)
	}

	if err := m.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if m.flow != flowRedate {
		t.Errorf("reload should not close the open flow, got flow=%v", m.flow)
	}
	if m.sel.mode != SelActive {
		t.Errorf("reload should keep the flow's Active scope, got %v", m.sel.mode)
	}
}

func TestRedateFlow_reopeningDiscardsPartlyTypedQuota(t *testing.T) {
	m := newTestModelN(t, 12)
	manualPin := -1
	for i, s := range m.strategies {
		if s.Name() == "Manual Pin" {
			manualPin = i
		}
	}
	m = selectStrategy(m, manualPin)
	m = send(m, "esc", "tab") // params -> list -> calendar
	day := m.calCursor.Format(dateLayout)
	m = send(m, "2")      // start typing a quota
	m = send(m, "esc")    // leave the flow
	m = send(m, "m", "d") // reopen it

	m = send(m, "1")

	if got := m.activeStrat().quotas[day]; got != 1 {
		t.Errorf("quota = %d after reopening and typing 1; want 1", got)
	}
}
