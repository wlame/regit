package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/squash"
)

// selectedHashSet returns the EDITABLE commit hashes the current selection covers:
// the Set directly, or the inclusive Range span; empty for other modes. Frozen
// commits (before the base) are excluded even when a selection or a range dragged
// across the base boundary includes them — the same filtering commitsInSet /
// commitsInRange apply — so squash and reauthor (which both consume this set) can
// never rewrite the frozen cone the UI renders as protected.
func (m Model) selectedHashSet() map[string]bool {
	out := map[string]bool{}
	switch m.sel.mode {
	case SelSet:
		for _, c := range m.plan.Active() {
			if m.sel.isSet(c.Hash) {
				out[c.Hash] = true
			}
		}
	case SelRange:
		lo, hi := m.sel.rangeBounds(m.listCursor)
		for d := lo; d <= hi; d++ {
			storage := len(m.plan.Commits) - 1 - d
			if storage < 0 || storage >= len(m.plan.Commits) || m.plan.IsFrozen(storage) {
				continue
			}
			out[m.plan.Commits[storage].Hash] = true
		}
	}
	return out
}

// affectedChains derives the squash chains (and ignored singletons) from the
// current selection over the root-first plan commits.
func (m Model) affectedChains() ([]model.SquashChain, []string) {
	return model.DetectChains(m.plan.Commits, m.selectedHashSet())
}

// squashChange is the staged squash mutation that drops into the apply pipeline.
// commits and plan are captured at staging time (history cannot change while the
// overlay is open).
type squashChange struct {
	commits []model.Commit
	plan    model.SquashPlan
	// cmd is the CLI-equivalent line for this squash, set once at staging time.
	cmd string
}

func (c squashChange) commandLines() []string {
	if c.cmd == "" {
		return nil
	}
	return []string{c.cmd}
}

func (c squashChange) title() string {
	squashed := 0
	for _, ch := range c.plan.Chains {
		squashed += len(ch.Members)
	}
	return fmt.Sprintf("Squash %d commit(s) into %d", squashed, len(c.plan.Chains))
}

// guard mirrors safety.Manager.ApplySquash's internal checks so the pipeline can
// block/confirm BEFORE applying. Keeping this in lockstep with ApplySquash means
// the guard never disagrees with what the apply will actually allow.
// Pushed-detection uses c.plan.RewrittenCommits(c.commits) — the suffix from the
// earliest chain's first member onward that ApplySquash actually re-emits — not
// the full c.commits history, so an untouched pushed commit further back cannot
// force a confirmation on a squash that never reaches it.
func (c squashChange) guard(state model.RepoState) safety.Guard {
	for _, ch := range c.plan.Chains {
		for _, mem := range ch.Members {
			if mem.IsMerge {
				return safety.Guard{Blocked: true, BlockReason: "cannot squash across a merge commit"}
			}
		}
	}
	if state.IsDirty {
		return safety.Guard{
			Blocked:     true,
			BlockReason: "working tree has uncommitted changes; commit or stash them first",
		}
	}
	if model.AnyPushed(c.plan.RewrittenCommits(c.commits)) {
		return safety.Guard{
			NeedsConfirmation: true,
			ConfirmWord:       state.Branch,
			ConfirmReason: "some commits already exist on a remote; squashing rewrites " +
				"history and will require a force-push",
		}
	}
	return safety.Guard{}
}

// preview lists one line per chain: "first..last  N → 1  <subject>".
func (c squashChange) preview(st styles) previewModel {
	lines := make([]string, 0, len(c.plan.Chains))
	for _, ch := range c.plan.Chains {
		lines = append(lines, fmt.Sprintf("%s..%s  %d → 1  %s",
			short(ch.First().Hash), short(ch.Last().Hash), len(ch.Members), ch.Subject))
	}
	return previewModel{title: c.title(), lines: lines}
}

func (c squashChange) apply(mgr *safety.Manager, state model.RepoState, opts safety.ApplyOptions) (safety.ApplyResult, error) {
	return mgr.ApplySquash(c.commits, c.plan, state, opts)
}

// --- Squash wizard (modeSquash) --------------------------------------------

// squashField identifies the focused editor field in the wizard.
type squashField int

const (
	fldTitle squashField = iota
	fldBody
	fldAuthor
	fldDate
)

// squashFieldCount is the number of cyclable RESULT fields, used to wrap the
// tab-to-next-field cycle.
const squashFieldCount = 4

