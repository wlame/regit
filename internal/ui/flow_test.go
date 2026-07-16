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
