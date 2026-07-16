package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/termsafe"
)

// purgeChange is the staged purge that drops into the apply pipeline: the
// validated spec, the affected-commit set, and the shared preview.
type purgeChange struct {
	spec     model.PurgeSpec
	affected map[string]bool
	pv       capability.PurgePreview
	commits  []model.Commit
	cmd      string
	// base is the corrected rewrite boundary — gitio.PurgeBase's result,
	// computed once at stage time (see stagePurge) — into commits, the SAME
	// value gitio.ApplyPurge and safety.Manager.ApplyPurge independently
	// recompute for the actual apply. guard() uses this stored value instead
	// of re-deriving a naive (uncorrected) boundary of its own, so the
	// wizard's pre-stage guard and the real apply can never disagree about
	// where the rewritten suffix starts (see gitio.PurgeBase's doc comment
	// for why the naive boundary alone is not safe to trust).
	base int
	// sidePaths holds any spec.Paths entries gitio.PathsInSideHistory found
	// touched by a merged-in side branch's own history (including a "pocket":
	// added then removed within the side branch before it merged) — computed
	// once at stage time alongside base. regit can never purge these, so
	// guard() blocks on them the same way the safety/capability layers do.
	sidePaths []string
}

func (c purgeChange) commandLines() []string {
	if c.cmd == "" {
		return nil
	}
	return []string{c.cmd}
}

func (c purgeChange) title() string {
	return fmt.Sprintf("Purge %d path(s) from %d commit(s)", len(c.spec.Paths), len(c.pv.Affected))
}

// guard mirrors safety.Manager.ApplyPurge's checks so the pipeline can block
// or ask for typed confirmation BEFORE applying. It uses c.base — the
// gitio.PurgeBase-corrected boundary stagePurge already computed — rather
// than re-deriving a naive firstAffected of its own: a naive boundary can
// miss a merge commit that git's own pathspec-limited rev-list simplified
// out of the affected set while the merge's tree still carries the purge
// path forward (see gitio.PurgeBase's doc comment), which would let this
// pre-stage guard wave a purge through that the real apply must block.
func (c purgeChange) guard(state model.RepoState) safety.Guard {
	if c.base < 0 {
		return safety.Guard{Blocked: true, BlockReason: "no commits touch the selected paths"}
	}
	suffix := c.commits[c.base:]
	for _, cm := range suffix {
		if cm.IsMerge {
			return safety.Guard{Blocked: true, BlockReason: "cannot purge across a merge commit"}
		}
	}
	// Checked AFTER the merge-in-suffix loop above on purpose — see
	// safety.Manager.ApplyPurge's Guards closure for why this check is a
	// superset of that one (a side-branch-sourced path always has that
	// branch's own add commit off the first-parent chain), so ordering it
	// second keeps the merge guard's more specific message for the case it
	// already covers.
	if len(c.sidePaths) > 0 {
		return safety.Guard{Blocked: true, BlockReason: fmt.Sprintf(
			"%s also exists in a merged side branch's history, which regit cannot rewrite — see git filter-repo",
			strings.Join(c.sidePaths, ", "))}
	}
	if state.IsDirty {
		return safety.Guard{Blocked: true,
			BlockReason: "working tree has uncommitted changes; commit or stash them first"}
	}
	if len(c.pv.Dropped) >= len(suffix) && c.base == 0 {
		return safety.Guard{Blocked: true, BlockReason: "purge would remove every commit on the branch"}
	}
	if model.AnyPushed(suffix) {
		return safety.Guard{
			NeedsConfirmation: true,
			ConfirmWord:       state.Branch,
			ConfirmReason: "some commits already exist on a remote; purging rewrites " +
				"history and will require a force-push",
		}
	}
	return safety.Guard{}
}

// preview: paths first, then affected commits newest-first with drop markers,
// then the reparented count. Directory paths render with a trailing "/" and
// are never expanded.
func (c purgeChange) preview(st styles) previewModel {
	var lines []string
	for _, p := range c.spec.Paths {
		lines = append(lines, st.newDate.Render("  remove "+p))
	}
	lines = append(lines, "")
	for i := len(c.pv.Affected) - 1; i >= 0; i-- {
		cm := c.pv.Affected[i]
		// cm.Subject is the repo-derived commit subject; sanitize for DISPLAY
		// ONLY (c.pv.Dropped stays keyed on cm.Hash, never on this text).
		row := fmt.Sprintf("%s  %s", short(cm.Hash), truncate(termsafe.Line(cm.Subject), 40))
		if c.pv.Dropped[cm.Hash] {
			lines = append(lines, st.statusErr.Render("  ✖ "+row+"  (will be dropped)"))
		} else {
			lines = append(lines, st.newDate.Render("    "+row))
		}
	}
	if c.pv.Reparented > 0 {
		lines = append(lines, st.dim.Render(fmt.Sprintf(
			"  … plus %d later commit(s) carried forward (their own changes untouched)", c.pv.Reparented)))
	}
	return previewModel{title: c.title(), lines: lines}
}

