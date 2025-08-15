package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/reword"
)

// rewordState holds the active reword data while in modeReword.
// buffer stores results keyed by commit hash; inFlight tracks hashes for which
// a Generate call is in progress so we never issue duplicate requests.
type rewordState struct {
	commit    model.Commit
	buffer    map[string]reword.Result // hash -> result
	inFlight  map[string]bool          // hashes currently being generated
	sel       reword.Selection
	targets   []string // hashes to reword in order (Set selection, or just the cursor)
	targetIdx int      // index into targets of the commit currently in the picker
}

// rewordResultMsg is the async message returned when Generate completes.
type rewordResultMsg struct {
	hash   string
	result reword.Result
	err    error
}

// cycleTitle advances (delta=+1) or retreats (delta=-1) the title selection
// index, wrapping around within the variant list for the current commit. It is
// a no-op when the buffer has no entry for the current commit.
func (r *rewordState) cycleTitle(delta int) {
	res, ok := r.buffer[r.commit.Hash]
	if !ok || len(res.Variants) == 0 {
		return
	}
	n := len(res.Variants)
	r.sel.TitleIdx = ((r.sel.TitleIdx+delta)%n + n) % n
}

// cycleBody advances (delta=+1) or retreats (delta=-1) the body selection
// index, wrapping around within the variant list. It is a no-op when the
// buffer has no entry for the current commit.
func (r *rewordState) cycleBody(delta int) {
	res, ok := r.buffer[r.commit.Hash]
	if !ok || len(res.Variants) == 0 {
		return
	}
	n := len(res.Variants)
	r.sel.BodyIdx = ((r.sel.BodyIdx+delta)%n + n) % n
}

// toggleNoBody flips the NoBody flag on the current selection.
func (r *rewordState) toggleNoBody() {
	r.sel.NoBody = !r.sel.NoBody
}

// renderRewordPicker renders the reword variant picker as a plain string.
// It is a pure function — no I/O — which makes it straightforward to unit test.
//
// Layout:
//
//	Reword commit
//
//	  <selected title>
//
//	  <body or "— no body —">
//
//	  title variant i/N
//	  ←/→ title · j/k body · b no-body · enter apply · esc cancel
func renderRewordPicker(r reword.Result, sel reword.Selection) string {
	var b strings.Builder

	b.WriteString("Reword commit\n\n")

	// Guard: show a loading indicator when the result has no variants yet.
	if len(r.Variants) == 0 {
		b.WriteString("  (generating…)\n\n")
		b.WriteString("  esc cancel")
		return b.String()
	}

	// Selected title (TitleIdx is always valid because callers initialise it
	// from DefaultSelection and cycle functions clamp to len(Variants)).
	titleIdx := sel.TitleIdx
	if titleIdx >= len(r.Variants) {
		titleIdx = 0
	}
	b.WriteString(fmt.Sprintf("  %s\n\n", r.Variants[titleIdx].Title))

	// Body section.
	if sel.NoBody {
		b.WriteString("  — no body —\n\n")
	} else {
		bodyIdx := sel.BodyIdx
		if bodyIdx >= len(r.Variants) {
			bodyIdx = 0
		}
		body := r.Variants[bodyIdx].Body
		if body == "" {
			b.WriteString("  (empty body)\n\n")
		} else {
			b.WriteString(fmt.Sprintf("  %s\n\n", body))
		}
	}

	// Variant indicator: show both title and body indices (1-indexed, human-friendly).
	n := len(r.Variants)
	bodyIdx2 := sel.BodyIdx
	if bodyIdx2 >= n {
		bodyIdx2 = 0
	}
	b.WriteString(fmt.Sprintf("  title %d/%d · body %d/%d\n", titleIdx+1, n, bodyIdx2+1, n))

	// Hint line.
	b.WriteString("  ←/→ title · j/k body · b no-body · enter apply · esc cancel")

	return b.String()
}

