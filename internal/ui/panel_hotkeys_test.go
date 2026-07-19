package ui

import "testing"

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
