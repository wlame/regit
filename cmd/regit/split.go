package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/split"
)

// splitFlags holds every parsed flag for the `regit split` subcommand.
type splitFlags struct {
	// Commit is the commit-ish to split (positional arg, defaults to "HEAD").
	Commit string
	// Repo is the path to the git repository (defaults to ".").
	Repo string
	// LLM is the provider/model string, e.g. "ollama/qwen2.5-coder".
	LLM string
	// Agent is the agent CLI binary to use instead of an HTTP backend.
	Agent string
	// BaseURL overrides the inferred OpenAI-compatible endpoint.
	BaseURL string
	// Prompt is a short user hint passed into the LLM request.
	Prompt string
	// MaxCommits caps the number of resulting commits (0 = use config default).
	MaxCommits int
	// TestCmd is a shell command to run after each intermediate stage.
	TestCmd string
	// DryRun prints the proposal and writes the cache without applying anything.
	DryRun bool
	// Yes skips the interactive confirmation prompt (useful in CI/one-shot mode).
	Yes bool
	// NoCache ignores any cached split proposal (forces a fresh LLM call).
	NoCache bool
	// ClearCache deletes the split cache directory and exits immediately.
	ClearCache bool
}

// parseSplitFlags parses `regit split [<commit-ish>] [flags]`. The commit-ish
// may appear anywhere in the arg list as the first non-flag token; it defaults
// to "HEAD" when absent. Flags may appear before or after the positional arg
// (we pre-scan and extract the commit-ish so the flag set sees only flags).
func parseSplitFlags(args []string) (splitFlags, error) {
	// Pre-scan: separate the first non-flag token (the commit-ish) from the
	// flag tokens. A non-flag token is any arg that does not start with "-".
	// This lets callers write: `regit split abc123 --dry-run` OR
	//                           `regit split --dry-run abc123`
	var commitArg string
	var flagArgs []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && commitArg == "" {
			commitArg = a
		} else {
			flagArgs = append(flagArgs, a)
		}
	}

	fs := flag.NewFlagSet("split", flag.ContinueOnError)
	var f splitFlags
	fs.StringVar(&f.Repo, "repo", ".", "path to the git repository")
	fs.StringVar(&f.LLM, "llm", "", "provider/model, e.g. ollama/qwen2.5-coder")
	fs.StringVar(&f.Agent, "agent", "", "agent CLI to drive, e.g. claude")
	fs.StringVar(&f.BaseURL, "llm-base-url", "", "override OpenAI-compatible endpoint")
	fs.StringVar(&f.Prompt, "prompt", "", "short guidance passed to the LLM")
	fs.IntVar(&f.MaxCommits, "max-commits", 0, "max resulting commits (0 = config default)")
	fs.StringVar(&f.TestCmd, "test-cmd", "", "command to validate each intermediate commit")
	fs.BoolVar(&f.DryRun, "dry-run", false, "print the proposal and write cache without applying")
	fs.BoolVar(&f.Yes, "yes", false, "skip confirmation (CI/one-shot)")
	fs.BoolVar(&f.NoCache, "no-cache", false, "ignore any cached proposal")
	fs.BoolVar(&f.ClearCache, "clear-cache", false, "delete the split cache and exit")
	if err := fs.Parse(flagArgs); err != nil {
		return f, err
	}

	if commitArg != "" {
		f.Commit = commitArg
	} else {
		f.Commit = "HEAD"
	}
	return f, nil
}

