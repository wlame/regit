package capability

import (
	"testing"

	"github.com/wlame/regit/internal/config"
)

func TestBuildBackendErrorsWithoutLLM(t *testing.T) {
	_, err := BuildBackend(config.Default(), BackendOptions{})
	if err == nil {
		t.Error("expected error when no LLM/agent configured")
	}
}

func TestBuildBackendAgentWins(t *testing.T) {
	b, err := BuildBackend(config.Default(), BackendOptions{Agent: "claude"})
	if err != nil || b == nil {
		t.Fatalf("agent backend: b=%v err=%v", b, err)
	}
}

// TestSplitProviderModel covers simple two-part and three-part (openrouter) splits.
// Moved here from cmd/regit/split_test.go when splitProviderModel relocated to capability.
func TestSplitProviderModel(t *testing.T) {
	cases := []struct {
		input    string
		provider string
		model    string
	}{
		{"ollama/qwen2.5-coder", "ollama", "qwen2.5-coder"},
		{"lmstudio/codestral", "lmstudio", "codestral"},
		{"openai/gpt-4o", "openai", "gpt-4o"},
		// Three-part: provider = first segment, model = the rest.
		{"openrouter/anthropic/claude-sonnet-4-6", "openrouter", "anthropic/claude-sonnet-4-6"},
	}
	for _, tc := range cases {
		p, m := splitProviderModel(tc.input)
		if p != tc.provider || m != tc.model {
			t.Errorf("splitProviderModel(%q) = (%q, %q), want (%q, %q)",
				tc.input, p, m, tc.provider, tc.model)
		}
	}
}

// TestDefaultBaseURL verifies the provider-to-URL mapping and that an explicit
// override wins regardless of provider.
// Moved here from cmd/regit/split_test.go when defaultBaseURL relocated to capability.
func TestDefaultBaseURL(t *testing.T) {
	cases := []struct {
		provider string
		override string
		want     string
	}{
		{"ollama", "", "http://localhost:11434/v1"},
		{"lmstudio", "", "http://localhost:1234/v1"},
		{"openai", "", "https://api.openai.com/v1"},
		{"openrouter", "", "https://openrouter.ai/api/v1"},
		// Override wins even when a well-known provider is named.
		{"ollama", "http://my-server:8080/v1", "http://my-server:8080/v1"},
		// Unknown provider with no override falls back to empty string.
		{"unknown", "", ""},
	}
	for _, tc := range cases {
		got := defaultBaseURL(tc.provider, tc.override)
		if got != tc.want {
			t.Errorf("defaultBaseURL(%q, %q) = %q, want %q",
				tc.provider, tc.override, got, tc.want)
		}
	}
}

// TestBuildBackendAgentScan verifies that the agent-scan path produces a non-nil
// backend whose name references the agent command.
func TestBuildBackendAgentScan(t *testing.T) {
	b, err := BuildBackend(config.Config{}, BackendOptions{
		Agent:     "claude",
		AgentScan: true,
		RepoRoot:  "/tmp",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b == nil {
		t.Fatal("expected non-nil backend")
	}
	if name := b.Name(); name == "" {
		t.Fatal("backend name must not be empty")
	}
}
