package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/termsafe"
)

// linearizeChange is the staged linearize mutation for the apply pipeline.
// commits and plan are captured at staging time over the active (non-frozen)
// commits — history cannot change while the overlay is open. There is no
// wizard: the fold messages are fully synthesized by model.BuildLinearizePlan,
// so the panel row stages straight into preview/apply, same as purge/redact.
type linearizeChange struct {
	commits []model.Commit
	plan    model.LinearizePlan
	// cmd is the CLI-equivalent line for this linearize, set once at staging time.
	cmd string
}

func (c linearizeChange) commandLines() []string {
	if c.cmd == "" {
		return nil
	}
	return []string{c.cmd}
}

func (c linearizeChange) title() string {
	return fmt.Sprintf("Linearize %d merge(s)", len(c.plan.Folds))
}

// guard mirrors safety.Manager.ApplyLinearize's internal checks, in the same
// order, so the pipeline can block/confirm BEFORE applying: an empty plan (no
// merges to fold) or a dirty tree hard-blocks; a rewrite that touches an
// already-pushed commit requires the typed branch-name confirmation instead —
// exactly the same lockstep contract as squashChange.guard and
// splitChange.guard. Pushed-detection uses c.plan.RewrittenCommits(c.commits) —
// the suffix from the earliest fold's merge onward that ApplyLinearize
// actually re-emits — not the full c.commits history, so an untouched pushed
// commit further back cannot force a confirmation.
func (c linearizeChange) guard(state model.RepoState) safety.Guard {
	if len(c.plan.Folds) == 0 {
		return safety.Guard{Blocked: true, BlockReason: "nothing to linearize: no merge commits"}
	}
	if reason := state.BlockReason(model.NeedsNoTrackedChanges); reason != "" {
		return safety.Guard{Blocked: true, BlockReason: reason}
	}
	if model.AnyPushed(c.plan.RewrittenCommits(c.commits)) {
		return safety.Guard{
			NeedsConfirmation: true,
			ConfirmWord:       state.Branch,
			ConfirmReason: "some commits already exist on a remote; linearizing rewrites " +
				"history and will require a force-push",
		}
	}
	return safety.Guard{}
}

// preview lists one line per fold: "merge  N → 1  <new title>". NewTitle is
// history-derived (synthesized from a merged branch's commit subjects), so it
// is sanitized through termsafe.Line before rendering — the same convention
// list.go, purge.go, pipeline.go, and detail.go use for every repo-derived
// string that reaches the screen.
func (c linearizeChange) preview(st styles) previewModel {
	lines := make([]string, 0, len(c.plan.Folds))
	for _, f := range c.plan.Folds {
		lines = append(lines, fmt.Sprintf("%s  %d → 1  %s",
			short(f.Merge.Hash), len(f.Side)+1, termsafe.Line(f.NewTitle)))
	}
	return previewModel{title: c.title(), lines: lines}
}

func (c linearizeChange) apply(mgr *safety.Manager, state model.RepoState, opts safety.ApplyOptions) (safety.ApplyResult, error) {
	return mgr.ApplyLinearize(c.commits, c.plan, state, opts)
}

// beginLinearize builds the linearize plan over the active (non-frozen)
// commits and stages it straight into the pipeline. It follows the same
// (tea.Model, tea.Cmd, bool) shape every other begin-X uses (beginSquash,
// beginReword, beginReauthor, beginSplit) so feature.go's Run closures stay
// uniform; the trailing bool is a vestigial "handled" flag none of those
// callers ever branch on, kept only for shape parity. Using m.plan.Active()
// rather than the full m.plan.Commits keeps the frozen base off-limits, the
// same exclusion selectedHashSet applies for squash/reauthor — a merge before
// the frozen boundary is protected the same way any other frozen commit is.
func (m Model) beginLinearize() (tea.Model, tea.Cmd, bool) {
	active := m.plan.Active()
	sideOf := map[string][]model.SideCommit{}
	for _, c := range active {
		if !c.IsMerge {
			continue
		}
		side, err := m.repo.SideCommits(c.Hash)
		if err != nil {
			m.setStatus("linearize: "+err.Error(), statusErr)
			return m, nil, true
		}
		sideOf[c.Hash] = side
	}
	plan := model.BuildLinearizePlan(active, sideOf)
	change := linearizeChange{
		commits: active,
		plan:    plan,
		cmd:     capability.Render("linearize", nil),
	}
	guard := m.guardNow(change)
	if guard.Blocked {
		m.setStatus(guard.BlockReason, statusErr)
		return m, nil, true
	}
	m.pending = change
	m.pendingGuard = guard
	m.pipelineOrigin = modeMain // staged from main; esc returns here (set explicitly, defensively)
	m.mode = modePipeline
	m.pipelineApply = true
	m.previewOffset = 0
	return m, nil, true
}
