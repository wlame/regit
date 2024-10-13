package model

import "time"

// Plan is the working document of a ghist session: the ordered list of commits
// (root-first, matching how the rewrite engine replays them) plus the pending
// edits keyed by commit hash. It also carries the most recent validation result
// so the UI can render sequence violations without recomputing them.
//
// Commits are stored root-first (oldest first) because that is the order the
// rewrite engine needs to walk: each commit's new parent must already have been
// rebuilt. The TUI may display them newest-first for familiarity, but the
// canonical storage order here is root-first.
type Plan struct {
	Commits    []Commit            // root-first (oldest -> newest)
	Edits      map[string]Edit     // commit hash -> pending edit
	Violations []SequenceViolation // populated by the strategy engine's check

	// Base is the index into Commits of the first editable commit. Everything
	// before it is frozen: strategies skip those commits and the rewrite
	// engine never replays them, so their hashes are guaranteed unchanged.
	Base int
}

// SequenceViolation records a place where a child commit's effective date is
// earlier than its parent's, which would put history "out of order". The UI
// highlights these and the apply path refuses to run while any remain.
type SequenceViolation struct {
	ChildHash  string
	ParentHash string
	ChildDate  time.Time
	ParentDate time.Time
}

// NewPlan builds a Plan from a root-first slice of commits with an empty edit set.
func NewPlan(commits []Commit) *Plan {
	return &Plan{
		Commits: commits,
		Edits:   make(map[string]Edit),
	}
}

// EditFor returns the pending edit for a commit (zero Edit if none), and whether
// one was present.
func (p *Plan) EditFor(hash string) (Edit, bool) {
	e, ok := p.Edits[hash]
	return e, ok
}

// SetEdit stores or clears the edit for a commit. An empty edit is removed so
// the map only ever holds meaningful changes.
func (p *Plan) SetEdit(hash string, e Edit) {
	if e.IsEmpty() {
		delete(p.Edits, hash)
		return
	}
	p.Edits[hash] = e
}

// EffectiveAuthorDate returns the author date a commit would have after applying
// any pending edit, falling back to its original author date.
func (p *Plan) EffectiveAuthorDate(c Commit) time.Time {
	if e, ok := p.Edits[c.Hash]; ok {
		return e.EffectiveAuthorDate(c)
	}
	return c.OrigAuthorDate
}

// HasChanges reports whether the plan contains any pending edits at all.
func (p *Plan) HasChanges() bool {
	return len(p.Edits) > 0
}

// Active returns the editable suffix of the history: the commits from the
// base boundary (inclusive) to HEAD, in root-first order.
func (p *Plan) Active() []Commit {
	return p.Commits[p.Base:]
}

// IsFrozen reports whether the commit at storage index i lies before the base
// boundary and must therefore never be modified.
func (p *Plan) IsFrozen(i int) bool {
	return i < p.Base
}

// SetBase moves the frozen/editable boundary to storage index i (clamped so
// at least one commit stays editable) and drops any pending edits on commits
// that became frozen.
func (p *Plan) SetBase(i int) {
	if i < 0 {
		i = 0
	}
	if last := len(p.Commits) - 1; i > last {
		i = last
	}
	p.Base = i
	for _, c := range p.Commits[:p.Base] {
		delete(p.Edits, c.Hash)
	}
}

// IsValid reports whether the plan is free of sequence violations.
func (p *Plan) IsValid() bool {
	return len(p.Violations) == 0
}
