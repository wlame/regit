package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/termsafe"
)

// pipeline.go is the stage → preview → confirm → apply funnel. Every
// history mutation is expressed as a pendingChange: the UI shows its preview, gates
// pushed rewrites behind a typed confirmation, and then calls its apply, which runs
// through safety.Manager. The interface keeps the apply UX uniform across features
// and lets future ops (join, purge) drop in by implementing it.

// previewModel is the structured before/after a pendingChange renders into the
// pipeline overlay: a title plus already-styled, scrollable body lines.
type previewModel struct {
	title string
	lines []string
}

// pendingChange is a staged, not-yet-applied history mutation.
type pendingChange interface {
	// title is a one-line summary for the overlay header / status line.
	title() string
	// guard runs the safety checks for this change against the repo state:
	// Blocked stops it; NeedsConfirmation requires the typed branch name.
	guard(state model.RepoState) safety.Guard
	// preview renders the explicit before/after for the overlay.
	preview(st styles) previewModel
	// apply performs the mutation through the safety manager (backup → op →
	// verify → branch-ref CAS) and returns the backup ref in the result.
	apply(mgr *safety.Manager, state model.RepoState, opts safety.ApplyOptions) (safety.ApplyResult, error)
	// commandLines are the CLI-equivalent line(s) for THIS change alone, owned by
	// the change itself rather than a shared slot on the model — commitPending
	// flushes exactly these to the footer/on-disk log, so an applied change can
	// never log another staged-but-unrelated action's line (or vice versa).
	commandLines() []string
}

// diskLogger is implemented by a change whose command lines must not be
// written to the on-disk command log verbatim (redact: the line contains the
// secret). commitPending writes diskCommandLines to disk instead, while the
// in-memory panel still shows commandLines.
type diskLogger interface {
	diskCommandLines() []string
}

// planChange applies the plan's accumulated date/message edits — the change `A`
// (and the staged reword/retitle/drop edits) produce. cmds is a snapshot, taken at
// staging time, of every plan-edit command line accumulated so far (the active
// strategy's redate line plus any drop-bodies/retitle/reword/editor notes); it is
// NOT recomputed from the model, so it survives a reload that wipes the plan.
// impact is likewise a staging-time snapshot: the pre-apply ref-impact lines
// (see refImpactLines) for whatever tags/branches the plan's active range would
// orphan, computed once so the overlay never re-queries the repo on every
// render.
type planChange struct {
	plan   *model.Plan
	loc    *time.Location
	cmds   []string
	impact []string
}

// newPlanChange wraps the current plan as a pendingChange, snapshotting cmds
// (the plan-edit command lines accumulated at staging time) and impact (the
// pre-apply ref-impact lines, see refImpactLines) onto the change.
func newPlanChange(plan *model.Plan, loc *time.Location, cmds []string, impact []string) planChange {
	return planChange{plan: plan, loc: loc, cmds: cmds, impact: impact}
}

// refImpactLines computes the pre-apply impact report for the TUI preview,
// mirroring capability.reportImpact: every tag/branch that points at a commit
// in active is listed, except the branch being rewritten (branch) — that ref
// moves WITH the rewrite, so it is never reported as an orphan. Read-only and
// synchronous (a single RefsPointingAt call, run when the pipeline is staged —
// no goroutine, no tea.Cmd); any error reading refs is swallowed, since the
// report is advisory and must never block staging the change.
func refImpactLines(repo *gitio.Repo, active []model.Commit, branch string) []string {
	if repo == nil || len(active) == 0 {
		return nil
	}
	set := make(map[string]bool, len(active))
	for _, c := range active {
		set[c.Hash] = true
	}
	impacts, err := repo.RefsPointingAt(set)
	if err != nil || len(impacts) == 0 {
		return nil
	}
	movingBranch := "refs/heads/" + branch
	shown := impacts[:0]
	for _, im := range impacts {
		if im.Name == movingBranch {
			continue // this ref moves with the rewrite; not an orphan
		}
		shown = append(shown, im)
	}
	if len(shown) == 0 {
		return nil
	}
	lines := make([]string, 0, len(shown)+1)
	lines = append(lines, fmt.Sprintf("⚠ %d ref(s) reference commits this rewrite will orphan:", len(shown)))
	for _, im := range shown {
		lines = append(lines, fmt.Sprintf("    %s %s → %s", im.Kind, im.Name, short(im.Commit)))
	}
	return lines
}

