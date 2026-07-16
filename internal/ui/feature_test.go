package ui

import (
	"testing"

	"github.com/wlame/regit/internal/capability"
)

// TestFeatureBindings_keysAreRegistryCommands guards against a stale binding for a
// command name that no longer exists in the registry.
func TestFeatureBindings_keysAreRegistryCommands(t *testing.T) {
	for name := range featureBindings {
		if _, ok := capability.Lookup(name); !ok {
			t.Errorf("featureBindings has %q, which is not a registry command", name)
		}
	}
}

// TestPanelRows_everyWorkspaceCarryingCommandHasABinding guards the other
// direction: every registry command that still carries a (legacy) Workspace
// tag must have a TUI binding and show up in the action panel — the panel is
// now the sole menu surface, so a command tagged for a workspace but missing
// its binding would be invisible to the user entirely.
func TestPanelRows_everyWorkspaceCarryingCommandHasABinding(t *testing.T) {
	m := newTestModel(t)
	panelVerbs := map[string]bool{}
	for _, r := range m.panelRows() {
		panelVerbs[r.Verb] = true
	}
	for _, c := range capability.Registry() {
		if c.Workspace == capability.WSNone {
			continue
		}
		if _, ok := featureBindings[c.Name]; !ok {
			t.Errorf("registry command %q carries a workspace but has no featureBinding", c.Name)
		}
		if !panelVerbs[c.Name] {
			t.Errorf("registry command %q carries a workspace but is missing from panelRows", c.Name)
		}
	}
}
