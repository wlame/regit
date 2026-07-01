package capability

import (
	"fmt"
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
			// Agent-scan mode: the agent may browse the repo read-only
			// (Read/Grep/Glob; no exec). The diff is still gathered by regit and
			// sent in the prompt. Any user --agent-args are APPENDED to the default
			// scan args, never a replacement — so the deny-list, plan mode, and
			// allow-list can never be silently dropped. A user arg that tries to
			// override a protective flag is refused outright.
			args := append([]string{}, llm.ScanDefaultArgs...)
			if o.AgentArgs != "" {
				extra := strings.Fields(o.AgentArgs)
				if bad := firstProtectedFlag(extra); bad != "" {
					return nil, fmt.Errorf("--agent-args may not override the agent-scan safety flag %q", bad)
				}
				args = append(args, extra...)
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

	// An empty model would fail deep inside the request; catch it here. A common
	// cause is `--llm=gpt-4o` (no slash), which parses as provider "gpt-4o" with no
	// model — surface that clearly.
	if modelName == "" {
		return nil, fmt.Errorf("no model in --llm: use provider/model, e.g. openai/gpt-4o (got provider %q)", provider)
	}

	// Determine base URL: explicit flag > config > provider default.
	baseURL := o.BaseURL
	if baseURL == "" {
		baseURL = llm.DefaultBaseURL(provider, cfg.LLM.BaseURL)
	}
	// An unknown provider resolves to an empty base URL, which would otherwise fail
	// obscurely at request time. Fail fast with the list of known providers.
	if baseURL == "" {
		return nil, fmt.Errorf("unknown LLM provider %q; known: ollama, lmstudio, openai, openrouter (or pass --llm-base-url)", provider)
	}

	timeoutSec := cfg.LLM.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 60 // sane default; zero would reject all requests
	}
	return llm.NewHTTPBackend(baseURL, modelName, llm.ResolveAPIKey(provider), time.Duration(timeoutSec)*time.Second), nil
}

// protectedScanFlags are the agent-scan flags a user's --agent-args must not
// override — doing so could re-enable Bash, widen the allow-list, or drop the
// deny-list. firstProtectedFlag returns the first offending token, or "".
var protectedScanFlags = []string{"--permission-mode", "--allowedTools", "--disallowedTools"}

func firstProtectedFlag(args []string) string {
	for _, a := range args {
		name := a
		if i := strings.IndexByte(a, '='); i >= 0 { // handle --flag=value form
			name = a[:i]
		}
		for _, p := range protectedScanFlags {
			if name == p {
				return p
			}
		}
	}
	return ""
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
