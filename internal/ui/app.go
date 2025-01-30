// Package ui implements regit's Bubble Tea terminal interface: a three-pane main
// screen (commit list, contribution calendar, status) plus modal overlays for the
// strategy palette, the per-commit editor, the preview, and the apply
// confirmation. It depends on the engine (strategy), git layer (gitio), safety
// manager, and config; it holds no rewrite logic of its own.
package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/reword"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/split"
	"github.com/wlame/regit/internal/strategy"
)

type mode int

const (
	modeMain mode = iota
	modePalette
	modeEditor
	modePreview
	modeConfirm
	modeHelp
	modeRetitle
	modeSplit
	modeReword
)

type focusArea int

const (
	focusList focusArea = iota
	focusCalendar
	focusParams // the strategy/params pane below the calendar
)

type statusKind int

const (
	statusNone statusKind = iota
	statusOK
	statusWarn
	statusErr
)

// Model is the root Bubble Tea model.
type Model struct {
	repo  *gitio.Repo
	mgr   *safety.Manager
	cfg   config.Config
	loc   *time.Location
	state model.RepoState
	plan  *model.Plan

	width, height int
	mode          mode
	focus         focusArea

	listCursor int
	listOffset int

	calCursor time.Time
	calYear   int
	quotas    map[string]int  // day key -> pending Manual Pin quota
	picked    map[string]bool // day keys toggled (space) for Picked Days
	// quotaTyping is true while consecutive digits on the same day build a
	// two-digit quota; any cursor move or focus change starts a fresh number.
	quotaTyping bool

	strategies    []strategy.Strategy
	paletteCursor int
	form          paramsForm
	editor        editorState
	retitle       retitleState
	previewOffset int

	confirmInput textinput.Model
	confirmGuard safety.Guard

	splitReview splitState
	rewordSt    rewordState

	statusMsg  string
	statusKind statusKind

	st       styles
	quitting bool
}

// New constructs the root model from already-loaded repo data.
func New(repo *gitio.Repo, mgr *safety.Manager, cfg config.Config, state model.RepoState, commits []model.Commit) Model {
	loc := cfg.Location()
	plan := model.NewPlan(commits)

	// Start on the oldest commit not yet pushed to the upstream, so the user
	// begins right at the sync point and can press [b] to freeze all pushed
	// history. Only relocate when an actual pushed prefix exists (idx > 0);
	// with no upstream every commit is "unpushed", so we keep the default of
	// the newest commit instead of jumping to the root.
	listCursor := 0
	cursorCommit := len(commits) - 1
	if idx, ok := plan.FirstUnpushedIndex(); ok && idx > 0 {
		listCursor = len(commits) - 1 - idx // storage index -> display index
		cursorCommit = idx
	}

	cursor := time.Now().In(loc)
	if len(commits) > 0 {
		cursor = commits[cursorCommit].OrigAuthorDate.In(loc)
	}

	m := Model{
		repo:       repo,
		mgr:        mgr,
		cfg:        cfg,
		loc:        loc,
		state:      state,
		plan:       plan,
		mode:       modeMain,
		focus:      focusList,
		listCursor: listCursor,
		calCursor:  cursor,
		calYear:    cursor.Year(),
		quotas:     make(map[string]int),
		picked:     make(map[string]bool),
		strategies: strategy.Strategies(),
		st:         newStyles(),
	}
	// The strategy pane always shows a configurable form; seed it with the
	// default strategy so it is usable before the palette is ever opened.
	m.form = newParamsForm(m.strategies[m.defaultStrategyIndex()], cfg, cursor)

	if idx, ok := plan.FirstUnpushedIndex(); ok && idx > 0 {
		m.setStatus(fmt.Sprintf("%d commit(s) already pushed — press b here to freeze them", idx), statusOK)
	}
	return m
}

// Init satisfies tea.Model; regit needs no startup command.
func (m Model) Init() tea.Cmd { return nil }

