package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
)

// pipeline.go is the stage → preview → confirm → apply funnel (spec §6.7). Every
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
}

// planChange applies the plan's accumulated date/message edits — the change `A`
// (and the staged reword/retitle/drop edits) produce.
type planChange struct {
	plan *model.Plan
	loc  *time.Location
}

// newPlanChange wraps the current plan as a pendingChange.
func newPlanChange(plan *model.Plan, loc *time.Location) planChange {
	return planChange{plan: plan, loc: loc}
}

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
	lines := make([]string, 0, len(c.plan.Commits))
	for i := len(c.plan.Commits) - 1; i >= 0; i-- {
		commit := c.plan.Commits[i]
		orig := commit.OrigAuthorDate.In(c.loc).Format("2006-01-02 15:04")
		neu := c.plan.EffectiveAuthorDate(commit).In(c.loc).Format("2006-01-02 15:04")
		_, changed := c.plan.EditFor(commit.Hash)
		arrow := "  =  "
		if changed {
			arrow = " → "
		}
		text := fmt.Sprintf("%s  %-32s %s%s%s", short(commit.Hash), truncate(commit.Subject, 32), orig, arrow, neu)
		switch {
		case shifted[commit.Hash]:
			lines = append(lines, st.statusWarn.Render("⚠ "+text))
		case changed:
			lines = append(lines, st.newDate.Render("  "+text))
		default:
			lines = append(lines, st.dim.Render("  "+text))
		}
	}
	title := fmt.Sprintf("Apply — %d edited · %d conflicting", len(c.plan.Edits), len(c.plan.Shifted))
	return previewModel{title: title, lines: lines}
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
}

func (c splitChange) title() string {
	return fmt.Sprintf("Split %s into %d commit(s)", short(c.commitHash), len(c.groups))
}

// guard mirrors safety.Manager.ApplySplit's internal checks so the pipeline can
// gate a split BEFORE applying (a graceful block/confirm) instead of failing at
// apply time: a merge commit or a dirty tree is blocked, and any repo with pushed
// (upstream) commits requires the typed branch-name confirmation — splitting
// rewrites history, so it needs the same force-push acknowledgement a plan rewrite
// does. Keeping this in lockstep with ApplySplit means the guard never disagrees
// with what the apply will actually allow.
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
	if state.HasPushedCommits() {
		return safety.Guard{
			NeedsConfirmation: true,
			ConfirmWord:       state.Branch,
			ConfirmReason: "some commits are already on the upstream; splitting rewrites " +
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
// scrollable body window, and a footer whose actions depend on the mode (apply
// vs a read-only peek).
func viewPipeline(p previewModel, offset, visible int, applyMode bool, st styles) string {
	var b strings.Builder
	b.WriteString(st.modalTitle.Render(p.title) + "\n\n")

	if offset > len(p.lines) {
		offset = len(p.lines)
	}
	end := offset + visible
	if end > len(p.lines) {
		end = len(p.lines)
	}
	for _, line := range p.lines[offset:end] {
		b.WriteString(line + "\n")
	}

	footer := "↑/↓ scroll · esc close"
	if applyMode {
		footer = "↑/↓ scroll · enter apply · esc cancel"
	}
	b.WriteString("\n" + st.help.Render(footer))
	return st.modal.Render(b.String())
}
