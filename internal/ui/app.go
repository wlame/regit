// Package ui implements ghist's Bubble Tea terminal interface: a three-pane main
// screen (commit list, contribution calendar, status) plus modal overlays for the
// strategy palette, the per-commit editor, the preview, and the apply
// confirmation. It depends on the engine (strategy), git layer (gitio), safety
// manager, and config; it holds no rewrite logic of its own.
package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/ghist/internal/config"
	"github.com/wlame/ghist/internal/gitio"
	"github.com/wlame/ghist/internal/model"
	"github.com/wlame/ghist/internal/safety"
	"github.com/wlame/ghist/internal/strategy"
)

type mode int

const (
	modeMain mode = iota
	modePalette
	modeParams
	modeEditor
	modePreview
	modeConfirm
	modeHelp
)

type focusArea int

const (
	focusList focusArea = iota
	focusCalendar
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
	quotas    map[string]int // day key -> pending Manual Pin quota

	strategies    []strategy.Strategy
	paletteCursor int
	form          paramsForm
	editor        editorState
	previewOffset int

	confirmInput textinput.Model
	confirmGuard safety.Guard

	statusMsg  string
	statusKind statusKind

	st       styles
	quitting bool
}

// New constructs the root model from already-loaded repo data.
func New(repo *gitio.Repo, mgr *safety.Manager, cfg config.Config, state model.RepoState, commits []model.Commit) Model {
	loc := cfg.Location()
	plan := model.NewPlan(commits)

	cursor := time.Now().In(loc)
	if len(commits) > 0 {
		cursor = commits[len(commits)-1].OrigAuthorDate.In(loc)
	}

	return Model{
		repo:       repo,
		mgr:        mgr,
		cfg:        cfg,
		loc:        loc,
		state:      state,
		plan:       plan,
		mode:       modeMain,
		focus:      focusList,
		calCursor:  cursor,
		calYear:    cursor.Year(),
		quotas:     make(map[string]int),
		strategies: strategy.Strategies(),
		st:         newStyles(),
	}
}

// Init satisfies tea.Model; ghist needs no startup command.
func (m Model) Init() tea.Cmd { return nil }

// Update is the central event handler, dispatching by mode.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
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
	case modeParams:
		return m.updateParams(msg)
	case modeEditor:
		return m.updateEditor(msg)
	case modePreview:
		return m.updatePreview(msg)
	case modeConfirm:
		return m.updateConfirm(msg)
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
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "?":
		m.mode = modeHelp
	case "tab":
		if m.focus == focusList {
			m.focus = focusCalendar
		} else {
			m.focus = focusList
		}
	case "s":
		m.mode = modePalette
		m.paletteCursor = m.defaultStrategyIndex()
	case "e", "enter":
		if m.focus == focusList && len(m.plan.Commits) > 0 {
			c := m.commitAtCursor()
			m.editor = newEditorState(c, m.plan, m.loc)
			m.mode = modeEditor
		}
	case "p":
		m.mode = modePreview
		m.previewOffset = 0
	case "A":
		return m.beginApply()
	case "u":
		return m.doUndo()
	case "r":
		m.plan.Edits = make(map[string]model.Edit)
		m.plan.Violations = nil
		m.setStatus("Cleared all pending edits", statusOK)
	case "g":
		return m.applyQuotas()
	default:
		m.moveCursor(msg)
	}
	return m, nil
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
	// Calendar focus.
	switch msg.String() {
	case "left", "h":
		m.calCursor = m.calCursor.AddDate(0, 0, -1)
	case "right", "l":
		m.calCursor = m.calCursor.AddDate(0, 0, 1)
	case "up", "k":
		m.calCursor = m.calCursor.AddDate(0, 0, -7)
	case "down", "j":
		m.calCursor = m.calCursor.AddDate(0, 0, 7)
	case "[":
		m.calYear--
		m.calCursor = m.calCursor.AddDate(-1, 0, 0)
	case "]":
		m.calYear++
		m.calCursor = m.calCursor.AddDate(1, 0, 0)
	default:
		if d := digit(msg.String()); d >= 0 {
			m.quotas[m.calCursor.Format("2006-01-02")] = d
			m.setStatus(fmt.Sprintf("Set quota %d on %s (press g to apply)", d, m.calCursor.Format("2006-01-02")), statusOK)
		}
	}
	m.calYear = m.calCursor.Year()
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
		m.form = newParamsForm(m.strategies[m.paletteCursor], m.cfg, m.calCursor)
		m.mode = modeParams
	}
	return m, nil
}

// --- Params form -----------------------------------------------------------

func (m Model) updateParams(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modePalette
		return m, nil
	case "enter":
		return m.applyStrategy()
	}
	cmd := m.form.update(msg)
	return m, cmd
}

func (m Model) applyStrategy() (tea.Model, tea.Cmd) {
	params, err := m.form.buildParams(m.cfg, m.quotas)
	if err != nil {
		m.setStatus(err.Error(), statusErr)
		return m, nil
	}
	tm := pickTimeModel(m.form)
	if err := strategy.Apply(m.plan, m.form.strat, tm, params, m.cfg.LinkDates); err != nil {
		m.setStatus(err.Error(), statusErr)
		return m, nil
	}
	m.mode = modeMain
	m.reportPlan(m.form.strat.Name())
	return m, nil
}

func (m Model) applyQuotas() (tea.Model, tea.Cmd) {
	if len(m.quotas) == 0 {
		m.setStatus("No quotas set — focus the calendar and press 0-9 on days first", statusWarn)
		return m, nil
	}
	start, end := quotaRange(m.quotas, m.loc)
	params := strategy.Params{
		Location:    m.loc,
		Start:       start,
		End:         end,
		WeekdayMask: strategy.AllWeekdays(),
		Quotas:      m.quotas,
		DayStartMin: m.cfg.WorkdayStartMin(),
		DayEndMin:   m.cfg.WorkdayEndMin(),
		Seed:        m.cfg.Seed,
	}
	tm := timeModelByName(m.cfg.DefaultTimeModel)
	if err := strategy.Apply(m.plan, strategy.ManualPin{}, tm, params, m.cfg.LinkDates); err != nil {
		m.setStatus(err.Error(), statusErr)
		return m, nil
	}
	m.reportPlan("Manual Pin")
	return m, nil
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

// quotaRange returns the min and max day among the quota keys, as the strategy's
// eligible range.
func quotaRange(quotas map[string]int, loc *time.Location) (time.Time, time.Time) {
	var keys []string
	for k := range quotas {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	start, _ := time.ParseInLocation("2006-01-02", keys[0], loc)
	end, _ := time.ParseInLocation("2006-01-02", keys[len(keys)-1], loc)
	return start, end
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
	case modeParams:
		overlay = m.form.view(m.st)
	case modeEditor:
		overlay = m.editor.view(m.commitByHash(m.editor.hash), m.st)
	case modePreview:
		overlay = viewPreview(m.plan, m.loc, m.previewOffset, m.previewVisibleRows(), m.st)
	case modeConfirm:
		overlay = m.viewConfirm()
	case modeHelp:
		overlay = m.st.modal.Render(strings.Join(helpLines, "\n"))
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
