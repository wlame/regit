// internal/capability/run_redact.go
package capability

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/redact"
	"github.com/wlame/regit/internal/safety"
	"github.com/wlame/regit/internal/termsafe"
)

// HygieneWarning is printed after every --execute redact: the secret value
// was just written, verbatim, into .git/regit/command.log (the transparency
// log every mutating verb appends to — see cli-tools.md's command-log
// convention) and into the operator's shell history (the invocation itself
// carries --match=<secret>). Redacting history does not retroactively scrub
// either of those, so the credential must be treated as burned regardless of
// how clean the rewritten history looks. Exported so the TUI's redactChange
// (internal/ui/redact.go) can surface the SAME wording on its post-apply
// status line — the headless and TUI surfaces must never say this two
// different ways.
const HygieneWarning = "⚠ the secret now appears in .git/regit/command.log and your shell history — " +
	"clear both and rotate the credential."

// SideHistoryAdvisory is printed BEFORE staging the apply when the redacted
// literal also has add/remove history on a merged side branch (see
// gitio.SecretInSideHistory). regit only ever rewrites the first-parent
// chain, so that side branch's own commits can never be scrubbed no matter
// what happens on the main line — and, since headless always sets
// allSelected=true, safety.Manager.ApplyRedact's own GLOBAL absence tier
// will independently rediscover the exact same reachable content once the
// rewrite runs and abort rather than report a partial removal (see
// ApplyRedact's doc comment). This note is worded to PREDICT that abort in
// the same terms, so the note and the eventual "still reachable from the
// rewritten head's history" failure read as one story instead of two
// unrelated problems. run_redact still does not add its own guard for this
// (no exit-2 block): the attempt proceeds and safety's own verification is
// what refuses it, late but safely (ref never moves). Exported so the TUI's
// redactChange can surface this same note in its PREVIEW (a warning, not a
// guard block — see redactChange.guard's doc comment) before the operator
// ever stages the apply.
const SideHistoryAdvisory = "note: the secret also lives in merged side-branch history, which regit " +
	"cannot rewrite (it only rewrites the first-parent chain). This run will abort rather than " +
	"report a partial removal — remove that branch from history or use git filter-repo to reach it."

// RedactPreview is everything the CLI dry-run (and, later, the TUI pipeline)
// needs: the scanned occurrences with their context/cascade, any binary-only
// hits, and how the transform reshapes the affected commit range.
type RedactPreview struct {
	Base      int             // index into commits (root-first) the rewrite starts at; -1 = nothing to redact
	Affected  []model.Commit  // commits[Base:], root-first
	Dropped   map[string]bool // hash -> becomes empty (equals its rewritten parent's tree) and vanishes
	Rewritten map[string]bool // hash -> the commit's OWN tree actually changes, vs. merely being reparented
}

// BuildRedactPreview predicts gitio.ApplyRedact's effect without creating any
// commit objects: it reuses repo.RedactBase for the rewrite boundary, then
// walks commits[Base:] applying the SAME logic ApplyRedact's own replay loop
// uses (see its doc comment) — probing each commit's ORIGINAL tree through
// RedactBlobsInTree and comparing against the running "previous emitted
// tree" — to predict, per commit, whether it will be dropped (collapses to
// its rewritten parent's tree and vanishes), rewritten (its own tree
// changes), or merely carried forward untouched. Because this walks the
// identical sequence ApplyRedact's replay does, the prediction always agrees
// with what a real apply produces (this is exactly BuildPurgePreview's
// contract for PurgeBase/ApplyPurge — see its doc comment).
//
// RedactBlobsInTree writes blob/tree objects into the repository's object
// store as a side effect (via hash-object -w) even during this read-only
// preview; that mirrors what a real apply's replay does at the same step and
// is harmless — no ref moves and nothing becomes reachable until a later
// commit-tree/UpdateRef actually happens.
func BuildRedactPreview(repo *gitio.Repo, commits []model.Commit, spec model.RedactSpec) (RedactPreview, error) {
	pv := RedactPreview{Dropped: map[string]bool{}, Rewritten: map[string]bool{}}
	base, err := repo.RedactBase(commits, spec)
	if err != nil {
		return RedactPreview{}, err
	}
	pv.Base = base
	if base < 0 {
		return pv, nil
	}
	pv.Affected = commits[base:]

	emptyTree, err := repo.EmptyTree()
	if err != nil {
		return RedactPreview{}, err
	}
	prevTree := emptyTree
	if base > 0 {
		prevTree = commits[base-1].TreeHash
	}
	for i := base; i < len(commits); i++ {
		c := commits[i]
		origParentTree := emptyTree
		if i > 0 {
			origParentTree = commits[i-1].TreeHash
		}
		alreadyEmpty := c.TreeHash == origParentTree

		tree, err := repo.RedactBlobsInTree(c.TreeHash, spec)
		if err != nil {
			return RedactPreview{}, fmt.Errorf("preview %s: %w", Short(c.Hash), err)
		}
		if tree == prevTree && !alreadyEmpty {
			pv.Dropped[c.Hash] = true
			continue
		}
		if tree != c.TreeHash {
			pv.Rewritten[c.Hash] = true
		}
		prevTree = tree
	}
	return pv, nil
}