func (c planChange) commandLines() []string { return c.cmds }

func (c planChange) title() string {
	return fmt.Sprintf("Apply %d commit edit(s)", len(c.plan.Edits))
}

func (c planChange) guard(state model.RepoState) safety.Guard {
	return safety.CheckGuards(state, c.plan)
}

// preview renders a before/after row per commit (newest-first): the original and
// effective author dates with a → arrow on edited commits, and a ⚠ flag on rows
// in the out-of-order blast radius.
func (c planChange) preview(st styles) previewModel {
	shifted := shiftedSet(c.plan)
	lines := make([]string, 0, len(c.plan.Commits)+len(c.impact))
	for i := len(c.plan.Commits) - 1; i >= 0; i-- {
		commit := c.plan.Commits[i]
		orig := commit.OrigAuthorDate.In(c.loc).Format("2006-01-02 15:04")
		neu := c.plan.EffectiveAuthorDate(commit).In(c.loc).Format("2006-01-02 15:04")
		edit, changed := c.plan.EditFor(commit.Hash)
		// The date columns only reflect a date change; use "→" when the date
		// actually moved, "=" otherwise, so a message- or author-only edit does not
		// falsely imply a date shift.
		arrow := "  =  "
		if !c.plan.EffectiveAuthorDate(commit).Equal(commit.OrigAuthorDate) {
			arrow = " → "
		}
		// Show the EFFECTIVE (post-edit) subject so a retitle/reword is visible in
		// the row itself.
		text := fmt.Sprintf("%s  %-32s %s%s%s", short(commit.Hash),
			truncate(termsafe.Line(c.plan.EffectiveSubject(commit)), 32), orig, arrow, neu)
		switch {
		case shifted[commit.Hash]:
			lines = append(lines, st.statusWarn.Render("⚠ "+text))
		case changed:
			lines = append(lines, st.newDate.Render("  "+text))
		default:
			lines = append(lines, st.dim.Render("  "+text))
		}
		// Message and author edits are invisible in the date columns, so spell them
		// out on indented sub-lines — otherwise the mandatory preview would gate a
		// rewrite whose actual content change it never displays.
		for _, note := range editNotes(commit, edit) {
			lines = append(lines, st.dim.Render("        "+note))
		}
	}
	// Impact lines (tags/branches this rewrite would orphan) go last, styled
	// the same as the ⚠ conflict rows above — mirrors the headless preview's
	// wording (see capability.reportImpact) so the same warning reads the same
	// way whether the user is at a terminal or in the TUI.
	for _, line := range c.impact {
		lines = append(lines, st.statusWarn.Render(line))
	}
	title := fmt.Sprintf("Apply — %d edited · %d conflicting", len(c.plan.Edits), len(c.plan.Shifted))
	return previewModel{title: title, lines: lines}
}

// editNotes returns human-readable one-liners describing the non-date changes an
// edit makes to a commit (new title, dropped/updated body, changed author identity),
// so the preview surfaces exactly what a reword/retitle/drop-body/reauthor will do.
func editNotes(commit model.Commit, e model.Edit) []string {
	var notes []string
	if e.NewSubject != nil && *e.NewSubject != commit.Subject {
		notes = append(notes, fmt.Sprintf("title: %q → %q",
			truncate(termsafe.Line(commit.Subject), 26), truncate(termsafe.Line(*e.NewSubject), 26)))
	}
	switch {
	case e.DropBody && commit.Body != "":
		notes = append(notes, "body dropped")
	case e.NewBody != nil && *e.NewBody != commit.Body:
		notes = append(notes, "body updated")
	}
	if e.NewAuthorName != nil || e.NewAuthorEmail != nil {
		name := commit.AuthorName
		if e.NewAuthorName != nil {
			name = *e.NewAuthorName
		}
		email := commit.AuthorEmail
		if e.NewAuthorEmail != nil {
			email = *e.NewAuthorEmail
		}
		notes = append(notes, fmt.Sprintf("author → %s <%s>", termsafe.Line(name), termsafe.Line(email)))
	}
	return notes
}

func (c planChange) apply(mgr *safety.Manager, state model.RepoState, opts safety.ApplyOptions) (safety.ApplyResult, error) {
	return mgr.Apply(c.plan, state, opts)
}