// buildRewordEngine constructs a reword Engine from the TUI's config, using
// the same resolution logic as cmd/regit's backend builder.
//
// TODO: unify with cmd reword backend builder once extraction into a shared
// helper package is feasible.
func buildRewordEngine(m *Model) (*reword.Engine, error) {
	cfg := m.cfg
	provider := cfg.LLM.Provider
	modelName := cfg.LLM.Model
	if provider == "" {
		return nil, fmt.Errorf("no LLM configured — set llm.provider and llm.model in config or REGIT_LLM_PROVIDER/MODEL env vars")
	}

	// Determine base URL from config.
	baseURL := defaultRewordBaseURL(provider, cfg.LLM.BaseURL)

	// API key comes only from environment (never from config file or CLI args).
	apiKey := resolveRewordAPIKey(provider)
	timeoutSec := cfg.LLM.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	timeout := time.Duration(timeoutSec) * time.Second
	backend := llm.NewHTTPBackend(baseURL, modelName, apiKey, timeout)

	gitDir, err := m.repo.GitDir()
	if err != nil {
		return nil, fmt.Errorf("resolve git dir: %w", err)
	}
	cache := reword.NewCache(gitDir)
	eng := reword.NewEngine(m.repo, backend, cache)
	return eng, nil
}

// providerRewordBaseURLs maps well-known provider names to their default
// OpenAI-compatible base URLs.
var providerRewordBaseURLs = map[string]string{
	"ollama":     "http://localhost:11434/v1",
	"lmstudio":   "http://localhost:1234/v1",
	"openai":     "https://api.openai.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
}

func defaultRewordBaseURL(provider, override string) string {
	if override != "" {
		return override
	}
	return providerRewordBaseURLs[provider]
}

// providerRewordEnvKeys maps provider names to their conventional API-key env var.
var providerRewordEnvKeys = map[string]string{
	"openai":     "OPENAI_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
}

func resolveRewordAPIKey(provider string) string {
	if key := os.Getenv("REGIT_LLM_API_KEY"); key != "" {
		return key
	}
	if envVar, ok := providerRewordEnvKeys[provider]; ok {
		return os.Getenv(envVar)
	}
	return ""
}

// generateRewordCmd fires an async Generate call and returns the result as a
// rewordResultMsg on the Bubble Tea message bus when done.
func generateRewordCmd(eng *reword.Engine, c model.Commit, parentTree, readme string, opts reword.Options) tea.Cmd {
	return func() tea.Msg {
		result, err := eng.Generate(context.Background(), c, parentTree, readme, opts)
		return rewordResultMsg{hash: c.Hash, result: result, err: err}
	}
}

