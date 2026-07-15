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
)

// purgeChange is the staged purge that drops into the apply pipeline: the
// validated spec, the affected-commit set, and the shared preview.
type purgeChange struct {
	spec     model.PurgeSpec
	affected map[string]bool
	pv       capability.PurgePreview
	commits  []model.Commit
	cmd      string
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
// or ask for typed confirmation BEFORE applying.
func (c purgeChange) guard(state model.RepoState) safety.Guard {
	firstAffected := -1
	for i, cm := range c.commits {
		if c.affected[cm.Hash] {
			firstAffected = i
			break
		}
	}
	if firstAffected < 0 {
		return safety.Guard{Blocked: true, BlockReason: "no commits touch the selected paths"}
	}
	suffix := c.commits[firstAffected:]
	for _, cm := range suffix {
		if cm.IsMerge {
			return safety.Guard{Blocked: true, BlockReason: "cannot purge across a merge commit"}
		}
	}
	if state.IsDirty {
		return safety.Guard{Blocked: true,
			BlockReason: "working tree has uncommitted changes; commit or stash them first"}
	}
	if len(c.pv.Dropped) >= len(suffix) && firstAffected == 0 {
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
		row := fmt.Sprintf("%s  %s", short(cm.Hash), truncate(cm.Subject, 40))
		if c.pv.Dropped[cm.Hash] {
			lines = append(lines, st.statusErr.Render("  ✖ "+row+"  (will be dropped)"))
		} else {
			lines = append(lines, st.newDate.Render("    "+row))
		}
	}
	if c.pv.Reparented > 0 {
		lines = append(lines, st.dim.Render(fmt.Sprintf("  … plus %d later commit(s) reparented unchanged", c.pv.Reparented)))
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
}

// purgeScanMsg delivers the async history scan (or its failure).
type purgeScanMsg struct {
	index *gitio.PathIndex
	err   error
}

// newPurgeState builds the wizard state with the scan already marked in
// flight and the query field focused, following the retitle/reauthor
// textinput conventions (no prompt, fixed width).
func newPurgeState() purgeState {
	q := textinput.New()
	q.Prompt = ""
	q.CharLimit = 120
	q.Width = 48
	q.Focus()
	return purgeState{scanning: true, query: q, selected: map[string]bool{}}
}

// beginPurge opens the finder and kicks off the history scan off-thread.
func (m Model) beginPurge() (tea.Model, tea.Cmd, bool) {
	m.purgeSt = newPurgeState()
	m.mode = modePurge
	repo := m.repo
	scan := func() tea.Msg {
		idx, err := repo.ScanHistoryPaths()
		return purgeScanMsg{index: idx, err: err}
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
	change := purgeChange{spec: spec, affected: affected, pv: pv, commits: m.plan.Commits,
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

// renderPurgeWizard renders the finder modal body (pure, testable).
func renderPurgeWizard(st purgeState, idxAdded map[string]string, s styles) string {
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
			if _, isFile := idxAdded[p]; !isFile {
				row += "/"
			}
			line := fmt.Sprintf("%s %s", check, truncate(row, 56))
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

// viewPurge frames the wizard in the modal style.
func (m Model) viewPurge() string {
	var added map[string]string
	if m.purgeSt.index != nil {
		added = m.purgeSt.index.AddedIn
	}
	return m.st.modal.Render(renderPurgeWizard(m.purgeSt, added, m.st))
}
