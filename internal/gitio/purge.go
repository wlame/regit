// internal/gitio/purge.go
package gitio

import (
	"fmt"
	"sort"
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
//
// -z NUL-delimits the path list and, unlike the default text mode, never
// C-quotes a path (octal-escaping non-ASCII bytes as e.g. "caf\303\251.txt").
// Splitting on "\n" instead would compare that escaped literal against the
// real path and never match, silently excluding non-ASCII paths from the
// drop prediction — mirrors DiffChangeSet's own -z handling.
func (r *Repo) CommitTouchedFiles(hash string) ([]string, error) {
	out, err := r.gitRaw("diff-tree", "-r", "-z", "--name-only", "--no-commit-id", "--root", hash)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	tokens := strings.Split(out, "\x00")
	files := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t != "" {
			files = append(files, t)
		}
	}
	return files, nil
}

// PurgeBase computes the corrected rewrite boundary for a purge: the index
// (into commits, root-first as ReadCommits returns it) of the earliest
// commit the rewrite must start from. Returns -1 when no commit in commits
// is affected at all.
//
// The naive boundary — the first commits[i] flagged in affected — is not
// always correct. affected comes from `git rev-list HEAD -- paths`, which
// applies git's default merge simplification: a merge commit that is
// TREESAME to one parent for the given pathspec is dropped from the list in
// favor of that parent, even when the merge's OWN tree already carries the
// path (e.g. a side branch adds the path and gets merged in with no further
// change to it). Trusting the naive boundary would then freeze that merge
// commit into the "never touched" prefix, keep its original hash, and leave
// the path fully reachable from the new head — a silent partial purge.
//
// PurgeBase closes that gap: starting from the naive boundary, it walks
// backward while the boundary ancestor's tree — commits[firstAffected-1], or
// the empty tree at a true root start — still contains one of paths (checked
// with `git ls-tree`, so a directory's members are never enumerated back into
// Go). Each backward step pulls a previously-frozen commit into the
// rewritten range; if the walk lands on a merge commit, the caller's own
// merge-in-suffix guard (see safety.Manager.ApplyPurge) then blocks the
// purge instead of silently completing it.
func (r *Repo) PurgeBase(commits []model.Commit, paths []string, affected map[string]bool) (int, error) {
	firstAffected := -1
	for i, c := range commits {
		if affected[c.Hash] {
			firstAffected = i
			break
		}
	}
	if firstAffected < 0 {
		return -1, nil
	}
	for firstAffected > 0 {
		boundaryTree := commits[firstAffected-1].TreeHash
		has, err := r.treeContainsAnyPath(boundaryTree, paths)
		if err != nil {
			return -1, err
		}
		if !has {
			break
		}
		firstAffected--
	}
	return firstAffected, nil
}

// treeContainsAnyPath reports whether any of paths (files or whole
// directories) is present in tree. -r plus --name-only is safe even for a
// directory pathspec — git matches the pathspec prefix internally, so the
// caller never needs to enumerate a directory's members; the output here is
// used only for its emptiness and never reaches the UI.
func (r *Repo) treeContainsAnyPath(tree string, paths []string) (bool, error) {
	args := append([]string{"ls-tree", "-r", "--name-only", tree, "--"}, paths...)
	out, err := r.git(args...)
	if err != nil {
		return false, fmt.Errorf("ls-tree %s: %w", short(tree), err)
	}
	return out != "", nil
}

