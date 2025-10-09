package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
)

// reauthorChange is the staged author/committer rewrite that drops into the apply
// pipeline. It wraps a plan built at staging time (history cannot change while the
// wizard is open) plus the set of edited hashes for the preview.
type reauthorChange struct {
	plan    *model.Plan
	targets map[string]bool
}

// newReauthorChange builds the plan: an identity Edit on each target commit, with
// the base set to the oldest edited commit so the rewrite replays from there.
func newReauthorChange(commits []model.Commit, targets map[string]bool, name, email string, committer bool) reauthorChange {
	plan := model.NewPlan(commits)
	edited := map[string]bool{}
	minIdx := len(commits)
	for i, c := range commits {
		if !targets[c.Hash] {
			continue
		}
		e := model.NewIdentityEdit(c, name, email, committer)
		if e.IsEmpty() {
			continue
		}
		plan.Edits[c.Hash] = e
		edited[c.Hash] = true
		if i < minIdx {
			minIdx = i
		}
	}
	if minIdx < len(commits) {
		plan.SetBase(minIdx) // freezes older history; keeps the edits at/after base
	}
	return reauthorChange{plan: plan, targets: edited}
}

func (c reauthorChange) title() string {
	return fmt.Sprintf("Change author on %d commit(s)", len(c.targets))
}

// guard reuses the standard plan guard: dirty tree / out-of-order block, pushed
// history needs the typed confirmation.
func (c reauthorChange) guard(state model.RepoState) safety.Guard {
	return safety.CheckGuards(state, c.plan)
}

// preview lists each edited commit (newest-first) as old → new author; unedited
// rows are dimmed. When the edit also changes the committer identity, a second
// indented line is appended showing old → new committer.
func (c reauthorChange) preview(st styles) previewModel {
	lines := make([]string, 0, len(c.plan.Commits))
	for i := len(c.plan.Commits) - 1; i >= 0; i-- {
		commit := c.plan.Commits[i]
		e, ok := c.plan.EditFor(commit.Hash)
		if !ok {
			text := fmt.Sprintf("%s  %-28s %s <%s>", short(commit.Hash),
				truncate(commit.Subject, 28), commit.AuthorName, commit.AuthorEmail)
			lines = append(lines, st.dim.Render("  "+text))
			continue
		}
		authorLine := fmt.Sprintf("%s  %-28s %s <%s> → %s <%s>", short(commit.Hash),
			truncate(commit.Subject, 28),
			commit.AuthorName, commit.AuthorEmail,
			e.EffectiveAuthorName(commit), e.EffectiveAuthorEmail(commit))
		lines = append(lines, st.newDate.Render("  "+authorLine))
		if e.NewCommitterName != nil || e.NewCommitterEmail != nil {
			committerLine := fmt.Sprintf("%s  %-28s %s <%s> → %s <%s>",
				strings.Repeat(" ", len(short(commit.Hash))),
				"(committer)",
				commit.CommitterName, commit.CommitterEmail,
				e.EffectiveCommitterName(commit), e.EffectiveCommitterEmail(commit))
			lines = append(lines, st.newDate.Render("  "+committerLine))
		}
	}
	return previewModel{title: c.title(), lines: lines}
}

func (c reauthorChange) apply(mgr *safety.Manager, state model.RepoState, opts safety.ApplyOptions) (safety.ApplyResult, error) {
	return mgr.Apply(c.plan, state, opts)
}

// --- Change-author wizard (modeReauthor) -----------------------------------

// reauthorField identifies the focused field in the wizard.
type reauthorField int

const (
	fldRName reauthorField = iota
	fldREmail
	fldRCommitter
)

// reauthorFieldCount bounds the tab cycle.
const reauthorFieldCount = 3

// reauthorState holds the live wizard state: the edited name/email buffers, the
// committer checkbox, the focused field, and the captured target commits.
type reauthorState struct {
	nameBuf      string
	emailBuf     string
	committerToo bool
	field        reauthorField
	targets      []model.Commit // root-first; targets[0] is the oldest
}

// reauthorTargetCommits returns the commits the wizard will act on: the Set/Range
// selection when present, otherwise the single cursor commit. Root-first.
func reauthorTargetCommits(m Model) []model.Commit {
	hashes := m.selectedHashSet()
	if len(hashes) == 0 {
		if len(m.plan.Commits) == 0 {
			return nil
		}
		hashes = map[string]bool{m.commitAtCursor().Hash: true}
	}
	out := make([]model.Commit, 0, len(hashes))
	for _, c := range m.plan.Commits { // preserve root-first order
		if hashes[c.Hash] {
			out = append(out, c)
		}
	}
	return out
}