// runSplit is the end-to-end headless entry point for `regit split`. It is
// deliberately kept free of global state so the TUI can call the same helpers.
// repoDir is the caller-supplied repo path (from the top-level --repo flag);
// if the split-local --repo flag is set, it takes precedence.
func runSplit(repoDir string, args []string) error {
	f, err := parseSplitFlags(args)
	if err != nil {
		return err
	}

	// The split-subcommand --repo flag wins over the top-level value.
	if f.Repo != "." {
		repoDir = f.Repo
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	repo, err := gitio.Open(repoDir)
	if err != nil {
		return err
	}

	// --clear-cache deletes the split cache directory and exits immediately.
	if f.ClearCache {
		gitDir, err := repo.GitDir()
		if err != nil {
			return fmt.Errorf("resolve git dir: %w", err)
		}
		return split.NewCache(gitDir).Clear()
	}

	commits, err := repo.ReadCommits()
	if err != nil {
		return fmt.Errorf("read commits: %w", err)
	}

	idx, err := findCommit(repo, commits, f.Commit)
	if err != nil {
		return err
	}
	target := commits[idx]

	parentTree, err := parentTreeOf(repo, target)
	if err != nil {
		return fmt.Errorf("resolve parent tree: %w", err)
	}

	opts := buildOptions(cfg, f)

	// Decide whether the commit is even worth splitting BEFORE requiring an LLM.
	// Trivial/no-op cases (too small, single hunk, binary-only, pure rename) need
	// no model, so we must not error out with "no LLM configured" for them. This
	// mirrors the engine's own ShouldSkipSplit guard; the engine re-checks it for
	// the TUI path, so this is a cheap CLI-side pre-filter, not the source of truth.
	hunks, err := repo.Hunks(parentTree, target.TreeHash)
	if err != nil {
		return fmt.Errorf("read commit diff: %w", err)
	}
	if skip, reason := split.ShouldSkipSplit(hunks, opts.MinSplitLines); skip {
		fmt.Printf("Not splitting %s: %s\n", short(target.Hash), reason)
		return nil
	}

	backend, err := buildBackend(cfg, f)
	if err != nil {
		return err
	}

	gitDir, err := repo.GitDir()
	if err != nil {
		return fmt.Errorf("resolve git dir: %w", err)
	}

	// When --no-cache is set, use a throwaway cache dir so it always misses.
	var cache *split.Cache
	if f.NoCache {
		tmp, err := os.MkdirTemp("", "regit-split-nocache-*")
		if err != nil {
			return fmt.Errorf("create temp cache dir: %w", err)
		}
		defer os.RemoveAll(tmp)
		cache = split.NewCache(tmp)
	} else {
		cache = split.NewCache(gitDir)
	}

	eng := split.NewEngine(repo, backend, cache)
	readme := readmeIfRequested(repo, cfg)

	prop, trees, err := eng.Propose(context.Background(), target, parentTree, readme, opts)
	if err != nil {
		return err
	}
	if !prop.ShouldSplit || len(prop.Groups) < 2 {
		fmt.Printf("Not splitting %s: %s\n", short(target.Hash), prop.Reason)
		return nil
	}

	printProposal(target, prop)
	printEquivalentCommands(target, prop)

	if f.DryRun {
		fmt.Println("\n(dry run — nothing applied; proposal cached)")
		return nil
	}

	subs := split.BuildSubCommits(target, prop.Groups, trees, cfg.Split.WIPPrefix)
	tail := commits[idx+1:]

	state, err := repo.ReadState()
	if err != nil {
		return fmt.Errorf("read repository state: %w", err)
	}

	mgr := safety.NewManager(repo)
	res, err := mgr.ApplySplit(target, prop.ParentHash, subs, tail, state, safety.ApplyOptions{
		Sign:          gitio.DropSignatures,
		UnixTimestamp: time.Now().Unix(),
		Confirmed:     f.Yes,
	})
	if err != nil {
		return err
	}

	fmt.Printf("\nSplit %s into %d commits. New HEAD %s (backup %s)\n",
		short(target.Hash), len(subs), short(res.NewHead), res.BackupRef)
	return nil
}

// buildBackend constructs an LLM backend from flags/config. Flags take
// precedence over config values. Returns an error if no LLM is configured.
func buildBackend(cfg config.Config, f splitFlags) (llm.Backend, error) {
	// --agent wins over --llm and config.
	if f.Agent != "" {
		return llm.NewAgentBackend(f.Agent, nil), nil
	}

	// Start from config values; let --llm override them.
	provider, modelName := cfg.LLM.Provider, cfg.LLM.Model
	if f.LLM != "" {
		provider, modelName = splitProviderModel(f.LLM)
	}
	if provider == "" {
		return nil, fmt.Errorf("no LLM configured: pass --llm=provider/model or --agent=<cmd>")
	}

	// Determine base URL: explicit flag > config > provider default.
	baseURL := f.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL(provider, cfg.LLM.BaseURL)
	}

	apiKey := resolveAPIKey(provider)
	timeoutSec := cfg.LLM.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 60 // sane default; a zero-second timeout would reject all requests
	}
	timeout := time.Duration(timeoutSec) * time.Second
	return llm.NewHTTPBackend(baseURL, modelName, apiKey, timeout), nil
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
// compatible base URLs. Keeping these in a table avoids a chain of if/else and
// makes adding a new provider a one-line change.
var providerBaseURLs = map[string]string{
	"ollama":      "http://localhost:11434/v1",
	"lmstudio":    "http://localhost:1234/v1",
	"openai":      "https://api.openai.com/v1",
	"openrouter":  "https://openrouter.ai/api/v1",
}

