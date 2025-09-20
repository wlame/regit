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

	// New author/committer identity. Each is nil = unchanged, matching the
	// date-pointer idiom: a nil pointer is unambiguously "leave as-is", while
	// an empty *string would mean "set to empty". The reauthor action sets the
	// author pair (and, when the committer is also being updated, the committer
	// pair) to the same values.
	NewAuthorName     *string
	NewAuthorEmail    *string
	NewCommitterName  *string
	NewCommitterEmail *string
}

// IsEmpty reports whether the Edit would change nothing about the commit.
func (e Edit) IsEmpty() bool {
	return e.NewAuthorDate == nil &&
		e.NewCommitDate == nil &&
		e.NewSubject == nil &&
		e.NewBody == nil &&
		e.NewAuthorName == nil &&
		e.NewAuthorEmail == nil &&
		e.NewCommitterName == nil &&
		e.NewCommitterEmail == nil &&
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

// EffectiveAuthorName returns the author name this edit produces: the override
// if present, otherwise the commit's original author name.
func (e Edit) EffectiveAuthorName(c Commit) string {
	if e.NewAuthorName != nil {
		return *e.NewAuthorName
	}
	return c.AuthorName
}

// EffectiveAuthorEmail returns the author email this edit produces, falling back
// to the commit's original author email.
func (e Edit) EffectiveAuthorEmail(c Commit) string {
	if e.NewAuthorEmail != nil {
		return *e.NewAuthorEmail
	}
	return c.AuthorEmail
}

// EffectiveCommitterName returns the committer name this edit produces, falling
// back to the commit's original committer name.
func (e Edit) EffectiveCommitterName(c Commit) string {
	if e.NewCommitterName != nil {
		return *e.NewCommitterName
	}
	return c.CommitterName
}

// EffectiveCommitterEmail returns the committer email this edit produces, falling
// back to the commit's original committer email.
func (e Edit) EffectiveCommitterEmail(c Commit) string {
	if e.NewCommitterEmail != nil {
		return *e.NewCommitterEmail
	}
	return c.CommitterEmail
}

// NewIdentityEdit builds an Edit that rewrites a commit's author identity to the
// given name/email. Each field is set only when non-empty AND different from the
// commit's current value, so requesting the existing identity yields an empty
// Edit (callers can skip it). When committer is true, the committer identity
// mirrors the same changes. This is the single shared builder for both the
// headless reauthor command and the TUI change, so the two paths cannot diverge.
func NewIdentityEdit(c Commit, name, email string, committer bool) Edit {
	var e Edit
	if name != "" && name != c.AuthorName {
		n := name
		e.NewAuthorName = &n
	}
	if email != "" && email != c.AuthorEmail {
		em := email
		e.NewAuthorEmail = &em
	}
	if committer {
		if name != "" && name != c.CommitterName {
			n := name
			e.NewCommitterName = &n
		}
		if email != "" && email != c.CommitterEmail {
			em := email
			e.NewCommitterEmail = &em
		}
	}
	return e
}
