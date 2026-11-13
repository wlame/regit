package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/model"
)

// retitleState is the bulk title-rewrite modal: an optional Go regular
// expression with a replacement (supporting $1-style group references) plus
// two finishing toggles, applied to the titles of every editable commit.
type retitleState struct {
	pattern    textinput.Model
	replace    textinput.Model
	capitalize bool // upper-case the first letter
	ensureDot  bool // end the title with "." (unless it already ends a sentence)
	focusIdx   int  // 0 pattern, 1 replacement, 2 capitalize, 3 ensure dot
	errMsg     string
}

const retitleFieldCount = 4

func newRetitleState(inputW int) retitleState {
	pattern := textinput.New()
	pattern.Prompt = ""
	pattern.CharLimit = 80
	pattern.Width = inputW
	pattern.Focus()

	replace := textinput.New()
	replace.Prompt = ""
	replace.CharLimit = 80
	replace.Width = inputW

	return retitleState{pattern: pattern, replace: replace}
}

// update routes keys to the focused row; tab/↑/↓ cycle rows, space flips the
// toggle rows.
func (s *retitleState) update(msg tea.Msg) tea.Cmd {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "tab", "down":
			s.cycle(1)
			return nil
		case "shift+tab", "up":
			s.cycle(-1)
			return nil
		}
		if s.focusIdx >= 2 {
			if key.String() == " " {
				s.errMsg = ""
				if s.focusIdx == 2 {
					s.capitalize = !s.capitalize
				} else {
					s.ensureDot = !s.ensureDot
				}
			}
			return nil // toggle rows swallow other keys
		}
	}
	s.errMsg = "" // any edit invalidates the previous error
	var cmd tea.Cmd
	if s.focusIdx == 0 {
		s.pattern, cmd = s.pattern.Update(msg)
	} else {
		s.replace, cmd = s.replace.Update(msg)
	}
	return cmd
}

func (s *retitleState) cycle(delta int) {
	switch s.focusIdx {
	case 0:
		s.pattern.Blur()
	case 1:
		s.replace.Blur()
	}
	s.focusIdx = (s.focusIdx + delta + retitleFieldCount) % retitleFieldCount
	switch s.focusIdx {
	case 0:
		s.pattern.Focus()
	case 1:
		s.replace.Focus()
	}
}

// hasWork reports whether the current settings would do anything at all.
func (s retitleState) hasWork() bool {
	return s.pattern.Value() != "" || s.capitalize || s.ensureDot
}

// transform compiles the modal's settings into a title transformation. The
// pattern is used VERBATIM — a trailing space in "feat: " is meaningful.
func (s retitleState) transform() (func(string) string, error) {
	var re *regexp.Regexp
	if pat := s.pattern.Value(); pat != "" {
		var err error
		re, err = regexp.Compile(pat)
		if err != nil {
			return nil, err
		}
	}
	return capability.RetitleTransform(re, s.replace.Value(), s.capitalize, s.ensureDot), nil
}

// retitlePreview reports how many titles in the given scope the current settings
// would change, plus one before→after example for the modal's live preview.
func retitlePreview(scope []model.Commit, plan *model.Plan, s retitleState) (changed int, example string, err error) {
	transform, err := s.transform()
	if err != nil {
		return 0, "", err
	}
	for _, c := range scope {
		cur := plan.EffectiveSubject(c)
		neu := transform(cur)
		if neu == cur || neu == "" {
			continue
		}
		changed++
		if example == "" {
			example = truncate(cur, 25) + " → " + truncate(neu, 25)
		}
	}
	return changed, example, nil
}

// retitleLabelWidth is the label column of the pattern and replacement fields.
const retitleLabelWidth = 13

// body renders the retitle form with a live preview of the transformation.
func (s retitleState) body(scope []model.Commit, plan *model.Plan, st styles) string {
	var b strings.Builder
	b.WriteString(st.modalTitle.Render("Bulk retitle (regex)") + "\n")
	b.WriteString(st.dim.Render(fmt.Sprintf("Rewrites %d commit title(s) in scope", len(scope))) + "\n\n")

	var cursors [retitleFieldCount]string
	for i := range cursors {
		cursors[i] = "  "
	}
	cursors[s.focusIdx] = st.listCursor.Render("▸ ")

	checkbox := func(on bool) string {
		if on {
			return "[x]"
		}
		return "[ ]"
	}
	fmt.Fprintf(&b, "%s%-*s %s\n", cursors[0], retitleLabelWidth, "Pattern:", s.pattern.View())
	fmt.Fprintf(&b, "%s%-*s %s\n", cursors[1], retitleLabelWidth, "Replacement:", s.replace.View())
	b.WriteString(fmt.Sprintf("%s%s Capitalize the first letter\n", cursors[2], checkbox(s.capitalize)))
	b.WriteString(fmt.Sprintf("%s%s End the title with \".\"\n\n", cursors[3], checkbox(s.ensureDot)))

	if !s.hasWork() {
		b.WriteString(st.dim.Render("Go regexp; $1 references capture groups") + "\n")
	} else if changed, example, err := retitlePreview(scope, plan, s); err != nil {
		b.WriteString(st.statusWarn.Render("invalid pattern: "+truncate(err.Error(), 40)) + "\n")
	} else if changed == 0 {
		b.WriteString(st.dim.Render("no titles would change") + "\n")
	} else {
		b.WriteString(st.statusOK.Render(fmt.Sprintf("will change %d title(s)", changed)) + "\n")
		b.WriteString(st.dim.Render(example) + "\n")
	}

	if s.errMsg != "" {
		b.WriteString(st.statusErr.Render("✗ "+s.errMsg) + "\n")
	}
	b.WriteString("\n" + st.help.Render("tab next · space toggle · enter apply · esc cancel"))
	return b.String()
}
