package main

import "testing"

// TestParseSplitFlags_defaultsAndOverrides verifies that positional commit arg,
// numeric flags, string flags, and boolean flags are all parsed correctly.
func TestParseSplitFlags_defaultsAndOverrides(t *testing.T) {
	f, err := parseSplitFlags([]string{"abc123", "--max-commits=3", "--llm=ollama/qwen2.5-coder", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Commit != "abc123" {
		t.Fatalf("commit = %q", f.Commit)
	}
	if f.MaxCommits != 3 || !f.DryRun {
		t.Fatalf("flags = %+v", f)
	}
	if f.LLM != "ollama/qwen2.5-coder" {
		t.Fatalf("llm = %q", f.LLM)
	}
}

// TestParseSplitFlags_defaultCommitIsHEAD verifies that when no positional
// argument is provided, Commit defaults to "HEAD".
func TestParseSplitFlags_defaultCommitIsHEAD(t *testing.T) {
	f, err := parseSplitFlags([]string{"--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Commit != "HEAD" {
		t.Fatalf("expected Commit = HEAD, got %q", f.Commit)
	}
}

// TestParseSplitFlags_repoFlag verifies that --repo is parsed by the split
// flag set itself (not just the top-level flag set), so `regit split --repo=<path> <commit>`
// works correctly.
func TestParseSplitFlags_repoFlag(t *testing.T) {
	f, err := parseSplitFlags([]string{"--repo=/tmp/x", "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Repo != "/tmp/x" {
		t.Fatalf("Repo = %q, want %q", f.Repo, "/tmp/x")
	}
	if f.Commit != "HEAD" {
		t.Fatalf("Commit = %q, want %q", f.Commit, "HEAD")
	}
}

// TestSplitProviderModel covers simple two-part and three-part (openrouter) splits.
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
