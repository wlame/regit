package capability

import (
	"fmt"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// Short returns up to the first 8 characters of a hash string, guarding
// against hashes shorter than 8 characters (e.g. during tests or on empty input).
func Short(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// FindCommit resolves ref to a full commit hash and finds its index in the
// root-first commits slice. Returns an error if the ref cannot be resolved or
// is not on the linear first-parent history.
func FindCommit(repo *gitio.Repo, commits []model.Commit, ref string) (int, error) {
	hash, err := repo.ResolveRef(ref)
	if err != nil {
		return -1, fmt.Errorf("resolve %q: %w", ref, err)
	}
	for i, c := range commits {
		if c.Hash == hash {
			return i, nil
		}
	}
	return -1, fmt.Errorf("commit %s (%s) is not on the current first-parent history", ref, Short(hash))
}

// ParentTreeOf returns the tree hash of commit c's parent, or the empty-tree
// hash when c is the root commit (no parents).
func ParentTreeOf(repo *gitio.Repo, c model.Commit) (string, error) {
	if len(c.ParentHashes) == 0 {
		return repo.EmptyTree()
	}
	// "<parent>^{tree}" dereferences the parent commit object to its tree object.
	return repo.ResolveRef(c.ParentHashes[0] + "^{tree}")
}
