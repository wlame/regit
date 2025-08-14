package capability

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/llm"
)

// BackendOptions are the LLM-related inputs a verb collects from its flags.
// The zero value means "no LLM configured" — BuildBackend returns an error in
// that case.
type BackendOptions struct {
	LLM       string // "provider/model", e.g. "ollama/qwen2.5-coder"
	Agent     string // agent CLI binary, e.g. "claude"
	AgentArgs string // space-separated args replacing the default invocation
	BaseURL   string // override the inferred OpenAI-compatible endpoint
	AgentScan bool   // run the agent read-only in the repo (requires Agent)
	RepoRoot  string // subprocess cwd when AgentScan is true
}

// BuildBackend constructs an llm.Backend from options + config. --agent wins
// over --llm over config values. API keys come from env only. It unifies the
// former per-verb buildBackend (split) and buildRewordBackend (reword) into one
// function that handles both the standard agent path and the agent-scan path.
func BuildBackend(cfg config.Config, o BackendOptions) (llm.Backend, error) {
	if o.Agent != "" {
		if o.AgentScan {
			// Agent-scan mode: the agent inspects the repo read-only.
			// Use caller-supplied args or the canonical read-only scan defaults.
			args := llm.ScanDefaultArgs
			if o.AgentArgs != "" {
				args = strings.Fields(o.AgentArgs)
			}
			return llm.NewAgentBackendInDir(o.Agent, args, o.RepoRoot), nil
		}
		if o.AgentArgs != "" {
			return llm.NewAgentBackend(o.Agent, strings.Fields(o.AgentArgs)), nil
		}
		return llm.NewAgentBackend(o.Agent, nil), nil
	}

	// Start from config values; let o.LLM override them.
	provider, modelName := cfg.LLM.Provider, cfg.LLM.Model
	if o.LLM != "" {
		provider, modelName = splitProviderModel(o.LLM)
	}
	if provider == "" {
		return nil, fmt.Errorf("no LLM configured: pass --llm=provider/model or --agent=<cmd>")
	}

	// Determine base URL: explicit flag > config > provider default.
	baseURL := o.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL(provider, cfg.LLM.BaseURL)
	}

	timeoutSec := cfg.LLM.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 60 // sane default; zero would reject all requests
	}
	return llm.NewHTTPBackend(baseURL, modelName, resolveAPIKey(provider), time.Duration(timeoutSec)*time.Second), nil
}

// splitProviderModel splits "provider/model" on the first slash. For
// three-segment strings like "openrouter/anthropic/claude-x", the provider is
// "openrouter" and the model is "anthropic/claude-x".
func splitProviderModel(s string) (provider, modelName string) {
	idx := strings.IndexByte(s, '/')
	if idx < 0 {
		// No slash — treat the whole string as a provider with no model.
		return s, ""
	}
	return s[:idx], s[idx+1:]
}

// providerBaseURLs maps well-known provider names to their default OpenAI-
// compatible base URLs. A table avoids a chain of if/else and makes adding a
// new provider a one-line change.
var providerBaseURLs = map[string]string{
	"ollama":     "http://localhost:11434/v1",
	"lmstudio":   "http://localhost:1234/v1",
	"openai":     "https://api.openai.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
}

// defaultBaseURL returns the base URL for the given provider. The override
// (from --llm-base-url or config.LLM.BaseURL) wins when non-empty.
func defaultBaseURL(provider, override string) string {
	if override != "" {
		return override
	}
	return providerBaseURLs[provider]
}

// providerEnvKeys maps provider names to their conventional API-key env var.
var providerEnvKeys = map[string]string{
	"openai":     "OPENAI_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
}

// resolveAPIKey looks up the API key for the given provider from environment
// variables only — never from flags or config files. Resolution order:
//  1. REGIT_LLM_API_KEY (tool-wide override)
//  2. Provider-standard var (OPENAI_API_KEY, OPENROUTER_API_KEY, …)
//  3. "" for local providers (ollama, lmstudio) that need no key
func resolveAPIKey(provider string) string {
	if key := os.Getenv("REGIT_LLM_API_KEY"); key != "" {
		return key
	}
	if envVar, ok := providerEnvKeys[provider]; ok {
		return os.Getenv(envVar)
	}
	return ""
}