// squashDateLayout is the editable display/parse format for a chain's date.
const squashDateLayout = "2006-01-02 15:04"

// squashNarrowWidth is the usable-width threshold (in columns) below which the
// wizard renders as one stacked column instead of two side-by-side panes.
const squashNarrowWidth = 72

// squashBodyMaxLines caps how many rows the (possibly multi-line) body editor
// occupies in the RESULT pane; longer bodies scroll inside the textarea.
const squashBodyMaxLines = 3

// squashState holds the live wizard state: one tab per chain, the focused field,
// the inspect cursor, and which chains are still awaiting an LLM summary.
type squashState struct {
	chains     []model.SquashChain
	tab        int
	field      squashField
	listCursor int             // index into the ACTIVE chain's members (newest-first display)
	loading    map[string]bool // chain last-hash -> summary in flight
	edited     map[string]bool // chain last-hash -> user typed the title/body; a late summary must not clobber it
	dateBuf    []string        // editable date text per chain (indexed by tab); parsed on stage
	errMsg     string          // last stage failure (bad date, invalid plan), rendered inline
	// Field editors for the ACTIVE tab. chains/dateBuf stay the source of truth:
	// loadActiveEditors refreshes the editors from them (entry, tab switch, an
	// LLM summary landing on the open tab) and editField writes each keystroke
	// back. The body is a textarea because commit bodies can span lines.
	titleInput  textinput.Model
	bodyInput   textarea.Model
	authorInput textinput.Model
	dateInput   textinput.Model
}

// newSquashInput builds one single-line wizard field. The width is set at render
// time, when the RESULT pane's width is known.
func newSquashInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	return ti
}

// newSquashState builds the wizard state for the given chains: per-tab date
// buffers plus the field editors, loaded from the first chain with the title
// field focused.
func newSquashState(chains []model.SquashChain) squashState {
	st := squashState{chains: chains, loading: map[string]bool{}}
	st.dateBuf = make([]string, len(chains))
	for i := range chains {
		st.dateBuf[i] = chains[i].AuthorDate.Format(squashDateLayout)
	}
	st.titleInput = newSquashInput()
	st.authorInput = newSquashInput()
	st.dateInput = newSquashInput()
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0 // commit bodies may exceed the textarea's small default cap
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	st.bodyInput = ta
	return st.loadActiveEditors()
}

// loadActiveEditors refreshes the field editors from the active chain and date
// buffer, then focuses the editor for the current field. SetValue leaves each
// cursor at the end of the loaded text.
func (s squashState) loadActiveEditors() squashState {
	if len(s.chains) == 0 {
		return s
	}
	ch := s.active()
	s.titleInput.SetValue(ch.Subject)
	s.bodyInput.SetValue(ch.Body)
	s.authorInput.SetValue(ch.AuthorName)
	if s.tab < len(s.dateBuf) {
		s.dateInput.SetValue(s.dateBuf[s.tab])
	}
	return s.focusField()
}

// focusField gives keyboard focus to exactly the editor matching s.field (a
// blurred editor ignores keys and hides its cursor).
func (s squashState) focusField() squashState {
	s.titleInput.Blur()
	s.bodyInput.Blur()
	s.authorInput.Blur()
	s.dateInput.Blur()
	switch s.field {
	case fldTitle:
		s.titleInput.Focus()
	case fldBody:
		s.bodyInput.Focus()
	case fldAuthor:
		s.authorInput.Focus()
	case fldDate:
		s.dateInput.Focus()
	}
	return s
}

// markEdited records that the user manually edited the given chain's message, so a
// summary that lands afterward leaves their text alone.
func (s *squashState) markEdited(chainKey string) {
	if s.edited == nil {
		s.edited = map[string]bool{}
	}
	s.edited[chainKey] = true
}

// active returns the chain for the current tab.
func (s squashState) active() model.SquashChain { return s.chains[s.tab] }

// switchTab moves the tab by delta, wrapping, resets per-tab cursors, and
// reloads the field editors from the newly active chain.
func (s squashState) switchTab(delta int) squashState {
	n := len(s.chains)
	if n == 0 {
		return s
	}
	// The double-modulo keeps the result in [0, n) even for negative deltas, so
	// shift+← from tab 0 wraps to the last tab.
	s.tab = ((s.tab+delta)%n + n) % n
	s.field = fldTitle
	s.listCursor = 0
	return s.loadActiveEditors()
}

