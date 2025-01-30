package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/llm"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/reword"
	"github.com/wlame/regit/internal/safety"
)

// rewordFlags holds every parsed flag for the `regit reword` subcommand.
type rewordFlags struct {
	// Commits is the list of commit-ishes to reword (positional args, defaults
	// to ["HEAD"] when none are supplied before the first flag token).
	Commits []string
	// Repo is the path to the git repository (defaults to ".").
	Repo string
	// LLM is the provider/model string, e.g. "ollama/qwen2.5-coder".
	LLM string
	// Agent is the agent CLI binary to use instead of an HTTP backend.
	Agent string
	// AgentArgs, when non-empty, replaces the default agent invocation args.
	AgentArgs string
	// BaseURL overrides the inferred OpenAI-compatible endpoint.
	BaseURL string
	// Prompt is a short user hint passed into the LLM request.
	Prompt string
	// Variants is how many ranked alternatives to request (0 = config default).
	Variants int
	// Context is free-text context information to include in the prompt.
	Context string
	// ContextLines controls the -U<n> depth passed to git diff (0 = config default).
	ContextLines int
	// AgentScan gates the future agent-scanning code path (reserved for Task 10).
	AgentScan bool
	// DryRun prints proposals and writes cache without applying anything.
	DryRun bool
	// Yes skips the interactive confirmation prompt (useful in CI/one-shot mode).
	Yes bool
	// NoCache ignores any cached reword proposals (forces fresh LLM calls).
	NoCache bool
	// ClearCache deletes the reword cache directory and exits immediately.
	ClearCache bool
}

// parseRewordFlags parses `regit reword [<commit-ish>...] [flags]`. All leading
// non-flag tokens are treated as the commit list; they may interleave with flags
// as long as they appear before the first flag token (we pre-scan to extract them
// so the flag set receives only flag tokens). Defaults to ["HEAD"] when no
// commit-ish is provided.
func parseRewordFlags(args []string) (rewordFlags, error) {
	// Pre-scan: collect all leading non-flag tokens as the commit list, and
	// everything else as flag tokens. A non-flag token is any arg that does not
	// start with "-".
	var commits []string
	var flagArgs []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && len(flagArgs) == 0 {
			// Only treat as a commit-ish if we haven't started seeing flags yet.
			commits = append(commits, a)
		} else {
			flagArgs = append(flagArgs, a)
		}
	}

	fs := flag.NewFlagSet("reword", flag.ContinueOnError)
	var f rewordFlags
	fs.StringVar(&f.Repo, "repo", ".", "path to the git repository")
	fs.StringVar(&f.LLM, "llm", "", "provider/model, e.g. ollama/qwen2.5-coder")
	fs.StringVar(&f.Agent, "agent", "", "agent CLI to drive, e.g. claude")
	fs.StringVar(&f.AgentArgs, "agent-args", "", "space-separated args that replace the default agent invocation; use {schema} where the JSON schema should be injected")
	fs.StringVar(&f.BaseURL, "llm-base-url", "", "override OpenAI-compatible endpoint")
	fs.StringVar(&f.Prompt, "prompt", "", "short guidance passed to the LLM")
	fs.IntVar(&f.Variants, "variants", 0, "number of ranked alternatives to request (0 = config default)")
	fs.StringVar(&f.Context, "context", "", "extra context string passed to the LLM")
	fs.IntVar(&f.ContextLines, "context-lines", 0, "git diff -U context lines (0 = config default)")
	fs.BoolVar(&f.AgentScan, "agent-scan", false, "enable agent-scanning path (reserved for Task 10)")
	fs.BoolVar(&f.DryRun, "dry-run", false, "print proposals and write cache without applying")
	fs.BoolVar(&f.Yes, "yes", false, "skip confirmation (CI/one-shot)")
	fs.BoolVar(&f.NoCache, "no-cache", false, "ignore any cached proposals")
	fs.BoolVar(&f.ClearCache, "clear-cache", false, "delete the reword cache and exit")
	if err := fs.Parse(flagArgs); err != nil {
		return f, err
	}

	if len(commits) > 0 {
		f.Commits = commits
	} else {
		f.Commits = []string{"HEAD"}
	}
	return f, nil
}

// rewordItem bundles a resolved commit with its parent tree hash, ready to pass
// to the engine. Keeping these together avoids re-resolving the parent tree in
// the pipeline worker.
type rewordItem struct {
	index      int
	commit     model.Commit
	parentTree string
}

