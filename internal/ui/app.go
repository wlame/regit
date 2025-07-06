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
	modeEditor
	modePipeline // the stage → preview → confirm → apply overlay (was modePreview)
	modeConfirm
	modeHelp
	modeRetitle
	modeDropBodies
	modeReword
	modeDetail
	modePicker
)

type focusArea int

const (
	focusList focusArea = iota
	focusCalendar
	focusParams    // the strategy/params pane below the calendar
	focusWorkspace // a menu workspace's feature list (e.g. Restructure)
)

type statusKind int

const (
	statusNone statusKind = iota
	statusOK
	statusWarn
	statusErr
)

// strategyState is one strategy's editable state: its param form plus its calendar
// pins (Manual Pin reads quotas, Picked Days reads picked; other strategies leave
// them empty). Holding state per strategy lets the carousel switch between them
// without losing each strategy's inputs.
type strategyState struct {
	form   paramsForm
	quotas map[string]int
	picked map[string]bool
}

// Model is the root Bubble Tea model.
type Model struct {
	repo  *gitio.Repo
	mgr   *safety.Manager
	cfg   config.Config
	loc   *time.Location
	state model.RepoState
	plan  *model.Plan
	// baseBranch is the branch whose merge-base with the current branch defines
	// the frozen cone (auto-detected on load; "" means no automatic freeze). The
	// title bar shows it and the picker can change it.
	baseBranch string

	// workspace is the active category tab; wsMenuCursor is the cursor within a
	// menu workspace (Restructure). The tab bar and zone-3 widget are derived
	// from these plus the workspaces/features tables (feature.go).
	workspace    WorkspaceID
	wsMenuCursor int

	width, height int
	mode          mode
	focus         focusArea

	listCursor int
	listOffset int
	// groupByDate controls whether the commit list inserts per-day date dividers
	// (true, the default) or renders a flat newest-first list (false). Toggled
	// with the 'd' key.
	groupByDate bool
	// sel is the commit-list selection (mode + range/set state). Its zero value
	// is Single mode, so the list renders like a plain cursor list until a
	// selection key (Task 3) or workspace default changes the mode.
	sel selectionState

	calCursor time.Time
	calYear   int
	// quotaTyping is true while consecutive digits on the same day build a
	// two-digit quota; any cursor move or focus change starts a fresh number.
	quotaTyping bool

	strategies    []strategy.Strategy
	strats        []strategyState // parallel to strategies; per-strategy form + pins
	stratIdx      int             // active strategy (carousel position)
	editor        editorState
	retitle       retitleState
	previewOffset int

	// pending is the change staged for preview/apply (modePipeline); pendingGuard
	// is its guard result computed when staged; pipelineApply distinguishes an
	// apply (enter applies) from a read-only peek (enter closes).
	pending       pendingChange
	pendingGuard  safety.Guard
	pipelineApply bool

	confirmInput textinput.Model
	confirmGuard safety.Guard

	rewordSt rewordState

	// Lazily-fetched, hash-keyed commit file stats for the detail panel. A stat
	// is requested via a tea.Cmd when the cursor settles on a commit and cached
	// here so navigation never re-runs git for a commit already seen.
	statCache    map[string]gitio.Stat
	statErr      map[string]string
	statInFlight map[string]bool

	picker pickerState // active branch/base picker overlay (modePicker)

	statusMsg  string
	statusKind statusKind

	st       styles
	quitting bool
}

// activeStrat returns the currently-selected strategy's state.
func (m *Model) activeStrat() *strategyState { return &m.strats[m.stratIdx] }

// unstageActive removes the active strategy's staged author dates from every
// active commit, keeping any manual message edits (subject/body/drop), so the
// calendar falls back to plain tiles when the strategy is not ready.
func (m *Model) unstageActive() {
	for _, c := range m.plan.Active() {
		e, ok := m.plan.EditFor(c.Hash)
		if !ok {
			continue
		}
		e.NewAuthorDate = nil
		e.NewCommitDate = nil
		m.plan.SetEdit(c.Hash, e)
	}
	m.plan.Violations = nil
}

// stageActive previews the active strategy: when it is ready it stages its
// distribution (author-date edits) via the engine; otherwise it unstages.
// Called after every param edit, pin change, and carousel switch, so plan.Edits
// always reflects exactly the active strategy.
func (m *Model) stageActive() {
	a := m.activeStrat()
	pinBased := a.form.strat.Name() == "Manual Pin" || a.form.strat.Name() == "Picked Days"
	ready := a.form.ready()
	if pinBased {
		ready = len(a.quotas) > 0 || len(a.picked) > 0
	}
	if !ready {
		m.unstageActive()
		return
	}
	params, err := a.form.buildParams(m.cfg, a.quotas, a.picked)
	if err != nil {
		a.form.errMsg = err.Error()
		m.unstageActive()
		return
	}
	if err := strategy.Apply(m.plan, a.form.strat, pickTimeModel(a.form), params, m.cfg.LinkDates); err != nil {
		a.form.errMsg = err.Error()
		m.unstageActive()
		return
	}
	a.form.errMsg = ""
}

