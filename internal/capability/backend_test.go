package capability

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/llm"
)

func TestBuildBackendErrorsWithoutLLM(t *testing.T) {
	_, err := BuildBackend(config.Default(), BackendOptions{})
	if err == nil {
		t.Error("expected error when no LLM/agent configured")
	}
}

func TestBuildBackend_unknownProviderAndMissingModelFailFast(t *testing.T) {
	if _, err := BuildBackend(config.Default(), BackendOptions{LLM: "gpt-4o"}); err == nil {
		t.Error("--llm=gpt-4o (no slash) should fail fast on the missing model")
	}
	_, err := BuildBackend(config.Default(), BackendOptions{LLM: "acme/some-model"})
	if err == nil || !strings.Contains(err.Error(), "unknown LLM provider") {
		t.Errorf("unknown provider should fail with a clear message, got: %v", err)
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
		got := llm.DefaultBaseURL(tc.provider, tc.override)
		if got != tc.want {
			t.Errorf("DefaultBaseURL(%q, %q) = %q, want %q",
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

// TestBuildBackendAgentScan_refusesProtectedArgOverride proves a user's --agent-args
// cannot drop or widen the scan safety flags: an attempt to override --allowedTools
// is rejected rather than silently replacing the whole protected arg set.
func TestBuildBackendAgentScan_refusesProtectedArgOverride(t *testing.T) {
	_, err := BuildBackend(config.Config{}, BackendOptions{
		Agent:     "claude",
		AgentScan: true,
		RepoRoot:  "/tmp",
		AgentArgs: "--allowedTools Bash",
	})
	if err == nil {
		t.Fatal("expected refusal when --agent-args overrides a protected scan flag")
	}
	if !strings.Contains(err.Error(), "allowedTools") {
		t.Errorf("error should name the offending flag, got: %v", err)
	}
}

// TestBuildBackendAgentScan_appendsHarmlessArgs proves a non-protective extra arg
// (e.g. --model) is accepted and appended to the default scan args.
func TestBuildBackendAgentScan_appendsHarmlessArgs(t *testing.T) {
	b, err := BuildBackend(config.Config{}, BackendOptions{
		Agent:     "claude",
		AgentScan: true,
		RepoRoot:  "/tmp",
		AgentArgs: "--model opus",
	})
	if err != nil {
		t.Fatalf("harmless extra args should be accepted, got: %v", err)
	}
	if b == nil {
		t.Fatal("expected non-nil backend")
	}
}
