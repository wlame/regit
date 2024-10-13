// Package model defines the core domain types shared across regit: the commits
// we read from a repository, the pending edits a user builds up, and the plan
// that bundles those edits together with validation results.
//
// This package has no dependencies on git, the UI, or any strategy logic. It is
// the common vocabulary that every other package speaks, which keeps the
// dependency graph pointing inward toward these plain data types.
package model

import (
	"strings"
	"time"
)

// Commit is an immutable snapshot of a single commit as read from the
// repository. regit never mutates a Commit; instead it produces an Edit (see
// edit.go) describing the change, and the rewrite engine replays history.
//
// The original* date fields are preserved so strategies like "Offset Shift" and
// "Preserve-Relative" can compute new dates relative to where the commit
// started, and so the preview can show a faithful before/after.
type Commit struct {
	Hash         string   // full SHA-1/SHA-256 of the commit object
	ParentHashes []string // parent commit hashes (len 0 = root, >1 = merge)
	TreeHash     string   // SHA of the tree object; reused verbatim on rewrite

	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string

	OrigAuthorDate time.Time // author date as originally recorded
	OrigCommitDate time.Time // committer date as originally recorded

	Message string // full raw commit message, exactly as stored (%B)
	Subject string // first line of the commit message, for display/editing
	Body    string // message after the first line and its blank separator

	IsSigned bool // true if the commit carried a GPG/SSH signature
	IsPushed bool // true if this commit also exists on the upstream branch
	IsMerge  bool // true if the commit has more than one parent
}

// HasBody reports whether the commit message has content beyond its subject
// line. The TUI uses this to decide whether the "drop body" action is available.
func (c Commit) HasBody() bool {
	return c.Body != ""
}

// SplitMessage splits a raw commit message into its first line (the display
// subject) and the rest (the body), trimming the conventional blank separator
// line but otherwise preserving the body's formatting.
//
// Note this is a display-oriented split: unlike git's own %s/%b, it never
// folds a multi-line first paragraph into the subject, so no message content
// can hide in (or vanish from) the subject line.
func SplitMessage(raw string) (subject, body string) {
	raw = strings.TrimRight(raw, "\n")
	subject, body, _ = strings.Cut(raw, "\n")
	return subject, strings.TrimLeft(body, "\n")
}