// strategyStaged reports whether the active strategy has staged edits.
// Used by Task 5 to detect the previewActive signal.
func (m *Model) strategyStaged() bool { return m.plan.HasChanges() }

// New constructs the root model from already-loaded repo data.
func New(repo *gitio.Repo, mgr *safety.Manager, cfg config.Config, state model.RepoState, commits []model.Commit) Model {
	loc := cfg.Location()
	plan := model.NewPlan(commits)

	m := Model{
		repo:         repo,
		mgr:          mgr,
		cfg:          cfg,
		loc:          loc,
		state:        state,
		plan:         plan,
		mode:         modeMain,
		focus:        focusList,
		statCache:    make(map[string]gitio.Stat),
		statErr:      make(map[string]string),
		statInFlight: make(map[string]bool),
		strategies:   strategy.Strategies(),
		st:           newStyles(),
		groupByDate:  true,
	}

	// Auto-detect the base branch and freeze the shared-history cone, then start
	// the cursor on the newest editable commit and seed the calendar there.
	m.applyAutoBase()
	m.sel = newSelection()
	m.applyDefaultSelectionMode()
	m.listCursor = m.firstEditableDisplayIndex()

	seed := time.Now().In(loc)
	if len(commits) > 0 {
		seed = m.commitAtDisplayIndex(m.listCursor).OrigAuthorDate.In(loc)
	}
	m.calCursor = seed
	m.calYear = seed.Year()
	m.stratIdx = m.defaultStrategyIndex()
	m.strats = make([]strategyState, len(m.strategies))
	for i, s := range m.strategies {
		m.strats[i] = strategyState{
			form:   newParamsForm(s, cfg, seed),
			quotas: make(map[string]int),
			picked: make(map[string]bool),
		}
	}

	if m.baseBranch != "" && m.plan.Base > 0 {
		m.setStatus(fmt.Sprintf("Base %s — editing %d commit(s), %d frozen",
			m.baseBranch, len(m.plan.Active()), m.plan.Base), statusOK)
	}
	return m
}

// Init kicks off the first detail-stat fetch so the panel is populated before
// the user moves the cursor.
func (m Model) Init() tea.Cmd { return m.markAndFetch() }

// Update is the central event handler, dispatching by mode.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.syncListViewport()
		return m, nil
	case commitStatMsg:
		delete(m.statInFlight, msg.hash)
		if msg.err != nil {
			m.statErr[msg.hash] = msg.err.Error()
		} else {
			m.statCache[msg.hash] = msg.stat
		}
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
	case modeEditor:
		return m.updateEditor(msg)
	case modePipeline:
		return m.updatePipeline(msg)
	case modeConfirm:
		return m.updateConfirm(msg)
	case modeRetitle:
		return m.updateRetitle(msg)
	case modeDropBodies:
		return m.updateDropBodies(msg)
	case modeReword:
		return m.updateReword(msg)
	case modeDetail:
		if s := msg.String(); s == "." || s == "esc" || s == "q" {
			m.mode = modeMain
		}
		return m, nil
	case modePicker:
		return m.updatePicker(msg)
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
	if m.focus == focusWorkspace {
		return m.updateWorkspaceMenu(msg)
	}
	if m.focus == focusList {
		if model, cmd, ok := m.listSelectionKey(msg); ok {
			return model, cmd
		}
	}
	if m.focus == focusParams {
		return m.updateParamsPane(msg)
	}
	if m.focus == focusList && m.listNavKey(msg.String()) {
		m.syncListViewport()
		return m, m.markAndFetch()
	}
	if model, cmd, ok := m.globalAction(msg); ok {
		return model, cmd
	}
	switch msg.String() {
	case "tab":
		m.quotaTyping = false
		switch m.focus {
		case focusList:
			m.focus = m.focusAfterList()
		case focusCalendar:
			m.focus = focusParams
		}
	case "enter":
		if m.focus == focusList {
			return m.openEditorAtCursor()
		}
	default:
		if m.focus == focusCalendar {
			m.moveCursor(msg)
		}
		m.syncListViewport()
		return m, m.markAndFetch()
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
	case "e":
		model, cmd := m.openEditorAtCursor()
		return model, cmd, true
	case "b":
		if len(m.plan.Commits) > 0 {
			m.setBaseAtCursor()
		}
		return m, nil, true
	case "p":
		model, cmd := m.stagePlanApply(false)
		return model, cmd, true
	case "A":
		model, cmd := m.beginApply()
		return model, cmd, true
	case "U":
		model, cmd := m.doUndo()
		return model, cmd, true
	case "R":
		// Rebuild every strategy slot: fresh forms (dates unset) and empty pins.
		for i := range m.strats {
			m.strats[i] = strategyState{
				form:   newParamsForm(m.strategies[i], m.cfg, m.calCursor),
				quotas: make(map[string]int),
				picked: make(map[string]bool),
			}
		}
		m.plan.Edits = make(map[string]model.Edit)
		m.plan.Violations = nil
		m.quotaTyping = false
		m.setStatus("Reset — strategies and calendar cleared", statusOK)
		return m, nil, true
	case "d":
		m.groupByDate = !m.groupByDate
		m.syncListViewport()
		state := "off"
		if m.groupByDate {
			state = "on"
		}
		m.setStatus("Date grouping: "+state, statusOK)
		return m, nil, true
	case ".":
		m.mode = modeDetail
		return m, m.markAndFetch(), true
	case "B":
		branches, err := m.repo.Branches()
		if err != nil {
			m.setStatus("List branches: "+err.Error(), statusErr)
			return m, nil, true
		}
		m.picker = newPicker("Branches", branches)
		m.mode = modePicker
		return m, nil, true
	case ">", "]":
		m.nextWorkspace()
		return m, nil, true
	case "<", "[":
		m.prevWorkspace()
		return m, nil, true
	}
	return m, nil, false
}

