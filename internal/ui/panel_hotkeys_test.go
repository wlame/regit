package ui

import (
	"strings"
	"testing"
)

// TestFeatureBindings_hotkeysUniqueAndOffReservedKeys pins the hotkey table:
// every binding carries exactly one lowercase letter, no two bindings share
// one, and none collides with the panel's own navigation keys. Adding a verb
// without a key (or with a clash) must fail here, not misbehave at runtime.
func TestFeatureBindings_hotkeysUniqueAndOffReservedKeys(t *testing.T) {
	reserved := map[string]bool{
		"esc": true, "m": true, "up": true, "down": true, "j": true, "k": true,
		"enter": true, "left": true, "right": true, "[": true, "]": true,
	}
	seen := map[string]string{}
	for verb, b := range featureBindings {
		if len(b.Key) != 1 || b.Key[0] < 'a' || b.Key[0] > 'z' {
			t.Errorf("binding %q key %q must be exactly one lowercase letter", verb, b.Key)
		}
		if reserved[b.Key] {
			t.Errorf("binding %q key %q collides with a reserved panel navigation key", verb, b.Key)
		}
		if prev, dup := seen[b.Key]; dup {
			t.Errorf("bindings %q and %q share hotkey %q", prev, verb, b.Key)
		}
		seen[b.Key] = verb
	}
}

// TestUpdateActionPanel_hotkeyRunsEnabledRow: pressing an enabled row's letter
// is exactly enter on that row — it runs the binding and closes the panel.
// redate (Selection: SelActive) is enabled on any selection kind, so the
// plain merge-free fixture from newTestModel needs no extra setup.
func TestUpdateActionPanel_hotkeyRunsEnabledRow(t *testing.T) {
	m := newTestModel(t)
	m.panel.open = true

	mm := send(m, "d")

	if mm.panel.open {
		t.Fatalf("panel still open after running the hotkey: %+v", mm.panel)
	}
	if mm.flow != flowRedate {
		t.Fatalf("hotkey 'd' did not enter the redate flow: flow=%v", mm.flow)
	}
}

// TestUpdateActionPanel_hotkeyOnDisabledRowWarnsAndStaysOpen: pressing the
// letter of a disabled row must not run it — it reports the disabled reason
// in the status bar (statusWarn), moves the cursor to that row so the user
// sees which action refused, and leaves the panel open.
func TestUpdateActionPanel_hotkeyOnDisabledRowWarnsAndStaysOpen(t *testing.T) {
	m := newTestModel(t) // linear history, no merge commits: linearize is disabled
	m.panel.open = true

	rows := m.panelRows()
	wantIdx := -1
	for i, r := range rows {
		if r.Verb == "linearize" {
			wantIdx = i
			if r.Enabled {
				t.Fatalf("fixture assumption broken: linearize enabled with %+v", r)
			}
		}
	}
	if wantIdx == -1 {
		t.Fatal("linearize missing from panelRows")
	}

	mm := send(m, "l")

	if !mm.panel.open {
		t.Fatal("panel closed after hotkey on a disabled row")
	}
	if mm.panel.cursor != wantIdx {
		t.Fatalf("cursor = %d, want %d (the disabled linearize row)", mm.panel.cursor, wantIdx)
	}
	if mm.statusMsg != "no merge commits" {
		t.Fatalf("status message = %q, want the disabled reason", mm.statusMsg)
	}
	if mm.statusKind != statusWarn {
		t.Fatalf("status kind = %v, want statusWarn", mm.statusKind)
	}
}

// TestUpdateActionPanel_unmappedLetterSwallowedGlobalsPaused: a letter with no
// binding still must not leak past the panel. This is the fall-through case
// that keeps global actions paused while the panel is open (app.go's
// updateMain routes every key to updateActionPanel first) — pinning it stops
// a stray default case in the letter loop from accidentally closing the panel
// or letting the key fall back to globalAction.
func TestUpdateActionPanel_unmappedLetterSwallowedGlobalsPaused(t *testing.T) {
	m := newTestModel(t)
	m.panel.open = true
	wantMode, wantFlow, wantStatus := m.mode, m.flow, m.statusMsg

	mm := send(m, "z")

	if !mm.panel.open {
		t.Fatal("panel closed on an unmapped letter")
	}
	if mm.mode != wantMode || mm.flow != wantFlow {
		t.Fatalf("unmapped letter leaked past the panel: mode=%v flow=%v", mm.mode, mm.flow)
	}
	if mm.statusMsg != wantStatus {
		t.Fatalf("unmapped letter produced status %q — a global action fired", mm.statusMsg)
	}
}

// TestUpdateActionPanel_uppercaseGlobalDoesNotLeak: "z" (above) is a no-op
// everywhere, so it can't tell a swallowed key apart from one that leaked
// through and simply did nothing. "U" is the global undo key (wired in
// globalAction, not a lowercase hotkey), and here it has something real to
// undo — applyStrategyInPane + apply move HEAD, giving doUndo an actual
// restore to perform if the key ever reached it. Pressing "U" while the panel
// is open must leave the panel open, HEAD untouched, and the status message
// unchanged; a leak would move HEAD back and set the "Undid last apply —
// restored ..." status.
func TestUpdateActionPanel_uppercaseGlobalDoesNotLeak(t *testing.T) {
	m := newTestModel(t)
	origHead, _ := m.repo.ResolveRef("HEAD")

	m = applyStrategyInPane(m) // stage an edit (tab tab enter)
	m = send(m, "A", "enter")  // preview, then apply
	if m.statusKind == statusErr {
		t.Fatalf("setup: apply errored: %s", m.statusMsg)
	}
	appliedHead, _ := m.repo.ResolveRef("HEAD")
	if appliedHead == origHead {
		t.Fatal("setup: apply should have moved HEAD")
	}

	m.panel.open = true
	wantStatus := m.statusMsg

	mm := send(m, "U")

	if !mm.panel.open {
		t.Fatal("panel closed on the global undo key")
	}
	if mm.statusMsg != wantStatus {
		t.Fatalf("global undo key leaked through the panel: status = %q", mm.statusMsg)
	}
	head, _ := mm.repo.ResolveRef("HEAD")
	if head != appliedHead {
		t.Fatalf("global undo key leaked through the panel: HEAD moved from %s to %s", appliedHead, head)
	}
}

// TestViewActionPanel_rendersHotkeyPrefixes: the letters pinned by the
// invariant test above must actually be VISIBLE on the panel — this renders
// the open panel and checks the [key] prefix appears in the title cell, on
// both an enabled row (redate, "d") and a disabled one (linearize, "l", on
// the merge-free newTestModel fixture — dimming is style-level and invisible
// to a plain string assertion, but the prefix text itself must still be
// there). The footer hint must also spell out the new hotkey affordance.
func TestViewActionPanel_rendersHotkeyPrefixes(t *testing.T) {
	m := newTestModel(t) // linear history: linearize stays disabled but still shows its prefix
	m.panel.open = true

	out := m.viewActionPanel(120)

	for _, want := range []string{
		"[d] Date/time spread",
		"[l] Linearize history",
		// The widest prefixed title (22 chars): pins panelTitleWidth against
		// wrap regressions at the tightest fit, not just the typical case.
		"[p] Purge from history",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("panel view missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "[letter] run") {
		t.Fatalf("panel footer missing the hotkey hint:\n%s", out)
	}
}
