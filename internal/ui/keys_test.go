package ui

import (
	"fmt"
	"strings"
	"testing"
)

func TestKeybarHint_containsCoreActions(t *testing.T) {
	bar := keybarHint()
	for _, tok := range []string{"[s]trategy", "[A]pply", "[?]help", "[q]uit", "tabs"} {
		if !strings.Contains(bar, tok) {
			t.Errorf("keybar should contain %q, got: %s", tok, bar)
		}
	}
}

func TestHelpText_groupsScopesAndDocsNewKeys(t *testing.T) {
	help := helpText()
	for _, want := range []string{"GLOBAL", "WORKSPACES", "COMMIT LIST", "STRATEGY", "switch workspace"} {
		if !strings.Contains(help, want) {
			t.Errorf("help should contain %q", want)
		}
	}
}

func TestKeymap_noDuplicateKeyPerScope(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range keymap {
		k := fmt.Sprintf("%d/%s", b.Scope, b.Key)
		if seen[k] {
			t.Errorf("duplicate binding for key %q in scope %d", b.Key, b.Scope)
		}
		seen[k] = true
	}
}

func TestKeymap_everyRowDocumented(t *testing.T) {
	for _, b := range keymap {
		if strings.TrimSpace(b.Key) == "" || strings.TrimSpace(b.Desc) == "" {
			t.Errorf("keymap row %+v must have a Key and a Desc", b)
		}
	}
}

func TestView_helpOverlayUsesGeneratedText(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "?")
	if m.mode != modeHelp {
		t.Fatalf("? should open help, got %v", m.mode)
	}
	if !strings.Contains(m.View(), "WORKSPACES") {
		t.Error("help overlay should render the generated keymap including WORKSPACES")
	}
}