// defaultBaseURL returns the base URL to use for the given provider. The
// override (from --llm-base-url or config.LLM.BaseURL) wins when non-empty.
func defaultBaseURL(provider, override string) string {
	if override != "" {
		return override
	}
	return providerBaseURLs[provider]
}

// providerEnvKeys maps provider names to their conventional API-key env var.
// This data table replaces a chain of if/else for each provider.
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

// buildOptions converts config defaults and per-run flags into split.Options.
// Flag values with a meaningful non-zero value override the config defaults.
func buildOptions(cfg config.Config, f splitFlags) split.Options {
	opts := split.Options{
		MaxCommits:     cfg.Split.MaxCommits,
		LinesPerCommit: cfg.Split.LinesPerCommit,
		MinSplitLines:  cfg.Split.MinSplitLines,
		RepairAttempts: cfg.Split.RepairAttempts,
		WIPPrefix:      cfg.Split.WIPPrefix,
		ContextKey:     strings.Join(cfg.Split.Context, ","),
		UserHint:       f.Prompt,
		Now:            time.Now(),
	}
	if f.MaxCommits > 0 {
		opts.MaxCommits = f.MaxCommits
	}
	if f.TestCmd != "" {
		opts.TestCmd = strings.Fields(f.TestCmd)
	}
	return opts
}

// short returns up to the first 8 characters of a hash string, guarding
// against hashes shorter than 8 characters (e.g. during tests or on empty input).
func short(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// findCommit resolves ref to a full commit hash and finds its index in the
// root-first commits slice. Returns an error if the ref cannot be resolved or
// is not on the linear first-parent history.
func findCommit(repo *gitio.Repo, commits []model.Commit, ref string) (int, error) {
	hash, err := repo.ResolveRef(ref)
	if err != nil {
		return -1, fmt.Errorf("resolve %q: %w", ref, err)
	}
	for i, c := range commits {
		if c.Hash == hash {
			return i, nil
		}
	}
	return -1, fmt.Errorf("commit %s (%s) is not on the current first-parent history", ref, short(hash))
}

// parentTreeOf returns the tree hash of commit c's parent, or the empty-tree
// hash when c is the root commit (no parents).
func parentTreeOf(repo *gitio.Repo, c model.Commit) (string, error) {
	if len(c.ParentHashes) == 0 {
		return repo.EmptyTree()
	}
	// "<parent>^{tree}" dereferences the parent commit object to its tree object.
	return repo.ResolveRef(c.ParentHashes[0] + "^{tree}")
}

// readmeIfRequested reads README.md from the repo root when "readme" appears in
// cfg.Split.Context. Returns an empty string when not requested or unreadable
// (a missing README is not an error — it just provides no context).
func readmeIfRequested(repo *gitio.Repo, cfg config.Config) string {
	for _, ctx := range cfg.Split.Context {
		if ctx == "readme" {
			data, err := os.ReadFile(repo.Root() + "/README.md")
			if err != nil {
				return "" // README absent or unreadable — silently ignore
			}
			return string(data)
		}
	}
	return ""
}

// printProposal prints a human-readable summary of the split proposal.
func printProposal(target model.Commit, prop model.SplitProposal) {
	fmt.Printf("Split proposal for %s %q\n", short(target.Hash), target.Subject)
	fmt.Printf("  %d commits planned\n\n", len(prop.Groups))
	for i, g := range prop.Groups {
		wip := ""
		if g.WIP {
			wip = " [WIP]"
		}
		fmt.Printf("  %d. %s%s\n", i+1, g.Subject, wip)
		for _, id := range g.HunkIDs {
			fmt.Printf("       %s\n", id)
		}
	}
}

// printEquivalentCommands prints the equivalent git commands the split would
// issue, satisfying the CLI transparency principle: every UI action should be
// traceable to the concrete git commands behind it.
func printEquivalentCommands(target model.Commit, prop model.SplitProposal) {
	fmt.Printf("\nEquivalent git commands (for reference):\n")
	fmt.Printf("  # splitting commit %s %q\n", short(target.Hash), target.Subject)
	fmt.Printf("  git backup HEAD  # implicit safety backup\n")
	for i, g := range prop.Groups {
		subject := g.Subject
		if i == 0 {
			fmt.Printf("  git commit-tree <tree%d> -p %s -m %q\n", i+1, short(prop.ParentHash), subject)
		} else {
			fmt.Printf("  git commit-tree <tree%d> -p <prev> -m %q\n", i+1, subject)
		}
	}
	fmt.Printf("  git update-ref HEAD <last-sub-commit>\n")
	fmt.Printf("  # (tail commits replayed on top with remapped parents)\n")
}