// PathsReachableFrom reports whether any commit reachable from rev touches
// one of paths (a pathspec-limited `rev-list -- paths` walk, generalized to
// an arbitrary revision). Used post-rewrite to prove none of the purged
// paths remain reachable from a freshly built head, independent of however
// that head's base was computed.
//
// --full-history disables git's default merge simplification, which
// otherwise elides a "pocket": a commit where a path was added and then
// removed again entirely within a side branch before that branch merged.
// Because the merge ends up TREESAME to BOTH parents for that path (neither
// side has it at merge time), a plain rev-list treats the merge as
// uninteresting and never descends into the side branch's own history at
// all — so the intermediate commit that once held the blob, and the blob
// itself, stay invisible to this check even though they remain fully
// reachable from rev via the merge's second parent. --full-history walks
// every reachable commit regardless of simplification, so a pocket commit's
// own diff (against whichever parent it differs from) is seen.
func (r *Repo) PathsReachableFrom(rev string, paths []string) (bool, error) {
	args := append([]string{"rev-list", "--full-history", rev, "--"}, paths...)
	out, err := r.git(args...)
	if err != nil {
		return false, fmt.Errorf("rev-list %s: %w", short(rev), err)
	}
	return out != "", nil
}

// PathsInSideHistory reports which of paths are touched by a commit that is
// reachable from HEAD but does NOT sit on the first-parent chain commits
// represents (a "side branch" commit) — including a "pocket" commit whose
// net change by the time its branch merges is zero (added then removed
// within the side branch itself) and so never surfaces in AffectedCommits'
// default, merge-simplified rev-list at all (see PathsReachableFrom's doc
// comment for the same blind spot). commits is the FULL first-parent
// history (root-first, as ReadCommits returns it).
//
// regit only ever rewrites the first-parent chain (see ApplyPurge), so a
// path with side-branch history can never be fully purged: the blob stays
// reachable via the side branch's own, untouched commits no matter what
// happens to commits itself. This is what lets a guard fire BEFORE any
// rewrite is attempted, rather than only being caught by
// safety.Manager.verifyPurgeAbsence after the fact.
func (r *Repo) PathsInSideHistory(commits []model.Commit, paths []string) ([]string, error) {
	onChain := make(map[string]bool, len(commits))
	for _, c := range commits {
		onChain[c.Hash] = true
	}
	args := append([]string{"rev-list", "--full-history", "HEAD", "--"}, paths...)
	out, err := r.git(args...)
	if err != nil {
		return nil, fmt.Errorf("rev-list --full-history: %w", err)
	}
	found := map[string]bool{}
	for _, h := range strings.Fields(out) {
		if onChain[h] {
			continue // on the branch's own first-parent chain, not a side commit
		}
		files, err := r.CommitTouchedFiles(h)
		if err != nil {
			return nil, fmt.Errorf("side-history commit %s: %w", short(h), err)
		}
		for _, f := range files {
			for _, p := range paths {
				if model.UnderAnyPath(f, []string{p}) {
					found[p] = true
				}
			}
		}
	}
	if len(found) == 0 {
		return nil, nil
	}
	result := make([]string, 0, len(found))
	for p := range found {
		result = append(result, p)
	}
	sort.Strings(result)
	return result, nil
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
// descendants reparent past it — UNLESS it was already empty before the purge
// ran (its ORIGINAL tree already equalled its ORIGINAL parent's tree, e.g. a
// `git commit --allow-empty` marker). Purge only drops commits it itself
// empties; a commit that was already empty is preserved (re-emitted), so an
// intentional marker commit does not silently vanish as a side effect of an
// unrelated path purge.
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
	firstAffected, err := r.PurgeBase(commits, spec.Paths, affected)
	if err != nil {
		return "", remap, fmt.Errorf("ApplyPurge: compute base: %w", err)
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
		// The ORIGINAL parent's tree — commits[i-1] for a non-root commit, the
		// true empty tree for a root start — decides whether c was ALREADY
		// empty before this purge touched anything. This is independent of
		// prevTree, which tracks the REWRITTEN parent and would otherwise make
		// an already-empty commit indistinguishable from one the purge itself
		// just emptied.
		origParentTree := emptyTreeHash
		if i > 0 {
			origParentTree = commits[i-1].TreeHash
		}
		alreadyEmpty := c.TreeHash == origParentTree

		tree, err := r.RemovePathsFromTree(c.TreeHash, spec.Paths)
		if err != nil {
			return "", remap, fmt.Errorf("purge %s: %w", short(c.Hash), err)
		}
		if tree == prevTree && !alreadyEmpty {
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
