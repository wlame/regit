package gitio

import (
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/model"
)

// SideCommits lists every commit reachable from mergeHash but not from its
// first parent — the merge's side history, including nested merges — oldest
// first, with the fields fold-message synthesis needs. The merge commit itself
// is reachable from itself and must be filtered out. Field/record separators
// are control bytes (unit/record separator) so subjects with any printable
// characters round-trip.
func (r *Repo) SideCommits(mergeHash string) ([]model.SideCommit, error) {
	out, err := r.git("log", "--reverse", "--format=%H%x1f%aN%x1f%aE%x1f%s%x1e",
		mergeHash, "^"+mergeHash+"^1")
	if err != nil {
		return nil, fmt.Errorf("side commits of %s: %w", short(mergeHash), err)
	}
	var side []model.SideCommit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) != 4 {
			return nil, fmt.Errorf("side commits of %s: malformed record %q", short(mergeHash), rec)
		}
		if f[0] == mergeHash {
			continue
		}
		side = append(side, model.SideCommit{
			Hash: f[0], AuthorName: f[1], AuthorEmail: f[2], Subject: f[3],
		})
	}
	return side, nil
}

// RefsContaining returns the short names of local branches and tags that
// contain hash, excluding excludeBranch (the branch being rewritten). Used for
// the linearize preview's side-history advisory — reporting where folded side
// commits stay reachable after the apply.
func (r *Repo) RefsContaining(hash, excludeBranch string) ([]string, error) {
	out, err := r.git("for-each-ref", "--contains", hash,
		"--format=%(refname:short)", "refs/heads", "refs/tags")
	if err != nil {
		return nil, fmt.Errorf("refs containing %s: %w", short(hash), err)
	}
	var refs []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == excludeBranch {
			continue
		}
		refs = append(refs, line)
	}
	return refs, nil
}

// LinearizeRemap reports how a linearize mapped old commits to new ones for
// verification. Folded maps each merge's old hash to its new ordinary commit
// (tree preserved by construction). Passthrough maps every replayed non-merge
// commit. Both must verify content-identical (empty expectations).
type LinearizeRemap struct {
	Folded      map[string]string
	Passthrough map[string]string
}

// ApplyLinearize rebuilds the first-parent chain 1:1 with every merge folded
// into an ordinary commit: same tree, same author/committer identities and
// dates, synthesized message, single parent. Non-merge commits re-emit
// byte-identically with their first parent remapped. Commits before the first
// fold keep their original hashes (not re-emitted). No ref is moved.
func (r *Repo) ApplyLinearize(
	commits []model.Commit,
	plan model.LinearizePlan,
	sign SignMode,
) (string, LinearizeRemap, error) {
	remap := LinearizeRemap{Folded: map[string]string{}, Passthrough: map[string]string{}}
	if len(plan.Folds) == 0 {
		return "", remap, fmt.Errorf("ApplyLinearize: no folds")
	}
	foldBy := make(map[string]model.LinearizeFold, len(plan.Folds))
	for _, f := range plan.Folds {
		foldBy[f.Merge.Hash] = f
	}
	firstAffected := -1
	for i, c := range commits {
		if _, ok := foldBy[c.Hash]; ok {
			firstAffected = i
			break
		}
	}
	if firstAffected == -1 {
		return "", remap, fmt.Errorf("ApplyLinearize: folds not found on history")
	}

	// prevNew starts as the ORIGINAL first parent of the first affected commit
	// ("" when it is the root), because everything before it keeps its hash.
	var prevNew, newHead string
	if fp := commits[firstAffected].ParentHashes; len(fp) > 0 {
		prevNew = fp[0]
	}

	for i := firstAffected; i < len(commits); i++ {
		// Commits before firstAffected are absent from remap: they kept their original hashes.
		c := commits[i]
		if f, ok := foldBy[c.Hash]; ok {
			hash, err := r.commitFold(c, f.NewTitle, f.NewBody, prevNew, sign)
			if err != nil {
				return "", remap, fmt.Errorf("fold %s: %w", short(c.Hash), err)
			}
			remap.Folded[c.Hash] = hash
			prevNew, newHead = hash, hash
			continue
		}
		// Untouched commit: re-emit byte-identically; only the first parent moves.
		newParents := remapFirstParent(c.ParentHashes, prevNew)
		args := commitTreeArgs(c.TreeHash, newParents, sign)
		hash, err := r.gitWithEnvStdin(
			buildCommitEnv(c, model.Edit{}),
			buildMessage(c, model.Edit{}),
			args...,
		)
		if err != nil {
			return "", remap, fmt.Errorf("replay %s: %w", short(c.Hash), err)
		}
		remap.Passthrough[c.Hash] = hash
		prevNew, newHead = hash, hash
	}
	return newHead, remap, nil
}

// commitFold creates the ordinary commit a merge folds into: the merge's tree,
// identities, and dates, a synthesized message, and a single parent. Unlike a
// pass-through commit the message is fresh, so no encoding header is preserved
// (fresh messages are UTF-8) — the same reasoning as commitSquash.
func (r *Repo) commitFold(
	merge model.Commit,
	title, body, parent string,
	sign SignMode,
) (string, error) {
	env := []string{
		"GIT_AUTHOR_NAME=" + merge.AuthorName,
		"GIT_AUTHOR_EMAIL=" + merge.AuthorEmail,
		"GIT_AUTHOR_DATE=" + merge.OrigAuthorDate.Format(gitDateFormat),
		"GIT_COMMITTER_NAME=" + merge.CommitterName,
		"GIT_COMMITTER_EMAIL=" + merge.CommitterEmail,
		"GIT_COMMITTER_DATE=" + merge.OrigCommitDate.Format(gitDateFormat),
	}
	var parents []string
	if parent != "" {
		parents = []string{parent}
	}
	args := commitTreeArgs(merge.TreeHash, parents, sign)
	return r.gitWithEnvStdin(env, subCommitMessage(title, body), args...)
}
