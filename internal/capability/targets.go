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

// resolveTargetSet resolves a --commits spec that may be a comma-separated
// union of parts, each a single ref or an A..B range. Empty spec → all
// commits. The result preserves root-first order. This is the selection
// counterpart of resolveTargets, shared by reauthor and redate so a TUI
// selection logged as a comma-joined hash list round-trips on any verb.
func resolveTargetSet(repo *gitio.Repo, commits []model.Commit, spec string) ([]model.Commit, error) {
	if strings.TrimSpace(spec) == "" {
		return commits, nil
	}
	want := map[string]bool{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		sub, err := resolveTargets(repo, commits, part)
		if err != nil {
			return nil, err
		}
		for _, c := range sub {
			want[c.Hash] = true
		}
	}
	out := make([]model.Commit, 0, len(want))
	for _, c := range commits { // preserve root-first order
		if want[c.Hash] {
			out = append(out, c)
		}
	}
	return out, nil
}

func indexByHash(commits []model.Commit) map[string]int {
	m := make(map[string]int, len(commits))
	for i, c := range commits {
		m[c.Hash] = i
	}
	return m
}
