package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/redact"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/termsafe"
)

// redact.go implements the redact flow (flowRedact, flow.go): a three-step
// wizard hosted in the flow zone beside the commit list — unlike purge's
// full-screen modal wizard (purge.go), the commit list stays visible the
// whole time, matching redate's embedded-widget precedent. Step 1 collects
// the literal to search for; step 2 is an occurrence picker over an async
// history scan; step 3 chooses remove/replace and stages a redactChange, the
// pendingChange the standard pipeline (pipeline.go) then previews and applies.

// redactStep identifies which page of the wizard is showing.
type redactStep int

const (
	redactStepMatch redactStep = iota // step 1: match + glob inputs
	redactStepPick                    // step 2: occurrence picker (async scan)
	redactStepMode                    // step 3: remove/replace + stage
)

// redactRow is one flattened, cursor-addressable row of the step 2 picker:
// either an occurrence's own intro line (descIdx == -1) or one of its cascade
// descendants (descIdx indexes that occurrence's Descendants). Flattening the
// two levels into one slice up front keeps cursor movement, toggling, and
// rendering all working off the same simple int index instead of a nested
// (occurrence, descendant-or-none) pair everywhere.
type redactRow struct {
	occIdx  int
	descIdx int
}

// redactState is the wizard's live state across all three steps. The four
// textinputs cover every text field the wizard ever shows (match/glob at
// step 1, pattern/replacement at step 3); only the ones for the current step
// are ever rendered or reachable by key.
type redactState struct {
	step redactStep

	matchInput textinput.Model
	globInput  textinput.Model
	fieldFocus int // step 1 only: 0 match, 1 glob

	scanning bool
	scanErr  string
	scanRes  redact.ScanResult

	rows     []redactRow        // flattened occurrences+descendants once the scan lands
	cursor   int                // index into rows
	expanded map[int]bool       // occIdx -> ±2 context expanded
	sel      []redact.Selection // parallel to scanRes.Occurrences; the operator's checks

	mode         model.RedactMode
	patternInput textinput.Model
	replaceInput textinput.Model
	// modeFocus is step 3's row cursor: 0 the mode toggle, 1 pattern, 2
	// replacement. Rows 1/2 exist only in replace mode (see
	// redactModeFieldCount) — remove mode always has modeFocus pinned at 0.
	modeFocus int

	errMsg string
}

// redactModeFieldCount is the number of step-3 focus stops in replace mode
// (mode toggle, pattern, replacement); remove mode only ever has stop 0.
const redactModeFieldCount = 3

// newRedactState builds a fresh wizard at step 1, both textinputs sized from
// the caller's layout (mirrors newPurgeState/newRetitleState's convention).
// redactLabelWidth is the label column of every redact text field, sized to
// the longest label ("Secret to find").
const redactLabelWidth = 14

// redactInputWidth fits a redact text field inside the flow pane: the pane's
// border and padding (4), the "▸ " cursor (2), the label column and its gap,
// and the text input's own cursor cell. A longer value scrolls in the field
// instead of wrapping onto a second row.
func redactInputWidth(flowWidth int) int {
	return max(10, flowWidth-4-2-redactLabelWidth-1-1)
}

func newRedactState(inputW int) redactState {
	newField := func() textinput.Model {
		f := textinput.New()
		f.Prompt = ""
		f.CharLimit = 200
		f.Width = inputW
		return f
	}
	match := newField()
	match.Focus()
	glob := newField()
	glob.Placeholder = "all files"
	return redactState{
		step:         redactStepMatch,
		matchInput:   match,
		globInput:    glob,
		patternInput: newField(),
		replaceInput: newField(),
		expanded:     map[int]bool{},
	}
}

// redactScanMsg delivers the async history scan (or its failure). seq ties
// the result back to the beginRedactScan invocation that started it — see
// Model.redactScanSeq's doc comment for why this is a Model-level token
// rather than living on redactState like purgeSt.scanSeq does.
type redactScanMsg struct {
	res redact.ScanResult
	err error
	seq int
}

