package ui

import (
	"testing"

	"github.com/wlame/regit/internal/capability"
)

func TestWorkspaces_orderedFiveTabs(t *testing.T) {
	if len(workspaces) != 5 {
		t.Fatalf("want 5 fixed tabs, got %d", len(workspaces))
	}
	wantOrder := []WorkspaceID{WSDates, WSRestructure, WSHistory, WSAuthorship, WSSearch}
	for i, w := range workspaces {
		if w.ID != wantOrder[i] {
			t.Errorf("tab %d = %v, want %v", i, w.ID, wantOrder[i])
		}
	}
}

func TestWorkspaces_v1EnablementMatrix(t *testing.T) {
	enabled := map[WorkspaceID]bool{}
	for _, w := range workspaces {
		enabled[w.ID] = w.Enabled
	}
	for _, id := range []WorkspaceID{WSDates, WSRestructure} {
		if !enabled[id] {
			t.Errorf("%v should be enabled in v1", id)
		}
	}
	for _, id := range []WorkspaceID{WSHistory, WSAuthorship, WSSearch} {
		if enabled[id] {
			t.Errorf("%v should be a disabled stub in v1", id)
		}
	}
}

func TestFeaturesFor_restructureMenu(t *testing.T) {
	got := featuresFor(WSRestructure)
	want := []string{"Split commit", "Reword message", "Squash commits", "Retitle (regex)", "Drop bodies"}
	if len(got) != len(want) {
		t.Fatalf("Restructure should host %d features, got %d", len(want), len(got))
	}
	for i, f := range got {
		if f.Name != want[i] {
			t.Errorf("feature %d = %q, want %q", i, f.Name, want[i])
		}
		if f.Run == nil {
			t.Errorf("feature %q must have a Run action", f.Name)
		}
	}
}

func TestFeaturesFor_datesAndStubs(t *testing.T) {
	if got := featuresFor(WSDates); len(got) != 1 || got[0].Name != "Date/time spread" {
		t.Errorf("Dates should host exactly the spread feature, got %v", got)
	}
	for _, id := range []WorkspaceID{WSHistory, WSAuthorship, WSSearch} {
		if got := featuresFor(id); len(got) != 0 {
			t.Errorf("stub workspace %v should have no features yet, got %d", id, len(got))
		}
	}
}

func TestWorkspaceByID(t *testing.T) {
	if w, ok := workspaceByID(WSRestructure); !ok || w.Title != "Restructure" {
		t.Errorf("workspaceByID(WSRestructure) = %v ok=%v, want Restructure", w, ok)
	}
	if _, ok := workspaceByID(WorkspaceID(99)); ok {
		t.Error("workspaceByID for an unknown id should report not-found")
	}
}

// TestFeaturesFor_isProjectionOfRegistry proves every menu row corresponds to a
// registry command in the same workspace, carries that command's Selection, and
// appears in registry order — i.e. the menu is a projection of capability.Registry(),
// not a parallel table that could drift from it.
func TestFeaturesFor_isProjectionOfRegistry(t *testing.T) {
	for _, id := range []WorkspaceID{WSDates, WSRestructure} {
		got := featuresFor(id)
		var wantTitles []string
		var wantSel []SelectionMode
		for _, c := range capability.Registry() {
			if c.Workspace != id {
				continue
			}
			b, ok := featureBindings[c.Name]
			if !ok {
				continue
			}
			wantTitles = append(wantTitles, b.Title)
			wantSel = append(wantSel, c.Selection)
		}
		if len(got) != len(wantTitles) {
			t.Fatalf("workspace %v: %d rows, want %d", id, len(got), len(wantTitles))
		}
		for i := range got {
			if got[i].Name != wantTitles[i] {
				t.Errorf("workspace %v row %d name = %q, want %q", id, i, got[i].Name, wantTitles[i])
			}
			if got[i].Selection != wantSel[i] {
				t.Errorf("workspace %v row %d selection = %v, want %v", id, i, got[i].Selection, wantSel[i])
			}
			if got[i].Workspace != id {
				t.Errorf("workspace %v row %d workspace = %v, want %v", id, i, got[i].Workspace, id)
			}
		}
	}
}

// TestFeatureBindings_keysAreRegistryCommands guards against a stale binding for a
// command name that no longer exists in the registry.
func TestFeatureBindings_keysAreRegistryCommands(t *testing.T) {
	for name := range featureBindings {
		if _, ok := capability.Lookup(name); !ok {
			t.Errorf("featureBindings has %q, which is not a registry command", name)
		}
	}
}

// TestMenuWorkspaceCommandsHaveBindings guards the other direction: every registry
// command assigned to a menu workspace (Dates/Restructure) must have a TUI binding,
// so adding a verb to such a workspace without wiring its closure fails the test.
func TestMenuWorkspaceCommandsHaveBindings(t *testing.T) {
	for _, c := range capability.Registry() {
		if c.Workspace == WSRestructure || c.Workspace == WSDates {
			if _, ok := featureBindings[c.Name]; !ok {
				t.Errorf("registry command %q is in a menu workspace but has no featureBinding", c.Name)
			}
		}
	}
}

// TestFeaturesFor_restructureSelectionsMatchRegistry pins the concrete selection
// each Restructure action runs with, so a registry change is caught here too.
func TestFeaturesFor_restructureSelectionsMatchRegistry(t *testing.T) {
	want := map[string]SelectionMode{
		"Split commit":    SelSingle,
		"Reword message":  SelSingle,
		"Squash commits":  SelSet,
		"Retitle (regex)": SelActive,
		"Drop bodies":     SelActive,
	}
	for _, f := range featuresFor(WSRestructure) {
		if want[f.Name] != f.Selection {
			t.Errorf("%q selection = %v, want %v", f.Name, f.Selection, want[f.Name])
		}
	}
}
