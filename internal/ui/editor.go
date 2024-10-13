package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/ghist/internal/model"
)

// editorState backs the per-commit editor modal: title, body, and the author
// date. Committer date follows the author date (linked) when the app config
// keeps them linked, which is the default.
type editorState struct {
	hash        string
	title       textinput.Model
	date        textinput.Model
	body        textarea.Model
	focusIdx    int // 0 title, 1 date, 2 body
	bodyDropped bool
}

const editorDateLayout = "2006-01-02 15:04"

// newEditorState seeds the editor from a commit and any pending edit on it.
func newEditorState(c model.Commit, plan *model.Plan, loc *time.Location) editorState {
	e, _ := plan.EditFor(c.Hash)

	title := textinput.New()
	title.Prompt = ""
	title.CharLimit = 120
	title.Width = 50
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
	body.SetWidth(50)
	body.SetHeight(5)
	if e.DropBody {
		body.SetValue("")
	} else if e.NewBody != nil {
		body.SetValue(*e.NewBody)
	} else {
		body.SetValue(c.Body)
	}

	return editorState{hash: c.Hash, title: title, date: date, body: body, bodyDropped: e.DropBody}
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
	}

	var cmd tea.Cmd
	switch s.focusIdx {
	case 0:
		s.title, cmd = s.title.Update(msg)
	case 1:
		s.date, cmd = s.date.Update(msg)
	case 2:
		s.bodyDropped = false // typing in the body undoes a drop
		s.body, cmd = s.body.Update(msg)
	}
	return cmd
}

func (s *editorState) cycle(delta int) {
	s.title.Blur()
	s.date.Blur()
	s.body.Blur()
	s.focusIdx = (s.focusIdx + delta + 3) % 3
	switch s.focusIdx {
	case 0:
		s.title.Focus()
	case 1:
		s.date.Focus()
	case 2:
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

// view renders the editor modal.
func (s editorState) view(c model.Commit, st styles) string {
	var b strings.Builder
	b.WriteString(st.modalTitle.Render("Edit commit "+short(c.Hash)) + "\n\n")

	field := func(idx int, label, content string) {
		cursor := "  "
		if s.focusIdx == idx {
			cursor = st.listCursor.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("%s%-8s %s\n", cursor, label, content))
	}
	field(0, "Title", s.title.View())
	field(1, "Date", s.date.View())

	bodyLabel := "Body"
	if s.bodyDropped {
		bodyLabel = st.marker.Render("Body (dropped)")
	}
	cursor := "  "
	if s.focusIdx == 2 {
		cursor = st.listCursor.Render("▸ ")
	}
	b.WriteString(fmt.Sprintf("%s%-8s\n%s\n", cursor, bodyLabel, s.body.View()))

	b.WriteString("\n" + st.help.Render("tab next · ctrl+d drop body · enter save · esc cancel"))
	return st.modal.Render(b.String())
}

// short mirrors gitio.short for display without importing it.
func short(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}