// AllOccurrencesSelected builds a redact.Selection that checks every scanned
// occurrence AND every one of its cascade descendants. The headless CLI has
// no picker: --execute always redacts everything Scan found, cascading
// forward through rotations/renames unless --no-cascade suppressed that
// during the scan itself (in which case Descendants is already empty).
// Exported so the TUI's occurrence picker (internal/ui/redact.go) can seed
// its default "everything checked" selection state from the SAME logic,
// rather than re-deriving the "all true" shape by hand.
func AllOccurrencesSelected(res redact.ScanResult) []redact.Selection {
	sel := make([]redact.Selection, len(res.Occurrences))
	for i, occ := range res.Occurrences {
		descendants := make([]bool, len(occ.Descendants))
		for j := range descendants {
			descendants[j] = true
		}
		sel[i] = redact.Selection{Occurrence: true, Descendants: descendants}
	}
	return sel
}

// runRedact implements `regit redact`: scans history for --match, previews
// the removal/replacement (default) or applies it (--execute). The
// exit-code choreography mirrors run_purge.go's exactly — shallow/dirty,
// then merge-in-range, then would-empty-branch, then pushed/--force are all
// pre-checked here so a blocked redact reports exit 2 (guard) rather than
// falling through to safety.ApplyRedact's own internal Guards closure, whose
// failures applyGuardErr would otherwise map to exit 3.
func runRedact(ctx *RunContext, a ParsedArgs) error {
	repo := ctx.Repo
	match := a.String("match")

	modeStr := a.String("mode")
	if modeStr == "" {
		modeStr = "remove" // registry Default; Parse never fills defaults into values
	}
	mode := model.RedactRemove
	var rule model.RedactRule
	if modeStr == "replace" {
		mode = model.RedactReplace
		rule = model.RedactRule{Pattern: a.String("replace-pattern"), Replacement: a.String("replace-with")}
		if _, err := rule.Compile(); err != nil {
			return usageErr("invalid --replace-pattern regex: %v", err)
		}
	}

	eng := redact.Engine{Repo: repo}
	scanRes, err := eng.Scan(redact.ScanOptions{
		Match:   match,
		Globs:   a.List("in"),
		Cascade: !a.Bool("no-cascade"),
	})
	if err != nil {
		return runtimeErr("scan history: %v", err)
	}
	if len(scanRes.Occurrences) == 0 {
		if len(scanRes.Binary) > 0 {
			// scanRes.Binary entries are "path @ short-hash" built from
			// repo-derived paths (see redact.Engine.Scan): sanitize for DISPLAY
			// ONLY before folding them into this usage error, matching
			// purgeSuggestions' identical treatment of history-derived path
			// candidates in run_purge.go.
			safeBinary := make([]string, len(scanRes.Binary))
			for i, b := range scanRes.Binary {
				safeBinary[i] = termsafe.Line(b)
			}
			return usageErr("%q was found only inside binary blob(s), which cannot be redacted: %s",
				match, strings.Join(safeBinary, ", "))
		}
		return usageErr("no occurrences of %q found in history", match)
	}

	spec, err := eng.Plan(scanRes, AllOccurrencesSelected(scanRes), mode, rule, match)
	if err != nil {
		return usageErr("%v", err)
	}

	commits, err := repo.ReadCommits()
	if err != nil {
		return runtimeErr("read commits: %v", err)
	}

	pv, err := BuildRedactPreview(repo, commits, spec)
	if err != nil {
		return runtimeErr("build preview: %v", err)
	}
	printRedactPreview(ctx.Stdout, scanRes, spec, pv)

	if !a.Bool("execute") {
		fmt.Fprintln(ctx.Stdout, "\n(dry run — nothing applied; pass --execute to apply)")
		return nil
	}

	state, err := repo.ReadState()
	if err != nil {
		return runtimeErr("read state: %v", err)
	}
	// Pre-check the same conditions safety.Manager.ApplyRedact's own Guards
	// closure re-checks internally, on the SAME pv.Base BuildRedactPreview
	// already computed via repo.RedactBase — so a blocked redact is caught
	// HERE (guardErr, exit 2) rather than by ApplyRedact's own guard, whose
	// plain error applyGuardErr would otherwise map to exit 3. Mirrors
	// run_purge.go's identical reasoning for its own pre-checks.
	if state.IsShallow {
		return guardErr("shallow clone — history is incomplete; run 'git fetch --unshallow' first")
	}
	if state.IsDirty {
		return guardErr("working tree has uncommitted changes; commit or stash them first")
	}
	if pv.Base < 0 {
		return runtimeErr("redact base: no commit's snapshot contains the target content")
	}
	for _, c := range pv.Affected {
		if c.IsMerge {
			return guardErr("cannot redact across merge commit %s", Short(c.Hash))
		}
	}
	// pv.Dropped/pv.Base were computed by BuildRedactPreview walking the SAME
	// commits/spec gitio.ApplyRedact's own replay walks; when the rewrite
	// starts at the branch root AND every commit from there on is predicted
	// dropped, the redact would leave no commit at all — the same condition
	// gitio.ApplyRedact signals by returning newHead == "", which
	// safety.ApplyRedact otherwise only catches with a plain (non-guard)
	// error deep inside the rewrite step. Mirrors run_purge.go's identical
	// would-empty-branch pre-check.
	if pv.Base == 0 && len(pv.Dropped) == len(commits)-pv.Base {
		return guardErr("redact would remove every commit on the branch")
	}
	// commits[pv.Base:] is the same rewritten suffix BuildRedactPreview used
	// to predict Dropped/Rewritten: gitio.ApplyRedact rewrites every commit's
	// tree from the base commit onward (trees are full snapshots), so an
	// already-pushed commit anywhere in that suffix — even one whose own
	// changes never carried the target content — needs --force before its
	// hash is rewritten out from under a remote. Mirrors run_purge.go.
	if model.AnyPushed(commits[pv.Base:]) && !a.Bool("force") {
		return guardErr("some commits are already pushed — re-run with --force to rewrite them")
	}

	// Side-branch history is checked and reported here — BEFORE staging the
	// apply — because a positive result here means safety.Manager.ApplyRedact
	// is going to abort: its own GLOBAL absence tier (armed since allSelected
	// is always true headless) independently rediscovers this exact same
	// content reachable from newHead once the rewrite has actually run, and
	// — per that function's doc comment — reliably ABORTS rather than report
	// a partial removal. So SideHistoryAdvisory is worded to predict that
	// abort, printed here so the operator reads it BEFORE the (safe, late)
	// failure rather than being surprised by a bare verification error. This
	// is deliberately NOT a guardErr: run_redact adds no exit-2 block of its
	// own over this, unlike purge's hard PathsInSideHistory refusal — the
	// attempt still proceeds, and safety's own verification is what refuses
	// it (see ApplyRedact's doc comment for why callers should check this
	// up front rather than let the operator discover it from a late abort).
	sideHistory, err := repo.SecretInSideHistory(commits, match)
	if err != nil {
		return runtimeErr("check side-branch history: %v", err)
	}
	if sideHistory {
		fmt.Fprintln(ctx.Stdout, SideHistoryAdvisory)
	}

	sm, err := signMode(ctx, a)
	if err != nil {
		return err
	}
	mgr := safety.NewManager(repo)
	res, err := mgr.ApplyRedact(commits, spec, true, state, safety.ApplyOptions{
		Sign:          sm,
		UnixTimestamp: ctx.Now.Unix(),
		Confirmed:     a.Bool("force"),
		ExpectedHead:  commits[len(commits)-1].Hash,
	})
	if err != nil {
		return applyGuardErr(err)
	}
	fmt.Fprintf(ctx.Stdout, "\nRedacted %q across %d path(s), dropped %d commit(s). New HEAD %s (backup %s)\n",
		match, len(spec.Targets), len(pv.Dropped), Short(res.NewHead), res.BackupRef)
	fmt.Fprintln(ctx.Stdout, res.Verification.String())
	// History is already correct and the branch has already moved by the time
	// SyncWarning can be non-empty (see runApply), so this is reported — never
	// treated as a command failure — alongside the rest of the summary.
	if res.SyncWarning != "" {
		fmt.Fprintln(ctx.Stderr, res.SyncWarning)
	}
	fmt.Fprintln(ctx.Stdout, "note: old objects remain reachable via the backup ref and reflog until 'git gc' prunes them")
	pushHint(ctx.Stdout, state)
	fmt.Fprintln(ctx.Stdout, HygieneWarning)
	return nil
}

