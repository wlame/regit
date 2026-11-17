package gitio

import "fmt"

// SyncIndexToTree refreshes the repository's index from rev's tree, leaving the
// working tree on disk untouched and moving no ref.
//
// `git reset --mixed` would do the index half of this, but it ALSO moves the
// current branch — and by the time this runs the branch has already been moved
// by the apply's compare-and-swap, so resetting would be wrong. `read-tree`
// touches only the index, which is exactly the needed scope.
//
// This is purge's policy: nothing stays staged (a purged path staged for
// re-addition is how a "removed" file returns to history on the next commit),
// while the file itself remains on disk as an untracked copy for anyone who
// purged a large artifact they still want locally.
func (r *Repo) SyncIndexToTree(rev string) error {
	if _, err := r.git("read-tree", "--reset", rev); err != nil {
		return fmt.Errorf("sync index to %s: %w", short(rev), err)
	}
	return nil
}

// SyncIndexAndWorktreeToTree refreshes BOTH the index and the working tree from
// rev's tree, moving no ref (see SyncIndexToTree for why `git reset` is unfit).
//
// This is redact's policy: a redaction that leaves the secret sitting in the
// checkout has not redacted anything the operator cares about. The -u flag makes
// read-tree write the files out.
//
// Destroying uncommitted work is impossible here: every apply that refreshes
// the checkout is gated on model.NeedsCleanWorktree, which counts untracked
// files as well as modifications (Repo.WorktreeStatus). A clean gate proves
// there is nothing on disk that is not already in history.
func (r *Repo) SyncIndexAndWorktreeToTree(rev string) error {
	if _, err := r.git("read-tree", "--reset", "-u", rev); err != nil {
		return fmt.Errorf("sync index and working tree to %s: %w", short(rev), err)
	}
	return nil
}