// buildRedactRows flattens a scan result into the picker's row list: each
// occurrence's own intro row, immediately followed by its cascade
// descendants in order — so the rendered list groups a chain together
// exactly as the operator needs to read it (an occurrence with its rotations
// indented right below it), while cursor movement stays a plain int walk.
func buildRedactRows(res redact.ScanResult) []redactRow {
	var rows []redactRow
	for i, occ := range res.Occurrences {
		rows = append(rows, redactRow{occIdx: i, descIdx: -1})
		for j := range occ.Descendants {
			rows = append(rows, redactRow{occIdx: i, descIdx: j})
		}
	}
	return rows
}

// splitGlobs parses step 1's comma-separated glob field into the []string
// gitio/redact.ScanOptions.Globs and capability.RedactArgMap both expect —
// the same wire format RedactArgMap's "in" flag already renders as (see its
// doc comment), so what the operator types and what the logged --in value
// shows are the same shape.
func splitGlobs(raw string) []string {
	var out []string
	for _, g := range strings.Split(raw, ",") {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// beginRedactScan validates step 1's match field and, if non-empty, launches
// the async history scan, bumping m.redactScanSeq so a result from an
// abandoned earlier scan is recognizable as stale (see redactScanMsg's
// handling in Update, app.go). Cascade is always on: the wizard's step 2
// always shows cascade descendants (pre-checked, individually vetoable) —
// there is no TUI equivalent of --no-cascade, unlike the headless verb.
func (m Model) beginRedactScan() (tea.Model, tea.Cmd) {
	match := strings.TrimSpace(m.redactSt.matchInput.Value())
	if match == "" {
		m.redactSt.errMsg = "Enter the literal secret text to search for"
		return m, nil
	}
	m.redactScanSeq++
	seq := m.redactScanSeq
	globs := splitGlobs(m.redactSt.globInput.Value())
	repo := m.repo

	m.redactSt.step = redactStepPick
	m.redactSt.scanning = true
	m.redactSt.scanErr = ""

	scan := func() tea.Msg {
		eng := redact.Engine{Repo: repo}
		res, err := eng.Scan(redact.ScanOptions{Match: match, Globs: globs, Cascade: true})
		return redactScanMsg{res: res, err: err, seq: seq}
	}
	return m, tea.Batch(scan, m.spinner.Tick)
}

// updateRedactFlow owns every key while the redact flow has focus
// (focusRedact — see updateMain, app.go). esc is handled centrally here
// (rather than per step) so it always exits the flow regardless of which
// step is showing, per the flow's contract: "esc at any step exits."
func (m Model) updateRedactFlow(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.exitFlow()
		m.setStatus("Redact cancelled", statusWarn)
		return m, nil
	}
	switch m.redactSt.step {
	case redactStepMatch:
		return m.updateRedactMatch(msg)
	case redactStepPick:
		return m.updateRedactPick(msg)
	case redactStepMode:
		return m.updateRedactMode(msg)
	}
	return m, nil
}

// updateRedactMatch drives step 1: tab/shift+tab (or up/down) cycle between
// the match and glob fields, enter submits (kicking off the scan), anything
// else is forwarded to whichever field has focus.
func (m Model) updateRedactMatch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.redactSt
	switch msg.String() {
	// Step 1 has exactly two fields, so "next" and "previous" are the same
	// operation (+1 mod 2 == -1 mod 2) — unlike step 3's redactCycleModeFocus,
	// which takes a real delta because it may have three rows.
	case "tab", "down", "shift+tab", "up":
		st.matchInput.Blur()
		st.globInput.Blur()
		st.fieldFocus = (st.fieldFocus + 1) % 2
		redactFocusMatchField(&st)
		m.redactSt = st
		return m, nil
	case "enter":
		return m.beginRedactScan()
	}
	st.errMsg = ""
	var cmd tea.Cmd
	if st.fieldFocus == 0 {
		st.matchInput, cmd = st.matchInput.Update(msg)
	} else {
		st.globInput, cmd = st.globInput.Update(msg)
	}
	m.redactSt = st
	return m, cmd
}

// redactFocusMatchField focuses whichever step-1 field st.fieldFocus names.
func redactFocusMatchField(st *redactState) {
	if st.fieldFocus == 0 {
		st.matchInput.Focus()
	} else {
		st.globInput.Focus()
	}
}

