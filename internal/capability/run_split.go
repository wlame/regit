package capability

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/split"
)

func runSplit(ctx *RunContext, a ParsedArgs) error {
	repo := ctx.Repo
	gitDir, err := repo.GitDir()
	if err != nil {
		return runtimeErr("resolve git dir: %v", err)
	}
	if a.Bool("clear-cache") {
		return split.NewCache(gitDir).Clear()
	}

	commits, err := repo.ReadCommits()
	if err != nil {
		return runtimeErr("read commits: %v", err)
	}
	ref := "HEAD"
	if len(a.Positional) > 0 {
		ref = a.Positional[0]
	}
	idx, err := FindCommit(repo, commits, ref)
	if err != nil {
		return usageErr("%v", err)
	}
	target := commits[idx]
	parentTree, err := ParentTreeOf(repo, target)
	if err != nil {
		return runtimeErr("parent tree: %v", err)
	}

	opts := buildSplitOptions(ctx, a)

	// Decide whether the commit is even worth splitting BEFORE requiring an LLM.
	// Trivial/no-op cases (too small, single hunk, binary-only) need no model.
	hunks, err := repo.Hunks(parentTree, target.TreeHash)
	if err != nil {
		return runtimeErr("read commit diff: %v", err)
	}
	if skip, reason := split.ShouldSkipSplit(hunks, opts.MinSplitLines); skip {
		fmt.Fprintf(ctx.Stdout, "Not splitting %s: %s\n", Short(target.Hash), reason)
		return nil
	}

	backend := ctx.Backend
	if backend == nil {
		backend, err = BuildBackend(ctx.Cfg, BackendOptions{
			LLM: a.String("llm"), Agent: a.String("agent"), AgentArgs: a.String("agent-args"),
			BaseURL: a.String("llm-base-url"), RepoRoot: repo.Root(),
		})
		if err != nil {
			return usageErr("%v", err)
		}
	}

	cache := split.NewCache(gitDir)
	if a.Bool("no-cache") {
		tmp, err := os.MkdirTemp("", "regit-split-nocache-*")
		if err != nil {
			return runtimeErr("temp cache: %v", err)
		}
		defer os.RemoveAll(tmp)
		cache = split.NewCache(tmp)
	}

	eng := split.NewEngine(repo, backend, cache)
	readme := readmeForSplit(repo, ctx.Cfg) // ported from cmd/regit/split.go:readmeIfRequested
	prop, trees, err := eng.Propose(context.Background(), target, parentTree, readme, opts)
	if err != nil {
		return runtimeErr("propose split: %v", err)
	}
	if !prop.ShouldSplit || len(prop.Groups) < 2 {
		fmt.Fprintf(ctx.Stdout, "Not splitting %s: %s\n", Short(target.Hash), prop.Reason)
		return nil
	}

	printProposal(ctx.Stdout, target, prop)
	printEquivalentCommands(ctx.Stdout, target, prop)

	if !a.Bool("execute") {
		fmt.Fprintln(ctx.Stdout, "\n(dry run — nothing applied; proposal cached; pass --execute to apply)")
		return nil
	}

	// Guard the apply exactly as the plan-based verbs do, returning exit code 2
	// (guard-blocked) for a dirty tree or already-pushed history. Doing the checks
	// here — before ApplySplit — means ApplySplit's equivalent internal guards are
	// never the ones that fire, so a blocked split reports exit 2 (guard) instead
	// of exit 3 (runtime), matching redate / retitle / drop-bodies.
	state, err := repo.ReadState()
	if err != nil {
		return runtimeErr("read state: %v", err)
	}
	if state.IsDirty {
		return guardErr("working tree has uncommitted changes; commit or stash them first")
	}
	if state.HasPushedCommits() && !a.Bool("force") {
		return guardErr("commit %s is on a branch with already-pushed history — re-run with --force to rewrite it", Short(target.Hash))
	}

	subs := split.BuildSubCommits(target, prop.Groups, trees, ctx.Cfg.Split.WIPPrefix)
	tail := commits[idx+1:]
	mgr := safety.NewManager(repo)
	res, err := mgr.ApplySplit(target, prop.ParentHash, subs, tail, state, safety.ApplyOptions{
		Sign:          gitio.DropSignatures,
		UnixTimestamp: ctx.Now.Unix(),
		Confirmed:     a.Bool("force"),
	})
	if err != nil {
		return runtimeErr("%v", err)
	}
	fmt.Fprintf(ctx.Stdout, "\nSplit into %d commits. New HEAD %s (backup %s)\n", len(subs), Short(res.NewHead), res.BackupRef)
	fmt.Fprintln(ctx.Stdout, res.Verification.String())
	return nil
}

// buildSplitOptions maps flags+config to split.Options, deriving ContextKey from cfg.Split.Context.
// Uses ctx.Now (never time.Now()) so tests can inject a deterministic clock.
func buildSplitOptions(ctx *RunContext, a ParsedArgs) split.Options {
	o := split.Options{
		MaxCommits:     ctx.Cfg.Split.MaxCommits,
		LinesPerCommit: ctx.Cfg.Split.LinesPerCommit,
		MinSplitLines:  ctx.Cfg.Split.MinSplitLines,
		RepairAttempts: ctx.Cfg.Split.RepairAttempts,
		WIPPrefix:      ctx.Cfg.Split.WIPPrefix,
		ContextKey:     strings.Join(ctx.Cfg.Split.Context, ","),
		UserHint:       a.String("prompt"),
		Now:            ctx.Now,
	}
	if n := a.Int("max-commits"); n > 0 {
		o.MaxCommits = n
	}
	if tc := a.String("test-cmd"); tc != "" {
		o.TestCmd = []string{"sh", "-c", tc}
	}
	return o
}
