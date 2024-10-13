package model

// RepoState describes the repository ghist was launched in: which branch is
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
}

// CanRewriteSafely reports whether a rewrite can proceed without the loud,
// extra-confirmation path. A dirty tree blocks outright; pushed commits require
// explicit confirmation handled by the UI, so they are reported separately.
func (r RepoState) CanRewriteSafely() bool {
	return !r.IsDirty
}

// HasPushedCommits reports whether the branch has commits that already exist on
// the upstream and would therefore be rewritten — the case that demands a typed
// confirmation before applying.
func (r RepoState) HasPushedCommits() bool {
	return r.HasUpstream && r.AheadOfRemote >= 0 && r.UpstreamRef != ""
}