// updateRedactPick drives step 2: up/down move the row cursor, enter/. toggle
// ±2 context for the current row's occurrence, space toggles the current
// row's checkbox, a/n check all/none, tab advances to step 3. Only esc
// (handled centrally by updateRedactFlow) works while the scan is still in
// flight or has errored — there is nothing to pick yet.
func (m Model) updateRedactPick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.redactSt
	if st.scanning || st.scanErr != "" {
		return m, nil
	}
	switch msg.String() {
	case "up":
		if st.cursor > 0 {
			st.cursor--
		}
	case "down":
		if st.cursor < len(st.rows)-1 {
			st.cursor++
		}
	case "enter", ".":
		if st.cursor < len(st.rows) {
			occIdx := st.rows[st.cursor].occIdx
			st.expanded[occIdx] = !st.expanded[occIdx]
		}
	case " ":
		st = redactToggleRow(st, st.cursor)
	case "a":
		st.sel = capability.AllOccurrencesSelected(st.scanRes)
	case "n":
		st.sel = redactNoneSelected(st.scanRes)
	case "tab":
		if len(st.scanRes.Occurrences) == 0 {
			st.errMsg = "No occurrences found for this match"
		} else {
			st.step = redactStepMode
			st.errMsg = ""
		}
	}
	m.redactSt = st
	return m, nil
}

// redactToggleRow flips the checked state of the row at cursor: the
// occurrence's own checkbox for an intro row, or one Descendants entry for a
// cascade row.
func redactToggleRow(st redactState, cursor int) redactState {
	if cursor < 0 || cursor >= len(st.rows) {
		return st
	}
	row := st.rows[cursor]
	if row.descIdx < 0 {
		st.sel[row.occIdx].Occurrence = !st.sel[row.occIdx].Occurrence
	} else {
		st.sel[row.occIdx].Descendants[row.descIdx] = !st.sel[row.occIdx].Descendants[row.descIdx]
	}
	return st
}

// redactNoneSelected is AllOccurrencesSelected's inverse: every occurrence
// and descendant unchecked, the "n" (none) picker action.
func redactNoneSelected(res redact.ScanResult) []redact.Selection {
	sel := make([]redact.Selection, len(res.Occurrences))
	for i, occ := range res.Occurrences {
		sel[i] = redact.Selection{Descendants: make([]bool, len(occ.Descendants))}
	}
	return sel
}

// redactCheckedCounts sums how many rows (occurrences + descendants) are
// checked, out of the total — the step 2/3 "N/M selected" status line.
func redactCheckedCounts(sel []redact.Selection) (checked, total int) {
	for _, s := range sel {
		total++
		if s.Occurrence {
			checked++
		}
		for _, d := range s.Descendants {
			total++
			if d {
				checked++
			}
		}
	}
	return checked, total
}

// redactAllChecked reports whether every occurrence AND every descendant in
// sel is checked — the allSelected redactChange.apply passes to
// safety.Manager.ApplyRedact (see that function's doc comment for what this
// scope controls) and the condition commandLines uses to decide whether the
// logged command needs the "selection narrowed" note.
func redactAllChecked(sel []redact.Selection) bool {
	checked, total := redactCheckedCounts(sel)
	return checked == total
}

// updateRedactMode drives step 3. enter always stages (regardless of which
// row has focus, mirroring retitleState's modal). tab/shift+tab cycle rows in
// replace mode only (remove mode has nothing to cycle to). Left/right/[/]
// toggle the mode ONLY while the mode row itself has focus (modeFocus == 0):
// gating this on focus — rather than intercepting those keys globally,
// regardless of what has focus — is what keeps them available for their
// ordinary jobs on the pattern/replacement rows (arrow-key cursor movement,
// and '[' ']' as literal regex character-class characters).
func (m Model) updateRedactMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.redactSt
	switch msg.String() {
	case "enter":
		return m.stageRedact()
	case "tab", "down":
		if st.mode == model.RedactReplace {
			st = redactCycleModeFocus(st, 1)
		}
		m.redactSt = st
		return m, nil
	case "shift+tab", "up":
		if st.mode == model.RedactReplace {
			st = redactCycleModeFocus(st, -1)
		}
		m.redactSt = st
		return m, nil
	}
	if st.modeFocus == 0 {
		switch msg.String() {
		case "left", "right", "[", "]":
			st.errMsg = ""
			if st.mode == model.RedactRemove {
				st.mode = model.RedactReplace
			} else {
				st.mode = model.RedactRemove
				st.modeFocus = 0
			}
			m.redactSt = st
		}
		return m, nil
	}
	st.errMsg = ""
	var cmd tea.Cmd
	if st.modeFocus == 1 {
		st.patternInput, cmd = st.patternInput.Update(msg)
	} else {
		st.replaceInput, cmd = st.replaceInput.Update(msg)
	}
	m.redactSt = st
	return m, cmd
}

