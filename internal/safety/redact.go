package safety

import (
	"fmt"
	"sort"
	"strings"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// ApplyRedact rewrites branch history to remove or replace the spec's
// selected line contents, through the shared guarded apply skeleton: guards
// -> preflight -> backup -> rewrite -> verify -> move branch. It mirrors
// ApplyPurge's shape exactly (see that function's doc comment), substituting
// a content transform for a path removal.
//
// Verification is two-fold, both run BEFORE the branch ref moves:
//
//   - Content: every rewritten commit's ACTUAL change-set (gitio.DiffChangeSet,
//     driven by m.verify over the Expectations this function builds) must
//     equal EXACTLY the per-path Modified set the transform predicts — the
//     FIRST non-empty use of the expectation machinery purge never needed,
//     since purge only ever asserts "byte-identical" (see verify.go).
//   - Absence: verifyRedactAbsence, the content analog of verifyPurgeAbsence,
//     proves none of the targeted line content survives the rewrite.
//
// allSelected controls the SCOPE of that final absence check: true (the
// caller selected every occurrence Scan discovered) arms a GLOBAL sweep of
// the literal across newHead's entire reachable history; false — the
// operator deliberately left some discovered occurrence unredacted — limits
// the check to the PRECISE, per-target-path sweep alone, so an
// intentionally-vetoed occurrence elsewhere in the tree never trips a false
// abort. See verifyRedactAbsence's doc comment for the full two-tier
// contract.
//
// Unlike ApplyPurge, this does not guard on side-branch history. A purge
// must block up front on PathsInSideHistory because it guarantees a path can
// never remain in ANY reachable snapshot — a guarantee side-branch history
// would silently break. Redact's absence check is inherently scoped to the
// first-parent chain regit actually rewrites (a literal cannot be "removed"
// from a side branch's own untouched commits, no matter how the rewrite is
// computed), and the operator is warned about that scope up front by design.
// So side-branch content is reported advisory-only — by
// gitio.SecretInSideHistory, called by redact's CALLER (the capability verb
// and TUI layers), never computed inside this guarded apply.
//
// A consequence callers must know: when allSelected is true AND the secret
// also has merged side-branch history, THIS apply has no up-front guard for
// that at all — verifyRedactAbsence's GLOBAL tier (tier 2; see its doc
// comment) will still abort, correctly, but only LATE, after guards have
// passed, a backup ref has been created, and the rewrite has actually run.
// That is a safe abort (the branch ref never moves — the whole point of
// running verification before UpdateRef), but a poor user experience next to
// an up-front warning. So the capability verb and TUI layers should call
// gitio.SecretInSideHistory themselves BEFORE staging an apply, to steer the
// user ahead of time rather than let them discover it from a late abort.
func (m *Manager) ApplyRedact(
	commits []model.Commit, spec model.RedactSpec, allSelected bool,
	state model.RepoState, opts ApplyOptions,
) (ApplyResult, error) {
	base, baseErr := m.repo.RedactBase(commits, spec)
	return m.runApply(applyOp{
		Guards: func() error {
			if err := spec.Validate(); err != nil {
				return err
			}
			if baseErr != nil {
				return fmt.Errorf("compute redact base: %w", baseErr)
			}
			if base < 0 {
				return fmt.Errorf("nothing to redact: no commit's snapshot contains the target content")
			}
			suffix := commits[base:]
			for _, c := range suffix {
				if c.IsMerge {
					return fmt.Errorf("cannot redact across merge commit %s", shortHash(c.Hash))
				}
			}
			if state.IsDirty {
				return fmt.Errorf("rewrite blocked: working tree is dirty")
			}
			if model.AnyPushed(suffix) && !opts.Confirmed {
				return fmt.Errorf("confirmation required: commits are already pushed")
			}
			return nil
		},
		Rewrite: func(oldHead string) (rewriteResult, error) {
			newHead, remap, err := m.repo.ApplyRedact(commits, spec, opts.Sign)
			if err != nil {
				return rewriteResult{}, err
			}
			if newHead == "" {
				return rewriteResult{}, fmt.Errorf("redact would remove every commit on the branch")
			}
			expectations, err := m.redactExpectations(commits[base:], remap, spec)
			if err != nil {
				return rewriteResult{}, err
			}
			if err := m.verifyRedactAbsence(newHead, spec, allSelected, expectations); err != nil {
				return rewriteResult{}, err
			}
			oldLen := len(commits) - base
			return rewriteResult{
				NewHead:      newHead,
				Expectations: expectations,
				OldLen:       oldLen,
				NewLen:       oldLen - len(remap.Dropped),
			}, nil
		},
	}, opts)
}

// redactExpectations builds the change-set expectation for every commit from
// base onward, paired with what it became:
//
//   - Passthrough (byte-identical re-emit): Expected is empty, exactly like
//     every purge pair — the tree hashes alone prove it (see verify.go).
//   - Rewritten: Expected is the per-path Modified set redactModifiedPaths
//     computes by probing the ORIGINAL tree one target path at a time — the
//     FIRST non-empty expectation this codebase's verifier exercises.
//   - Dropped: no pairing at all. Its absence from the returned slice is
//     deliberate; the drop is instead captured by OldLen/NewLen's count in
//     ApplyRedact's Rewrite closure, exactly as it is for a purge drop.
func (m *Manager) redactExpectations(
	suffix []model.Commit, remap gitio.RedactRemap, spec model.RedactSpec,
) ([]model.Expectation, error) {
	var exps []model.Expectation
	for _, c := range suffix {
		if newHash, ok := remap.Passthrough[c.Hash]; ok {
			exps = append(exps, model.Expectation{OldCommit: c.Hash, NewCommit: newHash})
			continue
		}
		newHash, ok := remap.Rewritten[c.Hash]
		if !ok {
			continue // Dropped: no pairing to verify; captured by the drop count instead
		}
		changes, err := m.redactModifiedPaths(c.TreeHash, spec)
		if err != nil {
			return nil, fmt.Errorf("redact expectations for %s: %w", shortHash(c.Hash), err)
		}
		exps = append(exps, model.Expectation{
			OldCommit: c.Hash, NewCommit: newHash, Expected: model.NewChangeSet(changes),
		})
	}
	return exps, nil
}

// redactModifiedPaths re-runs the transform ONE PATH AT A TIME against tree
// (a commit's ORIGINAL, pre-redact tree) and reports which of spec.Targets'
// paths it actually changed there. Scoping each probe to a single path is
// what makes this precise per-commit, per-path signal possible: the combined
// multi-path RedactBlobsInTree call gitio.ApplyRedact itself makes only
// proves the WHOLE tree changed, not which path(s) within it did. This
// reuses RedactBlobsInTree's own guaranteed no-op contract (the input tree
// hash comes back unchanged when nothing in that single path matched) as the
// "did path P change" detector — each target path is edited independently of
// every other (see RedactBlobsInTree's doc comment), so probing them one at
// a time and combining the results is equivalent to the real multi-path
// transform, with no new gitio surface needed.
func (m *Manager) redactModifiedPaths(
	tree string, spec model.RedactSpec,
) ([]model.PathChange, error) {
	var changes []model.PathChange
	for path, lines := range spec.Targets {
		scoped := model.RedactSpec{
			Mode: spec.Mode, Rule: spec.Rule,
			Targets: map[string][]string{path: lines},
		}
		newTree, err := m.repo.RedactBlobsInTree(tree, scoped)
		if err != nil {
			return nil, err
		}
		if newTree != tree {
			changes = append(changes, model.PathChange{Path: path, Status: model.Modified})
		}
	}
	return changes, nil
}

// verifyRedactAbsence is the final, independent safety net, run BEFORE the
// branch ref moves — the content analog of verifyPurgeAbsence, in two tiers:
//
//  1. PRECISE (always, independent of allSelected): for every commit that
//     survived the rewrite (every entry in expectations — Passthrough and
//     Rewritten alike, Dropped commits having no pairing to check at all),
//     check EVERY path in spec.Targets — not merely the paths that
//     COMMIT'S OWN Expected set happens to name. Scoping to a commit's
//     Expected set would miss exactly the shape a broken transform produces:
//     a selected target line the transform silently fails to strip changes
//     NOTHING in its own path, so it never enters that commit's Expected set
//     at all, even though it is squarely one of spec.Targets' own paths.
//     spec.Targets — not Expected — is the true selection boundary: a
//     vetoed occurrence is by construction never added to Targets (see
//     redact.Engine.Plan), and a target path a given commit's tree never
//     carried is a natural, harmless no-op here. So widening the loop to all
//     of spec.Targets cannot cause a false abort on a partial selection; it
//     only closes the gap. Two independent sub-checks per path:
//     (a) none of that path's ORIGINAL target line contents may survive as a
//     whole line in the new tree (redactLineSurvives) — checked by reading
//     the tree's raw blob bytes directly (gitio.BlobContent) and comparing
//     lines, deliberately NOT routed through RedactBlobsInTree/redactLines,
//     the SAME matching logic the rewrite itself used: reusing it here would
//     let a bug in that matching hide itself from the one check meant to
//     catch exactly that;
//     (b) in replace mode only, the literal spec.Match must not survive
//     inside the RECOMPUTED text the rule produces for each selected line
//     (redactReplacedLineStillHasMatch) — checked against that recomputed
//     text alone, never the whole blob: grepping the whole blob would abort
//     on a literal the operator deliberately left on some OTHER, unselected
//     line of the SAME path, making that kind of same-path veto permanently
//     un-appliable.
//  2. GLOBAL (only when allSelected — every occurrence Scan discovered was
//     selected): spec.Match must be unreachable from newHead's entire
//     history (SecretAddedFrom) and absent from its tip tree (GrepTree).
//     This is a fully independent re-scan (a different git query altogether,
//     not built on remap/expectations at all) that sweeps for a survivor
//     OUTSIDE any target path entirely — a gap tier 1 cannot see by
//     construction, since it only ever looks at spec.Targets' own paths. A
//     caller that sets allSelected=true when the secret ALSO has merged
//     side-branch history (gitio.SecretInSideHistory) will still see this
//     tier abort here, LATE, with no earlier guard: this apply has no
//     up-front side-history check (see ApplyRedact's doc comment for why),
//     so a caller wanting to steer the user before staging must run
//     gitio.SecretInSideHistory itself beforehand.
func (m *Manager) verifyRedactAbsence(
	newHead string, spec model.RedactSpec, allSelected bool, expectations []model.Expectation,
) error {
	for _, e := range expectations {
		for _, path := range sortedTargetPaths(spec.Targets) {
			for _, line := range sortedTargetLines(spec.Targets[path]) {
				survived, err := m.redactLineSurvives(e.NewCommit, path, line)
				if err != nil {
					return fmt.Errorf("verify redact absence: %w", err)
				}
				if survived {
					return fmt.Errorf(
						"redact verification failed: %q still present in %s at %s",
						line, path, shortHash(e.NewCommit))
				}
			}
		}
	}

	if spec.Mode == model.RedactReplace {
		if err := redactReplacedLineStillHasMatch(spec); err != nil {
			return err
		}
	}

	if !allSelected {
		return nil
	}
	hashes, err := m.repo.SecretAddedFrom(newHead, spec.Match)
	if err != nil {
		return fmt.Errorf("verify redact absence: %w", err)
	}
	if len(hashes) > 0 {
		return fmt.Errorf(
			"redact verification failed: %q still reachable from the rewritten head's history", spec.Match)
	}
	found, err := m.repo.GrepTree(newHead, spec.Match)
	if err != nil {
		return fmt.Errorf("verify redact absence: %w", err)
	}
	if found {
		return fmt.Errorf(
			"redact verification failed: %q still present at the rewritten head", spec.Match)
	}
	return nil
}

// redactLineSurvives reports whether line still exists as a whole line
// somewhere in path's blob at tree (a commit-ish or a raw tree both work,
// same as GrepTree/BlobContent). Reads the tree's raw blob bytes directly via
// gitio.BlobContent and compares lines itself (split on "\n", TrimSuffix
// "\r", exact equality) — mechanism-independent of RedactBlobsInTree/
// redactLines, the transform that performed the rewrite. See
// verifyRedactAbsence's doc comment for why that independence matters: this
// is a check on what the rewrite actually DID to the real git object, not a
// re-run of the same logic that could share the same bug.
func (m *Manager) redactLineSurvives(tree, path, line string) (bool, error) {
	content, ok, err := m.repo.BlobContent(tree, path)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil // path absent from this tree: nothing can survive
	}
	for _, raw := range strings.Split(content, "\n") {
		if strings.TrimSuffix(raw, "\r") == line {
			return true, nil
		}
	}
	return false, nil
}

