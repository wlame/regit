package safety

import (
	"fmt"

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
//  1. PRECISE (always): for every expectation with a non-empty Expected (a
//     genuinely rewritten commit), re-examine ONLY the paths that
//     expectation's OWN Expected set names — never any other path in the
//     same tree. That scoping is deliberate: it is what lets an occurrence
//     the caller left unselected elsewhere in the tree pass cleanly instead
//     of tripping a false abort here, regardless of allSelected. Two
//     independent sub-checks per such path:
//     (a) none of that path's ORIGINAL target line contents may survive as a
//     whole line in the new tree (redactLineSurvives, reusing
//     RedactBlobsInTree's no-op contract as the detector);
//     (b) in replace mode only, the literal spec.Match itself must not
//     survive anywhere in that path's new blob (GrepPath) — this catches a
//     replace rule that changed SOME bytes on the target line without
//     actually stripping the secret text (sub-check (a) alone would miss
//     this, since the line's exact original text is gone either way).
//  2. GLOBAL (only when allSelected — every occurrence Scan discovered was
//     selected): spec.Match must be unreachable from newHead's entire
//     history (SecretAddedFrom) and absent from its tip tree (GrepTree).
//     This sweeps for a survivor OUTSIDE any target path entirely — exactly
//     the gap the precise, per-target-path check above cannot see by
//     construction (a path never named in any Expected set is never probed
//     by tier 1 at all).
func (m *Manager) verifyRedactAbsence(
	newHead string, spec model.RedactSpec, allSelected bool, expectations []model.Expectation,
) error {
	for _, e := range expectations {
		for _, ch := range e.Expected.Changes {
			for _, line := range spec.Targets[ch.Path] {
				survived, err := m.redactLineSurvives(e.NewCommit, ch.Path, line)
				if err != nil {
					return fmt.Errorf("verify redact absence: %w", err)
				}
				if survived {
					return fmt.Errorf(
						"redact verification failed: %q still present in %s at %s",
						line, ch.Path, shortHash(e.NewCommit))
				}
			}
			if spec.Mode != model.RedactReplace {
				continue
			}
			found, err := m.repo.GrepPath(e.NewCommit, ch.Path, spec.Match)
			if err != nil {
				return fmt.Errorf("verify redact absence: %w", err)
			}
			if found {
				return fmt.Errorf(
					"redact verification failed: %q still present in %s at %s after replace",
					spec.Match, ch.Path, shortHash(e.NewCommit))
			}
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
// same as GrepTree/GrepPath). It reuses RedactBlobsInTree's own guaranteed
// no-op contract as the detector: a REMOVE-mode probe scoped to exactly this
// one path/line either finds and strips it (the tree comes back changed —
// survived) or proves it is genuinely gone (no-op — the tree comes back
// unchanged).
func (m *Manager) redactLineSurvives(tree, path, line string) (bool, error) {
	probe := model.RedactSpec{
		Mode: model.RedactRemove, Targets: map[string][]string{path: {line}},
	}
	newTree, err := m.repo.RedactBlobsInTree(tree, probe)
	if err != nil {
		return false, err
	}
	return newTree != tree, nil
}