// splitChange applies a reviewed split proposal: it replaces one commit with the
// reviewed sub-commits and replays the tail, through safety.Manager.ApplySplit.
// subs and tail are derived once at staging time (history cannot change while the
// overlay is open), so apply just replays the stored values.
type splitChange struct {
	commit     model.Commit
	parentHash string
	subs       []gitio.SubCommit
	tail       []model.Commit
	groups     []model.HunkGroup
	commitHash string
	// cmd is the CLI-equivalent line for this split, set once at staging time.
	cmd string
}

func (c splitChange) title() string {
	return fmt.Sprintf("Split %s into %d commit(s)", short(c.commitHash), len(c.groups))
}

func (c splitChange) commandLines() []string {
	if c.cmd == "" {
		return nil
	}
	return []string{c.cmd}
}

// guard mirrors safety.Manager.ApplySplit's internal checks so the pipeline can
// gate a split BEFORE applying (a graceful block/confirm) instead of failing at
// apply time: a merge commit or a dirty tree is blocked, and a split whose
// rewrite set (the target commit plus its tail) touches a pushed commit
// requires the typed branch-name confirmation — splitting rewrites history, so
// it needs the same force-push acknowledgement a plan rewrite does. Keeping
// this in lockstep with ApplySplit means the guard never disagrees with what
// the apply will actually allow. Pushed-detection is scoped to c.commit and
// c.tail (model.AnyPushed), not to whether the repo has an upstream at all, so
// a pushed ancestor the split never touches does not force a confirmation.
func (c splitChange) guard(state model.RepoState) safety.Guard {
	if c.commit.IsMerge {
		return safety.Guard{Blocked: true, BlockReason: "cannot split a merge commit"}
	}
	if state.IsDirty {
		return safety.Guard{
			Blocked:     true,
			BlockReason: "working tree has uncommitted changes; commit or stash them first",
		}
	}
	if model.AnyPushed(append([]model.Commit{c.commit}, c.tail...)) {
		return safety.Guard{
			NeedsConfirmation: true,
			ConfirmWord:       state.Branch,
			ConfirmReason: "some commits already exist on a remote; splitting rewrites " +
				"history and will require a force-push",
		}
	}
	return safety.Guard{}
}

// preview lists the proposed sub-commits: "N. Subject [intent] (tests: pass|WIP)".
func (c splitChange) preview(st styles) previewModel {
	lines := make([]string, 0, len(c.groups))
	for _, g := range c.groups {
		testStatus := "pass"
		if g.WIP {
			testStatus = "WIP"
		}
		intent := g.Intent
		if intent == "" {
			intent = "?"
		}
		lines = append(lines, fmt.Sprintf("%d. %s [%s] (tests: %s)", g.Order, g.Subject, intent, testStatus))
	}
	return previewModel{title: fmt.Sprintf("Split %s into %d", short(c.commitHash), len(c.groups)), lines: lines}
}

func (c splitChange) apply(mgr *safety.Manager, state model.RepoState, opts safety.ApplyOptions) (safety.ApplyResult, error) {
	return mgr.ApplySplit(c.commit, c.parentHash, c.subs, c.tail, state, opts)
}

// viewPipeline renders the preview overlay for a pendingChange: title, the
// scrollable body window, and a footer whose actions depend on the mode
// (apply vs a read-only peek). width is the modal's full column budget
// (from layoutDims.previewWidth); body lines are clamped to the content
// area so an adversarially long path or subject cannot break the frame.
func viewPipeline(
	p previewModel, offset, visible int, applyMode bool, width int, st styles,
) string {
	var b strings.Builder
	b.WriteString(st.modalTitle.Render(p.title) + "\n\n")

	if offset > len(p.lines) {
		offset = len(p.lines)
	}
	end := offset + visible
	if end > len(p.lines) {
		end = len(p.lines)
	}
	// The modal style spends 4 columns on horizontal padding (2 each side);
	// clamping per line keeps ANSI-styled content from wrapping the frame.
	clamp := lipgloss.NewStyle().MaxWidth(width - 4)
	for _, line := range p.lines[offset:end] {
		b.WriteString(clamp.Render(line) + "\n")
	}

	footer := "↑/↓ scroll · esc close"
	if applyMode {
		footer = "↑/↓ scroll · enter apply · esc cancel"
	}
	b.WriteString("\n" + st.help.Render(footer))
	return st.modal.Width(width).Render(b.String())
}
