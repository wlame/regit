// Package ui implements regit's Bubble Tea terminal interface: a three-pane main
// screen (commit list, contribution calendar, status) plus modal overlays for the
// strategy palette, the per-commit editor, the preview, and the apply
// confirmation. It depends on the engine (strategy), git layer (gitio), safety
// manager, and config; it holds no rewrite logic of its own.
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/capability"
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
	modeSquash   // the tabbed squash wizard (one tab per chain)
	modeReauthor // the change-author wizard (single modal)
	modeSplitGen // an async split Propose call is in flight (spinner overlay)
	modePurge    // the purge finder wizard (async history scan + multi-select)
)

type focusArea int

const (
	focusList focusArea = iota
	focusCalendar
	focusParams // the strategy/params pane below the calendar
	focusRedact // the redact flow's wizard owns every key (redact.go)
)

type statusKind int

const (
	statusNone statusKind = iota
	statusOK
	statusWarn
	statusErr
)

// minTermWidth / minTermHeight are the smallest terminal the TUI renders in.
// Below this even the compact list-only layout cannot fit a usable frame, so
// View() shows a guard screen instead of a garbled one.
const (
	minTermWidth  = 60
	minTermHeight = 16
)

// strategyState is one strategy's editable state: its param form plus its calendar
// pins (Manual Pin reads quotas, Picked Days reads picked; other strategies leave
// them empty). Holding state per strategy lets the carousel switch between them
// without losing each strategy's inputs.
type strategyState struct {
	form   paramsForm
	quotas map[string]int
	picked map[string]bool
	// pickSeq/quotaSeq remember WHEN each day was picked/pinned (monotonic
	// counter, not wall time): the per-segment capacity rule evicts the most
	// RECENT choice, so recency must survive re-renders and re-staging.
	pickSeq  map[string]int
	quotaSeq map[string]int
	nextSeq  int
}

// resetPins returns a strategy slot's calendar state to empty: no picked days,
// no quotas, and a rewound recency counter. Shared by the R global reset and
// reload() so the two field lists can never drift apart.
func (s *strategyState) resetPins() {
	s.quotas = make(map[string]int)
	s.picked = make(map[string]bool)
	s.pickSeq = make(map[string]int)
	s.quotaSeq = make(map[string]int)
	s.nextSeq = 0
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
	// contribs is the ranked, color-assigned set of distinct authors in the
	// current history; contribIdx maps a contributorKey to its index in contribs.
	// Both are rebuilt by rebuildContributors on load/reload and read by the
	// colored-username render sites and the AUTHORS panel.
	contribs   []contributor
	contribIdx map[string]int

	// flow is the flow zone's occupant: flowNone shows the commit detail, or a
	// launched feature's interactive widget for the duration of the flow (see
	// flow.go) — the replacement for the old category-tab chassis.
	flow flowID

	// panel is the bottom action-panel state (actionpanel.go). Opening or
	// closing it never touches the commit selection — spec §2 invariant.
	panel panelState

	width, height int
	mode          mode
	focus         focusArea

	listCursor int
	listOffset int
	// groupByDate controls whether the commit list inserts per-day date dividers
	// (true, the default) or renders a flat newest-first list (false). Toggled
	// with the 'd' key.
	groupByDate bool
	// showAuthor controls whether each commit row shows an author column (the
	// author name, or the email's username when the name is blank). Off by
	// default; toggled with the 'a' key.
	showAuthor bool
	// sel is the commit-list selection (mode + range/set state). Its zero value
	// is Single mode, so the list renders like a plain cursor list until a
	// selection key, or the redate flow's default Active scope (enterRedateFlow,
	// flow.go), changes the mode.
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
	// helpOffset is the scroll offset (in lines) into helpText() for the modeHelp
	// overlay; reset to 0 whenever help is (re)opened.
	helpOffset int

	// pending is the change staged for preview/apply (modePipeline); pendingGuard
	// is its guard result computed when staged; pipelineApply distinguishes an
	// apply (enter applies) from a read-only peek (enter closes). pipelineOrigin
	// is the mode to return to on esc/cancel — modeMain for a plan apply staged
	// from the main view, or the wizard's mode (modeSquash, modeReauthor) when the
	// pipeline was staged from a wizard whose typed edits (squashSt/reauthorSt)
	// must survive the round trip. Its zero value is modeMain (the iota zero).
	pending        pendingChange
	pendingGuard   safety.Guard
	pipelineApply  bool
	pipelineOrigin mode

	confirmInput textinput.Model
	confirmGuard safety.Guard

	rewordSt   rewordState
	squashSt   squashState
	reauthorSt reauthorState
	purgeSt    purgeState
	redactSt   redactState
	// redactScanSeq is a per-invocation generation token for the redact flow's
	// async history scan, bumped by beginRedactScan each time a scan actually
	// launches. Unlike purgeSt.scanSeq (which lives inside purgeState because
	// purge's scan starts the instant the wizard opens), this is a Model-level
	// field: redact's scan does not start until step 1 is submitted, so the
	// token must survive a fresh redactState being built at flow-open time
	// (see enterRedactFlow) without losing track of a still-in-flight scan
	// from an abandoned earlier session.
	redactScanSeq int

	// spinner is the single shared throbber rendered by every async-generating
	// overlay (split proposal, reword variants, squash summaries) so there is one
	// ticking animation instead of one per feature.
	spinner spinner.Model
	// splitGenHash is the hash of the commit a split is being proposed for
	// (modeSplitGen). Used for the overlay label and the commit lookup once a
	// result is accepted — no longer for the stale-result guard itself, since a
	// second split on the same commit (abort, then immediately restart) shares
	// this hash with the abandoned first attempt.
	splitGenHash string
	// splitGenSeq is a per-invocation generation token, incremented by beginSplit
	// before every dispatch. A splitResultMsg is only accepted if its seq still
	// matches, so a result from an abandoned generation — even one sharing the
	// same commit hash as the current generation — is dropped instead of being
	// silently accepted over the real in-flight request.
	splitGenSeq int

	// Lazily-fetched, hash-keyed commit file stats for the detail panel. A stat
	// is requested via a tea.Cmd when the cursor settles on a commit and cached
	// here so navigation never re-runs git for a commit already seen.
	statCache    map[string]gitio.Stat
	statErr      map[string]string
	statInFlight map[string]bool

	picker pickerState // active branch/base picker overlay (modePicker)

	statusMsg  string
	statusKind statusKind

	// commandLog holds the CLI-equivalent commands for applied actions, shown in
	// the transparency footer so the user can copy them for headless use.
	commandLog []string
	// logVisible controls whether the command-log footer panel is rendered.
	// Toggled with 'L'.
	logVisible bool
	// redateCmd is the active strategy's rendered CLI-equivalent command. It is
	// OVERWRITTEN (never appended) by stageActive, which runs on every params
	// keystroke/pin/carousel switch — a live re-staging site, not a one-shot
	// commit — so appending here would fill the log with duplicate and
	// stale-parameter redate lines. Cleared by unstageActive and a plan reset.
	redateCmd string
	// redateSeed is the RNG seed the redate flow uses when the config sets
	// none (seed 0). The headless verb treats --seed=0 as "pick one from the
	// clock", so a fixed nonzero seed per session is what lets the logged
	// --seed replay the exact dates the TUI previewed.
	redateSeed int64
	// planCmds holds the CLI-equivalent line(s) for the other accumulated plan
	// edits — drop-bodies, retitle, reword, and the per-commit editor — each of
	// which is a one-shot commit to the plan (not re-staged live), so appending
	// is correct here: every distinct staged edit must survive to the log.
	planCmds []string

	st       styles
	quitting bool
}

