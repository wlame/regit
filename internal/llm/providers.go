package llm

import "os"

// This file is the single provider table. The capability backend builder, the
// TUI's split/reword/squash wiring, and the --llm-help environment section all
// project from it — it used to exist as three separate near-identical copies
// that could drift.

// providerBaseURLs maps well-known provider names to their default
// OpenAI-compatible base URLs.
var providerBaseURLs = map[string]string{
	"ollama":     "http://localhost:11434/v1",
	"lmstudio":   "http://localhost:1234/v1",
	"openai":     "https://api.openai.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
}

// DefaultBaseURL returns the endpoint for the given provider. The override
// (from --llm-base-url or config.LLM.BaseURL) wins when non-empty; an unknown
// provider with no override yields "" (the caller reports it).
func DefaultBaseURL(provider, override string) string {
	if override != "" {
		return override
	}
	return providerBaseURLs[provider]
}

// providerEnvKeys maps provider names to their conventional API-key env var.
// Local providers (ollama, lmstudio) need no key and are absent.
var providerEnvKeys = map[string]string{
	"openai":     "OPENAI_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
}

// ProviderEnvKeys returns a copy of the provider → API-key-variable table, so
// documentation surfaces (--llm-help) can be projected from it without being
// able to mutate it.
func ProviderEnvKeys() map[string]string {
	out := make(map[string]string, len(providerEnvKeys))
	for k, v := range providerEnvKeys {
		out[k] = v
	}
	return out
}

// ResolveAPIKey looks up the API key for the given provider from environment
// variables only — never from flags or config files. Resolution order:
//  1. REGIT_LLM_API_KEY (tool-wide override)
//  2. Provider-standard var (OPENAI_API_KEY, OPENROUTER_API_KEY, …)
//  3. "" for local providers (ollama, lmstudio) that need no key
func ResolveAPIKey(provider string) string {
	if key := os.Getenv("REGIT_LLM_API_KEY"); key != "" {
		return key
	}
	if envVar, ok := providerEnvKeys[provider]; ok {
		return os.Getenv(envVar)
	}
	return ""
}
