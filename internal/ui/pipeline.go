package ui

import (
	"fmt"
	"strings"
	"time"

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
// effective author dates with a → arrow on edited commits, and a ✗ flag on rows
// that are part of a sequence violation.
func (c planChange) preview(st styles) previewModel {
	violation := violationSet(c.plan)
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
		case violation[commit.Hash]:
			lines = append(lines, st.statusErr.Render("✗ "+text))
		case changed:
			lines = append(lines, st.newDate.Render("  "+text))
		default:
			lines = append(lines, st.dim.Render("  "+text))
		}
	}
	title := fmt.Sprintf("Apply — %d edited · %d violation(s)", len(c.plan.Edits), len(c.plan.Violations))
	return previewModel{title: title, lines: lines}
}

func (c planChange) apply(mgr *safety.Manager, state model.RepoState, opts safety.ApplyOptions) (safety.ApplyResult, error) {
	return mgr.Apply(c.plan, state, opts)
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