// activeStrat returns the currently-selected strategy's state.
func (m *Model) activeStrat() *strategyState { return &m.strats[m.stratIdx] }

// unstageActive removes staged author dates from every active commit, keeping any
// manual message edits (subject/body/drop), so the calendar falls back to plain tiles
// when the strategy is not ready. By design it clears the author-date dimension across
// the whole active range: in the redate flow the strategy owns dates, so an author
// date typed in the per-commit editor is subsumed here too — only message edits survive.
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
	m.plan.Shifted = nil
	m.redateCmd = ""
}

// stageActive previews the active strategy: when it is ready it stages its
// distribution (author-date edits) via the engine; otherwise it unstages.
// Called after every param edit, pin change, and carousel switch, so plan.Edits
// always reflects exactly the active strategy.
// activeReady reports whether the active strategy has enough input to stage: a valid
// date range, or — for the pin-based strategies Manual Pin / Picked Days — at least
// one calendar pin. Shared by stageActive and applyStrategy so the two can never
// disagree about readiness.
func (m *Model) activeReady() bool {
	a := m.activeStrat()
	if a.form.strat.Name() == "Manual Pin" || a.form.strat.Name() == "Picked Days" {
		return len(a.quotas) > 0 || len(a.picked) > 0
	}
	return a.form.ready()
}

func (m *Model) stageActive() {
	a := m.activeStrat()
	if !m.activeReady() {
		m.unstageActive()
		return
	}
	params, err := a.form.buildParams(m.cfg, a.quotas, a.picked)
	if params.Seed == 0 {
		params.Seed = m.redateSeed
	}
	if err != nil {
		a.form.errMsg = err.Error()
		m.unstageActive()
		return
	}
	// Fill PinOwner before Apply runs: a pin/pick sitting on a boundary shared by
	// two segments must be consumed by exactly one of them, and Apply itself has
	// no notion of "which segment picked this day first" — that recency-ordered
	// bookkeeping only exists in the strategyState maps below.
	m.pinOwnerFor(a, &params)
	// Clear any previously staged dates first: with selection-scoped staging
	// the target set can shrink between calls, and a stale edit on a commit
	// that just became an anchor would corrupt the anchor's effective date.
	m.unstageActive()
	warnings, err := strategy.Apply(m.plan, m.affectedCommits(), a.form.strat,
		pickTimeModel(a.form), params, m.cfg.LinkDates, time.Now())
	if err != nil {
		a.form.errMsg = err.Error()
		m.unstageActive()
		return
	}
	if !m.strategyStaged() && len(warnings) > 0 {
		// Nothing landed anywhere: treat the first warning as the form error so
		// the user sees WHY the calendar shows no preview.
		a.form.errMsg = warnings[0]
		m.unstageActive()
		return
	}
	if len(warnings) > 0 {
		m.setStatus(warnings[0], statusWarn)
	}
	a.form.errMsg = ""
	// Overwrite, not append: stageActive fires on every keystroke/pin/carousel
	// switch, so the slot always holds exactly the current form's rendered
	// command — the old single-slot self-deduplication that appending broke.
	m.redateCmd = capability.Render("redate", capability.RedateArgMap(
		a.form.strat.Name(), pickTimeModel(a.form).Name(), params, m.cfg.LinkDates,
		m.redateCommitSpec()))
}

// redateCommitSpec renders the redate target selection for the logged
// CLI-equivalent command: "" for the whole active range, a contiguous
// A^..B range when expressible, otherwise a comma-joined short-hash list
// (always expressible — resolveTargetSet unions comma parts).
func (m Model) redateCommitSpec() string {
	if !m.hasExplicitSelection() {
		return m.activeRangeSpec()
	}
	if spec, ok := m.selectionCommitSpec(); ok {
		return spec
	}
	affected := m.affectedCommits()
	parts := make([]string, len(affected))
	for i, c := range affected {
		parts[i] = short(c.Hash)
	}
	return strings.Join(parts, ",")
}

// activeRangeSpec renders the whole editable range as A^..B when older
// history is frozen, and "" when every commit is editable. Without it the
// logged command would target the frozen (often pushed) commits too, which
// the TUI never touched.
func (m Model) activeRangeSpec() string {
	active := m.plan.Active()
	if len(active) == 0 || m.plan.Base == 0 {
		return ""
	}
	return short(active[0].Hash) + "^.." + short(active[len(active)-1].Hash)
}

// sessionSeed derives a nonzero redate seed from the clock. Zero is avoided
// because both the config and the CLI read 0 as "no seed chosen".
func sessionSeed(now time.Time) int64 {
	if s := now.UnixNano() % 1_000_000_000; s > 0 {
		return s
	}
	return 1
}

// hasExplicitSelection reports whether the user marked specific commits, or is
// marking a range right now, as opposed to the redate flow's default
// whole-active-range selection. Used wherever staging or a preview must
// describe its scope ("the selection" vs "the editable range"). A band being
// marked counts even while it covers no editable commit, so the scope does not
// flip under the user mid-gesture.
func (m Model) hasExplicitSelection() bool {
	return len(m.markedHashes()) > 0 || m.sel.mode == SelRange
}

// segmentWindows returns the current selection's day windows, one per segment,
// derived the same way stageActive derives its targets (affectedCommits) so the
// calendar's capacity rules and the engine's actual placement never disagree.
func (m Model) segmentWindows() []strategy.Window {
	now := time.Now()
	segs := strategy.SegmentTargets(m.plan, m.affectedCommits(), now)
	return strategy.Windows(segs, m.loc, now)
}

// pinOwnerFor computes which segment owns each picked/pinned day, replaying
// them in recency order so the attribution matches what the pick controller
// (pickWithEviction/pinWithEviction) enforced interactively. Only Picked Days
// and Manual Pin place pins; every other strategy leaves params.PinOwner nil,
// which Apply treats as "a shared day serves whichever segment contains it."
func (m *Model) pinOwnerFor(a *strategyState, params *strategy.Params) {
	wins := m.segmentWindows()
	switch a.form.strat.Name() {
	case "Picked Days":
		unit := make(map[string]int, len(a.pickSeq))
		for k := range a.pickSeq {
			unit[k] = 1
		}
		params.PinOwner, _ = strategy.AttributePins(pickOrder(a.pickSeq), unit, wins, m.loc)
	case "Manual Pin":
		weights := make(map[string]int, len(a.quotaSeq))
		for k := range a.quotaSeq {
			weights[k] = a.quotas[k]
		}
		params.PinOwner, _ = strategy.AttributePins(pickOrder(a.quotaSeq), weights, wins, m.loc)
	}
}

// strategyStaged reports whether a strategy has staged a date distribution — an
// author-date edit on the active range. This is the signal the calendar previews as
// per-day numbers; a message-only edit (retitle/reword/drop-body) does NOT count, so
// the calendar stays in its tile heatmap until a strategy actually distributes dates.
func (m Model) strategyStaged() bool {
	for _, c := range m.plan.Active() {
		if e, ok := m.plan.EditFor(c.Hash); ok && e.NewAuthorDate != nil {
			return true
		}
	}
	return false
}