// Update is the central event handler, dispatching by mode.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case rewordResultMsg:
		// Lazy-init the buffer/inFlight maps in case the message arrives before
		// the user has opened the picker (prefetch of a neighbouring commit).
		if m.rewordSt.buffer == nil {
			m.rewordSt.buffer = make(map[string]reword.Result)
		}
		if m.rewordSt.inFlight == nil {
			m.rewordSt.inFlight = make(map[string]bool)
		}
		if msg.err == nil {
			m.rewordSt.buffer[msg.hash] = msg.result
		}
		delete(m.rewordSt.inFlight, msg.hash)
		// Clear the generating status if this is the commit being viewed.
		if m.mode == modeReword && msg.hash == m.rewordSt.commit.Hash {
			if msg.err != nil {
				m.setStatus("Reword generate failed: "+msg.err.Error(), statusErr)
			} else {
				m.setStatus("", statusNone)
			}
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global quit (except while typing in a confirm/editor field where esc is
	// the cancel path).
	if msg.String() == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}

	switch m.mode {
	case modeMain:
		return m.updateMain(msg)
	case modePalette:
		return m.updatePalette(msg)
	case modeEditor:
		return m.updateEditor(msg)
	case modePreview:
		return m.updatePreview(msg)
	case modeConfirm:
		return m.updateConfirm(msg)
	case modeRetitle:
		return m.updateRetitle(msg)
	case modeSplit:
		return m.updateSplit(msg)
	case modeReword:
		return m.updateReword(msg)
	case modeHelp:
		if msg.String() == "?" || msg.String() == "esc" || msg.String() == "q" {
			m.mode = modeMain
		}
		return m, nil
	}
	return m, nil
}

// --- Main view -------------------------------------------------------------

func (m Model) updateMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.focus == focusParams {
		return m.updateParamsPane(msg)
	}
	if model, cmd, ok := m.globalAction(msg); ok {
		return model, cmd
	}
	switch msg.String() {
	case "tab":
		m.quotaTyping = false
		switch m.focus {
		case focusList:
			m.focus = focusCalendar
		case focusCalendar:
			m.focus = focusParams
		}
	case "enter":
		if m.focus == focusList {
			return m.openEditorAtCursor()
		}
	default:
		m.moveCursor(msg)
	}
	return m, nil
}

// globalAction handles the keybar commands that work from ANY pane —
// navigation keys (arrows, h/j/k/l, digits, space, tab, enter, esc) stay
// pane-local, everything else is global. It reports whether the key was
// consumed.
func (m Model) globalAction(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit, true
	case "?":
		m.mode = modeHelp
		return m, nil, true
	case "s":
		m.mode = modePalette
		m.paletteCursor = m.defaultStrategyIndex()
		return m, nil, true
	case "e":
		model, cmd := m.openEditorAtCursor()
		return model, cmd, true
	case "b":
		if len(m.plan.Commits) > 0 {
			m.setBaseAtCursor()
		}
		return m, nil, true
	case "D":
		m.dropAllBodies()
		return m, nil, true
	case "R":
		m.retitle = newRetitleState()
		m.mode = modeRetitle
		return m, nil, true
	case "p":
		m.mode = modePreview
		m.previewOffset = 0
		return m, nil, true
	case "A":
		model, cmd := m.beginApply()
		return model, cmd, true
	case "u":
		model, cmd := m.doUndo()
		return model, cmd, true
	case "r":
		m.plan.Edits = make(map[string]model.Edit)
		m.plan.Violations = nil
		m.setStatus("Cleared all pending edits", statusOK)
		return m, nil, true
	case "S":
		return m.beginSplit()
	case "w":
		return m.beginReword()
	case "g":
		model, cmd := m.applyQuotas()
		return model, cmd, true
	}
	return m, nil, false
}

