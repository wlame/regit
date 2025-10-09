package ui

import (
	"strings"
	"testing"
)

// TestEditor_invalidDateShowsInlineError: an unparseable date on save renders an
// inline error in the editor modal, not a hidden status-bar message.
func TestEditor_invalidDateShowsInlineError(t *testing.T) {
	m := newTestModel(t)
	m = send(m, "e") // enter the editor on the selected commit (existing idiom, app_test.go)
	if m.mode != modeEditor {
		t.Fatalf("send 'e' should open the editor, got %v", m.mode)
	}
	m.editor.date.SetValue("not-a-date")
	m.editor.focusIdx = 1
	updated, _ := m.updateEditor(key("enter"))
	m = updated.(Model)
	if m.mode != modeEditor {
		t.Fatalf("invalid date should keep the editor open, got mode %v", m.mode)
	}
	if m.editor.errMsg == "" {
		t.Fatal("editor.errMsg should be set on an invalid date")
	}
	view := m.editor.view(m.commitByHash(m.editor.hash), m.st)
	if !strings.Contains(view, m.editor.errMsg) {
		t.Errorf("editor view should render the error line; got:\n%s", view)
	}
}
