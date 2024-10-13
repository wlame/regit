package model

import "time"

// Edit captures the pending changes for a single commit. An Edit is produced by
// the strategy engine (for date changes) or the commit editor (for message and
// manual date changes) and consumed by the rewrite engine.
//
// All fields are optional via pointers/flags so an Edit can express "change only
// the author date and leave everything else as-is". A zero-value Edit means "no
// change", which is why the date fields are *time.Time rather than time.Time:
// a nil pointer is unambiguously "unchanged", whereas the zero time.Time is a
// real (if unlikely) timestamp.
type Edit struct {
	// NewAuthorDate, when non-nil, overrides the commit's author date.
	NewAuthorDate *time.Time
	// NewCommitDate, when non-nil, overrides the committer date. When dates are
	// "linked" (the default), the engine sets this equal to NewAuthorDate.
	NewCommitDate *time.Time

	// NewSubject, when non-nil, replaces the commit subject.
	NewSubject *string
	// NewBody, when non-nil, replaces the commit body.
	NewBody *string

	// DropBody removes the body entirely, keeping only the subject. It takes
	// precedence over NewBody.
	DropBody bool
	// DropSignature strips an existing signature instead of attempting a re-sign.
	DropSignature bool
}

// IsEmpty reports whether the Edit would change nothing about the commit.
func (e Edit) IsEmpty() bool {
	return e.NewAuthorDate == nil &&
		e.NewCommitDate == nil &&
		e.NewSubject == nil &&
		e.NewBody == nil &&
		!e.DropBody &&
		!e.DropSignature
}

// EffectiveAuthorDate returns the author date this edit produces for the given
// commit: the override if present, otherwise the commit's original author date.
func (e Edit) EffectiveAuthorDate(c Commit) time.Time {
	if e.NewAuthorDate != nil {
		return *e.NewAuthorDate
	}
	return c.OrigAuthorDate
}

// EffectiveCommitDate returns the committer date this edit produces for the
// given commit, falling back to the original committer date when unset.
func (e Edit) EffectiveCommitDate(c Commit) time.Time {
	if e.NewCommitDate != nil {
		return *e.NewCommitDate
	}
	return c.OrigCommitDate
}
