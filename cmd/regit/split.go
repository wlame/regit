package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
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
	// AgentArgs, when non-empty, replaces the default agent invocation args.
	// Use the literal {schema} token where the JSON schema should be injected.
	AgentArgs string
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
	fs.StringVar(&f.AgentArgs, "agent-args", "", "space-separated args that replace the default agent invocation; use the literal {schema} token where the JSON schema should be injected")
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

	idx, err := capability.FindCommit(repo, commits, f.Commit)
	if err != nil {
		return err
	}
	target := commits[idx]

	parentTree, err := capability.ParentTreeOf(repo, target)
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
		fmt.Printf("Not splitting %s: %s\n", capability.Short(target.Hash), reason)
		return nil
	}

	backend, err := capability.BuildBackend(cfg, capability.BackendOptions{
		LLM:       f.LLM,
		Agent:     f.Agent,
		AgentArgs: f.AgentArgs,
		BaseURL:   f.BaseURL,
	})
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
		fmt.Printf("Not splitting %s: %s\n", capability.Short(target.Hash), prop.Reason)
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
		capability.Short(target.Hash), len(subs), capability.Short(res.NewHead), res.BackupRef)
	return nil
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
	fmt.Printf("Split proposal for %s %q\n", capability.Short(target.Hash), target.Subject)
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
	fmt.Printf("  # splitting commit %s %q\n", capability.Short(target.Hash), target.Subject)
	fmt.Printf("  git backup HEAD  # implicit safety backup\n")
	for i, g := range prop.Groups {
		subject := g.Subject
		if i == 0 {
			fmt.Printf("  git commit-tree <tree%d> -p %s -m %q\n", i+1, capability.Short(prop.ParentHash), subject)
		} else {
			fmt.Printf("  git commit-tree <tree%d> -p <prev> -m %q\n", i+1, subject)
		}
	}
	fmt.Printf("  git update-ref HEAD <last-sub-commit>\n")
	fmt.Printf("  # (tail commits replayed on top with remapped parents)\n")
}
