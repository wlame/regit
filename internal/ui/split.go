package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/split"
)

// splitState holds the active split review data while the split proposal is staged.
type splitState struct {
	proposal model.SplitProposal
	trees    []string
	// errMsg is non-empty if the engine call failed.
	errMsg string
}

// buildSplitEngine constructs a split Engine from the TUI's config, using
// the same resolution logic as cmd/regit buildBackend.
//
// TODO: unify with cmd/regit buildBackend once extraction into a shared helper
// package is feasible.
func buildSplitEngine(m *Model) (*split.Engine, error) {
	cfg := m.cfg
	provider := cfg.LLM.Provider
	modelName := cfg.LLM.Model
	if provider == "" {
		return nil, fmt.Errorf("no LLM configured — set llm.provider and llm.model in config or REGIT_LLM_PROVIDER/MODEL env vars")
	}

	// Determine base URL from config.
	baseURL := llm.DefaultBaseURL(provider, cfg.LLM.BaseURL)

	// API key comes only from environment (never from config file or CLI args).
	apiKey := llm.ResolveAPIKey(provider)
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
	cache := split.NewCache(gitDir)
	eng := split.NewEngine(m.repo, backend, cache)
	return eng, nil
}

// splitInputs is everything generateSplitCmd needs to run Propose off-thread —
// resolved on the UI thread first so the command closes over no *Model.
type splitInputs struct {
	eng        *split.Engine
	commit     model.Commit
	parentTree string
	opts       split.Options
}

// buildSplitInputs resolves the engine, parent tree, and options for a split on
// the UI thread. Returns an error the caller surfaces as a status message.
func buildSplitInputs(m *Model, commit model.Commit) (splitInputs, error) {
	eng, err := buildSplitEngine(m)
	if err != nil {
		return splitInputs{}, err
	}
	var parentTree string
	if len(commit.ParentHashes) == 0 {
		parentTree, err = m.repo.EmptyTree()
	} else {
		parentTree, err = m.repo.ResolveRef(commit.ParentHashes[0] + "^{tree}")
	}
	if err != nil {
		return splitInputs{}, err
	}
	cfg := m.cfg
	opts := split.Options{
		MaxCommits: cfg.Split.MaxCommits, LinesPerCommit: cfg.Split.LinesPerCommit,
		MinSplitLines: cfg.Split.MinSplitLines, RepairAttempts: cfg.Split.RepairAttempts,
		WIPPrefix: cfg.Split.WIPPrefix, ContextKey: strings.Join(cfg.Split.Context, ","),
		Now: time.Now(),
	}
	return splitInputs{eng: eng, commit: commit, parentTree: parentTree, opts: opts}, nil
}

// splitResultMsg carries the finished proposal back onto the Bubble Tea bus.
// seq is the generation token beginSplit assigned this call; Update only
// accepts a result whose seq still matches the model's current splitGenSeq,
// so a result from an abandoned generation is dropped even if it shares hash
// with the generation that replaced it (abort, then immediately restart).
type splitResultMsg struct {
	hash  string
	seq   int
	state splitState
}

// generateSplitCmd runs Engine.Propose off the UI thread (mirrors generateRewordCmd).
// seq is stamped onto every returned splitResultMsg so Update can discriminate
// this specific generation from any other in-flight or abandoned one.
func generateSplitCmd(in splitInputs, seq int) tea.Cmd {
	return func() tea.Msg {
		prop, trees, err := in.eng.Propose(context.Background(), in.commit, in.parentTree, "", in.opts)
		if err != nil {
			return splitResultMsg{hash: in.commit.Hash, seq: seq, state: splitState{errMsg: "propose: " + err.Error()}}
		}
		if !prop.ShouldSplit || len(prop.Groups) < 2 {
			reason := prop.Reason
			if reason == "" {
				reason = "commit is not worth splitting"
			}
			return splitResultMsg{hash: in.commit.Hash, seq: seq, state: splitState{errMsg: fmt.Sprintf("not splitting %s: %s", short(in.commit.Hash), reason)}}
		}
		return splitResultMsg{hash: in.commit.Hash, seq: seq, state: splitState{proposal: prop, trees: trees}}
	}
}
