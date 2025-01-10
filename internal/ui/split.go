package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/split"
)

// splitState holds the active split review data while in modeSplit.
type splitState struct {
	proposal model.SplitProposal
	trees    []string
	// errMsg is non-empty if the engine call or apply failed.
	errMsg string
}

// renderSplitReview renders a split proposal as a review screen.
// Each group gets a line: "N. Subject [intent] (tests: pass|WIP)"
// This is a pure function that does no I/O — ideal for unit testing.
func renderSplitReview(prop model.SplitProposal) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Split proposal for %s\n\n", short(prop.CommitHash)))
	for _, g := range prop.Groups {
		testStatus := "pass"
		if g.WIP {
			testStatus = "WIP"
		}
		intent := g.Intent
		if intent == "" {
			intent = "?"
		}
		b.WriteString(fmt.Sprintf("%d. %s [%s] (tests: %s)\n", g.Order, g.Subject, intent, testStatus))
	}
	b.WriteString("\nenter apply · esc cancel")
	return b.String()
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
	baseURL := defaultSplitBaseURL(provider, cfg.LLM.BaseURL)

	// API key comes only from environment (never from config file or CLI args).
	apiKey := resolveSplitAPIKey(provider)
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

// providerSplitBaseURLs maps well-known provider names to their default
// OpenAI-compatible base URLs.
var providerSplitBaseURLs = map[string]string{
	"ollama":     "http://localhost:11434/v1",
	"lmstudio":   "http://localhost:1234/v1",
	"openai":     "https://api.openai.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
}

func defaultSplitBaseURL(provider, override string) string {
	if override != "" {
		return override
	}
	return providerSplitBaseURLs[provider]
}

// providerSplitEnvKeys maps provider names to their conventional API-key env var.
var providerSplitEnvKeys = map[string]string{
	"openai":     "OPENAI_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
}

func resolveSplitAPIKey(provider string) string {
	if key := os.Getenv("REGIT_LLM_API_KEY"); key != "" {
		return key
	}
	if envVar, ok := providerSplitEnvKeys[provider]; ok {
		return os.Getenv(envVar)
	}
	return ""
}

// runSplitOnCommit runs the split engine on the given commit and returns a
// filled splitState. Blocking is acceptable for v1; the TUI shows a
// "thinking…" status message while this runs.
func runSplitOnCommit(m *Model, commit model.Commit) splitState {
	eng, err := buildSplitEngine(m)
	if err != nil {
		return splitState{errMsg: "build engine: " + err.Error()}
	}

	// Resolve the parent tree hash (empty tree for root commits).
	var parentTree string
	if len(commit.ParentHashes) == 0 {
		parentTree, err = m.repo.EmptyTree()
		if err != nil {
			return splitState{errMsg: "resolve empty tree: " + err.Error()}
		}
	} else {
		parentTree, err = m.repo.ResolveRef(commit.ParentHashes[0] + "^{tree}")
		if err != nil {
			return splitState{errMsg: "resolve parent tree: " + err.Error()}
		}
	}

	cfg := m.cfg
	opts := split.Options{
		MaxCommits:     cfg.Split.MaxCommits,
		LinesPerCommit: cfg.Split.LinesPerCommit,
		MinSplitLines:  cfg.Split.MinSplitLines,
		RepairAttempts: cfg.Split.RepairAttempts,
		WIPPrefix:      cfg.Split.WIPPrefix,
		ContextKey:     strings.Join(cfg.Split.Context, ","),
		Now:            time.Now(),
	}

	prop, trees, err := eng.Propose(context.Background(), commit, parentTree, "", opts)
	if err != nil {
		return splitState{errMsg: "propose: " + err.Error()}
	}
	if !prop.ShouldSplit || len(prop.Groups) < 2 {
		reason := prop.Reason
		if reason == "" {
			reason = "commit is not worth splitting"
		}
		return splitState{errMsg: fmt.Sprintf("not splitting %s: %s", short(commit.Hash), reason)}
	}

	return splitState{proposal: prop, trees: trees}
}