// applySummary fills the chain identified by its last-hash with an LLM proposal
// and clears its loading flag — UNLESS the user already typed their own title/body
// for that chain, in which case the summary is dropped so it cannot clobber their
// edit (summaries can land after the user has moved on and typed something).
func (s squashState) applySummary(chainKey string, p squash.Proposal) squashState {
	if s.edited[chainKey] {
		delete(s.loading, chainKey)
		return s
	}
	s.chains = append([]model.SquashChain(nil), s.chains...)
	for i := range s.chains {
		if s.chains[i].Last().Hash == chainKey {
			s.chains[i].Subject = p.Subject
			s.chains[i].Body = p.Body
			if i == s.tab {
				// The summary landed on the open tab: mirror it into the editors.
				s.titleInput.SetValue(p.Subject)
				s.bodyInput.SetValue(p.Body)
			}
		}
	}
	delete(s.loading, chainKey)
	return s
}

// renderSquashWizard renders the wizard modal body, honoring the terminal width
// and height. Wide terminals get two panes (commit list | RESULT editor); narrow
// terminals fall back to a single stacked column. The caller frames the result in
// the modal style.
func renderSquashWizard(st squashState, width, height int, s styles) string {
	if len(st.chains) == 0 {
		return "No chains to squash."
	}
	usable := squashUsableWidth(width)
	visible := squashVisibleRows(height)

	var body string
	if usable < squashNarrowWidth {
		body = squashStacked(st, usable, visible, s)
	} else {
		body = squashTwoColumn(st, usable, visible, s)
	}

	header := truncate(squashHeader(st), usable)
	detail := truncate(squashDetailLine(st), usable)
	footer := s.help.Render(truncate(squashFooter(len(st.chains) > 1), usable))
	parts := []string{header, "", body, detail}
	if st.errMsg != "" {
		// Surface a stage failure (bad date, invalid plan) inline, right above the
		// footer, instead of a status-bar line hidden under this modal.
		parts = append(parts, s.statusErr.Render("⚠ "+truncate(st.errMsg, usable)))
	}
	parts = append(parts, footer)
	return strings.Join(parts, "\n")
}

// squashUsableWidth is the column budget for the wizard body, leaving room for the
// modal border+padding and a small screen margin, and capped so very wide
// terminals do not stretch the editor unreadably.
func squashUsableWidth(termWidth int) int {
	w := termWidth - 10
	if w > 110 {
		w = 110
	}
	if w < 40 {
		w = 40
	}
	return w
}

// squashVisibleRows is how many commit rows the list shows before scrolling,
// derived from the terminal height minus the wizard's fixed chrome.
func squashVisibleRows(termHeight int) int {
	v := termHeight - 12
	if v < 3 {
		v = 3
	}
	return v
}

// squashColumnWidths splits the usable width into the left (commit list) and right
// (RESULT) content widths. Each bordered pane adds 4 columns (border 2 + padding 2)
// and a 1-column gutter sits between them, so the two content widths sum to
// usable-9.
func squashColumnWidths(usable int) (left, right int) {
	content := usable - 9
	left = content * 2 / 5
	if left < 24 {
		left = 24
	}
	right = content - left
	if right < 24 {
		right = 24
	}
	return left, right
}

// squashHeader is the top line: the squash summary plus a chain indicator when
// more than one chain is being squashed.
func squashHeader(st squashState) string {
	squashed := 0
	for _, c := range st.chains {
		squashed += len(c.Members)
	}
	h := fmt.Sprintf("SQUASH · %d → %d commit(s)", squashed, len(st.chains))
	if len(st.chains) > 1 {
		h += fmt.Sprintf("      chain ‹%d/%d›", st.tab+1, len(st.chains))
	}
	return h
}

// squashDetailLine shows the cursored member's identity below the panes, replacing
// the old INSPECT block.
func squashDetailLine(st squashState) string {
	ch := st.active()
	insp := ch.Members[len(ch.Members)-1-st.listCursor]
	return fmt.Sprintf("%s · %s · %s", short(insp.Hash), insp.AuthorName,
		insp.OrigAuthorDate.Format(squashDateLayout))
}

// squashFooter is the always-visible key hint. The ⇧←→ chain token only appears
// when there is more than one chain to switch between (plain ←/→ move the text
// cursor in the focused field).
func squashFooter(multiChain bool) string {
	if multiChain {
		return "↑↓ commit · ⇧←→ chain · tab field · type edit · enter apply · esc"
	}
	return "↑↓ commit · tab field · type edit · enter apply · esc"
}

