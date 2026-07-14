// internal/gitio/purge.go
package gitio

import (
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/model"
)

// PurgeRemap reports how a purge mapped old commits to new ones. Rewritten
// commits had target paths removed from their tree; Passthrough commits are
// byte-identical re-emits (only their parent changed); Dropped commits became
// empty (tree equal to their new parent's tree) and vanished.
type PurgeRemap struct {
	Rewritten   map[string]string
	Passthrough map[string]string
	Dropped     []string
}

// AffectedCommits returns the set of commit hashes on the current branch that
// touch any of the given paths (git's own pathspec limiting, so directories
// match everything beneath them without enumeration).
func (r *Repo) AffectedCommits(paths []string) (map[string]bool, error) {
	args := append([]string{"rev-list", "HEAD", "--"}, paths...)
	out, err := r.git(args...)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, h := range strings.Fields(out) {
		set[h] = true
	}
	return set, nil
}

// CommitTouchedFiles lists every path a commit changed (--root makes this work
// for the root commit too). Used to predict which commits a purge will empty.
func (r *Repo) CommitTouchedFiles(hash string) ([]string, error) {
	out, err := r.git("diff-tree", "-r", "--name-only", "--no-commit-id", "--root", hash)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// ApplyPurge rebuilds history with the spec's paths removed, as if they were
// never added. It walks root-first from the earliest affected commit;
// everything before keeps its original hash (the freeze guarantee). No ref is
// moved.
//
// Every commit's tree from firstAffected onward — not just the ones flagged
// in affected — is passed through RemovePathsFromTree. affected comes from
// `git rev-list -- paths`, which only flags a commit whose OWN diff touches a
// path; but commit trees are full snapshots, so a commit that never touched
// the path itself can still carry it forward unmodified from an ancestor (in
// this package's own test fixture, a commit that edits an unrelated file
// while a previously-added junk file rides along in its snapshot untouched).
// Skipping those commits would leave the purged blob reachable from history,
// defeating the purge. RemovePathsFromTree is already a safe no-op
// (--ignore-unmatch) when a path is genuinely absent from a given tree, so
// calling it unconditionally costs nothing but correctness.
//
// A commit whose new tree equals its (rewritten) parent's tree is DROPPED and
// descendants reparent past it.
//
// newHead == "" means EVERY commit was dropped from the root — the caller
// must refuse to move a ref there. That is the only case newHead is empty: a
// purge that drops every commit AFTER an untouched frozen prefix instead
// returns that frozen ancestor's ORIGINAL hash, a legal (if unusual) result —
// the branch simply shrinks back to its untouched prefix.
func (r *Repo) ApplyPurge(commits []model.Commit, spec model.PurgeSpec, affected map[string]bool, sign SignMode) (string, PurgeRemap, error) {
	remap := PurgeRemap{Rewritten: map[string]string{}, Passthrough: map[string]string{}}
	if len(affected) == 0 {
		return "", remap, fmt.Errorf("ApplyPurge: no affected commits")
	}
	firstAffected := -1
	for i, c := range commits {
		if affected[c.Hash] {
			firstAffected = i
			break
		}
	}
	if firstAffected < 0 {
		return "", remap, fmt.Errorf("ApplyPurge: affected commits not on this branch")
	}

	// prevNew / prevTree track the last EMITTED commit. They start at the
	// frozen ancestor of the first affected commit; for a root start they are
	// ""/emptyTreeHash so a fully-emptied root is dropped by the same rule.
	prevNew, prevTree := "", emptyTreeHash
	if firstAffected > 0 {
		prevNew = commits[firstAffected-1].Hash
		prevTree = commits[firstAffected-1].TreeHash
	}

	newHead := prevNew
	for i := firstAffected; i < len(commits); i++ {
		c := commits[i]
		tree, err := r.RemovePathsFromTree(c.TreeHash, spec.Paths)
		if err != nil {
			return "", remap, fmt.Errorf("purge %s: %w", short(c.Hash), err)
		}
		if tree == prevTree {
			// Nothing left: the commit only ever changed purged paths. Skip it;
			// the next commit parents on prevNew, erasing it from history.
			remap.Dropped = append(remap.Dropped, c.Hash)
			continue
		}
		hash, err := r.gitWithEnvStdin(
			buildCommitEnv(c, model.Edit{}),
			buildMessage(c, model.Edit{}),
			commitTreeArgs(tree, purgeParents(c.ParentHashes, prevNew), sign)...)
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
	// newHead only stays "" here when firstAffected was 0 (a true root start)
	// AND every commit from the root was dropped; any other empty walk keeps
	// newHead at the frozen ancestor's hash from the initialization above,
	// which is a legal result, not this all-dropped signal.
	if newHead == "" {
		return "", remap, nil
	}
	return newHead, remap, nil
}

// purgeParents remaps a commit's first parent to the last emitted commit. An
// empty prevNew means the entire ancestor chain was dropped, so the commit
// loses its first parent and becomes the new root.
func purgeParents(parents []string, prevNew string) []string {
	if len(parents) == 0 {
		return nil
	}
	if prevNew == "" {
		return parents[1:]
	}
	return remapFirstParent(parents, prevNew)
}
