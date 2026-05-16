package capability

import (
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// resolveTargets selects the commits a bulk verb operates on. Empty spec → all
// commits (the active range). "A..B" → commits after A through B. A single ref →
// just that commit. Returns commits root-first.
func resolveTargets(repo *gitio.Repo, commits []model.Commit, spec string) ([]model.Commit, error) {
	if spec == "" {
		return commits, nil
	}
	// Without these checks, Cut on "A...B" yields hi=".B" and "A.." yields
	// hi="" — both fed to FindCommit as bogus refs with a baffling error.
	if strings.Contains(spec, "...") {
		return nil, fmt.Errorf("range %q: three-dot ranges are not supported; use two-dot A..B (git range semantics, A excluded)", spec)
	}
	if lo, hi, ok := strings.Cut(spec, ".."); ok {
		if hi == "" {
			return nil, fmt.Errorf("range %q: missing endpoint after \"..\" — use A..B (e.g. %sHEAD)", spec, spec)
		}
		hiIdx, err := FindCommit(repo, commits, hi)
		if err != nil {
			return nil, err
		}
		loIdx := -1
		if lo != "" {
			loIdx, err = FindCommit(repo, commits, lo)
			if err != nil {
				return nil, err
			}
		}
		if loIdx > hiIdx {
			return nil, fmt.Errorf("range %q: A must be an ancestor of B", spec)
		}
		return commits[loIdx+1 : hiIdx+1], nil
	}
	idx, err := FindCommit(repo, commits, spec)
	if err != nil {
		return nil, err
	}
	return commits[idx : idx+1], nil
}

func indexByHash(commits []model.Commit) map[string]int {
	m := make(map[string]int, len(commits))
	for i, c := range commits {
		m[c.Hash] = i
	}
	return m
}
