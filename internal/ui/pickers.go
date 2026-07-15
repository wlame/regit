package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// pickerState is a searchable single-select list overlay (used for the branch /
// base-branch pickers). Items are filtered live by a case-insensitive substring
// typed into the embedded text input; the cursor selects among the filtered
// results. The filter captures letter keys, so the caller drives actions with
// non-letter keys (enter / tab / arrows).
type pickerState struct {
	title  string
	items  []string
	filter textinput.Model
	cursor int
	// errMsg is a guard failure (dirty tree, unsaved edits, …) shown inside the
	// overlay; the main status bar is hidden behind the picker, so errors must
	// surface here.
	errMsg string
}

// newPicker builds a picker over items with a focused, empty filter.
func newPicker(title string, items []string, filterW int) pickerState {
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = "filter…"
	ti.CharLimit = 64
	ti.Width = filterW
	ti.Focus()
	return pickerState{title: title, items: items, filter: ti}
}

// filtered returns the items containing the (case-insensitive) filter text, in
// original order. An empty filter returns every item.
func (p pickerState) filtered() []string {
	q := strings.ToLower(strings.TrimSpace(p.filter.Value()))
	if q == "" {
		return p.items
	}
	var out []string
	for _, it := range p.items {
		if strings.Contains(strings.ToLower(it), q) {
			out = append(out, it)
		}
	}
	return out
}

// moveCursor shifts the selection within the filtered list, clamped to range.
func (p *pickerState) moveCursor(delta int) {
	n := len(p.filtered())
	if n == 0 {
		p.cursor = 0
		return
	}
	p.cursor += delta
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor > n-1 {
		p.cursor = n - 1
	}
}

// selected returns the highlighted item and whether one exists.
func (p pickerState) selected() (string, bool) {
	f := p.filtered()
	if len(f) == 0 || p.cursor < 0 || p.cursor >= len(f) {
		return "", false
	}
	return f[p.cursor], true
}

// update feeds a key to the filter input (typing / backspace) and keeps the
// cursor in range as the filtered set shrinks. Navigation keys are handled by
// the caller, not here.
func (p *pickerState) update(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(msg)
	if n := len(p.filtered()); p.cursor > n-1 {
		p.cursor = max(0, n-1)
	}
	return cmd
}

// viewPicker renders the picker overlay: title, filter, the filtered list with
// the cursor, and a footer hint. The caller centers it over the backbone.
func viewPicker(p pickerState, footer string, st styles) string {
	var b strings.Builder
	b.WriteString(st.modalTitle.Render(p.title) + "\n")
	b.WriteString(p.filter.View() + "\n\n")
	f := p.filtered()
	if len(f) == 0 {
		b.WriteString(st.dim.Render("  (no matches)") + "\n")
	}
	for i, it := range f {
		if i == p.cursor {
			b.WriteString(st.selected.Render("▸ "+it) + "\n")
		} else {
			b.WriteString("  " + st.dim.Render(it) + "\n")
		}
	}
	if p.errMsg != "" {
		b.WriteString(st.statusErr.Render("✗ "+p.errMsg) + "\n")
	}
	b.WriteString("\n" + st.help.Render(footer))
	return st.modal.Render(b.String())
}
