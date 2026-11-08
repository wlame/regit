package ui

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/reword"
)

// rewordWizardAt opens the reword wizard at the given terminal size with a
// pre-buffered result, so no LLM call is made.
func rewordWizardAt(t *testing.T, w, h int) Model {
	t.Helper()
	m := resizeTo(newTestModelN(t, 4), w, h)
	c := m.commitAtCursor()
	m.rewordSt.buffer = map[string]reword.Result{c.Hash: {Variants: []reword.Variant{
		{Title: "Rotate the signing keys", Body: "Explain why the keys rotate."},
	}}}
	m.rewordSt.inFlight = map[string]bool{}
	model, _, _ := m.beginReword()
	m = model.(Model)
	if m.mode != modeReword {
		t.Fatalf("beginReword should open the wizard, got mode %v", m.mode)
	}
	return m
}

func TestRewordWizard_drawsInTheFlowZoneBesideTheList(t *testing.T) {
	m := rewordWizardAt(t, 120, 40)

	view := stripANSI(m.View())

	if !hasBorderTitle(view, "REWORD") || !strings.Contains(view, "Rotate the signing keys") {
		t.Errorf("the wizard should be a titled pane in the flow zone:\n%s", view)
	}
	if !strings.Contains(view, short(m.commitAtCursor().Hash)) {
		t.Errorf("the commit list should stay visible beside the wizard:\n%s", view)
	}
	assertFrameFits(t, m, 120, 40)
}

func TestRewordWizard_narrowTerminalShowsTheWizard(t *testing.T) {
	m := rewordWizardAt(t, 80, 24)

	if view := stripANSI(m.View()); !strings.Contains(view, "Rotate the signing keys") {
		t.Errorf("one-column layout should show the open wizard:\n%s", view)
	}
	assertFrameFits(t, m, 80, 24)
}

func TestRewordWizard_keybarDropsListKeys(t *testing.T) {
	m := rewordWizardAt(t, 120, 40)

	if bar := keybarHintFor(m.currentKeyCtx(), 120); strings.Contains(bar, "[space]mark") {
		t.Errorf("the wizard owns the keys; the keybar must not offer list keys: %q", bar)
	}
}