// redactCycleModeFocus moves step 3's row focus by delta among the
// redactModeFieldCount rows (replace mode only — remove mode never calls
// this), blurring/focusing the pattern/replacement textinputs to match.
func redactCycleModeFocus(st redactState, delta int) redactState {
	st.patternInput.Blur()
	st.replaceInput.Blur()
	st.modeFocus = (st.modeFocus + delta + redactModeFieldCount) % redactModeFieldCount
	switch st.modeFocus {
	case 1:
		st.patternInput.Focus()
	case 2:
		st.replaceInput.Focus()
	}
	return st
}

// stageRedact builds the validated model.RedactSpec from the wizard's
// selection, computes the preview and pre-stage side-history advisory, and
// hands a redactChange to the standard pipeline (mirrors stagePurge).
func (m Model) stageRedact() (tea.Model, tea.Cmd) {
	st := m.redactSt
	match := strings.TrimSpace(st.matchInput.Value())
	globs := splitGlobs(st.globInput.Value())

	var rule model.RedactRule
	if st.mode == model.RedactReplace {
		pattern := st.patternInput.Value()
		if pattern == "" {
			m.redactSt.errMsg = "Replace mode needs a pattern"
			return m, nil
		}
		rule = model.RedactRule{Pattern: pattern, Replacement: st.replaceInput.Value()}
		if _, err := rule.Compile(); err != nil {
			m.redactSt.errMsg = "invalid pattern: " + err.Error()
			return m, nil
		}
	}

	eng := redact.Engine{Repo: m.repo}
	spec, err := eng.Plan(st.scanRes, st.sel, st.mode, rule, match)
	if err != nil {
		m.redactSt.errMsg = err.Error()
		return m, nil
	}

	pv, err := capability.BuildRedactPreview(m.repo, m.plan.Commits, spec)
	if err != nil {
		m.redactSt.errMsg = "build preview: " + err.Error()
		return m, nil
	}

	// Pre-stage side-history check — the CRITICAL caller contract
	// safety.Manager.ApplyRedact's own doc comment describes: when the
	// selection is allSelected (see below) AND the secret also lives in
	// merged side-branch history, the apply's own GLOBAL absence tier will
	// abort LATE (safely — the ref never moves — but with no earlier
	// warning). Checking this now and surfacing it in the PREVIEW (not as a
	// guard block: see redactChange.guard's doc comment) means the operator
	// reads the same coherent warning run_redact prints, before they ever
	// confirm the apply, instead of being ambushed by that late abort.
	sideHistory, err := m.repo.SecretInSideHistory(m.plan.Commits, match)
	if err != nil {
		m.redactSt.errMsg = "check side-branch history: " + err.Error()
		return m, nil
	}

	modeStr, patternStr, withStr := "remove", "", ""
	if st.mode == model.RedactReplace {
		modeStr, patternStr, withStr = "replace", rule.Pattern, rule.Replacement
	}
	// RedactArgMap always represents the FULL headless-equivalent invocation
	// (every occurrence Scan found, cascaded) regardless of how the operator
	// narrowed the picker — see redactChange.commandLines for the note that
	// covers the gap when the two diverge.
	args := capability.RedactArgMap(match, globs, modeStr, patternStr, withStr, true)
	cmd := capability.Render("redact", args)
	diskCmd := capability.Render("redact", capability.MaskSecretArgs(args, match))

	change := redactChange{
		scanRes:     st.scanRes,
		spec:        spec,
		pv:          pv,
		commits:     m.plan.Commits,
		sideHistory: sideHistory,
		allSelected: redactAllChecked(st.sel),
		cmd:         cmd,
		diskCmd:     diskCmd,
	}
	guard := change.guard(m.state)
	if guard.Blocked {
		m.redactSt.errMsg = guard.BlockReason
		return m, nil
	}
	m.pending = change
	m.pendingGuard = guard
	m.pipelineOrigin = modeMain
	m.pipelineApply = true
	m.previewOffset = 0
	m.mode = modePipeline
	return m, nil
}

