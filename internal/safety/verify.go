package safety

import (
	"fmt"
	"sort"
	"strings"

	"github.com/wlame/regit/internal/model"
)

// verify checks that every expectation holds: the actual change-set between each
// old/new commit equals the expected one. An empty expectation is satisfied by
// tree-hash equality (identical tree ⟺ identical files); a diff is run only to
// build a helpful message when an empty expectation is violated. It returns a
// summary for the audit line.
//
// verify runs AFTER replay but BEFORE the branch ref moves, so a returned error
// leaves the repository byte-for-byte unchanged.
func (m *Manager) verify(expectations []model.Expectation) (model.VerificationSummary, error) {
	// Resolve every involved commit's tree in one batched call.
	var hashes []string
	seen := map[string]bool{}
	for _, e := range expectations {
		for _, h := range []string{e.OldCommit, e.NewCommit} {
			if !seen[h] {
				seen[h] = true
				hashes = append(hashes, h)
			}
		}
	}
	trees, err := m.repo.CommitTrees(hashes)
	if err != nil {
		return model.VerificationSummary{}, err
	}

	summary := model.VerificationSummary{Commits: len(expectations)}
	for _, e := range expectations {
		if e.Expected.IsEmpty() {
			if trees[e.OldCommit] == trees[e.NewCommit] {
				continue // identical snapshot — nothing changed, as promised
			}
			actual, derr := m.repo.DiffChangeSet(e.OldCommit, e.NewCommit)
			if derr != nil {
				return model.VerificationSummary{}, derr
			}
			return model.VerificationSummary{}, verifyError(e, actual)
		}
		actual, derr := m.repo.DiffChangeSet(e.OldCommit, e.NewCommit)
		if derr != nil {
			return model.VerificationSummary{}, derr
		}
		if !e.Expected.Equal(actual) {
			return model.VerificationSummary{}, verifyError(e, actual)
		}
		summary.ChangedPaths += len(actual.Changes)
	}
	return summary, nil
}

// verifyError renders a multi-line abort message listing each discrepancy.
func verifyError(e model.Expectation, actual model.ChangeSet) error {
	diffs := e.Expected.Mismatch(actual)
	return fmt.Errorf("verification failed between %s and %s:\n  %s",
		shortHash(e.OldCommit), shortHash(e.NewCommit), strings.Join(diffs, "\n  "))
}

// emptyExpectations builds an all-empty (must-be-identical) expectation list from
// an old→new remap, sorted by old hash for deterministic ordering.
func emptyExpectations(remap map[string]string) []model.Expectation {
	olds := make([]string, 0, len(remap))
	for old := range remap {
		olds = append(olds, old)
	}
	sort.Strings(olds)
	exps := make([]model.Expectation, 0, len(remap))
	for _, old := range olds {
		exps = append(exps, model.Expectation{OldCommit: old, NewCommit: remap[old]})
	}
	return exps
}
