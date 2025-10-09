package ui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

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

func newRetitleState() retitleState {
	pattern := textinput.New()
	pattern.Prompt = ""
	pattern.CharLimit = 80
	pattern.Width = 42
	pattern.Focus()

	replace := textinput.New()
	replace.Prompt = ""
	replace.CharLimit = 80
	replace.Width = 42

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

// togglesOnly reports whether the run used only the capitalize/ensure-dot
// finishing toggles, with no regex pattern. RetitleArgMap has no flag for
// those toggles — only match/replace/commits — so a run like this has no
// headless CLI equivalent to log yet.
func (s retitleState) togglesOnly() bool {
	return s.pattern.Value() == "" && (s.capitalize || s.ensureDot)
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
	return retitleTransform(re, s.replace.Value(), s.capitalize, s.ensureDot), nil
}

// retitleTransform builds the full title pipeline: optional regex replace,
// whitespace trim, then the finishing toggles.
func retitleTransform(re *regexp.Regexp, repl string, capitalize, ensureDot bool) func(string) string {
	return func(title string) string {
		out := title
		if re != nil {
			out = re.ReplaceAllString(out, repl)
		}
		// Titles never keep leading/trailing whitespace (e.g. after deleting
		// a "feat:" prefix while the space stayed behind).
		out = strings.TrimSpace(out)
		if capitalize {
			out = upperFirst(out)
		}
		if ensureDot && out != "" && !endsSentence(out) {
			out += "."
		}
		return out
	}
}

// upperFirst upper-cases the first rune of s.
func upperFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// endsSentence reports whether the title already ends like a sentence, so
// ensure-dot never produces endings like "Fix it!.".
func endsSentence(s string) bool {
	r := []rune(s)
	return strings.ContainsRune(".!?…:", r[len(r)-1])
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
		if neu == cur {
			continue
		}
		changed++
		if example == "" {
			example = truncate(cur, 25) + " → " + truncate(neu, 25)
		}
	}
	return changed, example, nil
}

// view renders the retitle modal with a live preview of the transformation.
func (s retitleState) view(scope []model.Commit, plan *model.Plan, st styles) string {
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
	b.WriteString(fmt.Sprintf("%s%-13s %s\n", cursors[0], "Pattern:", s.pattern.View()))
	b.WriteString(fmt.Sprintf("%s%-13s %s\n", cursors[1], "Replacement:", s.replace.View()))
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
	return st.modal.Render(b.String())
}
