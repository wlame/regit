// internal/capability/run_purge.go
package capability

import (
	"fmt"
	"io"
	"sort"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
)

// PurgePreview is everything both preview surfaces (CLI dry-run and the TUI
// pipeline) need: the affected commits root-first, which of them the purge
// will drop entirely, and how many later commits are merely carried forward.
type PurgePreview struct {
	Paths    []string
	Affected []model.Commit  // root-first
	Dropped  map[string]bool // hash -> becomes empty and vanishes
	// Reparented counts later commits, within the rewritten range, whose own
	// diff never touched the purge paths — but whose snapshot still carried
	// those paths forward from an ancestor. gitio.ApplyPurge rewrites the
	// tree of EVERY commit from the first affected one onward (trees are full
	// snapshots, not diffs), so these commits get a new parent hash — and
	// possibly a stripped tree — even though nothing THEY individually
	// changed was purged.
	Reparented int
}

// BuildPurgePreview predicts the purge's effect: a commit whose full touched-
// file list lies under the purge paths will be dropped. Directory selections
// stay directories — member files are never listed.
func BuildPurgePreview(repo *gitio.Repo, commits []model.Commit, spec model.PurgeSpec, affected map[string]bool) (PurgePreview, error) {
	pv := PurgePreview{Paths: spec.Paths, Dropped: map[string]bool{}}
	firstAffected := -1
	for i, c := range commits {
		if !affected[c.Hash] {
			continue
		}
		if firstAffected < 0 {
			firstAffected = i
		}
		pv.Affected = append(pv.Affected, c)
		files, err := repo.CommitTouchedFiles(c.Hash)
		if err != nil {
			return PurgePreview{}, err
		}
		dropped := len(files) > 0
		for _, f := range files {
			if !model.UnderAnyPath(f, spec.Paths) {
				dropped = false
				break
			}
		}
		if dropped {
			pv.Dropped[c.Hash] = true
		}
	}
	if firstAffected >= 0 {
		pv.Reparented = len(commits) - firstAffected - len(pv.Affected)
	}
	return pv, nil
}

// runPurge implements `regit purge`: --search lists candidate paths; with
// positional paths it previews (default) or applies (--execute) the removal.
func runPurge(ctx *RunContext, a ParsedArgs) error {
	repo := ctx.Repo
	if q := a.String("search"); q != "" {
		idx, err := repo.ScanHistoryPaths()
		if err != nil {
			return runtimeErr("scan history: %v", err)
		}
		matches := idx.Match(q)
		if len(matches) == 0 {
			fmt.Fprintf(ctx.Stdout, "no history paths match %q\n", q)
			return nil
		}
		for _, p := range matches {
			if added, ok := idx.AddedIn[p]; ok {
				fmt.Fprintf(ctx.Stdout, "%s\t(added in %s)\n", p, Short(added))
			} else {
				fmt.Fprintf(ctx.Stdout, "%s/\t(directory)\n", p)
			}
			if chain := idx.RenameChain(p); len(chain) > 1 {
				fmt.Fprintf(ctx.Stdout, "\trename chain: %v\n", chain)
			}
		}
		return nil
	}

	if len(a.Positional) == 0 {
		return usageErr("purge needs at least one path (try --search=<name> to find candidates)")
	}
	spec := model.PurgeSpec{Paths: a.Positional}
	if err := spec.Validate(); err != nil {
		return usageErr("%v", err)
	}

	commits, err := repo.ReadCommits()
	if err != nil {
		return runtimeErr("read commits: %v", err)
	}
	affected, err := repo.AffectedCommits(spec.Paths)
	if err != nil {
		return runtimeErr("find affected commits: %v", err)
	}
	if len(affected) == 0 {
		return usageErr("no commits touch %v%s", spec.Paths, purgeSuggestions(repo, spec.Paths))
	}

	pv, err := BuildPurgePreview(repo, commits, spec, affected)
	if err != nil {
		return runtimeErr("build preview: %v", err)
	}
	printPurgePreview(ctx.Stdout, pv)

	if !a.Bool("execute") {
		fmt.Fprintln(ctx.Stdout, "\n(dry run — nothing applied; pass --execute to apply)")
		return nil
	}

	state, err := repo.ReadState()
	if err != nil {
		return runtimeErr("read state: %v", err)
	}
	// Pre-check the same conditions the plan-based verbs guard on (see
	// run_split.go), so a dirty tree or shallow clone reports exit code 2
	// (guard-blocked) here rather than falling through to safety.ApplyPurge's
	// own internal Guards closure, whose failures applyGuardErr maps to
	// exit 3 (runtime error) because they carry no sentinel.
	if state.IsShallow {
		return guardErr("shallow clone — history is incomplete; run 'git fetch --unshallow' first")
	}
	if state.IsDirty {
		return guardErr("working tree has uncommitted changes; commit or stash them first")
	}
	sm, err := signMode(ctx, a)
	if err != nil {
		return err
	}
	mgr := safety.NewManager(repo)
	res, err := mgr.ApplyPurge(commits, spec, affected, state, safety.ApplyOptions{
		Sign:          sm,
		UnixTimestamp: ctx.Now.Unix(),
		Confirmed:     a.Bool("force"),
		ExpectedHead:  commits[len(commits)-1].Hash,
	})
	if err != nil {
		return applyGuardErr(err)
	}
	fmt.Fprintf(ctx.Stdout, "\nPurged %d path(s) across %d commit(s), dropped %d. New HEAD %s (backup %s)\n",
		len(spec.Paths), len(pv.Affected), len(pv.Dropped), Short(res.NewHead), res.BackupRef)
	fmt.Fprintln(ctx.Stdout, res.Verification.String())
	fmt.Fprintln(ctx.Stdout, "note: old objects remain reachable via the backup ref and reflog until 'git gc' prunes them")
	pushHint(ctx.Stdout, state)
	return nil
}

// purgeSuggestions turns a failed path lookup into "did you mean" candidates
// from the history scan. Best-effort: scan errors return no suggestions.
func purgeSuggestions(repo *gitio.Repo, paths []string) string {
	idx, err := repo.ScanHistoryPaths()
	if err != nil {
		return ""
	}
	seen := map[string]bool{}
	var cands []string
	for _, p := range paths {
		for _, m := range idx.Match(p) {
			if !seen[m] {
				seen[m] = true
				cands = append(cands, m)
			}
		}
	}
	sort.Strings(cands)
	if len(cands) == 0 {
		return ""
	}
	if len(cands) > 5 {
		cands = cands[:5]
	}
	return fmt.Sprintf(" — did you mean: %v?", cands)
}

// printPurgePreview renders paths, then affected commits newest-first with
// drop markers, then the carried-forward-suffix summary line.
func printPurgePreview(w io.Writer, pv PurgePreview) {
	fmt.Fprintf(w, "Purging %d path(s):\n", len(pv.Paths))
	for _, p := range pv.Paths {
		fmt.Fprintf(w, "  %s\n", p)
	}
	fmt.Fprintf(w, "\n%d affected commit(s):\n", len(pv.Affected))
	for i := len(pv.Affected) - 1; i >= 0; i-- {
		c := pv.Affected[i]
		marker := "rewritten"
		if pv.Dropped[c.Hash] {
			marker = "✖ will be dropped"
		}
		fmt.Fprintf(w, "  %s  %-50s %s\n", Short(c.Hash), c.Subject, marker)
	}
	if pv.Reparented > 0 {
		fmt.Fprintf(w, "  … plus %d later commit(s) carried forward (their own changes untouched)\n", pv.Reparented)
	}
}