// listSelectionKey handles the commit-list selection keys (Set toggling and
// visual-mode range) when the list is focused. It returns ok=false for keys it
// does not own (movement, enter-to-edit, globals) so they fall through. Movement
// needs no special case: in visual mode the range is derived from the live cursor.
func (m Model) listSelectionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if len(m.plan.Commits) == 0 {
		return m, nil, false
	}
	switch msg.String() {
	case "v":
		if m.sel.mode == SelRange && m.sel.visual {
			m.sel.confirmVisual()
			m.setStatus("Range fixed", statusOK)
		} else {
			m.sel.beginVisual(m.listCursor)
			m.setStatus("Visual range — move to extend, v/enter to fix, esc to cancel", statusNone)
		}
		return m, nil, true
	case " ":
		if m.plan.IsFrozen(m.storageIndexAtCursor()) {
			m.setStatus("Frozen commit — not selectable", statusWarn)
			return m, nil, true
		}
		m.sel.toggle(m.commitAtCursor().Hash, false)
		m.setStatus(fmt.Sprintf("%d commit(s) selected", len(m.sel.set)), statusOK)
		return m, nil, true
	case "a":
		var hashes []string
		for _, c := range m.plan.Active() {
			hashes = append(hashes, c.Hash)
		}
		m.sel.selectAll(hashes)
		m.setStatus(fmt.Sprintf("Selected all %d active commit(s)", len(hashes)), statusOK)
		return m, nil, true
	case "n":
		m.sel.clear()
		m.setStatus("Selection cleared", statusOK)
		return m, nil, true
	case "enter":
		if m.sel.mode == SelRange && m.sel.visual {
			m.sel.confirmVisual()
			m.setStatus("Range fixed", statusOK)
			return m, nil, true
		}
		return m, nil, false // not visual: fall through to open the editor
	case "esc":
		if m.sel.mode == SelSet || m.sel.mode == SelRange {
			m.sel.reset()
			m.applyDefaultSelectionMode()
			m.setStatus("Selection reset", statusNone)
			return m, nil, true
		}
		return m, nil, false
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

// editorFieldWidth sizes the editor's title/body fields. The math lives in
// computeLayout (layout.go) so every region shares one source of truth; the
// field stays within a readable [editorMinWidth, editorMaxWidth] band.
func (m Model) editorFieldWidth() int {
	return computeLayout(m.width, m.height).editorWidth
}

// listNavKey handles fast list-navigation keys when the commit list is focused.
// It returns true when it consumed the key. Routing this BEFORE globalAction
// lets g/G mean top/bottom of the list (spec binding); the calendar and params
// panes see g as a no-op (staging is automatic, so there is no re-apply key).
func (m *Model) listNavKey(key string) bool {
	last := len(m.plan.Commits) - 1
	if last < 0 {
		return false
	}
	page := max(1, m.listHeight()-1)
	switch key {
	case "up", "k":
		m.moveListCursor(-1)
	case "down", "j":
		m.moveListCursor(1)
	case "ctrl+u":
		m.moveListCursor(-page / 2)
	case "ctrl+d":
		m.moveListCursor(page / 2)
	case "pgup":
		m.moveListCursor(-page)
	case "pgdown":
		m.moveListCursor(page)
	case "g", "home":
		m.listCursor = 0
	case "G", "end":
		m.listCursor = last
	case "{":
		m.dayJump(-1)
	case "}":
		m.dayJump(1)
	default:
		return false
	}
	return true
}

// moveListCursor shifts the cursor by delta commits, clamped to the list bounds.
func (m *Model) moveListCursor(delta int) {
	m.listCursor += delta
	if m.listCursor < 0 {
		m.listCursor = 0
	}
	if last := len(m.plan.Commits) - 1; m.listCursor > last {
		m.listCursor = last
	}
}

// dayJump moves the cursor to the first commit of the next (dir=+1, older) or
// previous (dir=-1, newer) author-date day group, clamping at the list ends.
func (m *Model) dayJump(dir int) {
	n := len(m.plan.Commits)
	curKey := m.plan.EffectiveAuthorDate(m.commitAtDisplayIndex(m.listCursor)).In(m.loc).Format(dateLayout)
	for i := m.listCursor + dir; i >= 0 && i < n; i += dir {
		if k := m.plan.EffectiveAuthorDate(m.commitAtDisplayIndex(i)).In(m.loc).Format(dateLayout); k != curKey {
			m.listCursor = i // first commit of the neighbouring day group
			return
		}
	}
	// No further group in that direction: clamp to the corresponding end.
	if dir > 0 {
		m.listCursor = n - 1
	} else {
		m.listCursor = 0
	}
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
	case " ":
		// Toggle a picked day — only when the Picked Days strategy is active.
		// Other strategies ignore space so the key can be used elsewhere later.
		if m.activeStrat().form.strat.Name() == "Picked Days" {
			key := m.calCursor.Format(dateLayout)
			if m.activeStrat().picked[key] {
				delete(m.activeStrat().picked, key)
			} else {
				m.activeStrat().picked[key] = true
			}
			m.quotaTyping = false
			m.stageActive()
		}
	default:
		if d := digit(msg.String()); d >= 0 {
			// Pin a quota — only when the Manual Pin strategy is active.
			// Digits typed under any other strategy are silently ignored so they
			// do not accidentally overwrite another strategy's quota map.
			if m.activeStrat().form.strat.Name() == "Manual Pin" {
				key := m.calCursor.Format(dateLayout)
				// Consecutive digits on the same day build a two-digit quota:
				// 2 → 24 → 45 (the last two digits typed win).
				if m.quotaTyping {
					d = (m.activeStrat().quotas[key]%10)*10 + d
				}
				m.activeStrat().quotas[key] = d
				m.quotaTyping = true
				// Stage the Manual Pin strategy live so the commit list
				// reflects the pin as soon as the digit is pressed.
				m.stageActive()
			}
		}
	}
	m.calYear = m.calCursor.Year()
	if moved {
		m.quotaTyping = false // a new day starts a new number
		m.selectFirstCommitOfDay()
	}
}

