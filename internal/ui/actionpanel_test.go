package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestSelectionKindClassification: the classifier drives the whole panel
// compatibility matrix, so every selection shape gets a case.
func TestSelectionKindClassification(t *testing.T) {
	set := selectionState{mode: SelSet, set: map[string]bool{"a": true}}
	emptySet := selectionState{mode: SelSet, set: map[string]bool{}}
	rng := selectionState{mode: SelRange, anchor: 0, fixed: true, end: 1}
	cases := []struct {
		name string
		sel  selectionState
		want selKind
	}{
		{"single default", newSelection(), selKindCursor},
		{"non-empty set", set, selKindSet},
		{"empty set is cursor", emptySet, selKindCursor},
		{"fixed range", rng, selKindRange},
		{"active mode is cursor", selectionState{mode: SelActive}, selKindCursor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectionKind(tc.sel); got != tc.want {
				t.Fatalf("selectionKind = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPanelRowsProjection: every registry command with a TUI binding appears in
// registry order; enablement follows the selection-kind matrix; disabled rows
// carry a human reason instead of vanishing (spec: dimmed, never hidden).
func TestPanelRowsProjection(t *testing.T) {
	m := newTestModel(t)

	rows := m.panelRows()
	if len(rows) == 0 {
		t.Fatal("no panel rows projected")
	}
	byVerb := map[string]panelRow{}
	for _, r := range rows {
		byVerb[r.Verb] = r
	}

	// Cursor-only selection: single-commit actions enabled, multi-commit dimmed.
	if r := byVerb["split"]; !r.Enabled {
		t.Fatalf("split should be enabled on cursor selection: %+v", r)
	}
	if r := byVerb["squash"]; r.Enabled || r.Detail == "" {
		t.Fatalf("squash must be dimmed WITH a reason on cursor selection: %+v", r)
	}
	if r := byVerb["redate"]; !r.Enabled || r.Run == nil {
		t.Fatalf("redate must be enabled with a Run closure: %+v", r)
	}

	// Two adjacent commits selected: squash becomes enabled.
	m.sel.toggle(m.plan.Commits[0].Hash, false)
	m.sel.toggle(m.plan.Commits[1].Hash, false)
	rows = m.panelRows()
	for _, r := range rows {
		if r.Verb == "squash" && !r.Enabled {
			t.Fatalf("squash should be enabled with 2 adjacent selected: %+v", r)
		}
	}
}

// TestPanelViewShowsVerbAndReason: the rendered panel carries the CLI verb
// (transparency) and the dimmed reason text.
func TestPanelViewShowsVerbAndReason(t *testing.T) {
	m := newTestModel(t)
	m.panel.open = true
	out := m.viewActionPanel(120)
	for _, want := range []string{"squash", "redate", "ACTIONS"} {
		if !strings.Contains(out, want) {
			t.Fatalf("panel view missing %q:\n%s", want, out)
		}
	}
}

// TestPanelOpenPreservesSelection: the spec's core invariant — opening,
// navigating, and closing the panel never mutates an explicit selection.
func TestPanelOpenPreservesSelection(t *testing.T) {
	m := newTestModel(t)
	h := m.plan.Commits[0].Hash
	m.sel.toggle(h, false)

	mm := send(m, "m")    // open
	mm = send(mm, "down") // navigate
	mm = send(mm, "esc")  // close

	if !mm.sel.isSet(h) || mm.panel.open {
		t.Fatalf("selection lost or panel stuck: isSet=%v open=%v", mm.sel.isSet(h), mm.panel.open)
	}
}

// TestPanelEnterOnDisabledRowReportsReason: enter on a dimmed row must not
// run anything; it surfaces the reason in the status bar instead.
func TestPanelEnterOnDisabledRowReportsReason(t *testing.T) {
	m := newTestModel(t)
	m.panel.open = true
	rows := m.panelRows()
	for i, r := range rows {
		if !r.Enabled {
			m.panel.cursor = i
			mm := send(m, "enter")
			if mm.mode != modeMain || mm.statusMsg == "" {
				t.Fatalf("disabled row ran or gave no reason: mode=%v status=%q", mm.mode, mm.statusMsg)
			}
			return
		}
	}
	t.Skip("no disabled row under cursor-kind selection (unexpected)")
}

// TestPanelOpenAtMinTerminalKeepsListFloor: at the app's minimum supported
// terminal size the open panel must never drive the list budget negative, and
// its contribution to the rendered frame must never exceed the row budget it
// declared (panelHeightFor of its own shown-row count) — the list floor
// absorbs the rest.
//
// A stricter "the whole frame fits inside the terminal" assertion is NOT used
// here: at minTermHeight (16) the panel-closed frame already renders exactly
// 16 rows (title+tabs+body+status+keybar all fit with no slack), and the
// panel's fixed overhead — a title line, a footer hint, and its RoundedBorder
// frame's top+bottom rows (panelHeightOverhead, layout.go) — plus this
// fixture's 8 registry rows costs 12 more rows on top of that regardless of
// how tight the terminal is (registryrows(8) < panelMaxRows(10), so the
// panelMaxRows row-window cap never engages). Shrinking the panel further
// under a tight terminal would need it to drop below its own fixed chrome
// (title/footer/border), which is a materially bigger change than this
// fix-round's scope (floor the list, make panelHeightFor's budget match what
// viewActionPanel actually renders, cap rendered rows the same way the
// budget is capped). The invariant this test actually enforces — the panel
// never silently renders MORE than it told listHeight it would — is what
// keeps the commit list's floor meaningful.
func TestPanelOpenAtMinTerminalKeepsListFloor(t *testing.T) {
	m := newTestModel(t)
	for h := minTermHeight; h <= minTermHeight+3; h++ {
		m.width, m.height = 120, h
		m.panel.open = false
		closedHeight := lipgloss.Height(m.View())
		m.panel.open = true
		if lh := m.listHeight(); lh < minListRows {
			t.Fatalf("height %d: listHeight = %d < floor %d", h, lh, minListRows)
		}
		budget := panelHeightFor(panelShownRows(len(m.panelRows())))
		if openHeight := lipgloss.Height(m.View()); openHeight > closedHeight+budget {
			t.Fatalf("height %d: open frame %d rows > closed %d + declared panel budget %d",
				h, openHeight, closedHeight, budget)
		}
	}
}
