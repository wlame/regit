package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/model"
)

// editorState backs the per-commit editor: title, body, and the author date.
// Committer date follows the author date (linked) when the app config keeps
// them linked, which is the default. The editor draws in the flow zone
// (zone.go), and Model.fitEditorToZone sizes its fields to that pane.
type editorState struct {
	hash        string
	title       textinput.Model
	date        textinput.Model
	body        textarea.Model
	focusIdx    int // 0 title, 1 date, 2 body
	bodyDropped bool
	errMsg      string // last save failure (e.g. unparseable date), rendered inline
}

const editorDateLayout = "2006-01-02 15:04"

const (
	// editorLabelWidth is the label column before the title and date fields.
	editorLabelWidth = 8
	// editorMinBodyRows is the smallest the body field gets on a short terminal.
	editorMinBodyRows = 3
	// editorErrorRows is kept free under the body for a save error, which can
	// wrap onto a second row, so showing one never pushes the pane past its rows.
	editorErrorRows = 2
)

// editorFieldIndex values are the positions of the editor's fields in focus
// order.
const (
	editorFieldTitle = iota
	editorFieldDate
	editorFieldBody
	editorFieldCount
)

// newEditorState seeds the editor from a commit and any pending edit on it.
// The fields get their real size from setSize once the pane size is known.
func newEditorState(c model.Commit, plan *model.Plan, loc *time.Location) editorState {
	e, _ := plan.EditFor(c.Hash)

	title := textinput.New()
	title.Prompt = ""
	title.CharLimit = 120
	if e.NewSubject != nil {
		title.SetValue(*e.NewSubject)
	} else {
		title.SetValue(c.Subject)
	}
	title.Focus()

	date := textinput.New()
	date.Prompt = ""
	date.CharLimit = 16
	date.Width = 20
	date.SetValue(e.EffectiveAuthorDate(c).In(loc).Format(editorDateLayout))

	body := textarea.New()
	// Enter saves the editor, so the body never sees it; a new line comes from
	// newLineKeys in update instead.
	body.KeyMap.InsertNewline.SetEnabled(false)
	if e.DropBody {
		body.SetValue("")
	} else if e.NewBody != nil {
		body.SetValue(*e.NewBody)
	} else {
		body.SetValue(c.Body)
	}

	return editorState{hash: c.Hash, title: title, date: date, body: body, bodyDropped: e.DropBody}
}

// setSize fits the fields to a pane with the given inner width, giving the
// body field bodyRows rows.
func (s *editorState) setSize(innerWidth, bodyRows int) {
	// The title shares its row with the "▸ " cursor, the label column and its
	// gap, and the field's own cursor cell.
	s.title.Width = max(10, innerWidth-2-editorLabelWidth-1-1)
	// The field computes which part of its value is visible only when the
	// cursor moves, so re-place the cursor to apply the new width.
	s.title.SetCursor(s.title.Position())
	s.body.SetWidth(innerWidth)
	s.body.SetHeight(max(editorMinBodyRows, bodyRows))
}

// update routes key input to the focused field and handles field switching and
// the "drop body" shortcut. It returns a command and whether the editor should
// be saved/closed (handled by the caller on Enter).
func (s *editorState) update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if ok {
		switch key.String() {
		case "tab":
			s.cycle(1)
			return nil
		case "shift+tab":
			s.cycle(-1)
			return nil
		case "ctrl+d":
			// Drop the body, keeping only the title.
			s.body.SetValue("")
			s.bodyDropped = true
			return nil
		}
		if newLineKeys[key.String()] {
			// Only the body is multi-line; elsewhere the key does nothing.
			if s.focusIdx == editorFieldBody {
				s.bodyDropped = false
				s.body.InsertString("\n")
			}
			return nil
		}
	}

	var cmd tea.Cmd
	switch s.focusIdx {
	case editorFieldTitle:
		s.title, cmd = s.title.Update(msg)
	case editorFieldDate:
		s.date, cmd = s.date.Update(msg)
	case editorFieldBody:
		s.bodyDropped = false // typing in the body undoes a drop
		s.body, cmd = s.body.Update(msg)
	}
	return cmd
}

func (s *editorState) cycle(delta int) {
	s.title.Blur()
	s.date.Blur()
	s.body.Blur()
	s.focusIdx = (s.focusIdx + delta + editorFieldCount) % editorFieldCount
	switch s.focusIdx {
	case editorFieldTitle:
		s.title.Focus()
	case editorFieldDate:
		s.date.Focus()
	case editorFieldBody:
		s.body.Focus()
	}
}

// buildEdit converts the editor fields into a model.Edit relative to the original
// commit, only recording fields that actually changed. linkDates mirrors the
// author date onto the committer date.
func (s editorState) buildEdit(c model.Commit, loc *time.Location, linkDates bool) (model.Edit, error) {
	var e model.Edit

	if v := s.title.Value(); v != c.Subject {
		nv := v
		e.NewSubject = &nv
	}

	parsed, err := time.ParseInLocation(editorDateLayout, strings.TrimSpace(s.date.Value()), loc)
	if err != nil {
		return e, fmt.Errorf("date must be 'YYYY-MM-DD HH:MM'")
	}
	if !parsed.Equal(c.OrigAuthorDate) {
		na := parsed
		e.NewAuthorDate = &na
		if linkDates {
			nc := parsed
			e.NewCommitDate = &nc
		}
	}

	switch {
	case s.bodyDropped || (s.body.Value() == "" && c.Body != ""):
		e.DropBody = true
	case s.body.Value() != c.Body:
		nb := s.body.Value()
		e.NewBody = &nb
	}

	return e, nil
}

// editorKeyHints is the key line per focused field. Only the body is
// multi-line, so only its line offers the new-line keys.
var editorKeyHints = [editorFieldCount]string{
	editorFieldTitle: "enter save · tab next · ctrl+d drop body · esc cancel",
	editorFieldDate:  "enter save · tab next · ctrl+d drop body · esc cancel",
	editorFieldBody:  "enter save · shift+enter or ctrl+j new line · tab next · ctrl+d drop body · esc cancel",
}

// render draws the editor form for the flow zone.
func (s editorState) render(c model.Commit, st styles) string {
	var b strings.Builder
	b.WriteString(st.modalTitle.Render("Edit commit "+short(c.Hash)) + "\n\n")

	cursorFor := func(field int) string {
		if s.focusIdx == field {
			return st.listCursor.Render("▸ ")
		}
		return "  "
	}
	fmt.Fprintf(&b, "%s%-*s %s\n", cursorFor(editorFieldTitle), editorLabelWidth, "Title", s.title.View())
	fmt.Fprintf(&b, "%s%-*s %s\n", cursorFor(editorFieldDate), editorLabelWidth, "Date", s.date.View())

	bodyLabel := "Body"
	if s.bodyDropped {
		bodyLabel = st.marker.Render("Body (dropped)")
	}
	fmt.Fprintf(&b, "%s%s\n%s\n", cursorFor(editorFieldBody), bodyLabel, s.body.View())

	if s.errMsg != "" {
		b.WriteString(st.statusErr.Render("⚠ "+s.errMsg) + "\n")
	}
	b.WriteString("\n" + st.help.Render(editorKeyHints[s.focusIdx]))
	return b.String()
}

// short mirrors gitio.short for display without importing it.
func short(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}