// redactReplacedLineStillHasMatch checks, for every selected target line,
// whether spec.Match still occurs in the text the replace rule PRODUCES for
// that line — a pure, commit-independent computation (the rule is the same
// for every occurrence, so the recomputed text is the same regardless of
// which commit or how many carry the line). This is deliberately scoped to
// ONLY the lines spec.Targets names, not any other content in the same path:
// a literal the operator left on a different, unselected line is none of
// this check's business (see verifyRedactAbsence's doc comment for the
// false-abort this scoping avoids). spec.Rule was already validated by
// spec.Validate() in ApplyRedact's Guards, so Compile cannot fail here in
// practice; the error is still propagated rather than ignored, per this
// codebase's error-handling convention.
func redactReplacedLineStillHasMatch(spec model.RedactSpec) error {
	rule, err := spec.Rule.Compile()
	if err != nil {
		return fmt.Errorf("verify redact absence: %w", err)
	}
	for _, path := range sortedTargetPaths(spec.Targets) {
		for _, line := range sortedTargetLines(spec.Targets[path]) {
			replaced := rule.ReplaceAllString(line, spec.Rule.Replacement)
			if strings.Contains(replaced, spec.Match) {
				return fmt.Errorf(
					"redact verification failed: %q still present in %s after replacing %q",
					spec.Match, path, line)
			}
		}
	}
	return nil
}

// sortedTargetPaths returns spec.Targets' paths in sorted order. Both
// verifyRedactAbsence's precise sub-checks range over spec.Targets, a map —
// without sorting, an abort message naming the FIRST offending path/line
// would name a different one from run to run (Go deliberately randomizes map
// iteration order), which this codebase avoids elsewhere for the same reason
// (see emptyExpectations in verify.go). This does not change WHETHER a
// commit aborts, only which offending path/line a multi-survivor abort names
// first, so it is deterministic to test against.
func sortedTargetPaths(targets map[string][]string) []string {
	paths := make([]string, 0, len(targets))
	for path := range targets {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// sortedTargetLines returns a sorted COPY of lines — the caller's own
// spec.Targets[path] slice is never mutated by sorting it in place, since
// that slice may be shared or reused by the caller after this call returns.
func sortedTargetLines(lines []string) []string {
	sorted := append([]string(nil), lines...)
	sort.Strings(sorted)
	return sorted
}
