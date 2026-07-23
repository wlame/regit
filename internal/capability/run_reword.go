package capability

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/reword"
	"github.com/wlame/regit/internal/termsafe"
)

func runReword(ctx *RunContext, a ParsedArgs) error {
	repo := ctx.Repo
	gitDir, err := repo.GitDir()
	if err != nil {
		return runtimeErr("resolve git dir: %v", err)
	}
	if a.Bool("clear-cache") {
		return reword.NewCache(gitDir).Clear()
	}
	if a.Bool("agent-scan") && a.String("agent") == "" {
		return usageErr("--agent-scan requires --agent=<cmd>")
	}

	commits, err := repo.ReadCommits()
	if err != nil {
		return runtimeErr("read commits: %v", err)
	}

	refs := a.Positional
	if len(refs) == 0 {
		refs = []string{"HEAD"}
	}
	type item struct {
		index      int
		commit     model.Commit
		parentTree string
	}
	seen := map[int]bool{}
	var targets []item
	for _, ref := range refs {
		idx, err := FindCommit(repo, commits, ref)
		if err != nil {
			return usageErr("%v", err)
		}
		if seen[idx] {
			continue
		}
		seen[idx] = true
		pt, err := ParentTreeOf(repo, commits[idx])
		if err != nil {
			return runtimeErr("parent tree for %s: %v", ref, err)
		}
		targets = append(targets, item{idx, commits[idx], pt})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].index < targets[j].index })

	backend := ctx.Backend // tests inject a stub
	if backend == nil {
		backend, err = BuildBackend(ctx.Cfg, BackendOptions{
			LLM: a.String("llm"), Agent: a.String("agent"), AgentArgs: a.String("agent-args"),
			BaseURL: a.String("llm-base-url"), AgentScan: a.Bool("agent-scan"), RepoRoot: repo.Root(),
		})
		if err != nil {
			return usageErr("%v", err)
		}
	}

	cache := reword.NewCache(gitDir)
	if a.Bool("no-cache") {
		tmp, err := os.MkdirTemp("", "regit-reword-nocache-*")
		if err != nil {
			return runtimeErr("temp cache: %v", err)
		}
		defer os.RemoveAll(tmp)
		cache = reword.NewCache(tmp)
	}

	opts := reword.Options{
		Variants:     1, // headless: exactly one proposal, treated as the user's pick
		ContextLines: ctx.Cfg.Reword.ContextLines,
		Context:      ctx.Cfg.Reword.Context,
		UserHint:     a.String("prompt"),
		AgentScan:    a.Bool("agent-scan"),
		Now:          ctx.Now,
	}
	if n := a.Int("context-lines"); n > 0 {
		opts.ContextLines = n
	}
	if keys := a.List("context"); len(keys) > 0 {
		opts.Context = keys
	}

	eng := reword.NewEngine(repo, backend, cache)
	readme := reword.ReadmeForContext(repo, opts.Context)

	edits := map[string]model.Edit{}
	failed := 0
	for _, it := range targets {
		res, err := eng.Generate(context.Background(), it.commit, it.parentTree, readme, opts)
		if err != nil {
			failed++
			fmt.Fprintf(ctx.Stderr, "reword: %s: %v (skipping)\n", Short(it.commit.Hash), err)
			continue
		}
		if len(res.Variants) == 0 {
			fmt.Fprintf(ctx.Stdout, "%s %q\n  (no variants)\n", Short(it.commit.Hash), it.commit.Subject)
			continue
		}
		// best.Title/best.Body are LLM-generated text derived from repo content the
		// model has read, so they are untrusted the same way commit-sourced text
		// is: route the title through Line (single-line) and the body through
		// Block (preserves the body's internal newlines while still stripping
		// escapes).
		best := res.Variants[0]
		fmt.Fprintf(ctx.Stdout, "\n%s %q\n  -> %s\n",
			Short(it.commit.Hash), it.commit.Subject, termsafe.Line(best.Title))
		if best.Body != "" {
			fmt.Fprintf(ctx.Stdout, "     %s\n", termsafe.Block(best.Body))
		}
		edits[it.commit.Hash] = res.ToEdit(reword.DefaultSelection())
	}

	if len(edits) == 0 {
		// Every target that errored is an LLM/runtime failure, not a no-op: exit 3
		// (per the registry's documented exit codes), not a silent success.
		if failed > 0 {
			return runtimeErr("all %d reword target(s) failed to generate; see messages above", failed)
		}
		fmt.Fprintln(ctx.Stdout, "nothing to reword")
		return nil
	}

	plan := model.NewPlan(commits)
	minIdx := len(commits)
	for _, it := range targets {
		if _, ok := edits[it.commit.Hash]; ok && it.index < minIdx {
			minIdx = it.index
		}
	}
	plan.SetBase(minIdx)
	for hash, e := range edits {
		plan.SetEdit(hash, e)
	}

	// Proposals were already printed; the preview closure is a no-op.
	return applyOrPreview(ctx, plan, a, func(io.Writer, *model.Plan) {})
}