// New constructs the root model from already-loaded repo data.
func New(repo *gitio.Repo, mgr *safety.Manager, cfg config.Config, state model.RepoState, commits []model.Commit) Model {
	loc := cfg.Location()
	plan := model.NewPlan(commits)

	m := Model{
		redateSeed:   sessionSeed(time.Now()),
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
		flow:         flowNone,
	}
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	m.spinner = sp

	// Auto-detect the base branch and freeze the shared-history cone, then start
	// the cursor on the newest editable commit and seed the calendar there.
	m.applyAutoBase()
	m.rebuildContributors()
	m.sel = newSelection() // Single mode: no flow is open at startup (flow: flowNone above)
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
		m.strats[i] = strategyState{form: newParamsForm(s, cfg, seed)}
		m.strats[i].resetPins()
	}

	if m.baseBranch != "" && m.plan.Base > 0 {
		m.setStatus(fmt.Sprintf("Base %s — editing %d commit(s), %d frozen",
			m.baseBranch, len(m.plan.Active()), m.plan.Base), statusOK)
	}
	return m
}

// appendCommandLog mirrors a logged command into .git/regit/command.log so the
// history survives across sessions. Best-effort: write errors are swallowed.
func appendCommandLog(repo *gitio.Repo, line string) {
	gitDir, err := repo.GitDir()
	if err != nil {
		return
	}
	dir := filepath.Join(gitDir, "regit")
	// 0700/0600: the command log records the exact headless command for every
	// applied action, and `regit redact --match=<secret>` puts the secret itself
	// on that line — the verb's own Limitations entry says so. It must not be
	// readable by other local users. MkdirAll and OpenFile apply their mode
	// only on creation, so the explicit chmods also tighten a directory or log
	// left world-readable by an older regit.
	_ = os.MkdirAll(dir, 0o700)
	_ = os.Chmod(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, "command.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return
	}
	_, _ = f.WriteString(line + "\n\n")
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
		// Record the outcome on the picker itself (not just the main-view status
		// bar) so a failure is visible under the modal instead of hidden behind it.
		if m.mode == modeReword && msg.hash == m.rewordSt.commit.Hash {
			if msg.err != nil {
				m.rewordSt.errMsg = msg.err.Error()
			} else {
				m.rewordSt.errMsg = ""
			}
		}
		return m, nil
	case splitResultMsg:
		// Ignore a result for an abandoned generation (user pressed esc, or moved
		// on, or restarted the split — possibly on the same commit hash). The seq
		// token, not the hash, identifies the specific in-flight request.
		if m.mode != modeSplitGen || msg.seq != m.splitGenSeq {
			return m, nil
		}
		if msg.state.errMsg != "" {
			m.mode = modeMain
			m.setStatus(msg.state.errMsg, statusErr)
			return m, nil
		}
		c := m.commitByHash(msg.hash)
		sc, ok := m.buildSplitChange(c, msg.state)
		if !ok {
			m.mode = modeMain
			m.setStatus("Split commit not found in plan — history may have changed", statusErr)
			return m, nil
		}
		guard := sc.guard(m.state)
		if guard.Blocked {
			m.mode = modeMain
			m.setStatus(guard.BlockReason, statusErr)
			return m, nil
		}
		sc.cmd = capability.Render("split", capability.SplitArgMap(capability.Short(c.Hash)))
		m.pending = sc
		m.pendingGuard = guard
		m.pipelineApply = true
		m.previewOffset = 0
		m.mode = modePipeline
		m.pipelineOrigin = modeMain // staged from main; esc returns here (set explicitly, defensively)
		return m, nil
	case spinner.TickMsg:
		// Only keep the throbber animating while async work is actually in flight.
		// Otherwise the tick self-perpetuates for the rest of the session, waking the
		// event loop every ~100ms even when the UI is idle.
		if !m.spinnerBusy() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case summarizeSquashResultMsg:
		if msg.err == nil {
			m.squashSt = m.squashSt.applySummary(msg.chainKey, msg.prop)
		} else {
			delete(m.squashSt.loading, msg.chainKey)
			if m.mode == modeSquash {
				m.setStatus("Squash summarize failed: "+msg.err.Error(), statusErr)
			}
		}
		return m, nil
	case purgeScanMsg:
		// Ignore a result for an abandoned scan (the wizard was closed and
		// reopened, or reopened again before the first scan returned). The seq
		// token, not the mode alone, identifies the specific in-flight scan —
		// mirrors the splitResultMsg guard above.
		if m.mode != modePurge || msg.seq != m.purgeSt.scanSeq {
			return m, nil
		}
		m.purgeSt.scanning = false
		if msg.err != nil {
			m.purgeSt.scanErr = msg.err.Error()
		} else {
			m.purgeSt.index = msg.index
			m.purgeSt = m.purgeSt.refreshMatches()
		}
		return m, nil
	case redactScanMsg:
		// Ignore a result for an abandoned scan: the flow was closed (flow !=
		// flowRedact) or a newer scan has since started (seq mismatch) — mirrors
		// purgeScanMsg's identical guard, gated on m.flow rather than m.mode
		// since the redact flow lives in the flow zone, not a dedicated mode.
		if m.flow != flowRedact || msg.seq != m.redactScanSeq {
			return m, nil
		}
		m.redactSt.scanning = false
		if msg.err != nil {
			m.redactSt.scanErr = msg.err.Error()
		} else {
			m.redactSt.scanRes = msg.res
			m.redactSt.rows = buildRedactRows(msg.res)
			m.redactSt.sel = capability.AllOccurrencesSelected(msg.res)
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
	case modeSquash:
		return m.updateSquash(msg)
	case modeReauthor:
		return m.updateReauthor(msg)
	case modePurge:
		return m.updatePurge(msg)
	case modeSplitGen:
		// The only action while a split is generating is to abandon it; a late
		// splitResultMsg for this generation is then dropped by the mode+seq guard
		// in Update, even if the user immediately restarts a split on the same
		// commit (same hash, new seq).
		if msg.String() == "esc" {
			m.mode = modeMain
			m.splitGenHash = ""
			m.setStatus("Split cancelled", statusWarn)
		}
		return m, nil
	case modeDetail:
		if s := msg.String(); s == "." || s == "esc" || s == "q" {
			m.mode = modeMain
		}
		return m, nil
	case modePicker:
		return m.updatePicker(msg)
	case modeHelp:
		switch msg.String() {
		case "?", "esc", "q":
			m.mode = modeMain
		case "down", "j":
			m.helpOffset = clampHelpOffset(m.helpOffset+1, m.height)
		case "up", "k":
			m.helpOffset = clampHelpOffset(m.helpOffset-1, m.height)
		}
		return m, nil
	}
	return m, nil
}

// clampHelpOffset bounds a candidate help-scroll offset to [0, maxOffset], where
// maxOffset is how many lines of helpText() overflow the viewport for the given
// terminal height (0 when the whole help text already fits).
func clampHelpOffset(offset, height int) int {
	lineCount := strings.Count(helpText(), "\n") + 1
	viewportRows := max(1, height-6)
	maxOffset := max(0, lineCount-viewportRows)
	return max(0, min(offset, maxOffset))
}

// --- Main view -------------------------------------------------------------

