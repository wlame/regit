package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/reword"
)

// rewordState holds the active reword data while in modeReword.
// buffer stores results keyed by commit hash; inFlight tracks hashes for which
// a Generate call is in progress so we never issue duplicate requests.
type rewordState struct {
	commit   model.Commit
	buffer   map[string]reword.Result // hash -> result
	inFlight map[string]bool          // hashes currently being generated
	sel      reword.Selection
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
	r.sel.TitleIdx = ((r.sel.TitleIdx + delta) % n + n) % n
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
	r.sel.BodyIdx = ((r.sel.BodyIdx + delta) % n + n) % n
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
	if m.plan.IsFrozen(m.storageIndexAtCursor()) {
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

	c := m.commitAtCursor()
	m.rewordSt.commit = c
	m.rewordSt.sel = reword.DefaultSelection()
	m.mode = modeReword

	var cmds []tea.Cmd

	// Fire a generate command for the current commit if it is not already
	// buffered or in-flight.
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
		cmds = append(cmds, generateRewordCmd(eng, c, parentTree, "", opts))
		m.rewordSt.inFlight[c.Hash] = true
		m.setStatus(fmt.Sprintf("Generating reword variants for %s…", short(c.Hash)), statusNone)

		// Best-effort prefetch the next commit in display order.
		nextDisplayIdx := m.listCursor + 1
		if nextDisplayIdx < len(m.plan.Commits) {
			nextStorage := len(m.plan.Commits) - 1 - nextDisplayIdx
			if !m.plan.IsFrozen(nextStorage) {
				nextC := m.commitAtDisplayIndex(nextDisplayIdx)
				if _, nCached := m.rewordSt.buffer[nextC.Hash]; !nCached && !m.rewordSt.inFlight[nextC.Hash] {
					nextParent, perr := resolveParentTree(&m, nextC)
					if perr == nil {
						cmds = append(cmds, generateRewordCmd(eng, nextC, nextParent, "", opts))
						m.rewordSt.inFlight[nextC.Hash] = true
					}
					// prefetch failure is silent — the result just won't be cached
				}
			}
		}
	}

	return m, tea.Batch(cmds...), true
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
		m.setStatus("Reword cancelled", statusWarn)
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
			edit := result.ToEdit(m.rewordSt.sel)
			m.plan.SetEdit(hash, edit)
			m.mode = modeMain
			m.setStatus(fmt.Sprintf("Reword applied to %s", short(hash)), statusOK)
		} else {
			// Still generating: stay in the picker and show a hint.
			m.setStatus("Still generating — please wait…", statusWarn)
		}
		return m, nil
	}
	return m, nil
}