// --- Bulk message operations -------------------------------------------------

// dropAllBodies marks every commit in the affected scope (the selection, else the
// whole editable range) that still has a body with a DropBody edit, leaving
// title-only messages.
func (m *Model) dropAllBodies() {
	dropped := 0
	for _, c := range m.affectedCommits() {
		e, _ := m.plan.EditFor(c.Hash)
		if !c.HasBody() || e.DropBody {
			continue
		}
		e.DropBody = true
		m.plan.SetEdit(c.Hash, e)
		dropped++
	}
	if dropped == 0 {
		m.setStatus("No bodies to drop in the affected range", statusWarn)
		return
	}
	m.setStatus(fmt.Sprintf("Dropped the body of %d commit(s) — titles kept", dropped), statusOK)
}

// updateDropBodies drives the drop-bodies confirmation popup: enter applies the
// drop across the affected scope, esc/q cancel. The popup has no fields, so only
// those keys matter.
func (m Model) updateDropBodies(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeMain
		return m, nil
	case "enter":
		m.dropAllBodies()
		m.mode = modeMain
		return m, nil
	}
	return m, nil
}

// dropBodiesCount reports how many commits in the affected scope still carry a
// non-empty body that a drop would remove (already-dropped commits do not count).
func (m Model) dropBodiesCount() int {
	n := 0
	for _, c := range m.affectedCommits() {
		e, _ := m.plan.EditFor(c.Hash)
		if c.HasBody() && !e.DropBody {
			n++
		}
	}
	return n
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
	for _, c := range m.affectedCommits() {
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

// affectedCommits returns the commits a bulk action or the action-panel count
// applies to, under the "selection wins, else all-active" rule: the explicit
// Set/Range selection when present (filtered to the editable range), otherwise the
// whole active range. Returned root-first (oldest → newest) to match plan.Active.
func (m Model) affectedCommits() []model.Commit {
	switch {
	case m.sel.mode == SelSet && len(m.sel.set) > 0:
		return m.commitsInSet()
	case m.sel.mode == SelRange:
		return m.commitsInRange()
	default:
		return m.plan.Active()
	}
}

// commitsInSet returns the selected (Set-mode) commits that are still editable,
// in root-first order.
func (m Model) commitsInSet() []model.Commit {
	var out []model.Commit
	for _, c := range m.plan.Active() {
		if m.sel.isSet(c.Hash) {
			out = append(out, c)
		}
	}
	return out
}

// commitsInRange returns the visual-range commits that are editable, in root-first
// order. The range is expressed in display indices (newest-first), so it walks the
// span and reverses to root-first.
func (m Model) commitsInRange() []model.Commit {
	lo, hi := m.sel.rangeBounds(m.listCursor)
	var out []model.Commit
	for d := lo; d <= hi; d++ {
		storage := len(m.plan.Commits) - 1 - d
		if storage < 0 || storage >= len(m.plan.Commits) || m.plan.IsFrozen(storage) {
			continue
		}
		out = append(out, m.plan.Commits[storage])
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// focusAfterList returns the zone the focus moves to when tab is pressed on the
// commit list. It depends on the active workspace: the Dates calendar, the
// Restructure feature menu, or (for a stub workspace with no focusable widget)
// the list itself, a no-op.
func (m Model) focusAfterList() focusArea {
	switch m.workspace {
	case WSDates:
		return focusCalendar
	case WSRestructure:
		if len(featuresFor(WSRestructure)) > 0 {
			return focusWorkspace
		}
	}
	return focusList
}

// applyDefaultSelectionMode sets the list's selection mode from the active
// workspace: Dates operates on the whole active range (Active), other workspaces
// default to a single-commit cursor (Single). Called on load, reload, and every
// workspace switch so the mode always matches the visible context.
func (m *Model) applyDefaultSelectionMode() {
	if m.workspace == WSDates {
		m.sel.mode = SelActive
	} else {
		m.sel.mode = SelSingle
	}
}

// applyAutoBase detects the base branch (spec §6.4 precedence) and freezes the
// commits up to and including the merge-base. It is best-effort: any git failure
// leaves the freeze unchanged rather than erroring. Called on load and after
// every reload, so the freeze re-establishes itself (and survives apply/undo).
//
// baseBranch and plan.Base are always set together: baseBranch is non-empty ONLY
// when a freeze was actually computed, so the title bar can never show a base
// branch whose cone is not frozen. A Branches() failure leaves both unchanged.
func (m *Model) applyAutoBase() {
	// Req #6: on an in-sync trunk branch, start with everything editable. The
	// merge-base would otherwise equal HEAD and freeze all but the newest commit.
	if startsUnlocked(m.state) {
		m.baseBranch = ""
		m.plan.SetBase(0)
		return
	}
	branches, err := m.repo.Branches()
	if err != nil {
		return // can't determine; leave baseBranch and plan.Base unchanged together
	}
	base := chooseBaseBranch(m.state, branches)
	if base == "" {
		m.baseBranch = ""
		m.plan.SetBase(0)
		return
	}
	mergeBase, err := m.repo.MergeBase(m.state.Branch, base)
	if err != nil {
		m.baseBranch = ""
		m.plan.SetBase(0)
		return
	}
	idx := baseIndexForMergeBase(m.plan.Commits, mergeBase)
	if idx == 0 {
		// The merge-base is off the first-parent line (e.g. the base branch was
		// merged in), so there is no honest cone to freeze on this linear
		// history. Claim no base rather than show a label without a freeze.
		m.baseBranch = ""
		m.plan.SetBase(0)
		return
	}
	m.baseBranch = base
	m.plan.SetBase(idx)
}

// firstEditableDisplayIndex returns the display-order index (newest = 0) where
// the cursor should start: the newest commit when nothing is frozen, otherwise
// the newest editable commit just below the frozen cone.
func (m Model) firstEditableDisplayIndex() int {
	n := len(m.plan.Commits)
	if n == 0 || m.plan.Base == 0 {
		return 0
	}
	idx := n - 1 - m.plan.Base // storage Base -> display index
	if idx < 0 {
		idx = 0
	}
	if idx > n-1 {
		idx = n - 1
	}
	return idx
}

// setBaseAtCursor toggles the frozen-history boundary at the selected commit:
// it becomes the first (oldest) editable commit and everything older is
// frozen. Pressing b on the current base commit unfreezes the whole history.
//
// A manual freeze is a cursor position, not a branch, so baseBranch is cleared
// on both paths: the title then reads "base: none" to reflect that the boundary
// no longer tracks any branch.
func (m *Model) setBaseAtCursor() {
	idx := m.storageIndexAtCursor()
	if idx == m.plan.Base && m.plan.Base > 0 {
		m.baseBranch = ""
		m.plan.SetBase(0)
		m.setStatus("Base cleared — editing the whole history", statusOK)
		return
	}
	m.baseBranch = ""
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

// cycleStrategy moves the carousel by delta (wrapping), unstages the old
// strategy's edits, then re-stages the new one if it is ready.
func (m *Model) cycleStrategy(delta int) {
	n := len(m.strategies)
	m.stratIdx = (m.stratIdx + delta + n) % n
	m.activeStrat().form.focusIdx = 0
	m.stageActive()
}

// --- Params pane -------------------------------------------------------------

// updateParamsPane routes keys while the strategy pane is focused: up/down
// move between fields, left/right cycle the strategy carousel (only on the
// carousel row), enter applies, tab/esc return focus to the list.
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
		m.activeStrat().form.moveFocus(-1)
		return m, nil
	case "down":
		m.activeStrat().form.moveFocus(1)
		return m, nil
	case "left":
		if m.activeStrat().form.onStrategy() {
			m.cycleStrategy(-1)
			return m, nil
		}
	case "right":
		if m.activeStrat().form.onStrategy() {
			m.cycleStrategy(1)
			return m, nil
		}
	}
	if model, cmd, ok := m.globalAction(msg); ok {
		return model, cmd
	}
	m.activeStrat().form.errMsg = "" // any edit invalidates the previous apply error
	cmd := m.activeStrat().form.update(msg)
	// Re-evaluate whether the strategy is ready after every keystroke, staging
	// live when it becomes ready and unstaging when it becomes incomplete again.
	m.stageActive()
	return m, cmd
}

func (m Model) applyStrategy() (tea.Model, tea.Cmd) {
	// Re-stage with the current form values (live staging may already have done
	// this, but enter explicitly re-evaluates so the user gets immediate feedback
	// if the form is incomplete), then hand focus back to the list.
	m.stageActive()
	if m.activeStrat().form.errMsg != "" {
		// Stage failed: stay in the params pane so the user can see the error.
		return m, nil
	}
	a := m.activeStrat()
	pinBased := a.form.strat.Name() == "Manual Pin" || a.form.strat.Name() == "Picked Days"
	ready := a.form.ready()
	if pinBased {
		ready = len(a.quotas) > 0 || len(a.picked) > 0
	}
	if !ready {
		// Nothing staged yet: report a helpful message and stay in the pane.
		if pinBased {
			a.form.errMsg = "Pin at least one day on the calendar first (0–9 for quotas, space for picked days)"
		} else {
			a.form.errMsg = "Fill the date fields before applying"
		}
		return m, nil
	}
	// Success: hand focus back to the list so global keys (A, p, u…) work
	// again immediately; tab twice returns here for further tweaking.
	m.focus = focusList
	m.reportPlan(a.form.strat.Name())
	return m, nil
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

// --- Pipeline overlay ------------------------------------------------------

// updatePipeline drives the preview overlay (modePipeline). Scrolling works in
// both peek and apply mode; enter applies only in apply mode (routing through the
// typed confirm first when the change rewrites pushed commits); esc/q close.
func (m Model) updatePipeline(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeMain
		m.pending = nil
		return m, nil
	case "up", "k":
		if m.previewOffset > 0 {
			m.previewOffset--
		}
		return m, nil
	case "down", "j":
		if m.previewOffset < m.pendingPreviewLen()-1 {
			m.previewOffset++
		}
		return m, nil
	case "enter":
		if !m.pipelineApply {
			m.mode = modeMain
			m.pending = nil
			return m, nil
		}
		if m.pendingGuard.NeedsConfirmation {
			ti := textinput.New()
			ti.Prompt = "› "
			ti.Placeholder = m.pendingGuard.ConfirmWord
			ti.Focus()
			m.confirmInput = ti
			m.confirmGuard = m.pendingGuard
			m.mode = modeConfirm
			return m, nil
		}
		return m.commitPending(false)
	}
	return m, nil
}

// pendingPreviewLen is the number of body lines in the staged change's preview,
// used to clamp the scroll offset.
func (m Model) pendingPreviewLen() int {
	if m.pending == nil {
		return 0
	}
	return len(m.pending.preview(m.st).lines)
}

// commitPending applies the staged pendingChange through the safety manager,
// reloads history, and reports the backup ref. It is the single apply funnel for
// every mutation (plan edits and split).
func (m Model) commitPending(confirmed bool) (tea.Model, tea.Cmd) {
	if m.pending == nil {
		m.mode = modeMain
		return m, nil
	}
	opts := safety.ApplyOptions{
		Sign:          gitio.DropSignatures,
		UnixTimestamp: time.Now().Unix(),
		Confirmed:     confirmed,
	}
	res, err := m.pending.apply(m.mgr, m.state, opts)
	if err != nil {
		m.mode = modeMain
		m.pending = nil
		m.setStatus(err.Error(), statusErr)
		return m, nil
	}
	title := m.pending.title()
	m.pending = nil
	if err := m.reload(); err != nil {
		m.mode = modeMain
		m.setStatus("Applied, but reload failed: "+err.Error(), statusWarn)
		return m, nil
	}
	m.mode = modeMain
	m.setStatus(fmt.Sprintf("%s ✓ backup at %s — press u to undo", title, short(res.BackupRef)), statusOK)
	return m, m.markAndFetch()
}

// --- Apply / Confirm -------------------------------------------------------

// beginApply (A) stages the plan's edits for an explicit preview + apply.
func (m Model) beginApply() (tea.Model, tea.Cmd) {
	return m.stagePlanApply(true)
}

// stagePlanApply builds a planChange, runs its guard, and opens the pipeline
// overlay. applyMode true means enter will apply (after the typed confirm when the
// commits are already pushed); false is a read-only peek. A hard guard block (e.g.
// a dirty tree) reports the reason and does not open the overlay.
func (m Model) stagePlanApply(applyMode bool) (tea.Model, tea.Cmd) {
	if !m.plan.HasChanges() {
		if applyMode {
			m.setStatus("Nothing to apply — no commits edited yet", statusWarn)
		} else {
			m.setStatus("No edits staged to preview", statusWarn)
		}
		return m, nil
	}
	pc := newPlanChange(m.plan, m.loc)
	guard := pc.guard(m.state)
	if applyMode && guard.Blocked {
		m.setStatus(guard.BlockReason, statusErr)
		return m, nil
	}
	m.pending = pc
	m.pendingGuard = guard
	m.pipelineApply = applyMode
	m.previewOffset = 0
	m.mode = modePipeline
	return m, nil
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
		return m.commitPending(true)
	}
	var cmd tea.Cmd
	m.confirmInput, cmd = m.confirmInput.Update(msg)
	return m, cmd
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
	return m, m.markAndFetch()
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
	m.statCache = make(map[string]gitio.Stat)
	m.statErr = make(map[string]string)
	m.statInFlight = make(map[string]bool)
	if m.listCursor >= len(commits) {
		m.listCursor = max(0, len(commits)-1)
	}
	m.applyAutoBase()
	m.sel.reset()
	m.applyDefaultSelectionMode()
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

// buildSplitChange turns a reviewed split proposal into a stageable splitChange:
// it builds the sub-commits and derives the tail from the proposal's commit hash
// (not the cursor, which the user may have moved). It returns ok=false with a
// status set when the commit is no longer in the plan.
func (m Model) buildSplitChange(c model.Commit, st splitState) (splitChange, bool) {
	prop := st.proposal
	// Locate the split commit in the plan first so we only build sub-commits for a
	// commit that is still present (and so the not-found path does no work).
	idx := -1
	for i, commit := range m.plan.Commits {
		if commit.Hash == prop.CommitHash {
			idx = i
			break
		}
	}
	if idx < 0 {
		return splitChange{}, false
	}
	return splitChange{
		commit:     c,
		parentHash: prop.ParentHash,
		subs:       split.BuildSubCommits(c, prop.Groups, st.trees, m.cfg.Split.WIPPrefix),
		tail:       m.plan.Commits[idx+1:],
		groups:     prop.Groups,
		commitHash: prop.CommitHash,
	}, true
}

// beginSplit is called when the user presses [S]. It runs Engine.Propose on the
// selected commit (blocking for v1) and stages the result as a splitChange in the
// shared modePipeline overlay so the user can review the groups, then apply or cancel.
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
	sc, ok := m.buildSplitChange(c, st)
	if !ok {
		m.setStatus("Split commit not found in plan — history may have changed", statusErr)
		return m, nil, true
	}
	guard := sc.guard(m.state)
	if guard.Blocked {
		m.setStatus(guard.BlockReason, statusErr)
		return m, nil, true
	}
	m.pending = sc
	m.pendingGuard = guard
	m.pipelineApply = true
	m.previewOffset = 0
	m.mode = modePipeline
	return m, nil, true
}

// --- Picker ----------------------------------------------------------------

// updatePicker drives the branch/base picker overlay. Letter keys type into the
// filter, so actions use non-letter keys: enter switches to the highlighted
// branch, tab sets it as the base (Task 6), arrows move, esc cancels.
func (m Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeMain
		return m, nil
	case "up":
		m.picker.errMsg = ""
		m.picker.moveCursor(-1)
		return m, nil
	case "down":
		m.picker.errMsg = ""
		m.picker.moveCursor(1)
		return m, nil
	case "enter":
		return m.pickerSwitchBranch()
	case "tab":
		return m.pickerSetBase()
	}
	m.picker.errMsg = "" // typing in the filter clears a stale error
	cmd := m.picker.update(msg)
	return m, cmd
}

// pickerSwitchBranch performs a real `git switch` to the highlighted branch,
// then reloads. It is refused unless the working tree is clean AND there are no
// unsaved pending edits — regit's safety model only ever edits the checked-out
// branch, so we keep HEAD authoritative.
func (m Model) pickerSwitchBranch() (tea.Model, tea.Cmd) {
	name, ok := m.picker.selected()
	if !ok {
		return m, nil
	}
	if name == m.state.Branch {
		m.picker.errMsg = "Already on " + name
		return m, nil
	}
	if m.state.IsDirty {
		m.picker.errMsg = "Working tree has uncommitted changes — commit or stash before switching"
		return m, nil
	}
	if m.plan.HasChanges() {
		m.picker.errMsg = "Unsaved edits — apply (A) or reset before switching branch"
		return m, nil
	}
	if err := m.repo.SwitchBranch(name); err != nil {
		m.picker.errMsg = err.Error()
		return m, nil
	}
	if err := m.reload(); err != nil {
		m.mode = modeMain
		m.setStatus("Switched, but reload failed: "+err.Error(), statusWarn)
		return m, nil
	}
	m.listCursor = m.firstEditableDisplayIndex()
	m.mode = modeMain
	m.setStatus(fmt.Sprintf("Switched to %s — %d commit(s)", name, len(m.plan.Commits)), statusOK)
	return m, m.markAndFetch()
}

// pickerSetBase sets the highlighted branch as the base: it recomputes the
// merge-base with the current branch and freezes everything up to it. Unlike a
// switch, this does not touch HEAD or reload — it only moves the freeze boundary
// on the current branch.
func (m Model) pickerSetBase() (tea.Model, tea.Cmd) {
	name, ok := m.picker.selected()
	if !ok {
		return m, nil
	}
	mergeBase, err := m.repo.MergeBase(m.state.Branch, name)
	if err != nil {
		m.picker.errMsg = "No common history with " + name + ": " + err.Error()
		return m, nil
	}
	idx := baseIndexForMergeBase(m.plan.Commits, mergeBase)
	if idx == 0 {
		m.picker.errMsg = name + " shares no first-parent history to freeze here"
		return m, nil
	}
	m.baseBranch = name
	m.plan.SetBase(idx)
	m.mode = modeMain
	m.setStatus(fmt.Sprintf("Base %s — editing %d commit(s), %d frozen",
		name, len(m.plan.Active()), m.plan.Base), statusOK)
	return m, nil
}

// commitStatMsg carries an async CommitStat result back onto the Bubble Tea bus.
type commitStatMsg struct {
	hash string
	stat gitio.Stat
	err  error
}

// fetchStatCmd asks gitio for a commit's file stats off the UI goroutine.
func fetchStatCmd(repo *gitio.Repo, hash string) tea.Cmd {
	return func() tea.Msg {
		stat, err := repo.CommitStat(hash)
		return commitStatMsg{hash: hash, stat: stat, err: err}
	}
}

// detailFetchCmd returns a command to fetch the cursor commit's stats, or nil
// when there is nothing to fetch (no commits, or the stat is already cached,
// already failed, or already in flight). When a non-nil command is returned the
// caller must also set statInFlight[hash] = true so duplicate fetches are
// suppressed; see markAndFetch below.
func (m Model) detailFetchCmd() tea.Cmd {
	if len(m.plan.Commits) == 0 {
		return nil
	}
	hash := m.commitAtCursor().Hash
	if _, ok := m.statCache[hash]; ok {
		return nil
	}
	if _, failed := m.statErr[hash]; failed {
		return nil
	}
	if m.statInFlight[hash] {
		return nil
	}
	return fetchStatCmd(m.repo, hash)
}

// markAndFetch is the normal call site used inside Update: it marks the cursor
// hash as in-flight (suppressing duplicate requests) and returns the fetch
// command to Bubble Tea. Because statInFlight is a map (a reference type), the
// mutation persists on the returned model even though Update uses value receivers.
// Do not use detailFetchCmd() directly from Update — it is kept side-effect-free
// so tests can call it without contaminating the in-flight set.
func (m Model) markAndFetch() tea.Cmd {
	cmd := m.detailFetchCmd()
	if cmd != nil && len(m.plan.Commits) > 0 {
		m.statInFlight[m.commitAtCursor().Hash] = true
	}
	return cmd
}

// View renders the current screen.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	main := m.viewMain()

	var overlay string
	switch m.mode {
	case modeEditor:
		overlay = m.editor.view(m.commitByHash(m.editor.hash), m.st)
	case modePipeline:
		overlay = viewPipeline(m.pending.preview(m.st), m.previewOffset, m.previewVisibleRows(), m.pipelineApply, m.st)
	case modeConfirm:
		overlay = m.viewConfirm()
	case modeHelp:
		overlay = m.st.modal.Render(helpText())
	case modeRetitle:
		overlay = m.retitle.view(m.affectedCommits(), m.plan, m.st)
	case modeDropBodies:
		overlay = m.viewDropBodies()
	case modeReword:
		// Show the buffered result if available; otherwise a generating indicator.
		if result, ok := m.rewordSt.buffer[m.rewordSt.commit.Hash]; ok {
			overlay = m.st.modal.Render(renderRewordPicker(result, m.rewordSt.sel))
		} else {
			overlay = m.st.modal.Render(renderRewordPicker(reword.Result{}, m.rewordSt.sel))
		}
	case modeDetail:
		c := m.commitAtCursor()
		stat, have := m.statCache[c.Hash]
		overlay = m.st.modal.Render(renderDetail(c, stat, have, m.statErr[c.Hash], m.plan, m.loc, 50, max(8, m.height-6), m.st))
	case modePicker:
		overlay = viewPicker(m.picker, "enter switch · tab set base · ↑/↓ move · esc cancel", m.st)
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
