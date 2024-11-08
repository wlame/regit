// internal/split/size.go
package split

import "github.com/wlame/regit/internal/model"

// changedLines totals additions+deletions across hunks (binary counts as 1).
func changedLines(hunks []model.Hunk) int {
	total := 0
	for _, h := range hunks {
		if h.Binary {
			total++
			continue
		}
		total += h.Additions + h.Deletions
	}
	return total
}

// EffectiveMaxCommits clamps the configured maximum down for small changes:
// cap = min(maxCommits, ceil(changedLines / linesPerCommit)), floored at 2.
func EffectiveMaxCommits(hunks []model.Hunk, maxCommits, linesPerCommit int) int {
	if linesPerCommit <= 0 {
		linesPerCommit = 40
	}
	lines := changedLines(hunks)
	cap := (lines + linesPerCommit - 1) / linesPerCommit // ceil
	if cap < 2 {
		cap = 2
	}
	if cap > maxCommits {
		cap = maxCommits
	}
	return cap
}

// ShouldSkipSplit returns true (with a human reason) when a commit is not worth
// splitting: too small, a single hunk in a single file, or a binary-only change.
func ShouldSkipSplit(hunks []model.Hunk, minSplitLines int) (bool, string) {
	if len(hunks) <= 1 {
		return true, "only one hunk to split"
	}
	if changedLines(hunks) < minSplitLines {
		return true, "change is too small to split"
	}
	files := map[string]bool{}
	allBinary := true
	for _, h := range hunks {
		files[h.File] = true
		if !h.Binary {
			allBinary = false
		}
	}
	if allBinary {
		return true, "binary-only change cannot be split by meaning"
	}
	if len(files) == 1 && len(hunks) == 1 {
		return true, "single hunk in a single file"
	}
	return false, ""
}
