// internal/gitio/redact.go
package gitio

import (
	"fmt"

	"github.com/wlame/regit/internal/model"
)

// RedactRemap reports how a redact mapped old commits to new ones. Rewritten
// commits had one or more target lines edited (removed or replaced) in their
// tree; Passthrough commits are byte-identical re-emits (only their parent
// changed); Dropped commits became empty (tree equal to their new parent's
// tree) and vanished. Mirrors PurgeRemap exactly — see its doc comment.
type RedactRemap struct {
	Rewritten   map[string]string
	Passthrough map[string]string
	Dropped     []string
}

// RedactBase computes the rewrite boundary for a redact: the index (into
// commits, root-first as ReadCommits returns it) of the earliest commit whose
// ORIGINAL tree the spec's transform would actually change. Returns -1 when
// no commit's tree is affected at all.
//
// Unlike PurgeBase, no backward-correction walk is needed here. PurgeBase
// exists because its starting point — `git rev-list -- paths`, i.e.
// AffectedCommits — only flags commits whose OWN diff touches a path, and
// git's merge simplification can additionally drop a merge from that list
// even when the merge's tree carries the path; PurgeBase then walks backward,
// re-checking tree content with `ls-tree`, to correct for both gaps. RedactBase
// never goes through a diff- or pathspec-based rev-list at all: it calls
// RedactBlobsInTree directly against each candidate's ORIGINAL tree and asks
// whether the transform's OUTPUT differs from the INPUT. That tree-content
// check is exactly the test PurgeBase's correction walk exists to approximate
// indirectly — doing it directly, root-first, needs no separate correction
// pass, because there is no earlier, imprecise signal left to correct.
func (r *Repo) RedactBase(commits []model.Commit, spec model.RedactSpec) (int, error) {
	for i, c := range commits {
		tree, err := r.RedactBlobsInTree(c.TreeHash, spec)
		if err != nil {
			return -1, fmt.Errorf("RedactBase: %s: %w", short(c.Hash), err)
		}
		if tree != c.TreeHash {
			return i, nil
		}
	}
	return -1, nil
}

// ApplyRedact rebuilds history with the spec's line edits applied, as if the
// targeted content had never been written that way. It walks root-first from
// RedactBase's boundary; everything before keeps its original hash (the
// freeze guarantee). No ref is moved.
//
// This clones ApplyPurge's walk shape exactly, substituting
// RedactBlobsInTree(c.TreeHash, spec) for RemovePathsFromTree: every commit's
// tree from the base onward is passed through the transform (not just commits
// whose own diff happens to touch a target line), because a commit's tree is
// a full snapshot that can carry a target line forward unmodified from an
// ancestor — see ApplyPurge's doc comment for the identical reasoning.
//
// A commit whose new tree equals its (rewritten) parent's tree is DROPPED and
// descendants reparent past it — UNLESS it was already empty before the
// redact ran (its ORIGINAL tree already equalled its ORIGINAL parent's tree,
// e.g. a `git commit --allow-empty` marker). This is the same alreadyEmpty
// preservation ApplyPurge uses, for the same reason: a redact should drop
// only the commits IT empties, never an intentional marker commit that
// happened to already be empty.
//
// newHead == "" means EVERY commit was dropped from the root — see
// ApplyPurge's doc comment for why that is the only case newHead is empty.
func (r *Repo) ApplyRedact(
	commits []model.Commit, spec model.RedactSpec, sign SignMode,
) (string, RedactRemap, error) {
	remap := RedactRemap{Rewritten: map[string]string{}, Passthrough: map[string]string{}}
	base, err := r.RedactBase(commits, spec)
	if err != nil {
		return "", remap, fmt.Errorf("ApplyRedact: compute base: %w", err)
	}
	if base < 0 {
		return "", remap, fmt.Errorf("ApplyRedact: no commit's snapshot contains the target content")
	}

	// prevNew / prevTree track the last EMITTED commit. They start at the
	// frozen ancestor of the base commit; for a root start they are ""/
	// emptyTreeHash so a fully-emptied root is dropped by the same rule.
	prevNew, prevTree := "", emptyTreeHash
	if base > 0 {
		prevNew = commits[base-1].Hash
		prevTree = commits[base-1].TreeHash
	}

	newHead := prevNew
	for i := base; i < len(commits); i++ {
		c := commits[i]
		// The ORIGINAL parent's tree decides whether c was ALREADY empty
		// before this redact touched anything — independent of prevTree,
		// which tracks the REWRITTEN parent (see ApplyPurge's identical
		// origParentTree comment for why the two must not be conflated).
		origParentTree := emptyTreeHash
		if i > 0 {
			origParentTree = commits[i-1].TreeHash
		}
		alreadyEmpty := c.TreeHash == origParentTree

		tree, err := r.RedactBlobsInTree(c.TreeHash, spec)
		if err != nil {
			return "", remap, fmt.Errorf("redact %s: %w", short(c.Hash), err)
		}
		if tree == prevTree && !alreadyEmpty {
			// Nothing left: the commit only ever changed targeted lines. Skip
			// it; the next commit parents on prevNew, erasing it from history.
			remap.Dropped = append(remap.Dropped, c.Hash)
			continue
		}
		hash, err := r.gitWithEnvStdin(
			buildCommitEnv(c, model.Edit{}),
			buildMessage(c, model.Edit{}),
			commitTreeArgs(tree, replayParents(c.ParentHashes, prevNew), sign)...)
		if err != nil {
			return "", remap, fmt.Errorf("replay %s: %w", short(c.Hash), err)
		}
		if tree == c.TreeHash {
			remap.Passthrough[c.Hash] = hash
		} else {
			remap.Rewritten[c.Hash] = hash
		}
		prevNew, prevTree, newHead = hash, tree, hash
	}
	if newHead == "" {
		return "", remap, nil
	}
	return newHead, remap, nil
}
