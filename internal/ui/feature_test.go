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

// TestPanelRows_everyWorkspaceCarryingCommandHasABinding guards both
// directions of the (Workspace tag) <=> (TUI binding) correspondence — the
// panel is the sole menu surface, so the two can never legitimately diverge:
//
//   - Workspace present => binding present (the original guard): a command
//     tagged for a workspace but missing its binding would be invisible to
//     the user entirely.
//   - Binding present => Workspace present (the strengthened, reverse
//     direction): a command with a panel presence but a stale WSNone would
//     mean the panel shows an action whose registry row still claims to be
//     headless-only — exactly the gap a future task could leave behind by
//     adding a featureBinding without also flipping its registry row (redact
//     itself carried WSNone for exactly one task's duration; see
//     registry.go's redact row history).
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
	for name := range featureBindings {
		c, ok := capability.Lookup(name)
		if !ok {
			continue // already reported by TestFeatureBindings_keysAreRegistryCommands
		}
		if c.Workspace == capability.WSNone {
			t.Errorf("featureBindings has %q, which has a panel presence but registry Workspace=WSNone", name)
		}
	}
}