// squashCommitLines returns the visible commit rows for the active chain, one line
// each, newest-first, scrolled so the cursor stays in view and truncated to width.
func squashCommitLines(st squashState, width, visible int, s styles) []string {
	ch := st.active()
	n := len(ch.Members)
	offset := 0
	if st.listCursor >= visible {
		offset = st.listCursor - visible + 1
	}
	var lines []string
	for d := offset; d < n && d < offset+visible; d++ {
		mem := ch.Members[n-1-d] // display index d -> root-first index n-1-d
		prefix := "  "
		if d == st.listCursor {
			prefix = "▸ "
		}
		row := fmt.Sprintf("%s%s  %s", prefix, short(mem.Hash), truncate(mem.Subject, max(1, width-12)))
		if d == st.listCursor {
			row = s.listCursor.Render(row)
		}
		lines = append(lines, row)
	}
	return lines
}

// squashResultLines returns the RESULT editor rows for the active chain: each
// field editor framed by a label column and a focus bar. The focused editor
// renders its own cursor; the body block may span several rows. Editor widths
// are set here (on value copies) because the pane width is only known at render
// time.
func squashResultLines(st squashState, width int) []string {
	ch := st.active()
	inW := max(1, width-9)
	label := func(name string, f squashField) string {
		return fmt.Sprintf("%-7s%s", name, focusMark(st.field, f))
	}

	title := st.titleInput
	title.Width = inW

	body := st.bodyInput
	body.SetWidth(inW)
	bodyLines := strings.Count(body.Value(), "\n") + 1
	body.SetHeight(min(bodyLines, squashBodyMaxLines))

	// The email is not editable here, so it renders as a plain suffix after the
	// name editor, which shrinks to leave it room.
	emailSuffix := truncate(fmt.Sprintf(" <%s>", ch.AuthorEmail), inW/2)
	author := st.authorInput
	author.Width = max(1, inW-lipgloss.Width(emailSuffix))

	date := st.dateInput
	date.Width = inW

	lines := []string{label("Title", fldTitle) + title.View()}
	bodyBlock := lipgloss.JoinHorizontal(lipgloss.Top, label("Message", fldBody), body.View())
	lines = append(lines, strings.Split(bodyBlock, "\n")...)
	return append(lines,
		label("Author", fldAuthor)+author.View()+emailSuffix,
		label("Date", fldDate)+date.View(),
	)
}

// squashResultHeader is the RESULT pane title, with a summarizing note while the
// active chain's LLM summary is in flight.
func squashResultHeader(st squashState) string {
	h := "RESULT"
	if st.loading[st.active().Last().Hash] {
		h += "  (summarizing…)"
	}
	return h
}

