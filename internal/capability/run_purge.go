// internal/capability/run_purge.go
package capability

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/termsafe"
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
//
// The rewritten-range boundary is gitio.PurgeBase, the SAME helper
// gitio.ApplyPurge and safety.Manager.ApplyPurge use — so Reparented (and the
// merge / would-empty pre-checks in runPurge, which reuse this preview's
// Dropped map) always agree with what the actual apply will do, even when a
// merge commit that never shows up in affected still has to be pulled into
// the rewritten range (see PurgeBase's doc comment).
func BuildPurgePreview(repo *gitio.Repo, commits []model.Commit, spec model.PurgeSpec, affected map[string]bool) (PurgePreview, error) {
	pv := PurgePreview{Paths: spec.Paths, Dropped: map[string]bool{}}
	base, err := repo.PurgeBase(commits, spec.Paths, affected)
	if err != nil {
		return PurgePreview{}, err
	}
	for _, c := range commits {
		if !affected[c.Hash] {
			continue
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
	if base >= 0 {
		pv.Reparented = len(commits) - base - len(pv.Affected)
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
			// p (and the rename chain below) are history-derived paths, printed
			// verbatim to the terminal: termsafe.Line strips any embedded escape
			// sequence for DISPLAY ONLY. The lookups into idx.AddedIn and
			// idx.RenameChain below use the raw p, never the sanitized copy, so
			// matching/selection is unaffected.
			display := termsafe.Line(p)
			if added, ok := idx.AddedIn[p]; ok {
				fmt.Fprintf(ctx.Stdout, "%s\t(added in %s)\n", display, Short(added))
			} else {
				fmt.Fprintf(ctx.Stdout, "%s/\t(directory)\n", display)
			}
			if chain := idx.RenameChain(p); len(chain) > 1 {
				safeChain := make([]string, len(chain))
				for i, c := range chain {
					safeChain[i] = termsafe.Line(c)
				}
				fmt.Fprintf(ctx.Stdout, "\trename chain: %v\n", safeChain)
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
	// base is the SAME corrected rewrite boundary safety.Manager.ApplyPurge
	// and gitio.ApplyPurge independently recompute via gitio.PurgeBase (see
	// its doc comment): the first commits[i] flagged in affected, walked
	// backward past any earlier commit whose tree still carries a purge path
	// (the merge-simplification blind spot). Pre-checking the merge and
	// would-empty conditions here — on the SAME base — means
	// safety.ApplyPurge's own equivalent guards are never the ones that fire,
	// so a blocked purge reports exit 2 (guard) instead of exit 3 (runtime
	// error from a plain, sentinel-less Go error), matching split/squash.
	base, err := repo.PurgeBase(commits, spec.Paths, affected)
	if err != nil {
		return runtimeErr("compute purge base: %v", err)
	}
	if base < 0 {
		return runtimeErr("purge base: affected commits not on this branch")
	}
	for _, c := range commits[base:] {
		if c.IsMerge {
			return guardErr("cannot purge across merge commit %s", Short(c.Hash))
		}
	}
	// A path can have history entirely on a merged-in side branch — including
	// a "pocket" (added then removed within the side branch before it merged,
	// net zero by merge time) that base's tree-content walk cannot see, since
	// no surviving tree ever contains it, so the merge never lands in
	// commits[base:] and the check above has nothing to catch. regit only
	// rewrites the first-parent chain, so this can never be purged either way;
	// catching it here (guardErr, exit 2) rather than letting
	// safety.ApplyPurge's late verifyPurgeAbsence abort mid-rewrite keeps the
	// failure a clean, up-front guard block. Checked AFTER the merge scan on
	// purpose — see safety.Manager.ApplyPurge's Guards closure for why this
	// check is a superset of that one, and which error a user sees first.
	sidePaths, err := repo.PathsInSideHistory(commits, spec.Paths)
	if err != nil {
		return runtimeErr("check side-branch history: %v", err)
	}
	if len(sidePaths) > 0 {
		return guardErr("%s also exists in a merged side branch's history, which regit cannot rewrite — see git filter-repo",
			strings.Join(sidePaths, ", "))
	}
	// pv.Dropped was computed by BuildPurgePreview over the same commits and
	// affected set; when the corrected base starts at the branch root AND
	// every commit from there on is predicted dropped, the purge would leave
	// no commit at all — the same condition gitio.ApplyPurge signals by
	// returning newHead == "", which safety.ApplyPurge otherwise only catches
	// with a plain (non-guard) error deep inside the rewrite step.
	if base == 0 && len(pv.Dropped) == len(commits)-base {
		return guardErr("purge would remove every commit on the branch")
	}
	// commits[base:] is the same rewritten suffix BuildPurgePreview used to
	// compute Reparented: gitio.ApplyPurge rewrites every commit's tree from
	// the base commit onward (trees are full snapshots), so an already-pushed
	// commit anywhere in that suffix — even one whose own changes were never
	// under the purge paths — needs --force before its hash is rewritten out
	// from under a remote. Mirrors run_split.go and run_squash.go, which
	// scope model.AnyPushed to their own rewrite ranges rather than to
	// whether the branch has any upstream at all.
	if model.AnyPushed(commits[base:]) && !a.Bool("force") {
		return guardErr("some commits are already pushed — re-run with --force to rewrite them")
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
	// Purge's verification is split across two mechanisms: rewritten commits are
	// checked by verifyPurgeHeadDiff (the old→new HEAD diff must be deletions
	// under the purged paths only), while untouched commits are proven
	// byte-identical by empty expectations. Reporting only the second — which is
	// what Verification.String() covers — reads as "verified 0 commits" whenever
	// every commit was rewritten, i.e. exactly when the purge did the most work.
	fmt.Fprintf(ctx.Stdout,
		"verified %d rewritten commit(s) against the purge diff, %d unchanged commit(s) re-emitted identically\n",
		len(pv.Affected), res.Verification.Commits)
	// History is already correct and the branch has already moved by the time
	// SyncWarning can be non-empty (see runApply), so this is reported — never
	// treated as a command failure — alongside the rest of the summary.
	if res.SyncWarning != "" {
		fmt.Fprintln(ctx.Stderr, res.SyncWarning)
	}
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
	// cands are history-derived paths folded into a usage-error string that
	// ultimately reaches the terminal; sanitize for DISPLAY ONLY (the sort
	// above, and any earlier matching, already ran against the raw values).
	safeCands := make([]string, len(cands))
	for i, c := range cands {
		safeCands[i] = termsafe.Line(c)
	}
	return fmt.Sprintf(" — did you mean: %v?", safeCands)
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
		// c.Subject is the repo-derived commit subject; sanitize for DISPLAY
		// ONLY (pv.Dropped is keyed on c.Hash, never on this rendered text).
		fmt.Fprintf(w, "  %s  %-50s %s\n", Short(c.Hash), termsafe.Line(c.Subject), marker)
	}
	if pv.Reparented > 0 {
		fmt.Fprintf(w, "  … plus %d later commit(s) carried forward (their own changes untouched)\n", pv.Reparented)
	}
}
