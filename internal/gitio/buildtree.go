package gitio

import (
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/model"
)

// BuildTreeFromHunks materializes parentTree + the selected hunk subset in the
// worktree and returns the resulting tree hash. It always restarts from
// parentTree (so applying a growing cumulative subset never accumulates
// line-offset drift) and applies all selected hunks in one `git apply`.
//
// The caller must pass `selected` in the hunks' original file+line order (as
// produced by Repo.Hunks). Applying an empty subset returns the parent tree;
// applying all hunks returns the full child tree.
func (r *Repo) BuildTreeFromHunks(parentTree string, selected []model.Hunk, wt *Worktree) (string, error) {
	// Reset the worktree's index and files to the pristine parent tree.
	// --reset overwrites the index unconditionally; -u updates the working tree
	// to match. This is the key step that prevents line-offset drift: every
	// invocation restarts from the same baseline regardless of prior calls.
	if _, err := wt.git("read-tree", "--reset", "-u", parentTree); err != nil {
		return "", fmt.Errorf("reset worktree to parent: %w", err)
	}

	// Remove any untracked files left by a previous BuildTreeFromHunks call.
	// read-tree --reset -u only restores tracked files; if a prior call applied
	// a hunk that ADDED a new file (not present in parentTree), that file remains
	// untracked after the reset and causes `git apply` to fail on the next call
	// with "already exists in working directory". Cleaning first makes the
	// worktree truly pristine before applying the new subset.
	if _, err := wt.git("clean", "-fd"); err != nil {
		return "", fmt.Errorf("clean worktree before apply: %w", err)
	}

	if len(selected) > 0 {
		patch := assemblePatch(selected)
		// Apply all hunks in one shot via stdin so git can resolve any
		// inter-hunk context dependencies within the same file.
		// --whitespace=nowarn avoids rejections or silent fixups on trailing
		// whitespace, which would corrupt the index relative to the diff.
		// --index keeps the index in sync with the working tree so write-tree
		// captures the applied changes.
		if _, err := wt.gitWithEnvStdin(nil, patch, "apply", "--whitespace=nowarn", "--index", "-"); err != nil {
			return "", fmt.Errorf("apply cumulative patch: %w", err)
		}
	}

	// Snapshot the current index state as a tree object. The returned hash is
	// deterministic: identical file content always produces the same tree hash.
	tree, err := wt.git("write-tree")
	if err != nil {
		return "", fmt.Errorf("write-tree: %w", err)
	}
	return tree, nil
}

// assemblePatch builds one unified diff from the selected hunks: hunks are
// grouped by file (first-seen order), each file's header emitted once followed
// by its hunk bodies in their original order.
//
// The input order of `selected` must already be the hunks' natural file+line
// order (as produced by Hunks). The split engine sorts the cumulative subset by
// each hunk's index in the full Hunks() slice before calling this.
//
// Must be called with at least one hunk; an empty slice produces an empty patch.
func assemblePatch(selected []model.Hunk) string {
	var b strings.Builder
	var lastHeader string
	for _, h := range selected {
		// Emit the per-file diff header exactly once, even when a file has
		// multiple hunks. FileHeader is identical across all hunks for the same
		// file, so string equality suffices as a deduplication key.
		if h.FileHeader != lastHeader {
			b.WriteString(h.FileHeader)
			lastHeader = h.FileHeader
		}
		b.WriteString(h.Body)
	}
	return b.String()
}
