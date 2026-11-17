package ui

import (
	"fmt"
	"strings"
	"testing"
)

func TestKeybarHint_containsCoreActions(t *testing.T) {
	bar := keybarHint()
	// [s]trategy has been removed — carousel replaces it; the other tokens remain.
	for _, tok := range []string{"[A]pply", "[?]help", "[q]uit", "[tab]focus"} {
		if !strings.Contains(bar, tok) {
			t.Errorf("keybar should contain %q, got: %s", tok, bar)
		}
	}
}

func TestHelpText_groupsScopesAndDocsNewKeys(t *testing.T) {
	help := helpText()
	for _, want := range []string{
		"GLOBAL", "FLOW", "COMMIT LIST", "STRATEGY", "focus the active flow's widget",
	} {
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
	if !strings.Contains(m.View(), "FLOW") {
		t.Error("help overlay should render the generated keymap including FLOW")
	}
}

func TestHelpText_documentsSelectionKeys(t *testing.T) {
	help := helpText()
	for _, want := range []string{"SELECTION", "mark a range", "mark every editable commit", "multi-reword"} {
		if !strings.Contains(help, want) {
			t.Errorf("help should document selection: missing %q", want)
		}
	}
}

func TestKeybarHint_includesSelect(t *testing.T) {
	if !strings.Contains(keybarHint(), "[space]mark") {
		t.Errorf("keybar should advertise marking, got: %s", keybarHint())
	}
}

func TestKeybar_reflectsRetiredAndRenamedActions(t *testing.T) {
	bar := keybarHint()
	for _, want := range []string{"[U]ndo", "[R]eset", "[A]pply"} {
		if !strings.Contains(bar, want) {
			t.Errorf("keybar should contain %q, got: %s", want, bar)
		}
	}
	retiredTokens := []string{
		"[S]plit", "[w]reword", "[D]rop-bodies", "[R]etitle",
		"[u]ndo", "[r]eset", "[s]trategy", "[<>]tabs",
	}
	for _, gone := range retiredTokens {
		if strings.Contains(bar, gone) {
			t.Errorf("keybar should no longer contain %q, got: %s", gone, bar)
		}
	}
}

func TestKeymap_authorToggleAndRelocatedSelectAll(t *testing.T) {
	if !strings.Contains(keybarHint(), "[a]uthor") {
		t.Errorf("keybar should advertise the author toggle, got: %s", keybarHint())
	}
	help := helpText()
	if !strings.Contains(help, "author column") {
		t.Error("help should document the author column toggle")
	}
	// select-all moved off 'a' to 's'; its keymap row must now read 's'.
	for _, b := range keymap {
		if b.Scope == scopeSelection && strings.Contains(b.Desc, "mark every editable commit") && b.Key != "s" {
			t.Errorf("select all should be bound to 's', got %q", b.Key)
		}
	}
}

func TestKeybarHint_strategyGone(t *testing.T) {
	bar := keybarHint()
	if strings.Contains(bar, "[s]trategy") {
		t.Errorf("keybar should no longer advertise [s]trategy after carousel, got: %s", bar)
	}
}

func TestHelpText_documentsPreviewThenApply(t *testing.T) {
	help := helpText()
	if !strings.Contains(help, "preview") {
		t.Error("help should mention the apply preview step")
	}
	// The A action's description should make the preview-then-apply explicit.
	for _, want := range []string{"[A]pply", "[p]review"} {
		if !strings.Contains(keybarHint(), want) {
			t.Errorf("keybar should still advertise %q", want)
		}
	}
}

// TestKeybarFiltersByContext: the keybar shows list keys when the list is
// focused and panel keys while the panel is open — same table, filtered.
func TestKeybarFiltersByContext(t *testing.T) {
	list := keybarHintFor(ctxList, 500)
	panel := keybarHintFor(ctxPanel, 500)
	if !strings.Contains(list, "[m]actions") || strings.Contains(list, "enter run") {
		t.Fatalf("list keybar wrong: %q", list)
	}
	if !strings.Contains(panel, "esc") || strings.Contains(panel, "[m]actions") {
		t.Fatalf("panel keybar wrong: %q", panel)
	}
}

// TestCurrentKeyCtx: the model maps its state to the keybar context.
func TestCurrentKeyCtx(t *testing.T) {
	m := newTestModel(t)
	if m.currentKeyCtx() != ctxList {
		t.Fatal("default ctx should be list")
	}
	m.panel.open = true
	if m.currentKeyCtx() != ctxPanel {
		t.Fatal("open panel should own the keybar")
	}
	m.panel.open = false
	m.enterRedateFlow()
	if m.currentKeyCtx() != ctxRedate {
		t.Fatal("redate flow should own the keybar")
	}
	m.panel.open = true
	if m.currentKeyCtx() != ctxPanel {
		t.Fatal("panel open over an active flow should still own the keybar (panel takes priority)")
	}
}

func TestDocSections_projectsEveryKeymapRowOnceInScopeOrder(t *testing.T) {
	secs := DocSections()
	if len(secs) != len(scopeOrder) {
		t.Fatalf("got %d sections, want %d (one per scope)", len(secs), len(scopeOrder))
	}
	total := 0
	for i, sec := range secs {
		if want := scopeTitles[scopeOrder[i]]; sec.Title != want {
			t.Errorf("section %d title = %q, want %q", i, sec.Title, want)
		}
		total += len(sec.Bindings)
	}
	if total != len(keymap) {
		t.Errorf("sections carry %d bindings, want %d (every keymap row exactly once)", total, len(keymap))
	}
	first := secs[0].Bindings[0]
	if first.Key != "?" || first.Bar != "[?]help" {
		t.Errorf("first global binding = %+v, want the '?' row with bar token intact", first)
	}
}

func TestHelpText_documentsRedateBracketHelpers(t *testing.T) {
	text := helpText()
	for _, keys := range []string{"[ / ]", "< / >"} {
		if !strings.Contains(text, keys) {
			t.Errorf("help should document %q", keys)
		}
	}
}

// TestKeybarHintFor_contextKeysComeFirst: the global keys alone fill a wide
// terminal, so a context's own keys must lead the bar or they are cut off.
func TestKeybarHintFor_contextKeysComeFirst(t *testing.T) {
	bar := keybarHintFor(ctxRedate, 120)
	if !strings.Contains(bar, "[ ]strategy") || !strings.Contains(bar, "< >time") {
		t.Errorf("redate keybar at 120 columns lost its own keys: %q", bar)
	}
}

func TestHelpText_documentsPaneFocusKeys(t *testing.T) {
	if !strings.Contains(helpText(), "o / c / t") {
		t.Error("help should document the o / c / t pane focus keys")
	}
}

// TestKeybarHintFor_primaryKeysFitAt80Columns: the keys that start and finish
// every task must survive truncation on a standard 80-column terminal.
func TestKeybarHintFor_primaryKeysFitAt80Columns(t *testing.T) {
	list := keybarHintFor(ctxList, 80)
	for _, want := range []string{"[m]actions", "[A]pply", "[space]mark"} {
		if !strings.Contains(list, want) {
			t.Errorf("list keybar at 80 columns lost %s: %q", want, list)
		}
	}
	redate := keybarHintFor(ctxRedate, 80)
	for _, want := range []string{"[esc]close", "[m]actions", "[A]pply", "[ ]strategy"} {
		if !strings.Contains(redate, want) {
			t.Errorf("redate keybar at 80 columns lost %s: %q", want, redate)
		}
	}
}

// TestKeybarHintFor_learnedKeysComeLast: help and quit are the first keys a
// user learns, so they sit at the end of the bar where truncation cuts first.
func TestKeybarHintFor_learnedKeysComeLast(t *testing.T) {
	bar := keybarHintFor(ctxList, 500)
	actions := strings.Index(bar, "[m]actions")
	for _, late := range []string{"[?]help", "[q]uit"} {
		if i := strings.Index(bar, late); i < actions {
			t.Errorf("%s (at %d) should come after [m]actions (at %d): %q", late, i, actions, bar)
		}
	}
}

// TestKeybarHintFor_escOnlyAdvertisedInFlow: esc closes an open flow, but on
// the plain commit list it only clears marks, so the token belongs to the
// redate context alone.
func TestKeybarHintFor_escOnlyAdvertisedInFlow(t *testing.T) {
	if bar := keybarHintFor(ctxList, 500); strings.Contains(bar, "[esc]close") {
		t.Errorf("list keybar should not advertise [esc]close: %q", bar)
	}
}

func TestHelpText_documentsTheEditorKeys(t *testing.T) {
	help := helpText()
	for _, want := range []string{"COMMIT EDITOR", "shift+enter / ctrl+j", "save the edit, from any field"} {
		if !strings.Contains(help, want) {
			t.Errorf("help should document the editor: missing %q", want)
		}
	}
}
