package model

import "fmt"

// Hunk is one contiguous change region from diff(parent, commit). It carries a
// stable ID the LLM references and enough surrounding diff text (FileHeader +
// Body) to be re-applied with `git apply`, so splitting never reparses source —
// it is language-agnostic by construction.
type Hunk struct {
	ID         string // "H1", "H2", … assigned deterministically in file+line order
	File       string // post-image path
	OldPath    string // pre-image path for renames; "" otherwise
	FileHeader string // the per-file "diff --git…/---/+++" header block
	Body       string // the "@@ …" hunk line plus its +/-/context lines
	Binary     bool   // binary or mode-only change — never sub-split
	Additions  int
	Deletions  int
}

// Patch returns a standalone, applyable patch for this single hunk.
func (h Hunk) Patch() string { return h.FileHeader + h.Body }

// HunkGroup is one future sub-commit: the hunks it contains plus its message.
type HunkGroup struct {
	Order      int      // 1-based position in the new mini-chain
	HunkIDs    []string // members; every hunk lands in exactly one group
	Subject    string
	Body       string
	Intent     string
	Confidence float64
	TestsPass  bool // filled during validation
	WIP        bool // could not be made to pass → "WIP:" prefix on subject
}

// SplitProposal is the ordered plan for splitting one commit.
type SplitProposal struct {
	CommitHash  string
	ParentHash  string
	Hunks       []Hunk
	Groups      []HunkGroup // len 1 == "not worth splitting"
	ShouldSplit bool
	Reason      string // why-not, when ShouldSplit is false
}

// ValidateCoverage enforces the partition invariant: every hunk is assigned to
// exactly one group, and no group references an unknown hunk. This is the pure
// first line of defense behind the "sum of commits == original diff" guarantee
// and runs before any git object is created.
func ValidateCoverage(hunks []Hunk, groups []HunkGroup) error {
	known := make(map[string]bool, len(hunks))
	for _, h := range hunks {
		known[h.ID] = true
	}
	seen := make(map[string]bool, len(hunks))
	for _, g := range groups {
		for _, id := range g.HunkIDs {
			if !known[id] {
				return fmt.Errorf("group references unknown hunk %q", id)
			}
			if seen[id] {
				return fmt.Errorf("hunk %q assigned to more than one group", id)
			}
			seen[id] = true
		}
	}
	for id := range known {
		if !seen[id] {
			return fmt.Errorf("hunk %q is not assigned to any group", id)
		}
	}
	return nil
}