// openEditorAtCursor opens the per-commit editor on the selected commit,
// refusing frozen ones.
func (m Model) openEditorAtCursor() (tea.Model, tea.Cmd) {
	if len(m.plan.Commits) == 0 {
		return m, nil
	}
	if m.plan.IsFrozen(m.storageIndexAtCursor()) {
		m.setStatus("Commit is frozen (before the base) — press b on it to move the base", statusWarn)
		return m, nil
	}
	c := m.commitAtCursor()
	m.editor = newEditorState(c, m.plan, m.loc, m.editorFieldWidth())
	m.mode = modeEditor
	return m, nil
}

// editorFieldWidth sizes the editor's title/body fields: 1.5× the original 50
// columns when the terminal allows, shrinking on narrow terminals so the
// modal (fields + label column + modal frame) still fits on screen.
func (m Model) editorFieldWidth() int {
	const preferred, minimum = 75, 50
	if m.width == 0 {
		return minimum
	}
	avail := m.width - 17 // cursor + label column + modal padding and border
	if avail > preferred {
		return preferred
	}
	if avail < minimum {
		return minimum
	}
	return avail
}

// moveCursor handles navigation keys for whichever pane is focused.
func (m *Model) moveCursor(msg tea.KeyMsg) {
	if m.focus == focusList {
		switch msg.String() {
		case "up", "k":
			if m.listCursor > 0 {
				m.listCursor--
			}
		case "down", "j":
			if m.listCursor < len(m.plan.Commits)-1 {
				m.listCursor++
			}
		}
		return
	}
	// Calendar focus. The heatmap is GitHub-style: weekdays run top-to-bottom
	// within a week column, weeks run left-to-right — so vertical arrows move
	// by one day and horizontal arrows jump a whole week.
	moved := false
	switch msg.String() {
	case "up", "k":
		m.calCursor = m.calCursor.AddDate(0, 0, -1)
		moved = true
	case "down", "j":
		m.calCursor = m.calCursor.AddDate(0, 0, 1)
		moved = true
	case "left", "h":
		m.calCursor = m.calCursor.AddDate(0, 0, -7)
		moved = true
	case "right", "l":
		m.calCursor = m.calCursor.AddDate(0, 0, 7)
		moved = true
	case "[":
		m.calYear--
		m.calCursor = m.calCursor.AddDate(-1, 0, 0)
		moved = true
	case "]":
		m.calYear++
		m.calCursor = m.calCursor.AddDate(1, 0, 0)
		moved = true
	case " ":
		// Toggle the day in the Picked Days set and reshuffle immediately.
		key := m.calCursor.Format(dateLayout)
		if m.picked[key] {
			delete(m.picked, key)
		} else {
			m.picked[key] = true
		}
		m.quotaTyping = false
		m.applyPickedNow()
	default:
		if d := digit(msg.String()); d >= 0 {
			key := m.calCursor.Format(dateLayout)
			// Consecutive digits on the same day build a two-digit quota:
			// 2 → 24 → 45 (the last two digits typed win).
			if m.quotaTyping {
				d = (m.quotas[key]%10)*10 + d
			}
			m.quotas[key] = d
			m.quotaTyping = true
			// Quotas apply immediately so the commit list reflects the pin
			// as soon as the digit is pressed ('g' re-applies manually).
			m.applyQuotasNow()
		}
	}
	m.calYear = m.calCursor.Year()
	if moved {
		m.quotaTyping = false // a new day starts a new number
		m.selectFirstCommitOfDay()
	}
}

// --- Bulk message operations -------------------------------------------------

// dropAllBodies marks every editable commit that still has a body with a
// DropBody edit, leaving title-only messages. Frozen commits are untouched.
func (m *Model) dropAllBodies() {
	dropped := 0
	for _, c := range m.plan.Active() {
		e, _ := m.plan.EditFor(c.Hash)
		if !c.HasBody() || e.DropBody {
			continue
		}
		e.DropBody = true
		m.plan.SetEdit(c.Hash, e)
		dropped++
	}
	if dropped == 0 {
		m.setStatus("No bodies to drop in the editable range", statusWarn)
		return
	}
	m.setStatus(fmt.Sprintf("Dropped the body of %d commit(s) — titles kept", dropped), statusOK)
}