// rewordTargets returns the commits to reword: the active Set selection (in
// display order, newest first, frozen excluded) when a non-empty Set exists,
// otherwise just the commit under the cursor (single reword).
func (m Model) rewordTargets() []string {
	if m.sel.mode == SelSet && len(m.sel.set) > 0 {
		var out []string
		for i := 0; i < len(m.plan.Commits); i++ {
			c := m.commitAtDisplayIndex(i)
			if m.sel.isSet(c.Hash) && !m.plan.IsFrozen(len(m.plan.Commits)-1-i) {
				out = append(out, c.Hash)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{m.commitAtCursor().Hash}
}

// beginReword starts the reword flow for the commit under the cursor. It
// initialises the rewordState if needed, fires an async Generate command for
// the selected commit (unless the result is already cached), and best-effort
// prefetches the next commit in the list.
//
// The function mirrors the three-return-value signature of beginSplit so it
// can be called uniformly from globalAction.
func (m Model) beginReword() (tea.Model, tea.Cmd, bool) {
	if len(m.plan.Commits) == 0 {
		m.setStatus("No commit selected", statusWarn)
		return m, nil, true
	}
	// With no active Set the cursor IS the target, so a frozen cursor is refused.
	// With a Set, rewordTargets already excludes frozen hashes, so the cursor's
	// own frozen state is irrelevant.
	if (m.sel.mode != SelSet || len(m.sel.set) == 0) && m.plan.IsFrozen(m.storageIndexAtCursor()) {
		m.setStatus("Cannot reword a frozen commit — press b to move the base", statusWarn)
		return m, nil, true
	}

	// Lazy-init the buffer and in-flight maps on first use.
	if m.rewordSt.buffer == nil {
		m.rewordSt.buffer = make(map[string]reword.Result)
	}
	if m.rewordSt.inFlight == nil {
		m.rewordSt.inFlight = make(map[string]bool)
	}

	targets := m.rewordTargets()
	m.rewordSt.targets = targets
	m.rewordSt.targetIdx = 0
	c := m.commitByHash(targets[0])
	m.rewordSt.commit = c
	m.rewordSt.sel = reword.DefaultSelection()
	m.mode = modeReword

	var cmds []tea.Cmd

	// Fire a generate command for the first target if it is not already buffered
	// or in-flight.
	if _, cached := m.rewordSt.buffer[c.Hash]; !cached && !m.rewordSt.inFlight[c.Hash] {
		eng, err := buildRewordEngine(&m)
		if err != nil {
			m.mode = modeMain
			m.setStatus("build reword engine: "+err.Error(), statusErr)
			return m, nil, true
		}
		parentTree, err := resolveParentTree(&m, c)
		if err != nil {
			m.mode = modeMain
			m.setStatus("resolve parent tree: "+err.Error(), statusErr)
			return m, nil, true
		}
		opts := buildRewordOptions(m)
		readme := reword.ReadmeForContext(m.repo, opts.Context)
		cmds = append(cmds, generateRewordCmd(eng, c, parentTree, readme, opts))
		m.rewordSt.inFlight[c.Hash] = true
		if len(targets) > 1 {
			m.setStatus(fmt.Sprintf("Rewording %d commits — generating %s…", len(targets), short(c.Hash)), statusNone)
		} else {
			m.setStatus(fmt.Sprintf("Generating reword variants for %s…", short(c.Hash)), statusNone)
		}

		// Best-effort prefetch of the next target so its picker is ready.
		if len(targets) > 1 {
			nextC := m.commitByHash(targets[1])
			if _, nCached := m.rewordSt.buffer[nextC.Hash]; !nCached && !m.rewordSt.inFlight[nextC.Hash] {
				if nextParent, perr := resolveParentTree(&m, nextC); perr == nil {
					cmds = append(cmds, generateRewordCmd(eng, nextC, nextParent, readme, opts))
					m.rewordSt.inFlight[nextC.Hash] = true
				}
			}
		}
	}

	return m, tea.Batch(cmds...), true
}

// advanceReword moves the picker to the next commit in a multi-reword queue. It
// returns (model, cmd, true) when another target exists — firing its generate (if
// not buffered) and resetting the picker selection — or (m, nil, false) when the
// queue is exhausted.
func (m Model) advanceReword() (tea.Model, tea.Cmd, bool) {
	if m.rewordSt.targetIdx+1 >= len(m.rewordSt.targets) {
		return m, nil, false
	}
	m.rewordSt.targetIdx++
	hash := m.rewordSt.targets[m.rewordSt.targetIdx]
	c := m.commitByHash(hash)
	m.rewordSt.commit = c
	m.rewordSt.sel = reword.DefaultSelection()

	appliedCount := m.rewordSt.targetIdx
	total := len(m.rewordSt.targets)
	if _, cached := m.rewordSt.buffer[hash]; !cached && !m.rewordSt.inFlight[hash] {
		eng, err := buildRewordEngine(&m)
		if err != nil {
			// Set modeMain so the caller's more==true path exits the picker to
			// the main view showing this error, instead of trapping the user in
			// a picker whose variants will never load.
			m.mode = modeMain
			m.setStatus("build reword engine: "+err.Error(), statusErr)
			return m, nil, true
		}
		parentTree, err := resolveParentTree(&m, c)
		if err != nil {
			// Same rationale: return more==true with modeMain so the caller
			// surfaces this error in the main view rather than a dead picker.
			m.mode = modeMain
			m.setStatus("resolve parent tree: "+err.Error(), statusErr)
			return m, nil, true
		}
		opts := buildRewordOptions(m)
		readme := reword.ReadmeForContext(m.repo, opts.Context)
		m.rewordSt.inFlight[hash] = true
		m.setStatus(fmt.Sprintf("Reworded %d/%d — generating %s…", appliedCount, total, short(hash)), statusNone)
		return m, generateRewordCmd(eng, c, parentTree, readme, opts), true
	}
	m.setStatus(fmt.Sprintf("Reworded %d/%d — review %s", appliedCount, total, short(hash)), statusOK)
	return m, nil, true
}

// resolveParentTree returns the tree hash of the commit's parent, or the
// empty-tree hash for root commits.
func resolveParentTree(m *Model, c model.Commit) (string, error) {
	if len(c.ParentHashes) == 0 {
		return m.repo.EmptyTree()
	}
	return m.repo.ResolveRef(c.ParentHashes[0] + "^{tree}")
}

// buildRewordOptions constructs Options from the TUI's config for a reword
// Generate call.
func buildRewordOptions(m Model) reword.Options {
	cfg := m.cfg
	variants := cfg.Reword.Variants
	if variants <= 0 {
		variants = 3
	}
	contextLines := cfg.Reword.ContextLines
	if contextLines <= 0 {
		contextLines = 3
	}
	return reword.Options{
		Variants:     variants,
		ContextLines: contextLines,
		Context:      cfg.Reword.Context,
		Now:          time.Now(),
	}
}

// updateReword handles key events while in modeReword (the variant picker).
func (m Model) updateReword(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeMain
		// If we were partway through a multi-reword walk (targetIdx > 0 means at
		// least one commit was already applied before cancel), report how many were
		// applied rather than claiming nothing happened.
		// Note: targetIdx equals the applied count because advanceReword increments
		// it only after each successful apply.
		if len(m.rewordSt.targets) > 1 && m.rewordSt.targetIdx > 0 {
			m.setStatus(fmt.Sprintf("Reworded %d/%d — rest cancelled", m.rewordSt.targetIdx, len(m.rewordSt.targets)), statusWarn)
		} else {
			m.setStatus("Reword cancelled", statusWarn)
		}
		// Reset the selection so no stale Set lingers — mirrors the completion
		// path (enter-applies-last) which also clears the set.
		m.sel.reset()
		m.applyDefaultSelectionMode()
		return m, nil
	case "left":
		m.rewordSt.cycleTitle(-1)
		return m, nil
	case "right":
		m.rewordSt.cycleTitle(1)
		return m, nil
	case "j":
		m.rewordSt.cycleBody(1)
		return m, nil
	case "k":
		m.rewordSt.cycleBody(-1)
		return m, nil
	case "b":
		m.rewordSt.toggleNoBody()
		return m, nil
	case "enter":
		// Apply: look up the buffered result and set an edit on the plan.
		hash := m.rewordSt.commit.Hash
		if result, ok := m.rewordSt.buffer[hash]; ok {
			if len(result.Variants) == 0 {
				m.setStatus("No variants available", statusWarn)
				return m, nil
			}
			m.plan.SetEdit(hash, result.ToEdit(m.rewordSt.sel))
			m.pendingCmd = capability.Render("reword", capability.RewordArgMap(capability.Short(hash)))
			if next, cmd, more := m.advanceReword(); more {
				return next, cmd // walk to the next commit in the set
			}
			m.mode = modeMain
			m.sel.reset()
			m.applyDefaultSelectionMode()
			if total := len(m.rewordSt.targets); total > 1 {
				m.setStatus(fmt.Sprintf("Reworded %d commits", total), statusOK)
			} else {
				m.setStatus(fmt.Sprintf("Reword applied to %s", short(hash)), statusOK)
			}
		} else {
			// Still generating: stay in the picker and show a hint.
			m.setStatus("Still generating — please wait…", statusWarn)
		}
		return m, nil
	}
	return m, nil
}
