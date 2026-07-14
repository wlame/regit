package gitio

import (
	"fmt"
	"os"
)

// emptyTreeHash is git's well-known hash of the empty tree (SHA-1 repos). A
// root commit whose every path is purged ends up with exactly this tree.
const emptyTreeHash = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// RemovePathsFromTree returns the hash of tree with the given repo-relative
// paths (files or whole directories) removed. It stages the tree into a
// private temporary index and edits THAT, so the user's real index and
// working tree are never touched:
//
//	GIT_INDEX_FILE=<tmp> git read-tree <tree>
//	GIT_INDEX_FILE=<tmp> git rm -q -f --cached -r --ignore-unmatch -- <paths...>
//	GIT_INDEX_FILE=<tmp> git write-tree
//
// --ignore-unmatch makes a commit that never contained a path a no-op (the
// returned hash equals the input tree), which the purge walk relies on.
// -f (force) is required because `git rm --cached` otherwise refuses whenever
// the entry it is about to drop disagrees with the file actually checked out
// in the real working tree — which it always will here, since tree is almost
// always some HISTORICAL commit's tree, not whatever the caller has checked
// out. GIT_INDEX_FILE redirects which index is edited, but git rm's built-in
// safety check still peeks at the real worktree file regardless, so it must
// be silenced explicitly. This is safe: the real index and working tree are
// never touched (see above), so there is nothing for -f to lose.
// Directory contents are handled entirely inside git — nothing is enumerated
// back into regit, so huge directories cost no memory here.
func (r *Repo) RemovePathsFromTree(tree string, paths []string) (string, error) {
	if len(paths) == 0 {
		// A bare `git rm` with no pathspec hard-fails; nothing to remove is a
		// legitimate no-op, so return the tree untouched instead of erroring.
		return tree, nil
	}
	idx, err := os.CreateTemp("", "regit-purge-index-*")
	if err != nil {
		return "", fmt.Errorf("purge index: %w", err)
	}
	idxPath := idx.Name()
	idx.Close()
	defer os.Remove(idxPath)
	// read-tree wants to create the index file itself; hand it a free path.
	os.Remove(idxPath)

	env := []string{"GIT_INDEX_FILE=" + idxPath}
	if _, err := r.gitWithEnvStdin(env, "", "read-tree", tree); err != nil {
		return "", fmt.Errorf("read-tree %s: %w", short(tree), err)
	}
	args := append([]string{"rm", "-q", "-f", "--cached", "-r", "--ignore-unmatch", "--"}, paths...)
	if _, err := r.gitWithEnvStdin(env, "", args...); err != nil {
		return "", fmt.Errorf("remove paths from %s: %w", short(tree), err)
	}
	newTree, err := r.gitWithEnvStdin(env, "", "write-tree")
	if err != nil {
		return "", fmt.Errorf("write-tree: %w", err)
	}
	return newTree, nil
}