// updateRetitle drives the bulk-retitle modal.
func (m Model) updateRetitle(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeMain
		return m, nil
	case "enter":
		return m.applyRetitle()
	}
	cmd := m.retitle.update(msg)
	return m, cmd
}

// applyRetitle runs the modal's transformation (regex replacement plus the
// finishing toggles) over every editable title, staging NewSubject edits. A
// result that lands back on the original title clears the edit instead of
// storing a no-op.
func (m Model) applyRetitle() (tea.Model, tea.Cmd) {
	if !m.retitle.hasWork() {
		m.retitle.errMsg = "nothing to do: set a pattern or enable a toggle"
		return m, nil
	}
	transform, err := m.retitle.transform()
	if err != nil {
		m.retitle.errMsg = "invalid pattern: " + err.Error()
		return m, nil
	}

	changed := 0
	for _, c := range m.plan.Active() {
		cur := m.plan.EffectiveSubject(c)
		neu := transform(cur)
		if neu == cur {
			continue
		}
		e, _ := m.plan.EditFor(c.Hash)
		if neu == c.Subject {
			e.NewSubject = nil // back to the original: no edit needed
		} else {
			e.NewSubject = &neu
		}
		m.plan.SetEdit(c.Hash, e)
		changed++
	}
	if changed == 0 {
		m.retitle.errMsg = "no titles would change"
		return m, nil
	}
	m.mode = modeMain
	m.setStatus(fmt.Sprintf("Retitled %d commit(s)", changed), statusOK)
	return m, nil
}

// storageIndexAtCursor converts the list cursor (display order, newest first)
// to the root-first storage index used by the plan.
func (m Model) storageIndexAtCursor() int {
	return len(m.plan.Commits) - 1 - m.listCursor
}

// setBaseAtCursor toggles the frozen-history boundary at the selected commit:
// it becomes the first (oldest) editable commit and everything older is
// frozen. Pressing b on the current base commit unfreezes the whole history.
func (m *Model) setBaseAtCursor() {
	idx := m.storageIndexAtCursor()
	if idx == m.plan.Base && m.plan.Base > 0 {
		m.plan.SetBase(0)
		m.setStatus("Base cleared — editing the whole history", statusOK)
		return
	}
	m.plan.SetBase(idx)
	m.setStatus(fmt.Sprintf("Base set at %s — editing %d commit(s), %d older frozen",
		short(m.plan.Commits[m.plan.Base].Hash), len(m.plan.Active()), m.plan.Base), statusOK)
}

// selectFirstCommitOfDay moves the list cursor to the newest commit whose
// effective date falls on the calendar-cursor day, so navigating the calendar
// drives the commit list. The cursor stays put when the day has no commits.
func (m *Model) selectFirstCommitOfDay() {
	key := m.calCursor.In(m.loc).Format(dateLayout)
	for i := 0; i < len(m.plan.Commits); i++ {
		c := m.commitAtDisplayIndex(i)
		if m.plan.EffectiveAuthorDate(c).In(m.loc).Format(dateLayout) == key {
			m.listCursor = i
			return
		}
	}
}

// --- Palette ---------------------------------------------------------------

func (m Model) updatePalette(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeMain
	case "up", "k":
		if m.paletteCursor > 0 {
			m.paletteCursor--
		}
	case "down", "j":
		if m.paletteCursor < len(m.strategies)-1 {
			m.paletteCursor++
		}
	case "enter":
		// Chosen: load the strategy into the params pane and focus it there.
		m.form = newParamsForm(m.strategies[m.paletteCursor], m.cfg, m.calCursor)
		m.mode = modeMain
		m.focus = focusParams
	}
	return m, nil
}

// --- Params pane -------------------------------------------------------------