// genResult is one output from the generateAhead pipeline: either a successful
// Result or an error, always accompanied by the item's index for ordering.
type genResult struct {
	index  int
	result reword.Result
	err    error
}

// generateAhead runs a producer goroutine that calls gen for each item in order,
// streaming genResult values into a buffered channel of the given capacity. This
// allows the next item to be generated concurrently while the caller is still
// processing the current one (the "prefetch" pipeline). The worker stops on
// context cancellation; the returned channel is always closed when the worker
// finishes (either exhausted all items or was cancelled).
func generateAhead[T any](
	ctx context.Context,
	items []T,
	gen func(context.Context, T) (reword.Result, error),
	buffer int,
) <-chan genResult {
	if buffer < 1 {
		buffer = 1
	}
	out := make(chan genResult, buffer)
	go func() {
		defer close(out)
		for i, item := range items {
			r, err := gen(ctx, item)
			select {
			case out <- genResult{index: i, result: r, err: err}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// buildRewordBackend constructs an LLM backend from reword flags and config.
// It mirrors buildBackend (for the split subcommand) but takes rewordFlags
// so the two subcommands stay independent. API keys come from env only.
func buildRewordBackend(cfg config.Config, f rewordFlags) (llm.Backend, error) {
	// --agent wins over --llm and config.
	if f.Agent != "" {
		if f.AgentArgs != "" {
			return llm.NewAgentBackend(f.Agent, strings.Fields(f.AgentArgs)), nil
		}
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

	baseURL := f.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL(provider, cfg.LLM.BaseURL)
	}

	apiKey := resolveAPIKey(provider)
	timeoutSec := cfg.LLM.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 60
	}
	timeout := time.Duration(timeoutSec) * time.Second
	return llm.NewHTTPBackend(baseURL, modelName, apiKey, timeout), nil
}

// runReword is the end-to-end headless entry point for `regit reword`.
// repoDir is the caller-supplied repo path (from the top-level --repo flag);
// if the reword-local --repo flag is set, it takes precedence.
func runReword(repoDir string, args []string) error {
	f, err := parseRewordFlags(args)
	if err != nil {
		return err
	}

	// The reword-subcommand --repo flag wins over the top-level value.
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

	gitDir, err := repo.GitDir()
	if err != nil {
		return fmt.Errorf("resolve git dir: %w", err)
	}

	// --clear-cache deletes the reword cache directory and exits immediately.
	if f.ClearCache {
		return reword.NewCache(gitDir).Clear()
	}

	commits, err := repo.ReadCommits()
	if err != nil {
		return fmt.Errorf("read commits: %w", err)
	}

	// Resolve each commit-ish to an index in the root-first commits slice,
	// de-duplicating while preserving history (root-first) order.
	seen := make(map[int]bool)
	var targets []rewordItem
	for _, ref := range f.Commits {
		idx, err := findCommit(repo, commits, ref)
		if err != nil {
			return err
		}
		if seen[idx] {
			continue
		}
		seen[idx] = true
		parentTree, err := parentTreeOf(repo, commits[idx])
		if err != nil {
			return fmt.Errorf("resolve parent tree for %s: %w", ref, err)
		}
		targets = append(targets, rewordItem{
			index:      idx,
			commit:     commits[idx],
			parentTree: parentTree,
		})
	}
	// Sort targets in history (root-first) order.
	sortRewordItems(targets)

	// Build options: headless forces Variants=1 (no TUI to show multiple choices).
	opts := buildRewordOptions(cfg, f)

	// Build backend — this is required (unlike the dry-run path in split, the
	// reword engine always needs an LLM to produce proposals even in dry-run).
	backend, err := buildRewordBackend(cfg, f)
	if err != nil {
		return err
	}

	// Set up the cache; --no-cache uses a throwaway directory.
	var cache *reword.Cache
	if f.NoCache {
		tmp, err := os.MkdirTemp("", "regit-reword-nocache-*")
		if err != nil {
			return fmt.Errorf("create temp cache dir: %w", err)
		}
		defer os.RemoveAll(tmp)
		cache = reword.NewCache(tmp)
	} else {
		cache = reword.NewCache(gitDir)
	}

	eng := reword.NewEngine(repo, backend, cache)
	readme := readmeIfRequested(repo, cfg)

	// Pipeline: generate proposals for each target commit, prefetching the next
	// while the user answers the current Y/n.
	bufSize := cfg.Reword.Prefetch
	if bufSize < 1 {
		bufSize = 1
	}

	gen := func(ctx context.Context, item rewordItem) (reword.Result, error) {
		return eng.Generate(ctx, item.commit, item.parentTree, readme, opts)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := generateAhead(ctx, targets, gen, bufSize)

	// edits accumulates the accepted proposals, keyed by commit hash.
	edits := make(map[string]model.Edit)

	// Read proposals from the pipeline in order, prompting the user for each.
	stdin := bufio.NewReader(os.Stdin)
	for gr := range ch {
		if gr.err != nil {
			if errors.Is(gr.err, context.Canceled) || errors.Is(gr.err, context.DeadlineExceeded) {
				fmt.Fprintln(os.Stderr, "reword: cancelled")
				break
			}
			fmt.Fprintf(os.Stderr, "reword: generate %s: %v (skipping)\n",
				short(targets[gr.index].commit.Hash), gr.err)
			continue
		}

		item := targets[gr.index]
		result := gr.result

		// Print the proposal: new title and optional body.
		fmt.Printf("\n%s %q\n", short(item.commit.Hash), item.commit.Subject)
		if len(result.Variants) == 0 {
			fmt.Println("  (no variants returned)")
			continue
		}
		best := result.Variants[0]
		fmt.Printf("  -> %s\n", best.Title)
		if best.Body != "" {
			fmt.Printf("     %s\n", best.Body)
		}

		if f.DryRun {
			// Dry-run: show proposals, cache was already written by Generate, skip apply.
			continue
		}

		// Prompt for acceptance.
		accept := f.Yes
		if !f.Yes {
			fmt.Printf("Accept? [Y/n] ")
			line, err := stdin.ReadString('\n')
			if err != nil {
				// EOF or read error: treat as rejection to avoid hanging in CI.
				fmt.Println()
				continue
			}
			line = strings.TrimSpace(line)
			accept = line == "" || strings.EqualFold(line, "y")
		}

		if accept {
			edits[item.commit.Hash] = result.ToEdit(reword.DefaultSelection())
		}
	}

	if f.DryRun {
		fmt.Println("\n(dry run — nothing applied; proposals cached)")
		return nil
	}

	if len(edits) == 0 {
		fmt.Println("nothing to reword")
		return nil
	}

	// Build the plan: all commits on the branch, with the base set to the
	// earliest accepted edit's index so unchanged history is never replayed.
	plan := model.NewPlan(commits)
	minIdx := len(commits)
	for _, item := range targets {
		if _, ok := edits[item.commit.Hash]; ok {
			if item.index < minIdx {
				minIdx = item.index
			}
		}
	}
	plan.SetBase(minIdx)
	for hash, edit := range edits {
		plan.SetEdit(hash, edit)
	}

	state, err := repo.ReadState()
	if err != nil {
		return fmt.Errorf("read repository state: %w", err)
	}

	mgr := safety.NewManager(repo)
	res, err := mgr.Apply(plan, state, safety.ApplyOptions{
		Sign:          gitio.DropSignatures,
		UnixTimestamp: time.Now().Unix(),
		Confirmed:     f.Yes,
	})
	if err != nil {
		return err
	}

	fmt.Printf("\nRewrote %d commit(s). New HEAD %s (backup %s)\n",
		len(edits), short(res.NewHead), res.BackupRef)
	return nil
}

// buildRewordOptions converts config defaults and per-run flags into reword.Options.
// The headless path always forces Variants=1 (no TUI to select among alternatives).
// Flag values with a meaningful non-zero value override the config defaults.
func buildRewordOptions(cfg config.Config, f rewordFlags) reword.Options {
	opts := reword.Options{
		// Headless: always request exactly one variant (no picker UI).
		Variants:     1,
		ContextLines: cfg.Reword.ContextLines,
		Context:      cfg.Reword.Context,
		UserHint:     f.Prompt,
		AgentScan:    f.AgentScan,
		Now:          time.Now(),
	}
	if f.ContextLines > 0 {
		opts.ContextLines = f.ContextLines
	}
	if f.Context != "" {
		opts.Context = splitCSV(f.Context)
	}
	return opts
}

// splitCSV splits a comma-separated string into trimmed, non-empty tokens.
// Used to convert --context=readme,filestates into ["readme","filestates"].
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// sortRewordItems sorts targets in root-first (ascending index) order, which is
// the order the rewrite engine needs when replaying history.
func sortRewordItems(items []rewordItem) {
	sort.Slice(items, func(i, j int) bool { return items[i].index < items[j].index })
}
