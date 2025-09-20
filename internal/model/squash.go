package model

import (
	"fmt"
	"time"
)

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

// DefaultSquashConfig fills a chain's result fields with the no-LLM defaults:
// subject/body and author identity from the FIRST (oldest) member; author date
// from the LAST (newest) member. The LLM path overrides only Subject/Body.
func DefaultSquashConfig(c SquashChain) SquashChain {
	first, last := c.First(), c.Last()
	c.Subject = first.Subject
	c.Body = first.Body
	c.AuthorName = first.AuthorName
	c.AuthorEmail = first.AuthorEmail
	c.AuthorDate = last.OrigAuthorDate
	return c
}

// indexOf returns the position of hash in commits, or -1.
func indexOf(commits []Commit, hash string) int {
	for i, c := range commits {
		if c.Hash == hash {
			return i
		}
	}
	return -1
}

// ChainDateBounds returns the inclusive [lo, hi] window the squashed commit's
// author date may occupy while preserving monotonic ordering. lo is the parent
// commit's author date (hasLo=false when the chain starts at the root); hi is the
// next commit's author date (hasHi=false when the chain ends at HEAD).
func ChainDateBounds(commits []Commit, c SquashChain) (lo, hi time.Time, hasLo, hasHi bool) {
	firstIdx := indexOf(commits, c.First().Hash)
	lastIdx := indexOf(commits, c.Last().Hash)
	if firstIdx > 0 {
		lo, hasLo = commits[firstIdx-1].OrigAuthorDate, true
	}
	if lastIdx >= 0 && lastIdx < len(commits)-1 {
		hi, hasHi = commits[lastIdx+1].OrigAuthorDate, true
	}
	return lo, hi, hasLo, hasHi
}

// ClampChainDate constrains a chain's AuthorDate into its [lo, hi] window so the
// default (the last member's date) stays valid even when the original history is
// non-monotonic at the chain boundary (e.g. after a redate). When the window is
// empty (lo > hi — a genuinely inconsistent history) it leaves the date unchanged
// so Validate still surfaces a clear error.
func ClampChainDate(commits []Commit, c SquashChain) SquashChain {
	lo, hi, hasLo, hasHi := ChainDateBounds(commits, c)
	if hasLo && hasHi && lo.After(hi) {
		return c
	}
	if hasHi && c.AuthorDate.After(hi) {
		c.AuthorDate = hi
	}
	if hasLo && c.AuthorDate.Before(lo) {
		c.AuthorDate = lo
	}
	return c
}

// Validate checks the plan is internally consistent against the history: chains
// are non-empty (>=2 members), their members are consecutive in commits, no two
// chains overlap, and each chosen date sits within its window.
func (p SquashPlan) Validate(commits []Commit) error {
	used := map[int]bool{}
	for _, ch := range p.Chains {
		if len(ch.Members) < 2 {
			return fmt.Errorf("chain has %d member(s); a squash needs at least 2", len(ch.Members))
		}
		// Members must be a contiguous, increasing run in commits.
		start := indexOf(commits, ch.First().Hash)
		if start < 0 {
			return fmt.Errorf("chain member %s is not on the current history", ch.First().Hash)
		}
		for k, m := range ch.Members {
			idx := indexOf(commits, m.Hash)
			if idx != start+k {
				return fmt.Errorf("chain members are not consecutive at %s", m.Hash)
			}
			if used[idx] {
				return fmt.Errorf("commit %s is in more than one chain", m.Hash)
			}
			used[idx] = true
		}
		// Date window.
		lo, hi, hasLo, hasHi := ChainDateBounds(commits, ch)
		if hasLo && ch.AuthorDate.Before(lo) {
			return fmt.Errorf("chain date %s is before its lower bound %s", ch.AuthorDate, lo)
		}
		if hasHi && ch.AuthorDate.After(hi) {
			return fmt.Errorf("chain date %s is after its upper bound %s", ch.AuthorDate, hi)
		}
	}
	return nil
}
