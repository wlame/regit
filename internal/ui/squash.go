package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/squash"
)

// selectedHashSet returns the commit hashes the current selection covers: the Set
// directly, or the inclusive Range span; empty for other modes.
func (m Model) selectedHashSet() map[string]bool {
	out := map[string]bool{}
	switch m.sel.mode {
	case SelSet:
		for h := range m.sel.set {
			out[h] = true
		}
	case SelRange:
		lo, hi := m.sel.rangeBounds(m.listCursor)
		for i := lo; i <= hi; i++ {
			out[m.commitAtDisplayIndex(i).Hash] = true
		}
	}
	return out
}

// affectedChains derives the squash chains (and ignored singletons) from the
// current selection over the root-first plan commits.
func (m Model) affectedChains() ([]model.SquashChain, []string) {
	return model.DetectChains(m.plan.Commits, m.selectedHashSet())
}

// squashChange is the staged squash mutation that drops into the apply pipeline.
// commits and plan are captured at staging time (history cannot change while the
// overlay is open).
type squashChange struct {
	commits []model.Commit
	plan    model.SquashPlan
}

func (c squashChange) title() string {
	squashed := 0
	for _, ch := range c.plan.Chains {
		squashed += len(ch.Members)
	}
	return fmt.Sprintf("Squash %d commit(s) into %d", squashed, len(c.plan.Chains))
}

// guard mirrors safety.Manager.ApplySquash's internal checks so the pipeline can
// block/confirm BEFORE applying. Keeping this in lockstep with ApplySquash means
// the guard never disagrees with what the apply will actually allow.
func (c squashChange) guard(state model.RepoState) safety.Guard {
	for _, ch := range c.plan.Chains {
		for _, mem := range ch.Members {
			if mem.IsMerge {
				return safety.Guard{Blocked: true, BlockReason: "cannot squash across a merge commit"}
			}
		}
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
			ConfirmReason: "some commits are already on the upstream; squashing rewrites " +
				"history and will require a force-push",
		}
	}
	return safety.Guard{}
}

// preview lists one line per chain: "first..last  N → 1  <subject>".
func (c squashChange) preview(st styles) previewModel {
	lines := make([]string, 0, len(c.plan.Chains))
	for _, ch := range c.plan.Chains {
		lines = append(lines, fmt.Sprintf("%s..%s  %d → 1  %s",
			short(ch.First().Hash), short(ch.Last().Hash), len(ch.Members), ch.Subject))
	}
	return previewModel{title: c.title(), lines: lines}
}

func (c squashChange) apply(mgr *safety.Manager, state model.RepoState, opts safety.ApplyOptions) (safety.ApplyResult, error) {
	return mgr.ApplySquash(c.commits, c.plan, state, opts)
}

// --- Squash wizard (modeSquash) --------------------------------------------

// squashField identifies the focused editor field in the wizard.
type squashField int

const (
	fldList squashField = iota // inspecting the chain's commit list
	fldTitle
	fldBody
	fldAuthor
	fldDate
)

// squashFieldCount is the number of cyclable wizard fields, used to wrap the
// tab-to-next-field cycle.
const squashFieldCount = 5

// squashState holds the live wizard state: one tab per chain, the focused field,
// the inspect cursor, and which chains are still awaiting an LLM summary.
type squashState struct {
	chains     []model.SquashChain
	tab        int
	field      squashField
	listCursor int             // index into the ACTIVE chain's members (newest-first display)
	loading    map[string]bool // chain last-hash -> summary in flight
}

// active returns the chain for the current tab.
func (s squashState) active() model.SquashChain { return s.chains[s.tab] }

// switchTab moves the tab by delta, wrapping, and resets per-tab cursors.
func (s squashState) switchTab(delta int) squashState {
	n := len(s.chains)
	if n == 0 {
		return s
	}
	// The double-modulo keeps the result in [0, n) even for negative deltas, so
	// "[" from tab 0 wraps to the last tab.
	s.tab = ((s.tab+delta)%n + n) % n
	s.field = fldTitle
	s.listCursor = 0
	return s
}

// applySummary fills the chain identified by its last-hash with an LLM proposal
// and clears its loading flag. (User edits before the summary lands are rare;
// v1 lets the summary win, matching reword's buffer-overwrite behavior.)
func (s squashState) applySummary(chainKey string, p squash.Proposal) squashState {
	for i := range s.chains {
		if s.chains[i].Last().Hash == chainKey {
			s.chains[i].Subject = p.Subject
			s.chains[i].Body = p.Body
		}
	}
	delete(s.loading, chainKey)
	return s
}