// --- redactChange: the staged pendingChange (pipeline.go) -------------------

// redactChange is the redact flow's staged, not-yet-applied history rewrite.
// scanRes/pv/commits are snapshots taken at staging time (stageRedact), same
// contract as purgeChange's fields — history cannot change while the
// pipeline overlay is open, so apply just replays the stored values.
type redactChange struct {
	scanRes redact.ScanResult
	spec    model.RedactSpec
	pv      capability.RedactPreview
	commits []model.Commit
	// sideHistory is gitio.SecretInSideHistory's result, computed once at
	// staging time (see stageRedact) — surfaced by preview() as a warning,
	// never by guard() as a block (see guard's doc comment).
	sideHistory bool
	// allSelected is true iff the operator's picker selection covers every
	// occurrence AND every descendant Scan found (redactAllChecked) — passed
	// through to safety.Manager.ApplyRedact, whose doc comment explains what
	// this scope controls.
	allSelected bool
	// cmd is the CLI-equivalent line for this redact, rendered once at
	// staging time from the FULL scan configuration (see stageRedact) —
	// independent of allSelected, since RedactArgMap always represents every
	// occurrence Scan found (see commandLines for the narrowing note this
	// implies).
	cmd string
	// diskCmd is cmd with the secret masked (capability.MaskSecretArgs). It is
	// the only form written to .git/regit/command.log, which outlives the
	// session; the in-memory panel keeps the runnable cmd.
	diskCmd string
}

func (c redactChange) title() string {
	return fmt.Sprintf("Redact %q across %d path(s)", c.spec.Match, len(c.spec.Targets))
}

// commandLines returns the rendered redact command verbatim, plus — only
// when the operator's picker selection was narrower than "every occurrence
// Scan found" — an honest "#"-prefixed note that the logged command
// over-approximates what was actually staged. There is no headless flag for
// "redact exactly these hand-picked lines", so the logged command is the
// closest reproducible equivalent, not a byte-for-byte replay of the TUI
// selection; the note exists so that gap is never silently misleading.
func (c redactChange) commandLines() []string {
	if c.cmd == "" {
		return nil
	}
	lines := []string{c.cmd}
	if !c.allSelected {
		lines = append(lines, "# selection narrowed in TUI; the logged command redacts ALL occurrences")
	}
	return lines
}

// diskCommandLines implements diskLogger: the persistent log gets the masked
// line so the secret never lands in a file regit leaves behind.
func (c redactChange) diskCommandLines() []string {
	if c.diskCmd == "" {
		return nil
	}
	return []string{c.diskCmd}
}

// guard checks base<0 ("nothing to redact"), then mirrors
// safety.Manager.ApplyRedact's own Guards closure (merge, dirty, pushed) —
// the would-empty-branch check below is NOT part of that Guards closure
// (ApplyRedact only discovers it deep in its Rewrite closure, via
// newHead == ""); it is pre-computed here the same way run_redact.go's own
// headless pre-check does, from the SAME pv.Base/pv.Dropped
// BuildRedactPreview already produced, so a blocked redact is caught before
// ever staging the apply — same rationale as purgeChange.guard.
//
// Unlike purgeChange (which hard-blocks on c.sidePaths), c.sideHistory is
// DELIBERATELY not checked here: ApplyRedact's own doc comment explains why
// redact treats side-branch history as advisory rather than a hard block
// (the operator is told up front, but the attempt still proceeds; matching
// run_redact's headless contract) — the same content preview() surfaces as a
// warning line, not a guard.BlockReason.
func (c redactChange) guard(state model.RepoState) safety.Guard {
	if c.pv.Base < 0 {
		return safety.Guard{Blocked: true, BlockReason: "no commit's snapshot contains the target content"}
	}
	for _, cm := range c.pv.Affected {
		if cm.IsMerge {
			return safety.Guard{Blocked: true, BlockReason: "cannot redact across a merge commit"}
		}
	}
	if state.IsDirty {
		return safety.Guard{Blocked: true,
			BlockReason: "working tree has uncommitted changes; commit or stash them first"}
	}
	if c.pv.Base == 0 && len(c.pv.Dropped) == len(c.pv.Affected) {
		return safety.Guard{Blocked: true, BlockReason: "redact would remove every commit on the branch"}
	}
	if model.AnyPushed(c.pv.Affected) {
		return safety.Guard{
			NeedsConfirmation: true,
			ConfirmWord:       state.Branch,
			ConfirmReason: "some commits already exist on a remote; redacting rewrites " +
				"history and will require a force-push",
		}
	}
	return safety.Guard{}
}

