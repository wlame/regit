package model

import "time"

// SquashChain is one maximal run of >=2 consecutive selected commits collapsed
// into a single commit. Members are root-first: Members[0] is the first (oldest)
// commit, Members[len-1] the last (newest). The squashed commit reuses the LAST
// member's tree (so the snapshot is byte-identical), sits on the first member's
// parent, and carries the chosen message/identity/date.
type SquashChain struct {
	Members []Commit // >=2, root-first

	Subject     string    // chosen result title  (prefilled, user-editable)
	Body        string    // chosen result body
	AuthorName  string    // chosen identity  (default: first/oldest member)
	AuthorEmail string    // chosen identity
	AuthorDate  time.Time // chosen date      (default: last/newest member)
}

// First returns the oldest member of the chain.
func (c SquashChain) First() Commit { return c.Members[0] }

// Last returns the newest member of the chain (whose tree the squash reuses).
func (c SquashChain) Last() Commit { return c.Members[len(c.Members)-1] }

// SquashPlan is the full set of chains applied in one rewrite, plus the ignored
// singletons (for the "N ignored" report). Chains are root-first and non-overlapping.
type SquashPlan struct {
	Chains  []SquashChain
	Ignored []string
}

// DetectChains partitions the selected commit hashes into maximal runs of
// consecutive commits in the root-first history. Adjacency in the commits slice
// IS parent-child linkage (the slice is first-parent linear), so two selected
// commits join the same chain exactly when no unselected commit sits between
// them. Runs of length 1 (singletons) have nothing to squash with and are
// returned in ignored. A chain's first member normally has an unselected parent
// (the anchor the squash sits on); that never drops it. Chains carry empty config
// (the caller fills defaults via DefaultSquashConfig or the LLM engine).
func DetectChains(commits []Commit, selected map[string]bool) (chains []SquashChain, ignored []string) {
	var run []Commit
	flush := func() {
		switch {
		case len(run) >= 2:
			members := make([]Commit, len(run))
			copy(members, run)
			chains = append(chains, SquashChain{Members: members})
		case len(run) == 1:
			ignored = append(ignored, run[0].Hash)
		}
		run = nil
	}
	for _, c := range commits {
		if selected[c.Hash] {
			run = append(run, c)
			continue
		}
		flush()
	}
	flush()
	return chains, ignored
}