func (c purgeChange) apply(mgr *safety.Manager, state model.RepoState, opts safety.ApplyOptions) (safety.ApplyResult, error) {
	return mgr.ApplyPurge(c.commits, c.spec, c.affected, state, opts)
}

// --- Purge finder wizard (modePurge) ----------------------------------------

// purgeState is the live finder state: the async scan, the query input, the
// filtered matches, and the multi-selection.
type purgeState struct {
	scanning bool
	index    *gitio.PathIndex
	scanErr  string
	query    textinput.Model
	matches  []string
	cursor   int
	selected map[string]bool
	errMsg   string

	// scanSeq is a per-invocation generation token, incremented by beginPurge
	// before every dispatch. A purgeScanMsg is only accepted if its seq still
	// matches, so a result from an abandoned scan (the wizard was reopened,
	// starting a fresh one) is dropped instead of clobbering the current
	// generation's already-applied result — mirrors splitGenSeq's guard for
	// splitResultMsg (see Update's splitResultMsg case).
	scanSeq int
}

// purgeScanMsg delivers the async history scan (or its failure). seq ties the
// result back to the beginPurge invocation that started it; see purgeState.scanSeq.
type purgeScanMsg struct {
	index *gitio.PathIndex
	err   error
	seq   int
}

// newPurgeState builds the wizard state with the scan already marked in
// flight and the query field focused, following the retitle/reauthor
// textinput conventions (no prompt, width from the caller's layout).
func newPurgeState(inputW int) purgeState {
	q := textinput.New()
	q.Prompt = ""
	q.CharLimit = 120
	q.Width = inputW
	q.Focus()
	return purgeState{scanning: true, query: q, selected: map[string]bool{}}
}

// beginPurge opens the finder and kicks off the history scan off-thread.
// The scan sequence is bumped from the outgoing state so a late result from a
// prior invocation (the wizard was reopened before its scan finished) is
// recognizable as stale once the new one lands.
func (m Model) beginPurge() (tea.Model, tea.Cmd, bool) {
	seq := m.purgeSt.scanSeq + 1
	m.purgeSt = newPurgeState(computeLayout(m.width, m.height).inputWidth)
	m.purgeSt.scanSeq = seq
	m.mode = modePurge
	repo := m.repo
	scan := func() tea.Msg {
		idx, err := repo.ScanHistoryPaths()
		return purgeScanMsg{index: idx, err: err, seq: seq}
	}
	return m, tea.Batch(scan, m.spinner.Tick), true
}

// refreshMatches recomputes the filtered match list for the current query.
func (st purgeState) refreshMatches() purgeState {
	if st.index == nil {
		st.matches = nil
		return st
	}
	q := strings.TrimSpace(st.query.Value())
	if q == "" {
		st.matches = nil
		st.cursor = 0
		return st
	}
	st.matches = st.index.Match(q)
	if st.cursor >= len(st.matches) {
		st.cursor = 0
	}
	return st
}

// updatePurge: typing edits the query (textinput owns cursor/paste), ↑↓ move
// the match cursor, space toggles the row plus its rename chain, enter stages,
// esc cancels. Errors render inline (the status bar is hidden by this modal).
func (m Model) updatePurge(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.purgeSt
	switch msg.String() {
	case "esc":
		m.mode = modeMain
		m.setStatus("Purge cancelled", statusWarn)
		return m, nil
	case "up":
		if st.cursor > 0 {
			st.cursor--
		}
		m.purgeSt = st
		return m, nil
	case "down":
		if st.cursor < len(st.matches)-1 {
			st.cursor++
		}
		m.purgeSt = st
		return m, nil
	case " ":
		if st.cursor < len(st.matches) {
			p := st.matches[st.cursor]
			on := !st.selected[p]
			for _, member := range st.index.RenameChain(p) {
				st.selected[member] = on
			}
			st.selected[p] = on
			st.errMsg = ""
		}
		m.purgeSt = st
		return m, nil
	case "enter":
		return m.stagePurge()
	}
	st.errMsg = ""
	var cmd tea.Cmd
	st.query, cmd = st.query.Update(msg)
	m.purgeSt = st.refreshMatches()
	return m, cmd
}

