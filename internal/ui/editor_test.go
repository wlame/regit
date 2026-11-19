package ui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TestEditor_invalidDateShowsInlineError: an unparseable date on save renders an
// inline error in the editor, not a hidden status-bar message.
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
	view := m.editor.render(m.commitByHash(m.editor.hash), m.st)
	if !strings.Contains(view, m.editor.errMsg) {
		t.Errorf("editor view should render the error line; got:\n%s", view)
	}
}

// editorAtBody opens the editor on the cursor commit with the body focused.
func editorAtBody(t *testing.T, m Model) Model {
	t.Helper()
	m = send(m, "e", "tab", "tab")
	if m.mode != modeEditor || m.editor.focusIdx != 2 {
		t.Fatalf("expected the editor with the body focused, got mode %v focus %d", m.mode, m.editor.focusIdx)
	}
	return m
}

func update(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestEditor_enterSavesFromTheBody(t *testing.T) {
	m := editorAtBody(t, newTestModel(t))
	hash := m.editor.hash
	m.editor.body.SetValue("A new body")

	m = send(m, "enter")

	if m.mode != modeMain {
		t.Fatalf("enter in the body should save and close the editor, got mode %v", m.mode)
	}
	edit, ok := m.plan.EditFor(hash)
	if !ok || edit.NewBody == nil || *edit.NewBody != "A new body" {
		t.Errorf("the body edit was not staged: %+v", edit)
	}
}

// fakeUnknownCSI stands in for Bubble Tea's unexported message for an escape
// sequence it does not know: a named byte slice holding the raw sequence.
type fakeUnknownCSI []byte

func TestEditor_newLineKeysAddALineInTheBody(t *testing.T) {
	keys := map[string]tea.Msg{
		"ctrl+j":                        tea.KeyMsg{Type: tea.KeyCtrlJ},
		"alt+enter":                     tea.KeyMsg{Type: tea.KeyEnter, Alt: true},
		"shift+enter (modifyOtherKeys)": fakeUnknownCSI("\x1b[27;2;13~"),
		"shift+enter (CSI u)":           fakeUnknownCSI("\x1b[13;2u"),
	}
	for name, msg := range keys {
		t.Run(name, func(t *testing.T) {
			m := editorAtBody(t, newTestModel(t))
			m.editor.body.SetValue("first")

			m = update(m, msg)
			m = send(m, "x")

			if m.mode != modeEditor {
				t.Fatalf("a new-line key must not close the editor, got mode %v", m.mode)
			}
			if got := m.editor.body.Value(); got != "first\nx" {
				t.Errorf("body = %q, want %q", got, "first\nx")
			}
		})
	}
}

func TestEditor_newLineKeyOutsideTheBodyDoesNothing(t *testing.T) {
	m := send(newTestModel(t), "e") // title focused
	title := m.editor.title.Value()

	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlJ})

	if m.mode != modeEditor || m.editor.title.Value() != title {
		t.Errorf("ctrl+j in the title changed something: mode %v, title %q", m.mode, m.editor.title.Value())
	}
}

func TestEditor_hintNamesTheNewLineKeyOnlyInTheBody(t *testing.T) {
	m := send(resizeTo(newTestModel(t), 160, 50), "e")
	if strings.Contains(stripANSI(m.View()), "new line") {
		t.Error("the title field has no new-line key; the hint should not offer one")
	}
	m = send(m, "tab", "tab")
	if view := stripANSI(m.View()); !strings.Contains(view, "shift+enter") || !strings.Contains(view, "ctrl+j") {
		t.Errorf("the body hint should name the new-line keys:\n%s", view)
	}
}

func TestEditor_drawsInTheFlowZoneAndFits(t *testing.T) {
	for _, size := range [][2]int{{160, 50}, {120, 40}, {80, 24}, {60, 16}} {
		m := send(resizeTo(newTestModelN(t, 6), size[0], size[1]), "e")

		view := stripANSI(m.View())
		if !hasBorderTitle(view, "EDIT") {
			t.Errorf("%dx%d: the editor should be a titled pane in the flow zone:\n%s", size[0], size[1], view)
		}
		assertFrameFits(t, m, size[0], size[1])
	}
}

