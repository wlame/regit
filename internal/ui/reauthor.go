package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
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
	// cmd is the CLI-equivalent line for this reauthor, set once at staging time.
	cmd string
}

func (c reauthorChange) commandLines() []string {
	if c.cmd == "" {
		return nil
	}
	return []string{c.cmd}
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

// reauthorState holds the live wizard state: the name/email text inputs, the
// committer checkbox, the focused field, and the captured target commits.
type reauthorState struct {
	nameInput    textinput.Model
	emailInput   textinput.Model
	committerToo bool
	field        reauthorField
	targets      []model.Commit // root-first; targets[0] is the oldest
	errMsg       string         // last stage failure, rendered inline in the modal
}

// name and email expose the current buffer contents (unfocused inputs included).
func (st reauthorState) name() string  { return st.nameInput.Value() }
func (st reauthorState) email() string { return st.emailInput.Value() }

// newReauthorInput builds one wizard text field, following the retitle modal's
// textinput conventions (no prompt, width from the caller's layout). SetValue
// leaves the cursor at the end of the prefilled text.
func newReauthorInput(value string, w int) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 80
	ti.Width = w
	ti.SetValue(value)
	return ti
}

// newReauthorState builds the wizard state with a prefilled identity, the
// committer checkbox on, and focus on the name field.
func newReauthorState(name, email string, targets []model.Commit, inputW int) reauthorState {
	st := reauthorState{
		nameInput:    newReauthorInput(name, inputW),
		emailInput:   newReauthorInput(email, inputW),
		committerToo: true,
		field:        fldRName,
		targets:      targets,
	}
	st.nameInput.Focus()
	return st
}

// cycleField moves focus by delta, keeping exactly the focused text input active
// (a blurred textinput ignores keys, so focus must track the field).
func (st reauthorState) cycleField(delta int) reauthorState {
	st.nameInput.Blur()
	st.emailInput.Blur()
	st.field = reauthorField((int(st.field) + delta + reauthorFieldCount) % reauthorFieldCount)
	switch st.field {
	case fldRName:
		st.nameInput.Focus()
	case fldREmail:
		st.emailInput.Focus()
	}
	return st
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
	m.reauthorSt = newReauthorState(oldest.AuthorName, oldest.AuthorEmail, targets,
		m.layout().inputWidth)
	m.mode = modeReauthor
	return m, nil, true
}

// updateReauthor handles keys while the wizard is open: tab/shift+tab cycle
// fields, enter stages the change from any field, esc cancels. On the committer
// checkbox, space toggles and A stages; every other key on a text field goes to
// the focused textinput, which owns editing (cursor movement, insertion at the
// cursor, backspace, and paste).
func (m Model) updateReauthor(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.reauthorSt
	switch msg.String() {
	case "esc":
		m.mode = modeMain
		m.setStatus("Change author cancelled", statusWarn)
		return m, nil
	case "tab", "down":
		m.reauthorSt = st.cycleField(1)
		return m, nil
	case "shift+tab", "up":
		m.reauthorSt = st.cycleField(-1)
		return m, nil
	case "enter":
		return m.stageReauthor()
	}
	if st.field == fldRCommitter {
		// A is the apply key ONLY on this non-text checkbox field; in the name
		// and email fields it is a literal letter (P0: typing "Alice" must not
		// apply the wizard) and is handled by the textinput below.
		switch msg.String() {
		case "A":
			return m.stageReauthor()
		case " ":
			st.committerToo = !st.committerToo
			st.errMsg = ""
			m.reauthorSt = st
		}
		return m, nil
	}
	st.errMsg = "" // any edit invalidates the previous stage error
	var cmd tea.Cmd
	switch st.field {
	case fldRName:
		st.nameInput, cmd = st.nameInput.Update(msg)
	case fldREmail:
		st.emailInput, cmd = st.emailInput.Update(msg)
	}
	m.reauthorSt = st
	return m, cmd
}

// stageReauthor validates input, builds a reauthorChange, and enters the pipeline
// (preview → confirm → apply), reusing the same overlay squash uses.
func (m Model) stageReauthor() (tea.Model, tea.Cmd) {
	st := m.reauthorSt
	name := strings.TrimSpace(st.name())
	email := strings.TrimSpace(st.email())
	// Stage failures surface on errMsg, rendered inside the wizard — a status-bar
	// message would be invisible under this modal.
	if name == "" && email == "" {
		m.reauthorSt.errMsg = "Enter a name or email to apply"
		return m, nil
	}
	targets := map[string]bool{}
	for _, c := range st.targets {
		targets[c.Hash] = true
	}
	change := newReauthorChange(m.plan.Commits, targets, name, email, st.committerToo)
	if len(change.targets) == 0 {
		m.reauthorSt.errMsg = "No commits would change (identity already matches)"
		return m, nil
	}

	// Run the pre-flight guard before opening the pipeline: a hard block (dirty
	// tree, out-of-order) reports its reason and never enters the overlay; pushed
	// history records NeedsConfirmation so updatePipeline runs the typed-confirm step.
	guard := change.guard(m.state)
	if guard.Blocked {
		m.reauthorSt.errMsg = guard.BlockReason
		return m, nil
	}

	hashes := make([]string, 0, len(st.targets))
	for _, c := range st.targets {
		hashes = append(hashes, short(c.Hash))
	}
	change.cmd = capability.Render("reauthor",
		capability.ReauthorArgMap(name, email, st.committerToo, strings.Join(hashes, ",")))
	m.pending = change
	m.pendingGuard = guard
	m.pipelineOrigin = modeReauthor // esc returns to the wizard, preserving its typed edits
	m.mode = modePipeline
	m.pipelineApply = true
	m.previewOffset = 0
	return m, nil
}

// renderReauthorWizard renders the modal body (pure, testable). The focused
// textinput draws its own cursor; focusBar marks the active row.
func renderReauthorWizard(st reauthorState, s styles) string {
	var b strings.Builder
	b.WriteString(s.modalTitle.Render(fmt.Sprintf("CHANGE AUTHOR · %d commit(s)", len(st.targets))) + "\n\n")
	fmt.Fprintf(&b, "%s Name    %s\n", focusBar(st.field, fldRName), st.nameInput.View())
	fmt.Fprintf(&b, "%s Email   %s\n", focusBar(st.field, fldREmail), st.emailInput.View())
	check := "[ ]"
	if st.committerToo {
		check = "[x]"
	}
	fmt.Fprintf(&b, "%s %s also update committer\n", focusBar(st.field, fldRCommitter), check)
	if st.errMsg != "" {
		// Surface a stage failure inline, right above the footer, instead of a
		// status-bar line hidden under this modal.
		b.WriteString("\n" + s.statusErr.Render("⚠ "+st.errMsg))
	}
	// The footer is context-sensitive: A only applies from the checkbox field,
	// so [A]pply is advertised only there (P0 — elsewhere A is literal text).
	hint := "[tab] field · [enter] apply · [esc] cancel"
	if st.field == fldRCommitter {
		hint = "[tab] field · [space] toggle committer · [A]pply / [enter] apply · [esc] cancel"
	}
	b.WriteString("\n" + s.help.Render(hint))
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