// updateParamsPane routes keys while the strategy pane is focused: up/down
// move between fields, enter applies, tab/esc return focus to the list.
// Global action keys still work — the form fields are dates and numbers, so
// letters are never needed for typing.
func (m Model) updateParamsPane(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "esc":
		m.focus = focusList
		return m, nil
	case "enter":
		return m.applyStrategy()
	case "up", "shift+tab":
		m.form.moveFocus(-1)
		return m, nil
	case "down":
		m.form.moveFocus(1)
		return m, nil
	}
	if model, cmd, ok := m.globalAction(msg); ok {
		return model, cmd
	}
	m.form.errMsg = "" // any edit invalidates the previous apply error
	cmd := m.form.update(msg)
	return m, cmd
}

func (m Model) applyStrategy() (tea.Model, tea.Cmd) {
	params, err := m.form.buildParams(m.cfg, m.quotas, m.picked)
	if err != nil {
		m.form.errMsg = err.Error()
		return m, nil
	}
	tm := pickTimeModel(m.form)
	if err := strategy.Apply(m.plan, m.form.strat, tm, params, m.cfg.LinkDates); err != nil {
		m.form.errMsg = err.Error()
		return m, nil
	}
	// Success: hand focus back to the list so global keys (A, p, u…) work
	// again immediately; tab twice returns here for further tweaking.
	m.focus = focusList
	m.reportPlan(m.form.strat.Name())
	return m, nil
}

func (m Model) applyQuotas() (tea.Model, tea.Cmd) {
	m.applyQuotasNow()
	return m, nil
}

// applyQuotasNow re-runs the Manual Pin strategy over the current quota map,
// so quota edits are reflected in the commit list immediately.
func (m *Model) applyQuotasNow() {
	if len(m.quotas) == 0 {
		m.setStatus("No quotas set — focus the calendar and press 0-9 on days first", statusWarn)
		return
	}
	if err := strategy.Apply(m.plan, strategy.ManualPin{}, m.defaultTimeModel(), m.calendarParams(), m.cfg.LinkDates); err != nil {
		m.setStatus(err.Error(), statusErr)
		return
	}
	m.reportPlan("Manual Pin")
}

// applyPickedNow re-runs the Picked Days strategy over the picked-day set so
// toggling a day immediately reshuffles the commits across the selection.
func (m *Model) applyPickedNow() {
	if len(m.picked) == 0 {
		m.setStatus("No days picked — press space on calendar days first", statusWarn)
		return
	}
	if err := strategy.Apply(m.plan, strategy.PickedDays{}, m.defaultTimeModel(), m.calendarParams(), m.cfg.LinkDates); err != nil {
		m.setStatus(err.Error(), statusErr)
		return
	}
	m.reportPlan("Picked Days")
}

// calendarParams builds the strategy params for the interactive calendar
// workflows (quota pins and picked days), which need no date range: the
// chosen days themselves define it.
func (m *Model) calendarParams() strategy.Params {
	return strategy.Params{
		Location:    m.loc,
		WeekdayMask: strategy.WeekdayMask(m.cfg.WeekdayMask()),
		Quotas:      m.quotas,
		Picked:      m.picked,
		DayStartMin: m.cfg.WorkdayStartMin(),
		DayEndMin:   m.cfg.WorkdayEndMin(),
		LunchStart:  m.cfg.LunchStartMin(),
		LunchEnd:    m.cfg.LunchEndMin(),
		Seed:        m.cfg.Seed,
	}
}

func (m *Model) defaultTimeModel() strategy.TimeModel {
	return timeModelByName(m.cfg.DefaultTimeModel)
}

// --- Editor ----------------------------------------------------------------

func (m Model) updateEditor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeMain
		return m, nil
	case "enter":
		// Enter saves only when not editing the multi-line body (where enter is a
		// newline). Use ctrl+s-like behavior: save from title/date with enter.
		if m.editor.focusIdx != 2 {
			return m.saveEditor()
		}
	}
	cmd := m.editor.update(msg)
	return m, cmd
}

