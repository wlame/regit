// internal/capability/run_squash.go
package capability

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/squash"
)

// Ensure context is used (it will be wired in Task 10).
var _ = context.Background

// parseSquashSpec parses a comma-separated list of A..B ranges and single refs
// into the union of selected commit hashes. Each item resolves via resolveTargets,
// so A..B uses git semantics (commits AFTER A through B; A excluded) and a single
// ref selects just that commit. Whitespace around items is trimmed.
func parseSquashSpec(repo *gitio.Repo, commits []model.Commit, spec string) (map[string]bool, error) {
	selected := map[string]bool{}
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		targets, err := resolveTargets(repo, commits, item)
		if err != nil {
			return nil, err
		}
		for _, c := range targets {
			selected[c.Hash] = true
		}
	}
	return selected, nil
}

// runSquash collapses consecutive commit chains. Preview by default; --execute
// applies (guarded). Uses an LLM summary when one is configured, else the no-LLM
// defaults (first commit's message). Task 10 fills the LLM prefill block.
func runSquash(ctx *RunContext, a ParsedArgs) error {
	repo := ctx.Repo
	gitDir, err := repo.GitDir()
	if err != nil {
		return runtimeErr("resolve git dir: %v", err)
	}
	if a.Bool("clear-cache") {
		return squash.NewCache(gitDir).Clear()
	}

	commits, err := repo.ReadCommits()
	if err != nil {
		return runtimeErr("read commits: %v", err)
	}
	if len(a.Positional) == 0 {
		return usageErr("squash needs a commit range, e.g. regit squash A^..B,C")
	}
	// Tolerate a space-separated spec by re-joining positionals with commas.
	spec := strings.Join(a.Positional, ",")
	selected, err := parseSquashSpec(repo, commits, spec)
	if err != nil {
		return usageErr("%v", err)
	}

	chains, ignored := model.DetectChains(commits, selected)
	if len(chains) == 0 {
		fmt.Fprintf(ctx.Stdout, "Nothing to squash: no consecutive runs of 2+ commits (%d singleton(s) ignored).\n", len(ignored))
		return nil
	}

	// Always apply the identity+date defaults; the LLM (Task 10) overrides only the
	// message when configured.
	for i := range chains {
		chains[i] = model.DefaultSquashConfig(chains[i])
	}
	if err := prefillSquashMessages(ctx, a, gitDir, chains); err != nil {
		return err
	}

	plan := model.SquashPlan{Chains: chains, Ignored: ignored}
	if err := plan.Validate(commits); err != nil {
		return runtimeErr("invalid squash plan: %v", err)
	}

	printSquashPlan(ctx.Stdout, plan, ctx.Now.Location())

	if !a.Bool("execute") {
		fmt.Fprintln(ctx.Stdout, "\n(dry run — nothing applied; pass --execute to apply)")
		return nil
	}

	state, err := repo.ReadState()
	if err != nil {
		return runtimeErr("read state: %v", err)
	}
	if state.IsDirty {
		return guardErr("working tree has uncommitted changes; commit or stash them first")
	}
	if state.HasPushedCommits() && !a.Bool("force") {
		return guardErr("some commits are already pushed — re-run with --force to rewrite them")
	}

	mgr := safety.NewManager(repo)
	res, err := mgr.ApplySquash(commits, plan, state, safety.ApplyOptions{
		Sign:          gitio.DropSignatures,
		UnixTimestamp: ctx.Now.Unix(),
		Confirmed:     a.Bool("force"),
	})
	if err != nil {
		return runtimeErr("%v", err)
	}
	squashed := 0
	for _, ch := range plan.Chains {
		squashed += len(ch.Members)
	}
	fmt.Fprintf(ctx.Stdout, "\nSquashed %d commits into %d. New HEAD %s (backup %s)\n", squashed, len(plan.Chains), Short(res.NewHead), res.BackupRef)
	fmt.Fprintln(ctx.Stdout, res.Verification.String())
	return nil
}

// prefillSquashMessages is the LLM hook. In this task it is a no-op (no-LLM
// defaults already set); Task 10 replaces the body with the engine wiring.
func prefillSquashMessages(ctx *RunContext, a ParsedArgs, gitDir string, chains []model.SquashChain) error {
	return nil
}

// printSquashPlan renders one line per chain plus the ignored-singletons note.
func printSquashPlan(w io.Writer, plan model.SquashPlan, loc *time.Location) {
	squashed := 0
	for _, ch := range plan.Chains {
		squashed += len(ch.Members)
	}
	fmt.Fprintf(w, "Squash %d commit(s) into %d commit(s)", squashed, len(plan.Chains))
	if len(plan.Ignored) > 0 {
		fmt.Fprintf(w, " · %d ignored", len(plan.Ignored))
	}
	fmt.Fprintln(w)
	for _, ch := range plan.Chains {
		date := ch.AuthorDate.In(loc).Format("2006-01-02 15:04")
		fmt.Fprintf(w, "  %d commits → %q · %s <%s> · %s · %s..%s\n",
			len(ch.Members), ch.Subject, ch.AuthorName, ch.AuthorEmail, date,
			Short(ch.First().Hash), Short(ch.Last().Hash))
	}
}
