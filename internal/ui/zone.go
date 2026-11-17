package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/reword"
)

// zone.go draws wizards inside the flow zone, beside the commit list, instead
// of as a popup centered over the whole screen. The wizard still owns every
// key through its mode; only where it renders changes.

// zoneWizard is one wizard drawn in the flow zone: the title in its pane's
// top border and a renderer for its body at an inner width and height.
type zoneWizard struct {
	title string
	body  func(m Model, width, height int) string
}

// zoneWizards maps each wizard mode drawn in the flow zone to its pane.
var zoneWizards = map[mode]zoneWizard{
	modeReword:   {title: "REWORD", body: Model.rewordWizardBody},
	modeSquash:   {title: "SQUASH", body: Model.squashWizardBody},
	modeRetitle:  {title: "RETITLE", body: Model.retitleWizardBody},
	modeReauthor: {title: "CHANGE AUTHOR", body: Model.reauthorWizardBody},
	modeSplitGen: {title: "SPLIT", body: Model.splitProgressBody},
	modePurge:    {title: "PURGE", body: Model.purgeWizardBody},
	modeEditor:   {title: "EDIT", body: Model.editorWizardBody},
}

// zoneWizardMinWidth is the flow column's minimum width while a wizard is in
// it, matching the redate calendar: room for a commit title and its body
// without heavy wrapping.
const zoneWizardMinWidth = rightPaneWidth

// zoneInputWidth is the width of a wizard text field in the flow zone, after
// the pane's border and padding (4), the "▸ " cursor (2), a label column of
// labelWidth and its gap (1), and the field's own cursor cell (1). A longer
// value scrolls in the field instead of wrapping onto a second row.
func (m Model) zoneInputWidth(labelWidth int) int {
	return max(10, m.layoutWithWizard(true).flowWidth-4-2-labelWidth-1-1)
}

// zoneWizardOpen reports whether the open mode draws a wizard in the flow zone.
func (m Model) zoneWizardOpen() bool {
	_, ok := zoneWizards[m.mode]
	return ok
}

// viewZoneWizard renders the open wizard as the flow zone's pane, clamped to
// the zone's row budget.
func (m Model) viewZoneWizard(budget int) string {
	w := zoneWizards[m.mode]
	width := m.layout().flowWidth
	pane := m.st.paneActive
	const paneBorderRows = 2
	// The pane wraps a line longer than its inner width (a key-hint line, say);
	// the final MaxHeight keeps the result inside the zone's rows.
	body := w.body(m, width-2, budget-paneBorderRows)
	rendered := withBorderTitle(pane.Width(width).Render(body), pane, m.paneTitle(w.title, "", true))
	return lipgloss.NewStyle().MaxHeight(budget).Render(rendered)
}

// rewordWizardBody is the reword picker for the commit being reworded: the
// buffered variants, a generating indicator while the call runs, or the
// failure once it has errored.
func (m Model) rewordWizardBody(width, _ int) string {
	result, ok := m.rewordSt.buffer[m.rewordSt.commit.Hash]
	switch {
	case ok:
		return lipgloss.NewStyle().Width(width).Render(renderRewordPicker(result, m.rewordSt.sel, m.rewordSt.errMsg))
	case m.rewordSt.errMsg == "":
		return m.spinner.View() + " generating reword variants…\n\nesc cancel"
	default:
		return lipgloss.NewStyle().Width(width).Render(renderRewordPicker(reword.Result{}, m.rewordSt.sel, m.rewordSt.errMsg))
	}
}

// retitleWizardBody is the retitle form over the commits in scope.
func (m Model) retitleWizardBody(_, _ int) string {
	return m.retitle.body(m.affectedCommits(), m.plan, m.st)
}

// reauthorWizardBody is the change-author form.
func (m Model) reauthorWizardBody(_, _ int) string {
	return renderReauthorWizard(m.reauthorSt, m.st)
}

// splitProgressBody is shown while a split proposal is generated; the apply
// preview takes over once it arrives.
func (m Model) splitProgressBody(_, _ int) string {
	return m.spinner.View() + " proposing a split for " + short(m.splitGenHash) + "…\n\nesc cancel"
}

// editorWizardBody is the commit editor form.
func (m Model) editorWizardBody(_, _ int) string {
	return m.editor.render(m.commitByHash(m.editor.hash), m.st)
}

// fitEditorToZone sizes the editor's fields to the flow zone as it is now. The
// body field takes every row the rest of the form leaves free: the form is
// measured once with the smallest body, and the body grows by the difference.
// Called when the editor opens and when the terminal is resized under it.
func (m *Model) fitEditorToZone() {
	const paneBorderRows = 2
	innerWidth := m.layoutWithWizard(true).flowWidth - 2 // the pane's padding
	m.editor.setSize(innerWidth, editorMinBodyRows)
	// Measure with the body focused: its key line is the longest and wraps most.
	tallest := m.editor
	tallest.focusIdx = editorFieldBody
	form := tallest.render(m.commitByHash(m.editor.hash), m.st)
	// The pane wraps a long key line, so measure the form at the pane's width.
	usedRows := lipgloss.Height(lipgloss.NewStyle().Width(innerWidth).Render(form))
	freeRows := m.flowZoneRows() - paneBorderRows - editorErrorRows - usedRows
	m.editor.setSize(innerWidth, editorMinBodyRows+max(0, freeRows))
}