func TestEditor_bodyFieldGrowsWithTheTerminal(t *testing.T) {
	small := send(resizeTo(newTestModelN(t, 6), 120, 24), "e")
	large := send(resizeTo(newTestModelN(t, 6), 200, 60), "e")

	if s, l := small.editor.body.Height(), large.editor.body.Height(); l <= s || l < 30 {
		t.Errorf("body rows: %d at 24 rows, %d at 60 rows; the field should use the tall terminal", s, l)
	}
	if w := large.editor.title.Width; w <= 75 {
		t.Errorf("title field is %d columns on a 200-column terminal; it should pass the old 75 cap", w)
	}
}

func TestEditor_resizeWhileOpenRefitsTheFields(t *testing.T) {
	m := send(resizeTo(newTestModelN(t, 6), 200, 60), "e")

	m = resizeTo(m, 100, 24)

	assertFrameFits(t, m, 100, 24)
}

// TestShiftEnter_realTerminalBytesBecomeANewLineKey feeds the bytes a terminal
// sends for Shift+Enter through Bubble Tea's own input reader. It pins the
// assumption normalizeNewLineKey rests on: Bubble Tea delivers an escape
// sequence it does not know as a message whose underlying type is a byte slice.
func TestShiftEnter_realTerminalBytesBecomeANewLineKey(t *testing.T) {
	for _, sequence := range shiftEnterSequences {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		probe := &keyProbe{}
		program := tea.NewProgram(probe, tea.WithInput(strings.NewReader(sequence)),
			tea.WithOutput(io.Discard), tea.WithContext(ctx))

		_, err := program.Run()
		cancel()

		if err != nil || !probe.sawNewLineKey {
			t.Errorf("sequence %q was not recognized as a new-line key (err=%v)", sequence, err)
		}
	}
}

// keyProbe is a minimal Bubble Tea model that quits once it sees a new-line key.
type keyProbe struct{ sawNewLineKey bool }

func (p *keyProbe) Init() tea.Cmd { return nil }
func (p *keyProbe) View() string  { return "" }
func (p *keyProbe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := normalizeNewLineKey(msg).(tea.KeyMsg); ok && newLineKeys[key.String()] {
		p.sawNewLineKey = true
		return p, tea.Quit
	}
	return p, nil
}

// TestEditor_longTitleScrollsInsideItsField: a title longer than the field must
// scroll inside it. If the field showed the whole value, the pane would wrap
// the row and push the form past its rows.
func TestEditor_longTitleScrollsInsideItsField(t *testing.T) {
	m := send(resizeTo(newTestModelN(t, 3), 150, 30), "e") // the fixture subject is 63 characters

	shown := lipgloss.Width(m.editor.title.View())

	if limit := m.editor.title.Width + 1; shown > limit { // +1: the cursor cell
		t.Errorf("the title field shows %d columns, its width allows %d", shown, limit)
	}
}

// commitBodyWidth is the column most commit bodies are wrapped at.
const commitBodyWidth = 72

// TestEditor_fitsA72ColumnBodyAndTitle: a body wrapped at 72 columns and a
// 72-character title must show without extra wrapping wherever the editor is
// drawn — beside the list on a wide screen, or full width on a narrower one.
func TestEditor_fitsA72ColumnBodyAndTitle(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {120, 40}, {100, 30}} {
		m := send(resizeTo(newTestModelN(t, 3), size[0], size[1]), "e")

		if w := m.editor.title.Width; w < commitBodyWidth {
			t.Errorf("%dx%d: title field is %d columns, want at least %d", size[0], size[1], w, commitBodyWidth)
		}
		if w := m.editor.body.Width(); w < commitBodyWidth {
			t.Errorf("%dx%d: body text area is %d columns, want at least %d", size[0], size[1], w, commitBodyWidth)
		}
		assertFrameFits(t, m, size[0], size[1])
	}
}

func TestEditor_bodyHasNoLineNumbers(t *testing.T) {
	m := send(resizeTo(newTestModelN(t, 3), 160, 40), "e")

	if m.editor.body.ShowLineNumbers {
		t.Error("line numbers take body columns a commit message does not need")
	}
}