// narrowedSelectionPreviewNote warns that the occurrence/descendant lines
// capability.RedactPreviewLines lists (verbatim from printRedactPreview,
// which has no notion of the picker's per-row vetoes — it prints every
// occurrence Scan found) include some the operator left UNCHECKED. Without
// this, a narrowed selection would render identically to a full one, letting
// the operator mistake "listed in the preview" for "will be redacted".
const narrowedSelectionPreviewNote = "⚠ some occurrence/descendant line(s) listed above were left " +
	"UNCHECKED in the picker and will NOT be redacted by this apply."

// preview renders the SAME lines run_redact's dry-run prints
// (capability.RedactPreviewLines — a formatting-target change over
// printRedactPreview, not a re-implementation, see its doc comment), plus two
// conditional notes:
//
//   - narrowedSelectionPreviewNote, whenever the picker selection is
//     narrower than the full scan (see narrowedSelectionPreviewNote's doc).
//   - the side-history advisory, but ONLY when c.allSelected: SideHistoryAdvisory
//     is worded to predict safety.Manager.ApplyRedact's GLOBAL absence tier
//     aborting — and that tier is the ONE part of verifyRedactAbsence gated
//     on allSelected (see its doc comment: "if !allSelected { return nil }").
//     Showing this note for a narrowed selection would claim an abort that
//     will NOT happen — the apply would actually succeed, silently leaving
//     the side-branch copy unaddressed — so it is scoped to exactly the
//     condition under which the predicted abort is real. See guard's doc
//     comment for why this note lives here (a warning) rather than in guard
//     (a block) even when it does apply.
func (c redactChange) preview(st styles) previewModel {
	lines := capability.RedactPreviewLines(c.scanRes, c.spec, c.pv)
	if !c.allSelected {
		lines = append(lines, "", st.statusWarn.Render(narrowedSelectionPreviewNote))
	}
	if c.sideHistory && c.allSelected {
		lines = append(lines, "", st.statusWarn.Render(capability.SideHistoryAdvisory))
	}
	return previewModel{title: c.title(), lines: lines}
}

func (c redactChange) apply(mgr *safety.Manager, state model.RepoState, opts safety.ApplyOptions) (safety.ApplyResult, error) {
	return mgr.ApplyRedact(c.commits, c.spec, c.allSelected, state, opts)
}

// --- Rendering ---------------------------------------------------------------

// viewRedactFlow renders the flow zone's redact widget (embedded, not an
// overlay — see this file's top doc comment). The scanning spinner is
// spliced in as a post-processing step, mirroring purgeWizardBody/squashWizardBody: the
// pure renderRedactFlow stays testable without a live spinner.
func (m Model) viewRedactFlow() string {
	width := m.layout().flowWidth
	body := renderRedactFlow(m.redactSt, m.st)
	if m.redactSt.scanning {
		body = strings.ReplaceAll(body, "scanning history…", m.spinner.View()+" scanning history…")
	}
	return m.st.paneActive.Width(width).Render(body)
}

func renderRedactFlow(st redactState, s styles) string {
	switch st.step {
	case redactStepMatch:
		return renderRedactMatch(st, s)
	case redactStepPick:
		return renderRedactPick(st, s)
	case redactStepMode:
		return renderRedactMode(st, s)
	}
	return ""
}