// beginReauthor enters the wizard, prefilling name/email from the oldest target
// commit (the first historically). It mirrors beginSquash's three-return shape.
func (m Model) beginReauthor() (tea.Model, tea.Cmd, bool) {
	targets := reauthorTargetCommits(m)
	if len(targets) == 0 {
		m.setStatus("Select commit(s) to change the author (Space toggles, v range)", statusWarn)
		return m, nil, true
	}
	oldest := targets[0]
	m.reauthorSt = reauthorState{
		nameBuf:      oldest.AuthorName,
		emailBuf:     oldest.AuthorEmail,
		committerToo: true,
		field:        fldRName,
		targets:      targets,
	}
	m.mode = modeReauthor
	return m, nil, true
}

// updateReauthor handles keys while the wizard is open: tab cycles fields, runes
// edit the focused text buffer, space toggles the committer checkbox (only on its
// field), A stages the change, esc cancels.
func (m Model) updateReauthor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.reauthorSt
	switch msg.String() {
	case "esc":
		m.mode = modeMain
		m.setStatus("Change author cancelled", statusWarn)
		return m, nil
	case "tab":
		st.field = (st.field + 1) % reauthorFieldCount
		m.reauthorSt = st
		return m, nil
	case "A":
		return m.stageReauthor()
	case "backspace":
		switch st.field {
		case fldRName:
			st.nameBuf = trimLastRune(st.nameBuf)
		case fldREmail:
			st.emailBuf = trimLastRune(st.emailBuf)
		}
		m.reauthorSt = st
		return m, nil
	case " ":
		if st.field == fldRCommitter {
			st.committerToo = !st.committerToo
			m.reauthorSt = st
			return m, nil
		}
		// space in a text field is a literal space
		st = appendToField(st, " ")
		m.reauthorSt = st
		return m, nil
	default:
		if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
			m.reauthorSt = appendToField(st, msg.String())
		}
		return m, nil
	}
}

// appendToField appends s to the focused text buffer (no-op on the checkbox).
func appendToField(st reauthorState, s string) reauthorState {
	switch st.field {
	case fldRName:
		st.nameBuf += s
	case fldREmail:
		st.emailBuf += s
	}
	return st
}

// trimLastRune removes the last rune of s (safe on empty / multibyte input).
func trimLastRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return string(r[:len(r)-1])
}

// stageReauthor validates input, builds a reauthorChange, and enters the pipeline
// (preview → confirm → apply), reusing the same overlay squash uses.
func (m Model) stageReauthor() (tea.Model, tea.Cmd) {
	st := m.reauthorSt
	name := strings.TrimSpace(st.nameBuf)
	email := strings.TrimSpace(st.emailBuf)
	if name == "" && email == "" {
		m.setStatus("Enter a name or email to apply", statusErr)
		return m, nil
	}
	targets := map[string]bool{}
	for _, c := range st.targets {
		targets[c.Hash] = true
	}
	change := newReauthorChange(m.plan.Commits, targets, name, email, st.committerToo)
	if len(change.targets) == 0 {
		m.setStatus("No commits would change (identity already matches)", statusWarn)
		return m, nil
	}

	// Run the pre-flight guard before opening the pipeline: a hard block (dirty
	// tree, out-of-order) reports its reason and never enters the overlay; pushed
	// history records NeedsConfirmation so updatePipeline runs the typed-confirm step.
	guard := change.guard(m.state)
	if guard.Blocked {
		m.setStatus(guard.BlockReason, statusErr)
		return m, nil
	}

	hashes := make([]string, 0, len(st.targets))
	for _, c := range st.targets {
		hashes = append(hashes, short(c.Hash))
	}
	m.pendingCmd = capability.Render("reauthor",
		capability.ReauthorArgMap(name, email, st.committerToo, strings.Join(hashes, ",")))
	m.pending = change
	m.pendingGuard = guard
	m.mode = modePipeline
	m.pipelineApply = true
	m.previewOffset = 0
	return m, nil
}

// renderReauthorWizard renders the modal body (pure, testable).
func renderReauthorWizard(st reauthorState, s styles) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CHANGE AUTHOR · %d commit(s)\n\n", len(st.targets))
	fmt.Fprintf(&b, "Name      %s%s\n", focusBar(st.field, fldRName), st.nameBuf)
	fmt.Fprintf(&b, "Email     %s%s\n", focusBar(st.field, fldREmail), st.emailBuf)
	check := "[ ]"
	if st.committerToo {
		check = "[x]"
	}
	fmt.Fprintf(&b, "%s%s also update committer\n", focusBar(st.field, fldRCommitter), check)
	b.WriteString("\n" + s.help.Render("[tab] field · [space] toggle committer · [A]pply · [esc] cancel"))
	return b.String()
}

// focusBar returns the focus marker when field == want, else a space (reuses the
// squash wizard's visual idiom without depending on its enum).
func focusBar(field, want reauthorField) string {
	if field == want {
		return "┃"
	}
	return " "
}

// viewReauthor frames the wizard in the modal style.
func (m Model) viewReauthor() string {
	return m.st.modal.Render(renderReauthorWizard(m.reauthorSt, m.st))
}
