package model

import "strings"

// PushHintLines renders the post-rewrite "how to publish" guidance: a status
// note plus the exact push command. When upstreamIsAncestor is true the
// rewrite touched only unpushed commits, so a plain push fast-forwards and the
// hint says so; otherwise the upstream has diverged and the hint gives
// --force-with-lease. The caller computes the ancestry (model runs no git). The remote is derived from the
// upstream ref (e.g. "up/main" -> remote "up"), so a non-origin remote gets the
// correct command -- not a hardcoded "origin". Returns nil when the branch has no
// upstream (nothing to push to). Both the headless pipeline (capability.pushHint)
// and the TUI footer (ui.pushHintFooterLines) render these identical lines, so
// the wording cannot drift between surfaces.
func PushHintLines(state RepoState, upstreamIsAncestor bool) []string {
	if !state.HasUpstream || state.Branch == "" || state.UpstreamRef == "" {
		return nil
	}
	remote, _, _ := strings.Cut(state.UpstreamRef, "/") // "up/main" -> "up"
	if remote == "" {
		remote = "origin"
	}
	if upstreamIsAncestor {
		return []string{
			state.UpstreamRef + " is still an ancestor of your rewrite. To publish:",
			"git push " + remote + " " + state.Branch,
		}
	}
	return []string{
		state.UpstreamRef + " has diverged from your rewrite. To publish:",
		"git push --force-with-lease " + remote + " " + state.Branch,
	}
}