// redactRowTruncWidth bounds a picker row's rendered text so it stays inside
// the flow zone at its narrowest (flowMinWidth, layout.go) rather than
// wrapping and breaking the frame.
const redactRowTruncWidth = 40

// redactFieldHints explains the focused step-1 field, indexed by fieldFocus.
var redactFieldHints = [2]string{
	"The exact text of the secret, as it appears in your files. Not a pattern.",
	"Optional. Comma-separated globs such as *.env, config/** — leave empty to search every file.",
}

func renderRedactMatch(st redactState, s styles) string {
	var b strings.Builder
	b.WriteString(s.modalTitle.Render("REDACT — find a secret") + "\n\n")
	cursors := [2]string{"  ", "  "}
	cursors[st.fieldFocus] = s.listCursor.Render("▸ ")
	fmt.Fprintf(&b, "%s%-*s %s\n", cursors[0], redactLabelWidth, "Secret to find", st.matchInput.View())
	fmt.Fprintf(&b, "%s%-*s %s\n", cursors[1], redactLabelWidth, "Only in files", st.globInput.View())
	b.WriteString("\n" + s.dim.Render(redactFieldHints[st.fieldFocus]) + "\n")
	b.WriteString("\n" + s.dim.Render("Next, regit lists every commit that added or removed this text, and you choose which ones to clean.") + "\n")
	if st.errMsg != "" {
		b.WriteString("\n" + s.statusErr.Render("⚠ "+st.errMsg) + "\n")
	}
	b.WriteString("\n" + s.help.Render("tab next field · enter scan history · esc cancel"))
	return b.String()
}

func renderRedactPick(st redactState, s styles) string {
	var b strings.Builder
	b.WriteString(s.modalTitle.Render("REDACT — occurrences") + "\n\n")
	switch {
	case st.scanning:
		b.WriteString(s.dim.Render("scanning history…") + "\n")
	case st.scanErr != "":
		b.WriteString(s.statusErr.Render("scan failed: "+st.scanErr) + "\n")
	case len(st.scanRes.Occurrences) == 0:
		b.WriteString(s.dim.Render("no occurrences found") + "\n")
	default:
		for i, row := range st.rows {
			b.WriteString(renderRedactRow(st, row, i == st.cursor, s) + "\n")
			if row.descIdx < 0 && st.expanded[row.occIdx] {
				for _, ctxLine := range st.scanRes.Occurrences[row.occIdx].Context {
					b.WriteString("        " + s.dim.Render(truncate(termsafe.Line(ctxLine), redactRowTruncWidth)) + "\n")
				}
			}
		}
		checked, total := redactCheckedCounts(st.sel)
		b.WriteString("\n" + s.dim.Render(fmt.Sprintf("%d/%d row(s) selected", checked, total)) + "\n")
	}
	if notes := redactDimNotes(st.scanRes); notes != "" {
		b.WriteString("\n" + s.dim.Render(notes) + "\n")
	}
	if st.errMsg != "" {
		b.WriteString("\n" + s.statusErr.Render("⚠ "+st.errMsg) + "\n")
	}
	b.WriteString("\n" + s.help.Render("↑/↓ move · space toggle · enter/. context · a/n all/none · tab continue · esc cancel"))
	return b.String()
}

// renderRedactRow renders one picker row: a checkbox, then "short-hash ·
// path · line", all history-derived text sanitized through termsafe.Line for
// display (logging is verbatim by user decision — see stageRedact's
// RedactArgMap call — but the picker's rendered rows are terminal output like
// every other commit-derived text in this UI).
func renderRedactRow(st redactState, row redactRow, isCursor bool, s styles) string {
	occ := st.scanRes.Occurrences[row.occIdx]
	var checked bool
	var hash, path, line string
	indent := "  "
	if row.descIdx < 0 {
		checked = st.sel[row.occIdx].Occurrence
		hash, path, line = occ.Intro.Commit, occ.Intro.Path, occ.Intro.Line
	} else {
		d := occ.Descendants[row.descIdx]
		checked = st.sel[row.occIdx].Descendants[row.descIdx]
		hash, path, line = d.Commit, d.Path, d.Line
		indent = "    ↳ "
	}
	box := "[ ]"
	if checked {
		box = "[x]"
	}
	text := fmt.Sprintf("%s · %s · %s", short(hash), termsafe.Line(path), termsafe.Line(line))
	rendered := fmt.Sprintf("%s%s %s", indent, box, truncate(text, redactRowTruncWidth))
	if isCursor {
		return s.listCursor.Render("▸ " + rendered)
	}
	return "  " + rendered
}