// renderSquashWizard renders the whole modal as a string (pure, testable). The
// commit list and INSPECT pane show members NEWEST-FIRST to match the backbone.
func renderSquashWizard(st squashState, width, height int, s styles) string {
	if len(st.chains) == 0 {
		return "No chains to squash."
	}
	ch := st.active()
	var b strings.Builder

	// --- Title bar + tab strip ---
	squashed := 0
	for _, c := range st.chains {
		squashed += len(c.Members)
	}
	fmt.Fprintf(&b, "SQUASH · %d commit(s) into %d commit(s)\n", squashed, len(st.chains))
	b.WriteString("Chains: ")
	for i, c := range st.chains {
		marker := "  "
		if i == st.tab {
			marker = "▸ "
		}
		spin := ""
		if st.loading[c.Last().Hash] {
			spin = "⠋"
		}
		fmt.Fprintf(&b, "%s[%d]%s %s…%s ·%d  ", marker, i+1, spin, short(c.Last().Hash), short(c.First().Hash), len(c.Members))
	}
	b.WriteString("\n\n")

	// --- Two columns: left = commit list + INSPECT, right = result editor ---
	fmt.Fprintf(&b, "COMMITS IN CHAIN %d  (%d → 1)\n", st.tab+1, len(ch.Members))
	// Newest-first: iterate members in reverse (Members are root-first).
	for j := len(ch.Members) - 1; j >= 0; j-- {
		mem := ch.Members[j]
		cursor := "  "
		if (len(ch.Members) - 1 - j) == st.listCursor {
			cursor = "▸ "
		}
		fmt.Fprintf(&b, "%s%s  %s\n      %s · %s\n", cursor, short(mem.Hash), truncate(mem.Subject, 40),
			mem.AuthorName, mem.OrigAuthorDate.Format("2006-01-02 15:04"))
	}

	// INSPECT pane: the cursored member's full details.
	insp := ch.Members[len(ch.Members)-1-st.listCursor]
	b.WriteString("\nINSPECT · " + short(insp.Hash) + "\n")
	b.WriteString(insp.Subject + "\n")
	fmt.Fprintf(&b, "%s <%s>\n", insp.AuthorName, insp.AuthorEmail)
	fmt.Fprintf(&b, "author %s\n", insp.OrigAuthorDate.Format("2006-01-02 15:04"))
	if insp.Body != "" {
		b.WriteString("\n" + insp.Body + "\n")
	}

	// RESULT editor.
	b.WriteString("\nRESULT")
	if st.loading[ch.Last().Hash] {
		b.WriteString("   (summarizing…)")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Title    %s%s\n", focusMark(st.field, fldTitle), ch.Subject)
	fmt.Fprintf(&b, "Message  %s%s\n", focusMark(st.field, fldBody), strings.ReplaceAll(ch.Body, "\n", " "))
	fmt.Fprintf(&b, "Author   %s%s <%s>\n", focusMark(st.field, fldAuthor), ch.AuthorName, ch.AuthorEmail)
	fmt.Fprintf(&b, "Date     %s%s\n", focusMark(st.field, fldDate), ch.AuthorDate.Format("2006-01-02 15:04"))

	b.WriteString("\n[ ] chain · ↑/↓ inspect · tab field · type edit · A apply all · esc cancel")
	return b.String()
}

// focusMark returns a focus bar when field == want, else a single space, so the
// active result field is visually marked in the wizard.
func focusMark(field, want squashField) string {
	if field == want {
		return "┃"
	}
	return " "
}

// summarizeSquashResultMsg is the async message when a chain's summary completes.
type summarizeSquashResultMsg struct {
	chainKey string
	prop     squash.Proposal
	err      error
}

// summarizeSquashCmd runs Engine.Summarize off the UI thread, returning the
// result as a summarizeSquashResultMsg on the Bubble Tea bus when done.
func summarizeSquashCmd(eng *squash.Engine, ch model.SquashChain, opts squash.Options) tea.Cmd {
	return func() tea.Msg {
		prop, err := eng.Summarize(context.Background(), ch, opts)
		return summarizeSquashResultMsg{chainKey: ch.Last().Hash, prop: prop, err: err}
	}
}

// beginSquash enters the wizard. It builds chains from the selection, fills the
// no-LLM defaults, and (when an LLM is configured) fires async summaries. It
// mirrors beginSplit/beginReword's three-return-value shape so it can be called
// uniformly from the key dispatcher.
func (m Model) beginSquash() (tea.Model, tea.Cmd, bool) {
	chains, _ := m.affectedChains()
	if len(chains) == 0 {
		m.setStatus("Select 2+ consecutive commits to squash (Space toggles, v range)", statusWarn)
		return m, nil, true
	}
	for i := range chains {
		chains[i] = model.DefaultSquashConfig(chains[i])
	}
	st := squashState{chains: chains, tab: 0, field: fldTitle, loading: map[string]bool{}}

	// Fire async summaries only if an LLM is configured. buildSquashEngine returns
	// (nil, nil) for the no-LLM path, so the defaults above are the final values.
	var cmds []tea.Cmd
	if eng, err := buildSquashEngine(&m); err == nil && eng != nil {
		opts := squash.Options{Now: time.Now()}
		for _, ch := range chains {
			st.loading[ch.Last().Hash] = true
			cmds = append(cmds, summarizeSquashCmd(eng, ch, opts))
		}
	}
	m.squashSt = st
	m.mode = modeSquash
	return m, tea.Batch(cmds...), true
}

// updateSquash handles keys while the wizard is open: "[" / "]" switch chain
// tabs, tab cycles the focused field, ↑/↓ move the inspect cursor (only while the
// list is focused), A stages the plan, and any other rune edits the active text
// field.
func (m Model) updateSquash(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.squashSt
	switch msg.String() {
	case "esc":
		m.mode = modeMain
		m.setStatus("Squash cancelled", statusWarn)
		return m, nil
	case "[":
		m.squashSt = st.switchTab(-1)
		return m, nil
	case "]":
		m.squashSt = st.switchTab(1)
		return m, nil
	case "tab":
		st.field = (st.field + 1) % squashFieldCount
		m.squashSt = st
		return m, nil
	case "up", "k":
		if st.field == fldList && st.listCursor > 0 {
			st.listCursor--
			m.squashSt = st
		}
		return m, nil
	case "down", "j":
		if st.field == fldList && st.listCursor < len(st.active().Members)-1 {
			st.listCursor++
			m.squashSt = st
		}
		return m, nil
	case "A":
		return m.stageSquash()
	case "backspace":
		m.squashSt = editActiveField(st, "", true)
		return m, nil
	default:
		// Single-rune keys edit the active text field; everything else is ignored.
		if len(msg.String()) == 1 {
			m.squashSt = editActiveField(st, msg.String(), false)
		}
		return m, nil
	}
}

// editActiveField appends a rune (or deletes one with del=true) to the focused
// text field. The Date field is validated against its window on stage, not here;
// the List field is not text, so neither is editable through this path.
func editActiveField(st squashState, s string, del bool) squashState {
	ch := &st.chains[st.tab]
	apply := func(cur string) string {
		if del {
			if len(cur) > 0 {
				return cur[:len(cur)-1]
			}
			return cur
		}
		return cur + s
	}
	switch st.field {
	case fldTitle:
		ch.Subject = apply(ch.Subject)
	case fldBody:
		ch.Body = apply(ch.Body)
	case fldAuthor:
		ch.AuthorName = apply(ch.AuthorName)
	}
	return st
}

// stageSquash validates the plan and hands it to the apply pipeline (preview →
// confirm → apply), reusing the same overlay split uses.
func (m Model) stageSquash() (tea.Model, tea.Cmd) {
	plan := model.SquashPlan{Chains: m.squashSt.chains}
	if err := plan.Validate(m.plan.Commits); err != nil {
		m.setStatus("Invalid squash: "+err.Error(), statusErr)
		return m, nil
	}
	spec := chainsToSpec(plan.Chains)
	m.pendingCmd = capability.Render("squash", capability.SquashArgMap(spec))
	m.pending = squashChange{commits: m.plan.Commits, plan: plan}
	m.mode = modePipeline
	m.pipelineApply = true
	m.previewOffset = 0
	return m, nil
}

// chainsToSpec renders the chains as a comma-joined "first^..last" spec that
// round-trips through parseSquashSpec.
func chainsToSpec(chains []model.SquashChain) string {
	parts := make([]string, len(chains))
	for i, ch := range chains {
		parts[i] = short(ch.First().Hash) + "^.." + short(ch.Last().Hash)
	}
	return strings.Join(parts, ",")
}

// viewSquash renders the wizard overlay.
func (m Model) viewSquash() string {
	return m.st.modal.Render(renderSquashWizard(m.squashSt, m.width, m.height, m.st))
}

// buildSquashEngine builds a squash.Engine from the TUI config, or returns
// (nil, nil) when no LLM is configured (the no-LLM path). It mirrors
// buildRewordEngine's provider/baseURL/key resolution, reusing the package-private
// helpers in reword.go, then wraps a squash.Engine over a fresh HTTP backend.
func buildSquashEngine(m *Model) (*squash.Engine, error) {
	cfg := m.cfg
	if cfg.LLM.Provider == "" {
		return nil, nil // no LLM configured → caller uses no-LLM defaults
	}
	baseURL := defaultRewordBaseURL(cfg.LLM.Provider, cfg.LLM.BaseURL) // reuse the helper in reword.go
	apiKey := resolveRewordAPIKey(cfg.LLM.Provider)                    // reuse the helper in reword.go
	timeoutSec := cfg.LLM.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	backend := llm.NewHTTPBackend(baseURL, cfg.LLM.Model, apiKey, time.Duration(timeoutSec)*time.Second)
	gitDir, err := m.repo.GitDir()
	if err != nil {
		return nil, err
	}
	return squash.NewEngine(m.repo, backend, squash.NewCache(gitDir)), nil
}
