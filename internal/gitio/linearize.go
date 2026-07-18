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