func (m Model) updateMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The open panel owns every key first: it must swallow list/flow
	// navigation and globals alike so the underlying panes never react to a
	// keystroke the panel is also interpreting (e.g. 'j' moving the panel
	// cursor instead of the list cursor behind it).
	if m.panel.open {
		return m.updateActionPanel(msg)
	}
	if m.focus == focusList {
		if model, cmd, ok := m.listSelectionKey(msg); ok {
			return model, cmd
		}
	}
	if helper, ok := redateHelperKeys[msg.String()]; ok && m.inRedatePane() {
		helper(&m)
		return m, nil
	}
	if m.focus == focusParams {
		return m.updateParamsPane(msg)
	}
	if m.focus == focusRedact {
		// Full ownership, same reasoning as focusParams above: the redact
		// wizard's steps include free-text fields (match, glob, replace
		// pattern), so every key — including ones globalAction would otherwise
		// treat as a command (q, a, ...) — must reach the wizard verbatim
		// rather than being intercepted as a global action first.
		return m.updateRedactFlow(msg)
	}
	if m.focus == focusList && m.listNavKey(msg.String()) {
		m.syncListViewport()
		return m, m.markAndFetch()
	}
	if model, cmd, ok := m.globalAction(msg); ok {
		return model, cmd
	}
	switch msg.String() {
	case "esc":
		if m.flow != flowNone {
			m.exitFlow()
			return m, nil
		}
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
			m.moveCalendarCursor(msg)
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
	if m.focusPaneByKey(msg.String()) {
		return m, nil, true
	}
	switch msg.String() {
	case "q":
		m.quitting = true
		return m, tea.Quit, true
	case "?":
		m.mode = modeHelp
		m.helpOffset = 0
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
			m.strats[i].form = newParamsForm(m.strategies[i], m.cfg, m.calCursor)
			m.strats[i].resetPins()
		}
		m.plan.Edits = make(map[string]model.Edit)
		m.plan.Violations = nil
		m.plan.Shifted = nil
		m.quotaTyping = false
		m.redateCmd = ""
		m.planCmds = nil
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
	case "a":
		m.showAuthor = !m.showAuthor
		state := "off"
		if m.showAuthor {
			state = "on"
		}
		m.setStatus("Author column: "+state, statusOK)
		return m, nil, true
	case ".":
		// Guard the empty branch: modeDetail's View calls commitAtCursor, which would
		// index an empty slice. Every sibling action (e, b, split, …) guards this too.
		if len(m.plan.Commits) == 0 {
			return m, nil, true
		}
		m.mode = modeDetail
		return m, m.markAndFetch(), true
	case "B":
		branches, err := m.repo.Branches()
		if err != nil {
			m.setStatus("List branches: "+err.Error(), statusErr)
			return m, nil, true
		}
		m.picker = newPicker("Branches", branches, m.layout().inputWidth)
		m.mode = modePicker
		return m, nil, true
	case "L":
		m.logVisible = !m.logVisible
		m.syncListViewport() // the log takes rows from the list
		return m, nil, true
	case "m":
		// Opening/closing the panel only flips panel state; it never touches
		// m.sel, since the panel is a pure read of the current selection, not
		// a mutator of it (TestPanelOpenPreservesSelection pins this).
		m.panel.open = !m.panel.open
		m.panel.cursor = 0
		m.syncListViewport() // the panel takes rows from the list
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
		if m.sel.mode == SelRange {
			m.finishRange()
			m.setStatus(fmt.Sprintf("%d commit(s) marked", len(m.markedHashes())), statusOK)
		} else {
			m.sel.beginVisual(m.listCursor)
			m.setStatus("Marking a range — move to extend, v/enter to add it, esc to drop it", statusNone)
		}
		m.afterSelectionChange()
		return m, nil, true
	case " ":
		if m.plan.IsFrozen(m.storageIndexAtCursor()) {
			m.setStatus("Frozen commit — not selectable", statusWarn)
			return m, nil, true
		}
		m.sel.toggle(m.commitAtCursor().Hash, false)
		m.setStatus(fmt.Sprintf("%d commit(s) marked", len(m.markedHashes())), statusOK)
		m.afterSelectionChange()
		return m, nil, true
	case "s":
		var hashes []string
		for _, c := range m.plan.Active() {
			hashes = append(hashes, c.Hash)
		}
		m.sel.selectAll(hashes)
		m.setStatus(fmt.Sprintf("Marked all %d editable commit(s)", len(hashes)), statusOK)
		m.afterSelectionChange()
		return m, nil, true
	case "n":
		m.sel.clear()
		m.setStatus("Marks cleared", statusOK)
		m.afterSelectionChange()
		return m, nil, true
	case "enter":
		if m.sel.mode == SelRange {
			m.finishRange()
			m.setStatus(fmt.Sprintf("%d commit(s) marked", len(m.markedHashes())), statusOK)
			m.afterSelectionChange()
			return m, nil, true
		}
		return m, nil, false // not marking a range: fall through to open the editor
	case "esc":
		// esc undoes one level: a range being marked is dropped (earlier marks
		// stay); otherwise every mark is cleared.
		switch m.sel.mode {
		case SelRange:
			m.sel.endBand()
			m.setStatus("Range dropped — earlier marks kept", statusNone)
		case SelSet:
			m.sel.reset()
			m.setStatus("Marks cleared", statusNone)
		default:
			return m, nil, false
		}
		m.afterSelectionChange()
		return m, nil, true
	}
	return m, nil, false
}

// afterSelectionChange runs after any change to the marks: it restores the
// redate flow's whole-range scope when nothing is marked any more, then
// re-syncs the flow's staging to the new selection.
func (m *Model) afterSelectionChange() {
	m.restoreFlowSelectionScope()
	m.onDatesSelectionChanged()
}

// onDatesSelectionChanged re-syncs the active strategy's pins/picks and staged
// distribution after the commit-list selection changes while the redate flow
// is open: a narrower or wider selection can shrink or grow each segment's
// window, so picks/pins placed under the OLD selection may no longer fit the
// new one. reattributeActivePins MUST run before stageActive — it silently
// evicts or clamps whatever no longer fits, so stageActive then stages exactly
// what survived; the other order would briefly stage an edit built from a
// pick/pin that reattribution was about to drop.
//
// The leading flow guard below is what actually makes this a no-op while no
// flow is open: stageActive is NOT itself flow-guarded, and on an unready
// strategy it calls unstageActive, which clears NewAuthorDate / NewCommitDate
// on every active commit — including a date the user just typed in the
// per-commit editor. Without the guard, calling this from a selection-mutating
// site with no flow open would silently discard that editor edit.
// enterRedateFlow assigns the flow before calling this, so opening the redate
// flow still re-attributes and re-stages as before; only leaving (or staying
// off) it is now a true no-op.
func (m *Model) onDatesSelectionChanged() {
	if m.flow != flowRedate {
		return
	}
	m.reattributeActivePins()
	m.stageActive()
}