// redactDimNotes collects chain-end notes (a cascade that could not be
// tracked further, see model.RedactOccurrence.ChainNote) and binary-only
// hits (never redactable — no text line to act on) into one dimmed block at
// the bottom of the picker.
func redactDimNotes(res redact.ScanResult) string {
	var lines []string
	for _, occ := range res.Occurrences {
		if occ.ChainNote != "" {
			lines = append(lines, "note: "+truncate(termsafe.Line(occ.ChainNote), 70))
		}
	}
	for _, b := range res.Binary {
		lines = append(lines, "binary (not redactable): "+truncate(termsafe.Line(b), 60))
	}
	return strings.Join(lines, "\n")
}

func renderRedactMode(st redactState, s styles) string {
	var b strings.Builder
	b.WriteString(s.modalTitle.Render("REDACT — mode") + "\n\n")
	modeCursor := "  "
	if st.modeFocus == 0 {
		modeCursor = s.listCursor.Render("▸ ")
	}
	remove, replace := "remove", "replace"
	if st.mode == model.RedactRemove {
		remove = s.selected.Render(remove)
	} else {
		replace = s.selected.Render(replace)
	}
	b.WriteString(fmt.Sprintf("%sMode: [ %s | %s ]\n", modeCursor, remove, replace))
	if st.mode == model.RedactReplace {
		cursors := [2]string{"  ", "  "}
		if st.modeFocus >= 1 {
			cursors[st.modeFocus-1] = s.listCursor.Render("▸ ")
		}
		fmt.Fprintf(&b, "%s%-*s %s\n", cursors[0], redactLabelWidth, "Pattern:", st.patternInput.View())
		fmt.Fprintf(&b, "%s%-*s %s\n", cursors[1], redactLabelWidth, "Replacement:", st.replaceInput.View())
		if example := redactLiveExample(st); example != "" {
			b.WriteString(s.dim.Render(example) + "\n")
		}
	}
	checked, _ := redactCheckedCounts(st.sel)
	b.WriteString(s.dim.Render(fmt.Sprintf("%d row(s) staged", checked)) + "\n")
	if st.errMsg != "" {
		b.WriteString("\n" + s.statusErr.Render("⚠ "+st.errMsg) + "\n")
	}
	b.WriteString("\n" + s.help.Render("←/→ or [/] mode · tab field · enter stage · esc cancel"))
	return b.String()
}

// redactLiveExample renders a one-line before→after preview of the replace
// rule against the first CHECKED occurrence/descendant's line — reuses
// retitleState's live-preview approach (retitle.go's retitlePreview): show
// the operator the actual effect of what they typed before they stage it.
func redactLiveExample(st redactState) string {
	pattern := st.patternInput.Value()
	if pattern == "" {
		return ""
	}
	rule := model.RedactRule{Pattern: pattern, Replacement: st.replaceInput.Value()}
	re, err := rule.Compile()
	if err != nil {
		return "invalid pattern: " + truncate(err.Error(), 40)
	}
	line, ok := redactFirstCheckedLine(st)
	if !ok {
		return ""
	}
	after := re.ReplaceAllString(line, rule.Replacement)
	return truncate(termsafe.Line(line), 28) + " → " + truncate(termsafe.Line(after), 28)
}

// redactFirstCheckedLine returns the line content of the first checked row
// (occurrence intro or descendant, in scan order) — the example line
// redactLiveExample previews the replace rule against.
func redactFirstCheckedLine(st redactState) (string, bool) {
	for i, occ := range st.scanRes.Occurrences {
		if i >= len(st.sel) {
			break
		}
		if st.sel[i].Occurrence {
			return occ.Intro.Line, true
		}
		for j, d := range occ.Descendants {
			if j < len(st.sel[i].Descendants) && st.sel[i].Descendants[j] {
				return d.Line, true
			}
		}
	}
	return "", false
}
