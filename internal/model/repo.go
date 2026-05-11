package model

// RepoState describes the repository regit was launched in: which branch is
// checked out, whether the working tree is clean, and how the branch relates to
// its upstream. The safety guards read this to decide whether a rewrite is
// allowed and how loudly to warn.
type RepoState struct {
	Root          string // absolute path to the repository root
	Branch        string // current branch name (empty if detached HEAD)
	HeadHash      string // current HEAD commit hash
	IsDirty       bool   // uncommitted changes in working tree or index
	HasUpstream   bool   // an upstream tracking branch is configured
	UpstreamRef   string // e.g. "origin/main" (empty if none)
	AheadOfRemote int    // commits on the local branch not on the upstream
	IsShallow     bool   // repository is a shallow clone (history is incomplete)
}

// CanRewriteSafely reports whether a rewrite can proceed without the loud,
// extra-confirmation path. A dirty tree blocks outright; pushed commits require
// explicit confirmation handled by the UI, so they are reported separately.
//
// Pushed-commit detection is NOT a RepoState method: whether a rewrite needs the
// typed confirmation depends on which commits it actually touches, not on
// whether the branch has an upstream at all. See model.AnyPushed, which every
// mutating surface calls over the commits it is about to rewrite.
func (r RepoState) CanRewriteSafely() bool {
	return !r.IsDirty
}