// reattributeActivePins re-syncs the ACTIVE strategy slot's pins/picks
// against the current segment windows, dropping or clamping whatever no
// longer fits and surfacing a status warning when it does. A no-op while no
// flow is open. Shared by onDatesSelectionChanged (selection changed under the
// active strategy) and cycleStrategy (the carousel just made a DIFFERENT
// strategy slot active, one whose pins may have gone stale under a selection
// change that happened while it was not active — see cycleStrategy's comment).
func (m *Model) reattributeActivePins() {
	if m.flow != flowRedate {
		return
	}
	a := m.activeStrat()
	dropped := reattributePins(a, m.segmentWindows(), m.loc)
	if len(dropped) > 0 {
		m.setStatus(fmt.Sprintf("Dropped %d pick(s)/pin(s) outside the new windows", len(dropped)),
			statusWarn)
	}
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
// field stays within a readable [inputMinWidth, inputMaxWidth] band.
func (m Model) editorFieldWidth() int {
	return m.layout().inputWidth
}

// listNavKey handles fast list-navigation keys when the commit list is focused.
// It returns true when it consumed the key. Routing this BEFORE globalAction
// lets g/G mean top/bottom of the list; the calendar and params
// panes see g as a no-op (staging is automatic, so there is no re-apply key).
func (m *Model) listNavKey(key string) bool {
	last := len(m.plan.Commits) - 1
	if last < 0 {
		return false
	}
	page := m.listVisibleRows()
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

// moveCalendarCursor handles a navigation key while the calendar is focused
// (list movement goes through listNavKey).
func (m *Model) moveCalendarCursor(msg tea.KeyMsg) {
	// The heatmap is GitHub-style: weekdays run top-to-bottom
	// within a week column, weeks run left-to-right — so vertical arrows move
	// by one day and horizontal arrows jump a whole week. Movement snaps to
	// the nearest available day in the direction of travel (see
	// snapCalCursor) rather than landing on an unpickable one.
	switch msg.String() {
	case "up", "k":
		m.snapCalCursor(-1, 1)
	case "down", "j":
		m.snapCalCursor(+1, 1)
	case "left", "h":
		m.snapCalCursor(-1, 7)
	case "right", "l":
		m.snapCalCursor(+1, 7)
	case " ":
		// Toggle a picked day — only when the Picked Days strategy is active.
		// Other strategies ignore space so the key can be used elsewhere later.
		if m.activeStrat().form.strat.Name() == "Picked Days" {
			key := m.calCursor.Format(dateLayout)
			a := m.activeStrat()
			if a.picked[key] {
				// Already picked: toggle off. pickSeq must be cleared too, or a
				// later pick/eviction replay would still see this day and double-
				// count its (now-gone) weight against the segment's capacity.
				delete(a.picked, key)
				delete(a.pickSeq, key)
			} else if evicted, ok := pickWithEviction(a, key, m.segmentWindows(), m.loc); ok {
				if len(evicted) > 0 {
					m.setStatus(fmt.Sprintf("Unpicked %s — window full", evicted[0]), statusWarn)
				}
			} else {
				m.setStatus("Day is outside every available window", statusWarn)
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
				a := m.activeStrat()
				// Consecutive digits on the same day build a two-digit quota:
				// 2 → 24 → 45 (the last two digits typed win). The combined
				// value must be read from the OLD quota before that entry is
				// cleared below, or the second digit would combine with zero.
				if m.quotaTyping {
					d = (a.quotas[key]%10)*10 + d
				}
				if _, alreadyPinned := a.quotaSeq[key]; alreadyPinned {
					// Re-pinning an already-pinned day REPLACES its weight, it
					// never adds to it. pinWithEviction sums every entry in
					// a.quotaSeq to check the segment's capacity, so leaving the
					// stale entry in place here would double-count this day's
					// old quota and could wrongly evict an unrelated day.
					delete(a.quotas, key)
					delete(a.quotaSeq, key)
				}
				if d == 0 {
					m.quotaTyping = true
					m.stageActive()
					break // adapt to the switch structure: fall through to shared tail
				}
				if shrunk, clamped, ok := pinWithEviction(a, key, d, m.segmentWindows(), m.loc); ok {
					if clamped != d {
						m.setStatus(fmt.Sprintf("Quota clamped to %d — window capacity", clamped), statusWarn)
					} else if len(shrunk) > 0 {
						m.setStatus(fmt.Sprintf("Shrunk pin on %s — window full", shrunk[0]), statusWarn)
					}
				} else {
					m.setStatus("Day is outside every available window", statusWarn)
				}
				m.quotaTyping = true
				m.stageActive()
			}
		}
	}
	// snapCalCursor already updates calYear/quotaTyping/the list cursor for the
	// arrow-key cases above; this unconditional re-set is a no-op for those and
	// keeps the Space/digit cases' existing behavior (they never touch
	// quotaTyping or the list cursor here — quotaTyping has its own handling
	// above, and pin/pick actions intentionally leave the list cursor alone).
	m.calYear = m.calCursor.Year()
}

// calDayAvailable reports whether the cursor may rest on the given day:
// inside a segment window when an explicit selection exists, otherwise any
// day up to today (the future is never available).
func (m Model) calDayAvailable(day time.Time) bool {
	// Compare calendar days, not instants: calCursor carries a residual
	// time-of-day from commit timestamps, so an instant comparison would treat
	// "today at a later hour than now" as future and strand the cursor one day
	// short of today. dateLayout keys sort lexicographically = chronologically.
	if day.In(m.loc).Format(dateLayout) > time.Now().In(m.loc).Format(dateLayout) {
		return false
	}
	if !m.hasExplicitSelection() {
		return true
	}
	for _, w := range m.segmentWindows() {
		if w.ContainsDay(day, m.loc) {
			return true
		}
	}
	return false
}

// snapCalCursor moves the calendar cursor `jump` days in `dir` and then, if
// that day is unavailable, keeps stepping one day at a time in the same
// direction until an available day is found (snap-to-available). If none
// exists within two years the cursor stays put.
func (m *Model) snapCalCursor(dir, jump int) {
	cand := m.calCursor.AddDate(0, 0, dir*jump)
	const maxSteps = 730 // two years: generous bound so the search always terminates
	for i := 0; i < maxSteps; i++ {
		if m.calDayAvailable(cand) {
			m.calCursor = cand
			m.calYear = m.calCursor.Year()
			m.quotaTyping = false // a new day starts a new number
			m.selectFirstCommitOfDay()
			return
		}
		cand = cand.AddDate(0, 0, dir)
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
	if spec, ok := m.selectionCommitSpec(); ok {
		m.planCmds = append(m.planCmds, capability.Render("drop-bodies", capability.DropBodiesArgMap(spec)))
	} else {
		m.planCmds = append(m.planCmds, "# drop-bodies on the selected commits — set --commits=<range> to reproduce")
	}
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

	changed, skippedEmpty := 0, 0
	for _, c := range m.affectedCommits() {
		cur := m.plan.EffectiveSubject(c)
		neu := transform(cur)
		if neu == cur {
			continue
		}
		if neu == "" {
			// Same rule as the headless verb: a commit never gets an empty title.
			skippedEmpty++
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
		if skippedEmpty > 0 {
			m.retitle.errMsg = fmt.Sprintf("no titles would change (%d would become empty and are skipped)", skippedEmpty)
		}
		return m, nil
	}
	m.mode = modeMain
	msg := fmt.Sprintf("Retitled %d commit(s)", changed)
	if skippedEmpty > 0 {
		msg += fmt.Sprintf(" · skipped %d that would become empty", skippedEmpty)
	}
	m.setStatus(msg, statusOK)
	if spec, ok := m.selectionCommitSpec(); ok {
		m.planCmds = append(m.planCmds, capability.Render("retitle", capability.RetitleArgMap(capability.RetitleOptions{
			Match: m.retitle.pattern.Value(), Replace: m.retitle.replace.Value(), Commits: spec,
			Capitalize: m.retitle.capitalize, EnsurePeriod: m.retitle.ensureDot,
		})))
	} else {
		m.planCmds = append(m.planCmds, "# retitle on the selected commits — set --commits=<range> to reproduce")
	}
	return m, nil
}

// storageIndexAtCursor converts the list cursor (display order, newest first)
// to the root-first storage index used by the plan.
func (m Model) storageIndexAtCursor() int {
	return len(m.plan.Commits) - 1 - m.listCursor
}

// spinnerBusy reports whether any async generation is in flight that the shared
// throbber animates: a split proposal, one or more reword variants, a squash
// summary, or a purge history scan. When nothing is pending the spinner tick loop
// stops re-arming itself so the UI goes fully idle instead of waking every ~100ms.
func (m Model) spinnerBusy() bool {
	if m.mode == modeSplitGen || len(m.rewordSt.inFlight) > 0 {
		return true
	}
	if m.mode == modePurge && m.purgeSt.scanning {
		return true
	}
	if m.flow == flowRedact && m.redactSt.scanning {
		return true
	}
	for _, loading := range m.squashSt.loading {
		if loading {
			return true
		}
	}
	return false
}

// affectedCommits returns the commits a bulk action or the action-panel count
// applies to, under the "selection wins, else all-active" rule: the marked
// commits when there are any, otherwise the whole active range. Returned
// root-first (oldest → newest) to match plan.Active.
func (m Model) affectedCommits() []model.Commit {
	if !m.hasExplicitSelection() {
		return m.plan.Active()
	}
	marked := m.markedHashes()
	var out []model.Commit
	for _, c := range m.plan.Active() {
		if marked[c.Hash] {
			out = append(out, c)
		}
	}
	return out
}

// markedHashes is the single source of what the user has marked: every
// editable commit in the set plus, while a range is being marked, the pending
// band. Frozen commits are excluded even when a band is dragged across the base
// boundary, so no consumer (squash, reauthor, reword, redate, the panel count)
// can rewrite the frozen cone the UI renders as protected.
func (m Model) markedHashes() map[string]bool {
	out := map[string]bool{}
	for _, c := range m.plan.Active() {
		if m.sel.isSet(c.Hash) {
			out[c.Hash] = true
		}
	}
	for _, h := range m.bandHashes() {
		out[h] = true
	}
	return out
}

// bandHashes returns the editable commits inside the band being marked, or nil
// when no range is being marked.
func (m Model) bandHashes() []string {
	if m.sel.mode != SelRange {
		return nil
	}
	lo, hi := m.sel.rangeBounds(m.listCursor)
	var out []string
	for d := lo; d <= hi; d++ {
		storage := len(m.plan.Commits) - 1 - d
		if storage < 0 || storage >= len(m.plan.Commits) || m.plan.IsFrozen(storage) {
			continue
		}
		out = append(out, m.plan.Commits[storage].Hash)
	}
	return out
}

// finishRange adds the band being marked to the marked set and stops marking.
func (m *Model) finishRange() {
	for _, h := range m.bandHashes() {
		m.sel.set[h] = true
	}
	m.sel.endBand()
}

// selectionCommitSpec renders the current explicit selection as a headless
// --commits= value. resolveTargets accepts only "", "A..B", or a single ref, so
// only a contiguous, non-root run of the selection is expressible as a range;
// anything else (non-contiguous set, or a run touching the root) is not.
func (m Model) selectionCommitSpec() (spec string, expressible bool) {
	if !m.hasExplicitSelection() {
		return "", true // whole active range → empty --commits is valid
	}
	affected := m.affectedCommits() // root-first
	if len(affected) == 0 {
		return "", true
	}
	// Map hashes to storage indices and require a consecutive run.
	idx := make(map[string]int, len(m.plan.Commits))
	for i, c := range m.plan.Commits {
		idx[c.Hash] = i
	}
	lo, hi := idx[affected[0].Hash], idx[affected[len(affected)-1].Hash]
	if hi-lo+1 != len(affected) || lo == 0 { // gap, or includes the root (no parent for ^)
		return "", false
	}
	return short(affected[0].Hash) + "^.." + short(affected[len(affected)-1].Hash), true
}

// focusAfterList is where tab moves focus from the commit list: into the
// active flow's widget when one is open, else nowhere (the panel and global
// keys are the action surface — there is no menu to focus anymore).
func (m Model) focusAfterList() focusArea {
	if m.flow == flowRedate {
		return focusCalendar
	}
	return focusList
}

// applyAutoBase freezes the history that should not be rewritten by default:
// every pushed commit, and every commit shared with the base branch (per
// chooseBaseBranch's precedence) up to their merge-base — whichever boundary is
// newer. An in-sync trunk branch is the exception and starts fully editable.
// It is best-effort: a git failure on the merge-base side just drops that
// boundary. Called on load and after every reload, so the freeze re-establishes
// itself (and survives apply/undo).
//
// baseBranch and plan.Base are always set together: baseBranch is non-empty ONLY
// when a freeze was actually computed, so the title bar can never show a base
// whose cone is not frozen. It names the base branch, or pushedBaseLabel when
// the pushed boundary is the newer one.
func (m *Model) applyAutoBase() {
	// On an in-sync trunk branch, start with everything editable. Every commit
	// is pushed there, so the pushed boundary would freeze all but the newest.
	if startsUnlocked(m.state) {
		m.baseBranch = ""
		m.plan.SetBase(0)
		return
	}
	label, idx := m.mergeBaseFreeze()
	if pushed := pushedBaseIndex(m.plan.Commits); pushed > idx {
		label, idx = pushedBaseLabel, pushed
	}
	m.baseBranch = label
	m.plan.SetBase(idx)
}

// mergeBaseFreeze returns the base branch and the plan Base that freezes the
// history shared with it, or ("", 0) when there is no base branch, git fails,
// or the merge-base is off the first-parent line (e.g. the base branch was
// merged in), where there is no honest cone to freeze.
func (m *Model) mergeBaseFreeze() (string, int) {
	branches, err := m.repo.Branches()
	if err != nil {
		return "", 0
	}
	base := chooseBaseBranch(m.state, branches)
	if base == "" {
		return "", 0
	}
	mergeBase, err := m.repo.MergeBase(m.state.Branch, base)
	if err != nil {
		return "", 0
	}
	idx := baseIndexForMergeBase(m.plan.Commits, mergeBase)
	if idx == 0 {
		return "", 0
	}
	return base, idx
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

// cycleStrategy moves the carousel by delta (wrapping) and re-stages: stageActive on
// the now-active strategy overwrites the staged author dates with its distribution,
// or clears them if it is not ready — so the plan only ever holds the active
// strategy's dates.
//
// Each strategy slot keeps its own pins/picks (m.strats[i]), and
// onDatesSelectionChanged only re-attributes the slot that is active AT THE
// TIME the selection changes. So a slot the carousel is NOT sitting on when
// the selection narrows keeps pins sized for the old, wider windows — landing
// back on it here must re-attribute before staging, or a pick/pin outside the
// narrowed window would be staged verbatim (and logged in the footer's
// CLI-equivalent line) even though it can never actually apply. This mirrors
// onDatesSelectionChanged's own ordering: reattribute, THEN stage.
func (m *Model) cycleStrategy(delta int) {
	n := len(m.strategies)
	m.stratIdx = (m.stratIdx + delta + n) % n
	m.activeStrat().form.focusIdx = 0
	m.reattributeActivePins()
	m.stageActive()
}

// redateHelperKeys are the left/right helper pairs of the redate flow: they
// change the strategy and the time model from the calendar or the strategy
// pane, so neither change needs a focus switch. Arrows stay the calendar
// cursor; {/} stay the list's day-group jumps.
var redateHelperKeys = map[string]func(m *Model){
	"[": func(m *Model) { m.cycleStrategy(-1) },
	"]": func(m *Model) { m.cycleStrategy(1) },
	"<": func(m *Model) { m.cycleTimeModel(-1) },
	">": func(m *Model) { m.cycleTimeModel(1) },
}

// inRedatePane reports whether focus is on one of the redate flow's own panes.
func (m Model) inRedatePane() bool {
	return m.flow == flowRedate && (m.focus == focusCalendar || m.focus == focusParams)
}

// cycleTimeModel selects the next or previous time model of the active
// strategy and re-stages, exactly as the strategy pane's time-model row does.
func (m *Model) cycleTimeModel(delta int) {
	m.activeStrat().form.cycleTimeModel(delta)
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
	if !m.activeReady() {
		// Nothing staged yet: report a helpful message and stay in the pane.
		if a.form.strat.Name() == "Manual Pin" || a.form.strat.Name() == "Picked Days" {
			a.form.errMsg = "Pin at least one day on the calendar first (0–9 for quotas, space for picked days)"
		} else {
			a.form.errMsg = "Fill the date fields before applying"
		}
		return m, nil
	}
	// Success: hand focus back to the list so global keys (A, p, U…) work
	// again immediately; tab twice returns here for further tweaking.
	m.focus = focusList
	m.reportPlan(a.form.strat.Name())
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
	m.editor.errMsg = "" // any further edit invalidates a stale save error
	cmd := m.editor.update(msg)
	return m, cmd
}

func (m Model) saveEditor() (tea.Model, tea.Cmd) {
	c := m.commitByHash(m.editor.hash)
	edit, err := m.editor.buildEdit(c, m.loc, m.cfg.LinkDates)
	if err != nil {
		// Stay in the editor and show the error inline — a hidden status-bar
		// message would be invisible under this modal.
		m.editor.errMsg = err.Error()
		return m, nil
	}
	m.plan.SetEdit(c.Hash, edit)
	m.recomputeViolations()
	m.mode = modeMain
	m.reportPlan("Edited " + short(c.Hash))
	// The per-commit editor has no headless equivalent yet, so log an honest
	// note rather than staying silent — dedupe so re-editing the same commit in
	// one session doesn't stack a duplicate note for every save.
	note := "# edit " + short(c.Hash) + " — no headless equivalent yet"
	if !slices.Contains(m.planCmds, note) {
		m.planCmds = append(m.planCmds, note)
	}
	return m, nil
}

// --- Pipeline overlay ------------------------------------------------------

// updatePipeline drives the preview overlay (modePipeline). Scrolling works in
// both peek and apply mode; enter applies only in apply mode (routing through the
// typed confirm first when the change rewrites pushed commits); esc/q close.
func (m Model) updatePipeline(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		// Return to wherever the pipeline was staged from — the main view for a
		// plan apply, or the wizard (squash/reauthor) that staged it, so typed
		// edits still sitting in squashSt/reauthorSt aren't lost. Deliberately does
		// NOT touch redateCmd/planCmds: those describe edits still sitting in
		// m.plan, which survives this cancel (a peek in particular must not drop
		// them — only clearing m.pending here matters, since a direct change's
		// command line lives on the change itself and is discarded with it).
		m.mode = m.pipelineOrigin
		m.pending = nil
		m.pipelineOrigin = modeMain
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
			// A read-only peek closes without applying — same "keep the plan
			// lines, only drop pending" rule as esc/q above.
			m.mode = modeMain
			m.pending = nil
			m.pipelineOrigin = modeMain
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
// flushes ITS OWN command lines (never a shared slot — see pendingChange.commandLines),
// reloads history, and reports the backup ref. It is the single apply funnel for
// every mutation (plan edits, split, squash, reauthor).
func (m Model) commitPending(confirmed bool) (tea.Model, tea.Cmd) {
	if m.pending == nil {
		m.mode = modeMain
		return m, nil
	}
	opts := safety.ApplyOptions{
		Sign:          gitio.DropSignatures,
		UnixTimestamp: time.Now().Unix(),
		Confirmed:     confirmed,
		ExpectedHead:  m.state.HeadHash,
	}
	res, err := m.pending.apply(m.mgr, m.state, opts)
	if err != nil {
		m.mode = modeMain
		m.pending = nil
		m.setStatus(err.Error(), statusErr)
		return m, nil
	}
	title := m.pending.title()
	lines := m.pending.commandLines()
	// A redactChange needs two things no other pendingChange does: the
	// wizard's scanned occurrences reference commit hashes the rewrite just
	// replaced, so the flow MUST close rather than linger showing now-stale
	// data (see exitFlow below); and the hygiene warning must ride the status
	// line since redact is the one action that burns a credential regardless
	// of how clean the rewrite looks. A type assertion here (rather than a
	// new pendingChange method every other change would have to implement as
	// a no-op) keeps this special case local to its one actual use.
	_, isRedact := m.pending.(redactChange)
	// Flush the applied change's own lines BEFORE reload: an applied-but-then
	// reload-failed action still really happened (the git rewrite committed), so
	// it must not be silently dropped from the audit trail just because the
	// UI's post-apply refresh failed.
	if len(lines) > 0 {
		diskLines := lines
		if dl, ok := m.pending.(diskLogger); ok {
			diskLines = dl.diskCommandLines()
		}
		m.commandLog = append(m.commandLog, lines...)
		for _, line := range diskLines {
			// A "#"-prefixed line is an honest note (e.g. a non-expressible
			// selection), not a runnable command — keep it in the in-memory
			// panel but never write it to the on-disk command log.
			if !strings.HasPrefix(line, "#") {
				appendCommandLog(m.repo, line)
			}
		}
		vline := res.Verification.String()
		m.commandLog = append(m.commandLog, vline)
		appendCommandLog(m.repo, vline)
	}
	m.pending = nil
	m.pipelineOrigin = modeMain
	// Clear the plan-scoped lines regardless of change type: reload() below wipes
	// m.plan, so any redateCmd/planCmds describing it are stale either way. A
	// direct change's own line lived on the change and vanished with m.pending.
	m.redateCmd = ""
	m.planCmds = nil
	if err := m.reload(); err != nil {
		m.mode = modeMain
		msg := "Applied, but reload failed: " + err.Error()
		if isRedact {
			// The rewrite already committed (reload failing is a read-side
			// problem, not an undo) — the secret is just as burned as it is on
			// the success path below, so the hygiene warning must still reach
			// the operator here rather than being silently dropped.
			m.exitFlow()
			msg += " " + capability.HygieneWarning
		}
		m.setStatus(msg, statusWarn)
		return m, nil
	}
	m.mode = modeMain
	if isRedact {
		// The redact flow is one-shot: its scanned occurrences are now stale
		// (every hash they name was just rewritten away), so the wizard must
		// close rather than keep showing dead data — see the isRedact comment
		// above. The hygiene warning rides the SAME status line the ordinary
		// success message uses, verbatim to the headless wording (capability.
		// HygieneWarning), so the two surfaces never say this two ways.
		m.exitFlow()
		m.setStatus(fmt.Sprintf("%s ✓ backup at %s — press U to undo. %s",
			title, short(res.BackupRef), capability.HygieneWarning), statusWarn)
	} else {
		m.setStatus(fmt.Sprintf("%s ✓ backup at %s — press U to undo", title, short(res.BackupRef)), statusOK)
	}
	// Surface the same push-hint wording as the headless CLI's pushHint
	// (internal/capability/pipeline.go) in the command-log footer, using the
	// just-reloaded m.state — a history rewrite never changes the upstream
	// tracking config, so it is equally correct as the pre-apply state.
	for _, line := range pushHintFooterLines(m.repo, m.state) {
		m.commandLog = append(m.commandLog, line)
		// Same "#"-prefixed convention as above: the divergence note is an
		// honest observation, not a runnable command, so it never reaches the
		// on-disk log — only the git push line itself does.
		if !strings.HasPrefix(line, "#") {
			appendCommandLog(m.repo, line)
		}
	}
	return m, m.markAndFetch()
}

// pushHintFooterLines renders capability.PushHintLines for the command-log
// footer: the status note becomes a "#" comment and the push command stays
// runnable. Returns nil when the branch has no upstream.
func pushHintFooterLines(repo *gitio.Repo, state model.RepoState) []string {
	lines := capability.PushHintLines(repo, state)
	if lines == nil {
		return nil
	}
	return []string{"# " + lines[0], lines[1]}
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
	// Snapshot every plan-edit line accumulated so far onto the change: the active
	// strategy's redate line (if any) first, then the other staged edits in the
	// order they were made. This is the ONLY place redateCmd and planCmds are
	// combined — commitPending later flushes exactly this snapshot, not the live
	// (and by then stale, since reload wipes the plan) model fields.
	cmds := make([]string, 0, len(m.planCmds)+1)
	if m.redateCmd != "" {
		cmds = append(cmds, m.redateCmd)
	}
	cmds = append(cmds, m.planCmds...)
	impact := refImpactLines(m.repo, m.plan.Active(), m.state.Branch)
	pc := newPlanChange(m.plan, m.loc, cmds, impact)
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
	m.pipelineOrigin = modeMain // staged from main; esc returns here (set explicitly, defensively)
	return m, nil
}

func (m Model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// Return to wherever the pipeline was staged from (the wizard, for a
		// squash/reauthor confirm) instead of always modeMain, so a pushed-history
		// change cancelled at the typed-confirm prompt doesn't lose the wizard's
		// typed state. Clear pending so its command line (owned by the change,
		// not a shared slot) cannot leak into a later, unrelated apply; leave
		// redateCmd/planCmds untouched — they describe m.plan, unaffected by this
		// cancel.
		m.mode = m.pipelineOrigin
		m.pending = nil
		m.pipelineOrigin = modeMain
		m.setStatus("Apply cancelled", statusWarn)
		return m, nil
	case "enter":
		if strings.TrimSpace(m.confirmInput.Value()) != m.confirmGuard.ConfirmWord {
			// Wrong text: keep the user in the prompt to fix their typo rather than
			// discarding the staged change and forcing them to re-stage. esc (above)
			// is the deliberate cancel path.
			m.setStatus(fmt.Sprintf("Type %q exactly to confirm (esc cancels)", m.confirmGuard.ConfirmWord), statusWarn)
			return m, nil
		}
		return m.commitPending(true)
	}
	var cmd tea.Cmd
	m.confirmInput, cmd = m.confirmInput.Update(msg)
	return m, cmd
}

// doUndo restores the most recent backup. It never forces past the
// advanced-HEAD guard (no interactive confirmation flow exists yet for it in
// the TUI); a guard refusal surfaces the same way any other Undo error does,
// via the status line — the user can still force it from the CLI with
// `regit undo --force`. The pre-undo backup's timestamp comes from the wall
// clock at the moment of the keypress, same as every other apply in this file
// (see commitPending's UnixTimestamp: time.Now().Unix()); the Model has no
// injected clock to thread through instead.
func (m Model) doUndo() (tea.Model, tea.Cmd) {
	if !m.mgr.CanUndo() {
		m.setStatus("Nothing to undo", statusWarn)
		return m, nil
	}
	hash, warning, err := m.mgr.Undo(false, time.Now().Unix())
	if err != nil {
		m.setStatus(err.Error(), statusErr)
		return m, nil
	}
	// Log the undo itself, same as any other applied action, so the footer and
	// the on-disk command log reflect the real history including reversals. Done
	// BEFORE the reload-failure check below: the undo already rewrote history by
	// this point, so it must be logged even if the subsequent reload fails.
	line := capability.Render("undo", map[string]string{})
	m.commandLog = append(m.commandLog, line)
	appendCommandLog(m.repo, line)
	if err := m.reload(); err != nil {
		m.setStatus("Undo done, reload failed: "+err.Error(), statusWarn)
		return m, nil
	}
	if warning != "" {
		// The undo itself succeeded — the ref move is the undo, and it already
		// happened — so this rides the same status line rather than replacing it,
		// matching how commitPending appends the redact hygiene warning above.
		m.setStatus("Undid last apply — restored "+short(hash)+". "+warning, statusWarn)
	} else {
		m.setStatus("Undid last apply — restored "+short(hash), statusOK)
	}
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
	// Keep the redate flow's implicit scope alive across the reload instead of
	// silently dropping to Single underneath an open flow.
	m.restoreFlowSelectionScope()
	// After an apply/undo the plan's dates ARE the applied distribution: the
	// pre-apply pins were consumed by it and must not leak into the next
	// session's pinOwnerFor/AttributePins run against the rebuilt windows.
	for i := range m.strats {
		m.strats[i].resetPins()
	}
	m.rebuildContributors()
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
		m.setStatus(fmt.Sprintf("%s · %d edited · ⚠ %d conflicting", action, len(m.plan.Edits), len(m.plan.Shifted)), statusWarn)
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
	m.plan.Shifted = strategy.ShiftedByEnforcement(m.plan.Commits, times)
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

// beginSplit is invoked from the action panel's Split row (featureBindings,
// feature.go). It resolves the split inputs on the UI thread, then fires
// Engine.Propose asynchronously (generateSplitCmd) and enters modeSplitGen so
// the TUI stays responsive for the full LLM latency. The result lands later as
// a splitResultMsg, handled in Update.
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
	in, err := buildSplitInputs(&m, c)
	if err != nil {
		m.setStatus("split: "+err.Error(), statusErr)
		return m, nil, true
	}
	m.splitGenSeq++
	seq := m.splitGenSeq
	m.splitGenHash = c.Hash
	m.mode = modeSplitGen
	m.setStatus("", statusNone)
	return m, tea.Batch(m.spinner.Tick, generateSplitCmd(in, seq)), true
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
	if m.width > 0 && m.height > 0 && (m.width < minTermWidth || m.height < minTermHeight) {
		msg := fmt.Sprintf("Terminal too small\nneed ≥ %d×%d (now %d×%d)\nresize to continue",
			minTermWidth, minTermHeight, m.width, m.height)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.st.statusWarn.Render(msg))
	}
	main := m.viewMain()
	if m.zoneWizardOpen() {
		return main // drawn in the flow zone by viewFlowZone
	}

	var overlay string
	switch m.mode {
	case modeEditor:
		overlay = m.editor.view(m.commitByHash(m.editor.hash), m.st)
	case modePipeline:
		overlay = viewPipeline(m.pending.preview(m.st), m.previewOffset, m.previewVisibleRows(),
			m.pipelineApply, m.layout().previewWidth, m.st)
	case modeConfirm:
		overlay = m.viewConfirm()
	case modeHelp:
		lines := strings.Split(helpText(), "\n")
		rows := max(1, m.height-6)
		start := m.helpOffset
		if start > max(0, len(lines)-rows) {
			start = max(0, len(lines)-rows)
		}
		end := min(len(lines), start+rows)
		body := strings.Join(lines[start:end], "\n")
		if len(lines) > rows {
			body += "\n" + m.st.help.Render("↑/↓ scroll · esc close")
		}
		overlay = m.st.modal.Render(body)
	case modeDropBodies:
		overlay = m.viewDropBodies()
	case modeDetail:
		c := m.commitAtCursor()
		stat, have := m.statCache[c.Hash]
		overlay = m.st.modal.Render(renderDetail(c, stat, have, m.statErr[c.Hash], m.plan, m.loc,
			m.layout().detailOverlayWidth,
			max(8, m.height-6), m.st, m.authorColorStyle(c)))
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