func (m Model) saveEditor() (tea.Model, tea.Cmd) {
	c := m.commitByHash(m.editor.hash)
	edit, err := m.editor.buildEdit(c, m.loc, m.cfg.LinkDates)
	if err != nil {
		m.setStatus(err.Error(), statusErr)
		return m, nil
	}
	m.plan.SetEdit(c.Hash, edit)
	m.recomputeViolations()
	m.mode = modeMain
	m.reportPlan("Edited " + short(c.Hash))
	return m, nil
}

// --- Preview ---------------------------------------------------------------

func (m Model) updatePreview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "p":
		m.mode = modeMain
	case "up", "k":
		if m.previewOffset > 0 {
			m.previewOffset--
		}
	case "down", "j":
		if m.previewOffset < len(m.plan.Commits)-1 {
			m.previewOffset++
		}
	}
	return m, nil
}

// --- Apply / Confirm -------------------------------------------------------

func (m Model) beginApply() (tea.Model, tea.Cmd) {
	if !m.plan.HasChanges() {
		m.setStatus("Nothing to apply — no commits edited yet", statusWarn)
		return m, nil
	}
	guard := safety.CheckGuards(m.state, m.plan)
	if guard.Blocked {
		m.setStatus(guard.BlockReason, statusErr)
		return m, nil
	}
	if guard.NeedsConfirmation {
		ti := textinput.New()
		ti.Prompt = "› "
		ti.Placeholder = guard.ConfirmWord
		ti.Focus()
		m.confirmInput = ti
		m.confirmGuard = guard
		m.mode = modeConfirm
		return m, nil
	}
	return m.commitApply(false)
}

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeMain
		m.setStatus("Apply cancelled", statusWarn)
		return m, nil
	case "enter":
		if strings.TrimSpace(m.confirmInput.Value()) != m.confirmGuard.ConfirmWord {
			m.setStatus("Confirmation text did not match; apply cancelled", statusErr)
			m.mode = modeMain
			return m, nil
		}
		return m.commitApply(true)
	}
	var cmd tea.Cmd
	m.confirmInput, cmd = m.confirmInput.Update(msg)
	return m, cmd
}

func (m Model) commitApply(confirmed bool) (tea.Model, tea.Cmd) {
	opts := safety.ApplyOptions{
		Sign:          gitio.DropSignatures,
		UnixTimestamp: time.Now().Unix(),
		Confirmed:     confirmed,
	}
	res, err := m.mgr.Apply(m.plan, m.state, opts)
	if err != nil {
		m.mode = modeMain
		m.setStatus(err.Error(), statusErr)
		return m, nil
	}
	if err := m.reload(); err != nil {
		m.setStatus("Applied, but reload failed: "+err.Error(), statusWarn)
		m.mode = modeMain
		return m, nil
	}
	m.mode = modeMain
	m.setStatus(fmt.Sprintf("Applied ✓ backup at %s — press u to undo", short(res.BackupRef)), statusOK)
	return m, nil
}

func (m Model) doUndo() (tea.Model, tea.Cmd) {
	if !m.mgr.CanUndo() {
		m.setStatus("Nothing to undo", statusWarn)
		return m, nil
	}
	hash, err := m.mgr.Undo()
	if err != nil {
		m.setStatus(err.Error(), statusErr)
		return m, nil
	}
	if err := m.reload(); err != nil {
		m.setStatus("Undo done, reload failed: "+err.Error(), statusWarn)
		return m, nil
	}
	m.setStatus("Undid last apply — restored "+short(hash), statusOK)
	return m, nil
}

// reload re-reads commits and state after the history changes, resetting edits.
func (m *Model) reload() error {
	commits, err := m.repo.ReadCommits()
	if err != nil {
		return err
	}
	state, err := m.repo.ReadState()
	if err != nil {
		return err
	}
	m.state = state
	m.plan = model.NewPlan(commits)
	m.quotas = make(map[string]int)
	if m.listCursor >= len(commits) {
		m.listCursor = max(0, len(commits)-1)
	}
	return nil
}

