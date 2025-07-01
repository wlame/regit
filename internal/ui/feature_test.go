package ui

import "testing"

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
	want := []string{"Split commit", "Reword message", "Retitle (regex)", "Drop bodies"}
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
