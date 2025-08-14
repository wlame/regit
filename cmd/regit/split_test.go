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

// TestParseSplitFlags_agentArgs verifies that --agent and --agent-args are
// parsed independently and that AgentArgs captures the full space-separated string.
func TestParseSplitFlags_agentArgs(t *testing.T) {
	f, err := parseSplitFlags([]string{"--agent=claude", "--agent-args=-p --output-format json"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Agent != "claude" {
		t.Fatalf("Agent = %q, want %q", f.Agent, "claude")
	}
	if f.AgentArgs != "-p --output-format json" {
		t.Fatalf("AgentArgs = %q, want %q", f.AgentArgs, "-p --output-format json")
	}
}

// Note: TestSplitProviderModel and TestDefaultBaseURL were moved to
// internal/capability/backend_test.go when those helpers relocated there.
