// Package model defines the core domain types shared across ghist: the commits
// we read from a repository, the pending edits a user builds up, and the plan
// that bundles those edits together with validation results.
//
// This package has no dependencies on git, the UI, or any strategy logic. It is
// the common vocabulary that every other package speaks, which keeps the
// dependency graph pointing inward toward these plain data types.
package model

import "time"

// Commit is an immutable snapshot of a single commit as read from the
// repository. ghist never mutates a Commit; instead it produces an Edit (see
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

	Subject string // first line of the commit message
	Body    string // remaining message after the subject (may be empty)

	IsSigned bool // true if the commit carried a GPG/SSH signature
	IsPushed bool // true if this commit also exists on the upstream branch
	IsMerge  bool // true if the commit has more than one parent
}

// HasBody reports whether the commit message has content beyond its subject
// line. The TUI uses this to decide whether the "drop body" action is available.
func (c Commit) HasBody() bool {
	return c.Body != ""
}