// --- Helpers ---------------------------------------------------------------

// commitAtCursor returns the commit under the list cursor (the list shows
// newest-first, so it indexes from the end of the root-first slice).
func (m Model) commitAtCursor() model.Commit {
	return m.commitAtDisplayIndex(m.listCursor)
}

func (m Model) commitAtDisplayIndex(i int) model.Commit {
	return m.plan.Commits[len(m.plan.Commits)-1-i]
}

func (m Model) commitByHash(hash string) model.Commit {
	for _, c := range m.plan.Commits {
		if c.Hash == hash {
			return c
		}
	}
	return model.Commit{}
}

func (m *Model) setStatus(msg string, kind statusKind) {
	m.statusMsg = msg
	m.statusKind = kind
}

// reportPlan sets a status line summarizing the plan after a strategy/edit runs.
func (m *Model) reportPlan(action string) {
	if m.plan.IsValid() {
		m.setStatus(fmt.Sprintf("%s · %d commits edited · ✓ sequential", action, len(m.plan.Edits)), statusOK)
	} else {
		m.setStatus(fmt.Sprintf("%s · %d edited · ⚠ %d out-of-order", action, len(m.plan.Edits), len(m.plan.Violations)), statusWarn)
	}
}

// recomputeViolations rebuilds the sequence-violation list from the plan's
// current effective author dates (used after a manual single-commit edit).
func (m *Model) recomputeViolations() {
	times := make([]time.Time, len(m.plan.Commits))
	for i, c := range m.plan.Commits {
		times[i] = m.plan.EffectiveAuthorDate(c)
	}
	m.plan.Violations = strategy.FindViolations(m.plan.Commits, times)
}

func (m Model) defaultStrategyIndex() int {
	for i, s := range m.strategies {
		if s.Name() == m.cfg.DefaultStrategy {
			return i
		}
	}
	return 0
}

// dayCounts tallies how many commits land on each calendar day under the plan.
func (m Model) dayCounts() map[string]int {
	counts := make(map[string]int)
	for _, c := range m.plan.Commits {
		key := m.plan.EffectiveAuthorDate(c).In(m.loc).Format("2006-01-02")
		counts[key]++
	}
	return counts
}

// pickTimeModel chooses the time model for a strategy apply: the form's choice
// when the strategy uses one, otherwise a harmless default (preserved-time
// strategies ignore it anyway).
func pickTimeModel(f paramsForm) strategy.TimeModel {
	if f.usesTimeMod {
		return f.timeModel()
	}
	return strategy.PreserveTimeModel{}
}

func timeModelByName(name string) strategy.TimeModel {
	for _, tm := range strategy.TimeModels() {
		if tm.Name() == name {
			return tm
		}
	}
	return strategy.WorkingHoursModel{}
}

// digit parses a single-digit key string to its value, or -1 if not a digit.
func digit(s string) int {
	if len(s) == 1 && s[0] >= '0' && s[0] <= '9' {
		return int(s[0] - '0')
	}
	return -1
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- Split -----------------------------------------------------------------

// beginSplit is called when the user presses [S]. It runs Engine.Propose on the
// selected commit (blocking for v1) and switches to modeSplit so the user can
// review the groups, then apply or cancel.
func (m Model) beginSplit() (tea.Model, tea.Cmd, bool) {
	if len(m.plan.Commits) == 0 {
		m.setStatus("No commit selected", statusWarn)
		return m, nil, true
	}
	if m.plan.IsFrozen(m.storageIndexAtCursor()) {
		m.setStatus("Cannot split a frozen commit — press b to move the base", statusWarn)
		return m, nil, true
	}
	c := m.commitAtCursor()
	m.setStatus(fmt.Sprintf("Proposing split for %s — thinking…", short(c.Hash)), statusNone)

	// v1: blocking call. The TUI is unresponsive while the LLM responds.
	// A future version can wrap this in a tea.Cmd goroutine for async behavior.
	st := runSplitOnCommit(&m, c)
	if st.errMsg != "" {
		m.setStatus(st.errMsg, statusErr)
		return m, nil, true
	}
	m.splitReview = st
	m.mode = modeSplit
	return m, nil, true
}

// updateSplit handles keys while in modeSplit (the review screen).
func (m Model) updateSplit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeMain
		m.setStatus("Split cancelled", statusWarn)
		return m, nil
	case "enter":
		return m.applySplit()
	}
	return m, nil
}

