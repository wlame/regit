package model

// RepoState describes the repository regit was launched in: which branch is
// checked out, whether the working tree is clean, and how the branch relates to
// its upstream. The safety guards read this to decide whether a rewrite is
// allowed and how loudly to warn.
type RepoState struct {
	Root     string // absolute path to the repository root
	Branch   string // current branch name (empty if detached HEAD)
	HeadHash string // current HEAD commit hash
	// WorktreeStatus is embedded: its fields and methods are used directly on a
	// RepoState (state.HasTrackedChanges, state.BlockReason(need)).
	WorktreeStatus
	HasUpstream   bool   // an upstream tracking branch is configured
	UpstreamRef   string // e.g. "origin/main" (empty if none)
	AheadOfRemote int    // commits on the local branch not on the upstream
	IsShallow     bool   // repository is a shallow clone (history is incomplete)
}

// WorktreeStatus says what in the working tree is not committed. Pushed-commit
// detection is deliberately not here: whether a rewrite needs the typed
// confirmation depends on which commits it touches (see AnyPushed).
type WorktreeStatus struct {
	HasTrackedChanges bool // staged or unstaged changes to files git tracks
	HasUntrackedFiles bool // files git does not track (ignored files do not count)
}

// IsDirty reports whether anything at all is uncommitted, untracked files
// included.
func (w WorktreeStatus) IsDirty() bool {
	return w.HasTrackedChanges || w.HasUntrackedFiles
}

// WorktreeNeed is how clean the working tree must be before an operation runs.
type WorktreeNeed int

const (
	// NeedsNoTrackedChanges is for a rewrite whose new commits carry the same
	// files as the old ones (redate, reword, retitle, reauthor, squash, split,
	// linearize). It only moves the branch pointer and never touches the index
	// or the files on disk, so an untracked file cannot be harmed.
	NeedsNoTrackedChanges WorktreeNeed = iota
	// NeedsCleanWorktree is for a rewrite that changes file content and then
	// refreshes the checkout to match (purge, redact). Nothing on disk may be
	// outside history, untracked files included.
	NeedsCleanWorktree
)

// BlockReason returns why the working tree stops an operation with the given
// need, or "" when it does not. Every guard, headless or TUI, reads its
// dirty-tree verdict and wording from here.
func (w WorktreeStatus) BlockReason(need WorktreeNeed) string {
	switch {
	case w.HasTrackedChanges:
		return "working tree has uncommitted changes to tracked files; commit or stash them first"
	case w.HasUntrackedFiles && need == NeedsCleanWorktree:
		return "working tree has untracked files and this operation rewrites the checkout; " +
			"remove them, add them to .gitignore, or run 'git stash -u' first"
	default:
		return ""
	}
}
