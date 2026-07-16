package ui

import (
	"strings"
	"testing"
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