// applySplit calls safety.ApplySplit with the reviewed proposal and returns to
// the main view, reloading history on success.
func (m Model) applySplit() (tea.Model, tea.Cmd) {
	c := m.commitByHash(m.splitReview.proposal.CommitHash)
	if c.Hash == "" {
		m.mode = modeMain
		m.setStatus("Split commit no longer found — maybe history changed", statusErr)
		return m, nil
	}
	prop := m.splitReview.proposal
	trees := m.splitReview.trees

	subs := split.BuildSubCommits(c, prop.Groups, trees, m.cfg.Split.WIPPrefix)

	// Derive the tail from the commit hash recorded in the proposal rather than
	// from the cursor position. The user may have moved the cursor between
	// proposing the split and confirming it, which would cause the wrong tail to
	// be replayed and silently corrupt history.
	idx := -1
	for i, commit := range m.plan.Commits {
		if commit.Hash == prop.CommitHash {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.mode = modeMain
		m.setStatus("Split commit not found in plan — history may have changed", statusErr)
		return m, nil
	}
	tail := m.plan.Commits[idx+1:]

	opts := safety.ApplyOptions{
		Sign:          gitio.DropSignatures,
		UnixTimestamp: time.Now().Unix(),
	}
	res, err := m.mgr.ApplySplit(c, prop.ParentHash, subs, tail, m.state, opts)
	if err != nil {
		m.mode = modeMain
		m.setStatus(err.Error(), statusErr)
		return m, nil
	}
	if err := m.reload(); err != nil {
		m.mode = modeMain
		m.setStatus("Split applied, but reload failed: "+err.Error(), statusWarn)
		return m, nil
	}
	m.mode = modeMain
	m.setStatus(fmt.Sprintf("Split %s into %d commits ✓ backup at %s",
		short(c.Hash), len(subs), short(res.BackupRef)), statusOK)
	return m, nil
}

// View renders the current screen.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	main := m.viewMain()

	var overlay string
	switch m.mode {
	case modePalette:
		overlay = viewPalette(m.strategies, m.paletteCursor, m.st)
	case modeEditor:
		overlay = m.editor.view(m.commitByHash(m.editor.hash), m.st)
	case modePreview:
		overlay = viewPreview(m.plan, m.loc, m.previewOffset, m.previewVisibleRows(), m.st)
	case modeConfirm:
		overlay = m.viewConfirm()
	case modeHelp:
		overlay = m.st.modal.Render(strings.Join(helpLines, "\n"))
	case modeRetitle:
		overlay = m.retitle.view(m.plan, m.st)
	case modeSplit:
		overlay = m.st.modal.Render(renderSplitReview(m.splitReview.proposal))
	case modeReword:
		// Show the buffered result if available; otherwise a generating indicator.
		if result, ok := m.rewordSt.buffer[m.rewordSt.commit.Hash]; ok {
			overlay = m.st.modal.Render(renderRewordPicker(result, m.rewordSt.sel))
		} else {
			overlay = m.st.modal.Render(renderRewordPicker(reword.Result{}, m.rewordSt.sel))
		}
	}

	if overlay == "" {
		return main
	}
	if m.width == 0 || m.height == 0 {
		return overlay
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay)
}

func (m Model) previewVisibleRows() int {
	if m.height < 12 {
		return 8
	}
	return m.height - 8
}
