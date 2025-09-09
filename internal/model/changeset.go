package model

import (
	"fmt"
	"sort"
)

// ChangeStatus is how one path changed between two commits. Renamed is reserved
// for future operations that opt into git's rename detection; the default diff
// regit runs (no -M) reports a rename as a Deleted + Added pair, so Renamed is
// never produced today.
type ChangeStatus int

const (
	Added ChangeStatus = iota
	Modified
	Deleted
	Renamed
)

// String renders a status for human-readable mismatch messages.
func (s ChangeStatus) String() string {
	switch s {
	case Added:
		return "added"
	case Modified:
		return "modified"
	case Deleted:
		return "deleted"
	case Renamed:
		return "renamed"
	default:
		return "unknown"
	}
}

// PathChange is one path an operation intends to (or did) change.
type PathChange struct {
	Path    string
	Status  ChangeStatus
	OldPath string // set only when Status == Renamed
}

// ChangeSet is a set of path changes between two commits. The empty set means
// the two snapshots are byte-identical. Construct via NewChangeSet so Changes is
// normalized (sorted by Path) — that keeps comparison linear and the mismatch
// report deterministic.
type ChangeSet struct {
	Changes []PathChange
}

// NewChangeSet returns a ChangeSet whose Changes are sorted by Path.
func NewChangeSet(changes []PathChange) ChangeSet {
	sorted := make([]PathChange, len(changes))
	copy(sorted, changes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	return ChangeSet{Changes: sorted}
}

// IsEmpty reports whether the change-set describes no change at all.
func (cs ChangeSet) IsEmpty() bool { return len(cs.Changes) == 0 }

// Equal reports whether actual matches what expected describes, exactly.
func (expected ChangeSet) Equal(actual ChangeSet) bool {
	return len(expected.Mismatch(actual)) == 0
}

// Mismatch returns human-readable discrepancies between expected and actual:
// expected changes that did not happen, paths whose status differs, and paths
// that changed unexpectedly. An empty slice means they are equal. Expected
// paths are reported first (in sorted order), then any extra actual paths.
func (expected ChangeSet) Mismatch(actual ChangeSet) []string {
	index := func(cs ChangeSet) map[string]PathChange {
		m := make(map[string]PathChange, len(cs.Changes))
		for _, c := range cs.Changes {
			m[c.Path] = c
		}
		return m
	}
	exp := index(expected)
	act := index(actual)

	var diffs []string
	for _, c := range expected.Changes {
		a, ok := act[c.Path]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("expected %s %s, but it is unchanged", c.Path, c.Status))
			continue
		}
		if a.Status != c.Status {
			diffs = append(diffs, fmt.Sprintf("expected %s %s, but it was %s", c.Path, c.Status, a.Status))
		}
	}
	for _, a := range actual.Changes {
		if _, ok := exp[a.Path]; !ok {
			diffs = append(diffs, fmt.Sprintf("unexpected change to %s (%s)", a.Path, a.Status))
		}
	}
	return diffs
}

// Expectation pairs two commits with the change-set an operation promises
// between them. An empty Expected means "these two commits must be identical".
type Expectation struct {
	OldCommit string
	NewCommit string
	Expected  ChangeSet
}

// VerificationSummary feeds the success audit line.
type VerificationSummary struct {
	Commits      int
	ChangedPaths int
}

// String renders the audit line, e.g. "verified 14 commits, 0 paths changed".
func (s VerificationSummary) String() string {
	return fmt.Sprintf("verified %d %s, %d %s changed",
		s.Commits, plural(s.Commits, "commit"), s.ChangedPaths, plural(s.ChangedPaths, "path"))
}

// plural returns the singular noun for n == 1, else appends "s".
func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}