// RedactPreviewLines renders the same preview printRedactPreview writes to
// the headless dry-run's stdout, as plain text lines instead of an io.Writer
// stream. The TUI's redactChange (internal/ui/redact.go) renders exactly
// this content in its own pipeline preview overlay, so the headless dry-run
// and the TUI preview can never drift into describing the same scan/spec
// two different ways — this is a formatting-target change only, not a
// second implementation of the preview's content.
func RedactPreviewLines(res redact.ScanResult, spec model.RedactSpec, pv RedactPreview) []string {
	var buf bytes.Buffer
	printRedactPreview(&buf, res, spec, pv)
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
}

// printRedactPreview renders the match/mode header, then each scanned
// occurrence (intro line + diff context + any cascade descendants),
// binary-only hits, then affected commits newest-first with drop/rewrite/
// carry markers — mirroring printPurgePreview's shape and newest-first order.
func printRedactPreview(w io.Writer, res redact.ScanResult, spec model.RedactSpec, pv RedactPreview) {
	verb := "remove the matched line"
	if spec.Mode == model.RedactReplace {
		verb = fmt.Sprintf("replace %q within the matched line", spec.Rule.Pattern)
	}
	fmt.Fprintf(w, "Redacting %q (%s) — %d occurrence(s):\n", spec.Match, verb, len(res.Occurrences))
	for _, occ := range res.Occurrences {
		// occ.Intro.Path/Line and occ.Context are history-derived text (a
		// tracked file's content), printed verbatim to the terminal:
		// termsafe.Line strips any embedded escape sequence for DISPLAY ONLY.
		fmt.Fprintf(w, "\n  %s @ %s\n", termsafe.Line(occ.Intro.Path), Short(occ.Intro.Commit))
		for _, line := range occ.Context {
			fmt.Fprintf(w, "    %s\n", termsafe.Line(line))
		}
		fmt.Fprintf(w, "  > %s\n", termsafe.Line(occ.Intro.Line))
		for _, d := range occ.Descendants {
			fmt.Fprintf(w, "    cascades to %s @ %s: %q\n", termsafe.Line(d.Path), Short(d.Commit), termsafe.Line(d.Line))
		}
		if occ.ChainNote != "" {
			fmt.Fprintf(w, "    note: %s\n", termsafe.Line(occ.ChainNote))
		}
	}
	if len(res.Binary) > 0 {
		fmt.Fprintf(w, "\n%d binary hit(s) — not redactable (no text line to act on):\n", len(res.Binary))
		for _, b := range res.Binary {
			fmt.Fprintf(w, "  %s\n", termsafe.Line(b))
		}
	}

	fmt.Fprintf(w, "\n%d affected commit(s):\n", len(pv.Affected))
	for i := len(pv.Affected) - 1; i >= 0; i-- {
		c := pv.Affected[i]
		marker := "carried forward (unchanged)"
		switch {
		case pv.Dropped[c.Hash]:
			marker = "✖ will be dropped"
		case pv.Rewritten[c.Hash]:
			marker = "rewritten"
		}
		// c.Subject is the repo-derived commit subject; sanitize for DISPLAY
		// ONLY (pv.Dropped/pv.Rewritten are keyed on c.Hash, never on this text).
		fmt.Fprintf(w, "  %s  %-50s %s\n", Short(c.Hash), termsafe.Line(c.Subject), marker)
	}
}
