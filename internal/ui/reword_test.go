package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/reword"
)

func sampleRewordResult() reword.Result {
	return reword.Result{CommitHash: "abc", Variants: []reword.Variant{
		{Title: "Add retry to the client", Body: "explain why"},
		{Title: "Make the client resilient", Body: ""},
	}}
}

func TestRenderRewordPicker_showsSelectedTitleAndBody(t *testing.T) {
	view := renderRewordPicker(sampleRewordResult(), reword.Selection{TitleIdx: 0, BodyIdx: 0})
	if !strings.Contains(view, "Add retry to the client") || !strings.Contains(view, "explain why") {
		t.Fatalf("picker missing selected content:\n%s", view)
	}
}

func TestRenderRewordPicker_noBody(t *testing.T) {
	view := renderRewordPicker(sampleRewordResult(), reword.Selection{TitleIdx: 1, BodyIdx: 0, NoBody: true})
	if !strings.Contains(view, "Make the client resilient") {
		t.Fatalf("missing title:\n%s", view)
	}
	if !strings.Contains(strings.ToLower(view), "no body") {
		t.Fatalf("expected a 'no body' indicator:\n%s", view)
	}
}

func TestReword_multiRewordWalksSelectedSet(t *testing.T) {
	m := newTestModel(t) // 3 commits, none frozen, on Dates
	h0 := m.commitAtDisplayIndex(0).Hash
	h1 := m.commitAtDisplayIndex(1).Hash

	// Pre-seed reword results so beginReword/advanceReword never call the LLM
	// (the generate command is skipped when a hash is already buffered).
	res := reword.Result{Variants: []reword.Variant{{Title: "New title", Body: "New body"}}}
	m.rewordSt.buffer = map[string]reword.Result{h0: res, h1: res}
	m.rewordSt.inFlight = map[string]bool{}

	// Select the two newest commits as a Set.
	m.sel.mode = SelSet
	m.sel.set = map[string]bool{h0: true, h1: true}

	// Engage reword over the set.
	model, _, _ := m.beginReword()
	m = model.(Model)
	if m.mode != modeReword {
		t.Fatalf("beginReword should open the picker, got mode %v", m.mode)
	}
	if len(m.rewordSt.targets) != 2 {
		t.Fatalf("expected a 2-commit reword queue, got %d", len(m.rewordSt.targets))
	}

	// Apply the first -> advance to the second (still in the picker).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeReword {
		t.Fatalf("after the first apply the picker should advance, got mode %v", m.mode)
	}
	// Apply the second -> queue exhausted, back to main.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeMain {
		t.Fatalf("after the last apply reword should return to main, got mode %v", m.mode)
	}
	if _, ok := m.plan.EditFor(h0); !ok {
		t.Error("the first selected commit should have a reword edit")
	}
	if _, ok := m.plan.EditFor(h1); !ok {
		t.Error("the second selected commit should have a reword edit")
	}
}

func TestRewordTargets_fallsBackToCursorWithoutSet(t *testing.T) {
	m := newTestModel(t)
	got := m.rewordTargets()
	if len(got) != 1 || got[0] != m.commitAtCursor().Hash {
		t.Errorf("with no Set, rewordTargets should be just the cursor commit, got %v", got)
	}
}

// TestReword_escMidWalkReportsPartialAndResets verifies that pressing esc
// partway through a multi-reword walk (a) reports the partial progress
// accurately, (b) leaves already-applied edits intact, (c) does NOT apply an
// edit for the commit that was pending at cancel time, and (d) resets the Set
// selection back to the workspace default (Active for the Dates workspace).
func TestReword_escMidWalkReportsPartialAndResets(t *testing.T) {
	m := newTestModel(t) // 3 commits, none frozen, Dates → SelActive by default
	h0 := m.commitAtDisplayIndex(0).Hash
	h1 := m.commitAtDisplayIndex(1).Hash

	// Pre-seed reword results so beginReword/advanceReword never call the LLM.
	res := reword.Result{Variants: []reword.Variant{{Title: "T", Body: "B"}}}
	m.rewordSt.buffer = map[string]reword.Result{h0: res, h1: res}
	m.rewordSt.inFlight = map[string]bool{}

	// Select the two newest commits as a Set.
	m.sel.mode = SelSet
	m.sel.set = map[string]bool{h0: true, h1: true}

	// Begin reword — should open the picker over the set.
	model, _, _ := m.beginReword()
	m = model.(Model)
	if m.mode != modeReword {
		t.Fatalf("beginReword should open the picker, got mode %v", m.mode)
	}
	if len(m.rewordSt.targets) != 2 {
		t.Fatalf("expected a 2-commit reword queue, got %d", len(m.rewordSt.targets))
	}

	// Apply the first commit — picker should advance to the second target.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeReword {
		t.Fatalf("after first apply the picker should still be open, got mode %v", m.mode)
	}

	// Press esc — cancel mid-walk.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)

	// Should return to main mode.
	if m.mode != modeMain {
		t.Fatalf("esc should return to main mode, got mode %v", m.mode)
	}

	// The first commit (h0) must have a reword edit applied.
	if _, ok := m.plan.EditFor(h0); !ok {
		t.Error("h0 was applied before esc: it should have a reword edit")
	}

	// The second commit (h1) must NOT have an edit — it was pending at cancel time.
	if _, ok := m.plan.EditFor(h1); ok {
		t.Error("h1 was not applied (cancelled): it should NOT have a reword edit")
	}

	// Selection must be reset — no stale Set should linger.
	if m.sel.mode != SelActive {
		t.Errorf("selection mode should be reset to SelActive (Dates default), got %v", m.sel.mode)
	}
	if len(m.sel.set) != 0 {
		t.Errorf("selection set should be empty after cancel, got %v", m.sel.set)
	}
}

// TestReword_multiRewordLogsEveryHash: walking a Set must leave pendingCmd listing
// every reworded commit, not just the last one.
func TestReword_multiRewordLogsEveryHash(t *testing.T) {
	m := newTestModel(t) // 3 commits, none frozen
	h0 := m.commitAtDisplayIndex(0).Hash
	h1 := m.commitAtDisplayIndex(1).Hash

	res := reword.Result{Variants: []reword.Variant{{Title: "New title", Body: "New body"}}}
	m.rewordSt.buffer = map[string]reword.Result{h0: res, h1: res}
	m.rewordSt.inFlight = map[string]bool{}
	m.sel.mode = SelSet
	m.sel.set = map[string]bool{h0: true, h1: true}

	model, _, _ := m.beginReword()
	m = model.(Model)

	// Apply both commits in the walk.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeMain {
		t.Fatalf("walk should complete, mode = %v", m.mode)
	}

	for _, h := range []string{h0, h1} {
		if !strings.Contains(m.pendingCmd, capability.Short(h)) {
			t.Errorf("pendingCmd %q is missing reworded commit %s", m.pendingCmd, capability.Short(h))
		}
	}
	if !strings.Contains(m.pendingCmd, "reword") || !strings.Contains(m.pendingCmd, "--execute") {
		t.Errorf("pendingCmd is not a reword --execute line: %q", m.pendingCmd)
	}
}