// stagePurge validates the selection, computes the affected set and preview,
// and hands a purgeChange to the standard pipeline.
func (m Model) stagePurge() (tea.Model, tea.Cmd) {
	st := m.purgeSt
	var paths []string
	for p, on := range st.selected {
		if on {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		m.purgeSt.errMsg = "Select at least one path (space toggles)"
		return m, nil
	}
	spec := model.PurgeSpec{Paths: paths}
	if err := spec.Validate(); err != nil {
		m.purgeSt.errMsg = err.Error()
		return m, nil
	}
	affected, err := m.repo.AffectedCommits(spec.Paths)
	if err != nil {
		m.purgeSt.errMsg = "find affected commits: " + err.Error()
		return m, nil
	}
	if len(affected) == 0 {
		m.purgeSt.errMsg = "No commits touch the selected paths"
		return m, nil
	}
	pv, err := capability.BuildPurgePreview(m.repo, m.plan.Commits, spec, affected)
	if err != nil {
		m.purgeSt.errMsg = "build preview: " + err.Error()
		return m, nil
	}
	// base is the SAME corrected rewrite boundary gitio.ApplyPurge and
	// safety.Manager.ApplyPurge independently recompute at apply time (see
	// gitio.PurgeBase's doc comment); computing and storing it here lets
	// purgeChange.guard use it instead of a naive boundary of its own.
	base, err := m.repo.PurgeBase(m.plan.Commits, spec.Paths, affected)
	if err != nil {
		m.purgeSt.errMsg = "compute purge base: " + err.Error()
		return m, nil
	}
	// sidePaths mirrors the same up-front check safety.Manager.ApplyPurge and
	// runPurge perform (see gitio.PathsInSideHistory's doc comment): a path
	// with history on a merged-in side branch — including a "pocket" that
	// nets to zero by merge time — can never be purged, since regit only
	// rewrites the first-parent chain.
	sidePaths, err := m.repo.PathsInSideHistory(m.plan.Commits, spec.Paths)
	if err != nil {
		m.purgeSt.errMsg = "check side-branch history: " + err.Error()
		return m, nil
	}
	change := purgeChange{spec: spec, affected: affected, pv: pv, commits: m.plan.Commits, base: base, sidePaths: sidePaths,
		cmd: capability.Render("purge", capability.PurgeArgMap(paths))}
	guard := change.guard(m.state)
	if guard.Blocked {
		m.purgeSt.errMsg = guard.BlockReason
		return m, nil
	}
	m.pending = change
	m.pendingGuard = guard
	m.pipelineOrigin = modePurge
	m.mode = modePipeline
	m.pipelineApply = true
	m.previewOffset = 0
	return m, nil
}

// renderPurgeWizard renders the finder modal body (pure, testable). dirs is
// the set of paths the index classifies as directories (gitio.PathIndex.Dirs,
// as a lookup set) — the authoritative source for the trailing "/" suffix.
// AddedIn membership is NOT a valid stand-in: a rename destination is never a
// plain "A" add, so checking AddedIn instead would mislabel it as a directory.
func renderPurgeWizard(st purgeState, dirs map[string]bool, s styles) string {
	var b strings.Builder
	b.WriteString(s.modalTitle.Render("PURGE FROM HISTORY") + "\n\n")
	b.WriteString("Find    " + st.query.View() + "\n\n")
	switch {
	case st.scanning:
		b.WriteString(s.dim.Render("scanning history…") + "\n")
	case st.scanErr != "":
		b.WriteString(s.statusErr.Render("scan failed: "+st.scanErr) + "\n")
	case len(st.matches) == 0:
		b.WriteString(s.dim.Render("type to search files and directories ever added") + "\n")
	default:
		selected := 0
		for _, on := range st.selected {
			if on {
				selected++
			}
		}
		for i, p := range st.matches {
			check := "[ ]"
			if st.selected[p] {
				check = "[x]"
			}
			row := p
			if dirs[p] {
				row += "/"
			}
			// row is a history-derived path (plus the synthetic "/" suffix);
			// sanitize for DISPLAY ONLY. st.selected/RenameChain lookups
			// elsewhere in this function key off the raw p, never this copy.
			line := fmt.Sprintf("%s %s", check, truncate(termsafe.Line(row), 56))
			if chain := st.index.RenameChain(p); len(chain) > 1 {
				line += s.dim.Render(" (rename chain)")
			}
			if i == st.cursor {
				line = s.listCursor.Render("▸ " + line)
			} else {
				line = "  " + line
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n" + s.dim.Render(fmt.Sprintf("%d path(s) selected", selected)) + "\n")
	}
	if st.errMsg != "" {
		b.WriteString("\n" + s.statusErr.Render("⚠ "+st.errMsg))
	}
	b.WriteString("\n" + s.help.Render("type to search · ↑↓ move · space toggle · enter stage · esc cancel"))
	return b.String()
}

// purgeDirSet builds the directory-membership lookup renderPurgeWizard needs
// from a scanned index's sorted Dirs slice. Shared by viewPurge and tests so
// the set-building logic lives in exactly one place.
func purgeDirSet(idx *gitio.PathIndex) map[string]bool {
	if idx == nil {
		return nil
	}
	dirs := make(map[string]bool, len(idx.Dirs))
	for _, d := range idx.Dirs {
		dirs[d] = true
	}
	return dirs
}

// viewPurge renders the wizard overlay. renderPurgeWizard is a pure function
// (used directly in tests without a spinner), so the shared spinner is
// spliced in here as a post-processing step, mirroring viewSquash: while the
// scan is in flight its static "scanning history…" line is swapped for the
// ticking frame.
func (m Model) viewPurge() string {
	body := renderPurgeWizard(m.purgeSt, purgeDirSet(m.purgeSt.index), m.st)
	if m.purgeSt.scanning {
		body = strings.ReplaceAll(body, "scanning history…", m.spinner.View()+" scanning history…")
	}
	return m.st.modal.Render(body)
}
