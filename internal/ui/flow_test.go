package ui

import (
	"strings"
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

// TestEnterRedateFlowKeepsExplicitRangeSelection: a marked range (added to the
// marks with v/enter) survives entering the flow too (Set is covered by
// TestEnterRedateFlowKeepsExplicitSelection above).
func TestEnterRedateFlowKeepsExplicitRangeSelection(t *testing.T) {
	m := newTestModel(t)
	m.sel.beginVisual(0)
	m.listCursor = 1
	m.finishRange()

	m.enterRedateFlow()

	if m.sel.mode != SelSet || len(m.markedHashes()) != 2 {
		t.Fatalf("marked range lost entering the flow: mode=%v marked=%d",
			m.sel.mode, len(m.markedHashes()))
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

// narrowSizes are terminals below the two-column breakpoint, down to the
// minimum the app allows.
var narrowSizes = [][2]int{{80, 24}, {70, 20}, {60, 16}}

func TestRedateFlow_narrowTerminalShowsTheFocusedPane(t *testing.T) {
	for _, size := range narrowSizes {
		m := resizeTo(newTestModelN(t, 12), size[0], size[1])
		cursorHash := short(m.commitAtCursor().Hash)

		m = send(m, "m", "d") // open the flow; focus lands on the calendar
		flowView := stripANSI(m.View())
		if strings.Contains(flowView, cursorHash) {
			t.Errorf("%dx%d: the open flow should replace the list:\n%s", size[0], size[1], flowView)
		}
		// Below 20 rows the strategy pane is cut by the height budget at any
		// width, so only check for it where it fits.
		if size[1] >= 20 && !strings.Contains(flowView, "STRATEGY") {
			t.Errorf("%dx%d: the flow's strategy pane is missing:\n%s", size[0], size[1], flowView)
		}
		assertFrameFits(t, m, size[0], size[1])

		m = send(m, "tab", "tab") // calendar -> params -> list, flow still open
		listView := stripANSI(m.View())
		if m.flow != flowRedate || !strings.Contains(listView, cursorHash) {
			t.Errorf("%dx%d: with focus back on the list, the list should show:\n%s", size[0], size[1], listView)
		}
	}
}

func TestRedactFlow_narrowTerminalShowsTheFlow(t *testing.T) {
	for _, size := range narrowSizes {
		m := resizeTo(newTestModelN(t, 12), size[0], size[1])

		m = send(m, "m", "r")

		if view := stripANSI(m.View()); !strings.Contains(view, "REDACT") {
			t.Errorf("%dx%d: the redact flow is open but not shown:\n%s", size[0], size[1], view)
		}
		assertFrameFits(t, m, size[0], size[1])
	}
}

func TestRedateFlow_bracketKeysCycleStrategyAndTimeModelFromCalendar(t *testing.T) {
	m := send(newTestModelN(t, 6), "m", "d") // focus lands on the calendar
	strat, cursor := m.stratIdx, m.calCursor
	timeModel := m.activeStrat().form.timeModelIdx

	m = send(m, "]")
	if m.stratIdx != (strat+1)%len(m.strategies) {
		t.Errorf("] should select the next strategy: idx %d -> %d", strat, m.stratIdx)
	}
	m = send(m, "[")
	if m.stratIdx != strat {
		t.Errorf("[ should select the previous strategy: got idx %d, want %d", m.stratIdx, strat)
	}
	m = send(m, ">")
	if got := m.activeStrat().form.timeModelIdx; got != (timeModel+1)%len(m.activeStrat().form.timeModels) {
		t.Errorf("> should select the next time model: idx %d -> %d", timeModel, got)
	}
	m = send(m, "<")
	if got := m.activeStrat().form.timeModelIdx; got != timeModel {
		t.Errorf("< should select the previous time model: got idx %d, want %d", got, timeModel)
	}
	if m.focus != focusCalendar || !m.calCursor.Equal(cursor) {
		t.Errorf("the helpers must not move focus or the calendar cursor (focus=%v)", m.focus)
	}
}

func TestRedateFlow_bracketKeysAlsoWorkFromStrategyPane(t *testing.T) {
	m := send(newTestModelN(t, 6), "m", "d", "tab") // calendar -> strategy pane
	strat := m.stratIdx

	m = send(m, "]")

	if m.focus != focusParams || m.stratIdx != (strat+1)%len(m.strategies) {
		t.Errorf("] in the strategy pane should select the next strategy (focus=%v idx %d -> %d)", m.focus, strat, m.stratIdx)
	}
}

// TestRedateFlow_calendarGetsItsFullWidth: the calendar needs rightPaneWidth
// columns. A narrower flow column wraps every week row, doubles the calendar's
// height, and pushes the strategy pane off the screen.
func TestRedateFlow_calendarGetsItsFullWidth(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {100, 40}} {
		m := send(resizeTo(newTestModelN(t, 6), size[0], size[1]), "m", "d")

		if w := m.layout().flowWidth; w < rightPaneWidth {
			t.Errorf("%dx%d: flow column is %d wide, the calendar needs %d", size[0], size[1], w, rightPaneWidth)
		}
		if view := stripANSI(m.View()); !strings.Contains(view, "STRATEGY") {
			t.Errorf("%dx%d: the strategy pane is off the screen:\n%s", size[0], size[1], view)
		}
		assertFrameFits(t, m, size[0], size[1])
	}
}