// squashTwoColumn renders the commit list and RESULT editor as two bordered panes
// side by side.
func squashTwoColumn(st squashState, usable, visible int, s styles) string {
	lw, rw := squashColumnWidths(usable)
	left := append([]string{fmt.Sprintf("COMMITS (%d → 1)", len(st.active().Members))},
		squashCommitLines(st, lw, visible, s)...)
	right := append([]string{squashResultHeader(st)}, squashResultLines(st, rw)...)
	leftBox := s.pane.Width(lw).Render(strings.Join(left, "\n"))
	rightBox := s.pane.Width(rw).Render(strings.Join(right, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, leftBox, " ", rightBox)
}

// squashStacked is the narrow-terminal fallback: the same sections in one column.
func squashStacked(st squashState, usable, visible int, s styles) string {
	var b strings.Builder
	fmt.Fprintf(&b, "COMMITS (%d → 1)\n", len(st.active().Members))
	b.WriteString(strings.Join(squashCommitLines(st, usable, visible, s), "\n"))
	b.WriteString("\n\n" + squashResultHeader(st) + "\n")
	b.WriteString(strings.Join(squashResultLines(st, usable), "\n"))
	return b.String()
}

// focusMark returns a focus bar when field == want, else a single space, so the
// active result field is visually marked in the wizard.
func focusMark(field, want squashField) string {
	if field == want {
		return "┃"
	}
	return " "
}

// summarizeSquashResultMsg is the async message when a chain's summary completes.
type summarizeSquashResultMsg struct {
	chainKey string
	prop     squash.Proposal
	err      error
}

// summarizeSquashCmd runs Engine.Summarize off the UI thread, returning the
// result as a summarizeSquashResultMsg on the Bubble Tea bus when done.
func summarizeSquashCmd(eng *squash.Engine, ch model.SquashChain, opts squash.Options) tea.Cmd {
	return func() tea.Msg {
		prop, err := eng.Summarize(context.Background(), ch, opts)
		return summarizeSquashResultMsg{chainKey: ch.Last().Hash, prop: prop, err: err}
	}
}

// beginSquash enters the wizard. It builds chains from the selection, fills the
// no-LLM defaults, and (when an LLM is configured) fires async summaries. It
// mirrors beginSplit/beginReword's three-return-value shape so it can be called
// uniformly from the key dispatcher.
func (m Model) beginSquash() (tea.Model, tea.Cmd, bool) {
	chains, _ := m.affectedChains()
	if len(chains) == 0 {
		m.setStatus("Select 2+ consecutive commits to squash (Space toggles, v range)", statusWarn)
		return m, nil, true
	}
	for i := range chains {
		chains[i] = model.ClampChainDate(m.plan.Commits, model.DefaultSquashConfig(chains[i]))
	}
	st := newSquashState(chains)

	// Fire async summaries only if an LLM is configured. buildSquashEngine returns
	// (nil, nil) for the no-LLM path, so the defaults above are the final values.
	var cmds []tea.Cmd
	if eng, err := buildSquashEngine(&m); err == nil && eng != nil {
		opts := squash.Options{Now: time.Now()}
		for _, ch := range chains {
			st.loading[ch.Last().Hash] = true
			cmds = append(cmds, summarizeSquashCmd(eng, ch, opts))
		}
	}
	m.squashSt = st
	m.mode = modeSquash
	// Keep the shared spinner ticking while a chain's summary is loading; harmless
	// when nothing is in flight (the no-LLM path).
	cmds = append(cmds, m.spinner.Tick)
	return m, tea.Batch(cmds...), true
}

// updateSquash handles keys while the wizard is open: ↑/↓ move the commit cursor,
// shift+←/→ switch chains, tab/shift+tab cycle the RESULT fields, enter applies,
// esc cancels. Every other key — including ←/→, home/end, backspace, and pasted
// text — goes to the focused field editor, which owns editing. The commit list is
// always navigable (there is no separate "list field" to focus), and letter keys
// — including A — are literal text input (P0: typing "Add tests" must not stage
// the wizard).
func (m Model) updateSquash(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.squashSt
	switch msg.String() {
	case "esc":
		m.mode = modeMain
		m.setStatus("Squash cancelled", statusWarn)
		return m, nil
	case "shift+left":
		m.squashSt = st.switchTab(-1)
		return m, nil
	case "shift+right":
		m.squashSt = st.switchTab(1)
		return m, nil
	case "up":
		if st.listCursor > 0 {
			st.listCursor--
			m.squashSt = st
		}
		return m, nil
	case "down":
		if st.listCursor < len(st.active().Members)-1 {
			st.listCursor++
			m.squashSt = st
		}
		return m, nil
	case "tab":
		st.field = (st.field + 1) % squashFieldCount
		m.squashSt = st.focusField()
		return m, nil
	case "shift+tab":
		st.field = (st.field + squashFieldCount - 1) % squashFieldCount
		m.squashSt = st.focusField()
		return m, nil
	case "enter":
		return m.stageSquash()
	}
	var cmd tea.Cmd
	m.squashSt, cmd = st.editField(msg)
	return m, cmd
}

// editField routes msg to the focused field editor and writes the result back
// into the active chain (or date buffer). Title/body edits mark the chain as
// user-edited only when the text actually changed, so cursor movement alone
// cannot block a late LLM summary from filling the fields.
func (st squashState) editField(msg tea.KeyMsg) (squashState, tea.Cmd) {
	if len(st.chains) == 0 {
		return st, nil
	}
	st = st.focusField() // tolerate a directly assigned field (blurred editors drop keys)
	st.errMsg = ""       // any edit invalidates the previous stage error
	ch := &st.chains[st.tab]
	var cmd tea.Cmd
	switch st.field {
	case fldTitle:
		st.titleInput, cmd = st.titleInput.Update(msg)
		if v := st.titleInput.Value(); v != ch.Subject {
			ch.Subject = v
			st.markEdited(ch.Last().Hash)
		}
	case fldBody:
		st.bodyInput, cmd = st.bodyInput.Update(msg)
		if v := st.bodyInput.Value(); v != ch.Body {
			ch.Body = v
			st.markEdited(ch.Last().Hash)
		}
	case fldAuthor:
		st.authorInput, cmd = st.authorInput.Update(msg)
		ch.AuthorName = st.authorInput.Value()
	case fldDate:
		st.dateInput, cmd = st.dateInput.Update(msg)
		if st.tab < len(st.dateBuf) {
			st.dateBuf[st.tab] = st.dateInput.Value()
		}
	}
	return st, cmd
}

// stageSquash validates the plan and hands it to the apply pipeline (preview →
// confirm → apply), reusing the same overlay split uses.
func (m Model) stageSquash() (tea.Model, tea.Cmd) {
	chains := append([]model.SquashChain(nil), m.squashSt.chains...) // copy before mutating
	for i := range chains {
		if i < len(m.squashSt.dateBuf) {
			parsed, err := time.ParseInLocation(squashDateLayout, strings.TrimSpace(m.squashSt.dateBuf[i]), chains[i].AuthorDate.Location())
			if err != nil {
				// Stay in the wizard and show the error inline — a status-bar
				// message would be invisible under this modal.
				m.squashSt.errMsg = fmt.Sprintf("Chain %d: invalid date (use YYYY-MM-DD HH:MM)", i+1)
				return m, nil
			}
			chains[i].AuthorDate = parsed
		}
	}
	plan := model.SquashPlan{Chains: chains}
	if err := plan.Validate(m.plan.Commits); err != nil {
		m.squashSt.errMsg = "Invalid squash: " + err.Error()
		return m, nil
	}
	spec := chainsToSpec(plan.Chains)
	change := squashChange{commits: m.plan.Commits, plan: plan, cmd: capability.Render("squash", capability.SquashArgMap(spec))}
	// Run the pre-flight guard before opening the pipeline: a hard block (dirty
	// tree, merge) reports its reason inline (the status bar is hidden under this
	// modal) and never enters the overlay; a pushed-history change records
	// NeedsConfirmation so updatePipeline runs the typed-confirm step.
	guard := change.guard(m.state)
	if guard.Blocked {
		m.squashSt.errMsg = guard.BlockReason
		return m, nil
	}
	m.pending = change
	m.pendingGuard = guard
	m.pipelineOrigin = modeSquash // esc returns to the wizard, preserving its typed edits
	m.mode = modePipeline
	m.pipelineApply = true
	m.previewOffset = 0
	return m, nil
}

// chainsToSpec renders the chains as a comma-joined "first^..last" spec that
// round-trips through parseSquashSpec.
func chainsToSpec(chains []model.SquashChain) string {
	parts := make([]string, len(chains))
	for i, ch := range chains {
		parts[i] = short(ch.First().Hash) + "^.." + short(ch.Last().Hash)
	}
	return strings.Join(parts, ",")
}

// viewSquash renders the wizard overlay. renderSquashWizard is a pure function
// (used directly in tests without a spinner), so the shared spinner is spliced
// in here as a post-processing step: when the active chain's summary is still
// loading, its static "(summarizing…)" header is swapped for the ticking frame.
func (m Model) viewSquash() string {
	body := renderSquashWizard(m.squashSt, m.width, m.height, m.st)
	if len(m.squashSt.chains) > 0 && m.squashSt.loading[m.squashSt.active().Last().Hash] {
		body = strings.ReplaceAll(body, "(summarizing…)", m.spinner.View()+" summarizing…")
	}
	return m.st.modal.Render(body)
}

// buildSquashEngine builds a squash.Engine from the TUI config, or returns
// (nil, nil) when no LLM is configured (the no-LLM path). It mirrors
// buildRewordEngine's provider/baseURL/key resolution, reusing the package-private
// helpers in reword.go, then wraps a squash.Engine over a fresh HTTP backend.
func buildSquashEngine(m *Model) (*squash.Engine, error) {
	cfg := m.cfg
	if cfg.LLM.Provider == "" {
		return nil, nil // no LLM configured → caller uses no-LLM defaults
	}
	baseURL := llm.DefaultBaseURL(cfg.LLM.Provider, cfg.LLM.BaseURL)
	apiKey := llm.ResolveAPIKey(cfg.LLM.Provider)
	timeoutSec := cfg.LLM.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	backend := llm.NewHTTPBackend(baseURL, cfg.LLM.Model, apiKey, time.Duration(timeoutSec)*time.Second)
	gitDir, err := m.repo.GitDir()
	if err != nil {
		return nil, err
	}
	return squash.NewEngine(m.repo, backend, squash.NewCache(gitDir)), nil
}
