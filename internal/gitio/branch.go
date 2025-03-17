package gitio

import (
	"fmt"
	"sort"
	"strings"
)

// Branches returns the repository's local branch names, sorted. It feeds the
// branch/base pickers. Remote-tracking branches are intentionally excluded:
// regit only edits the checked-out branch, and switching to a remote ref would
// detach HEAD, breaking the safety model.
func (r *Repo) Branches() ([]string, error) {
	out, err := r.gitRaw("for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// MergeBase returns the best common ancestor commit of revisions a and b
// (`git merge-base a b`) — where the current branch diverged from its base, so
// the shared history older than it can be frozen. It errors when a and b have
// no common ancestor (unrelated histories).
func (r *Repo) MergeBase(a, b string) (string, error) {
	out, err := r.git("merge-base", a, b)
	if err != nil {
		return "", fmt.Errorf("merge-base %s %s: %w", a, b, err)
	}
	return out, nil // r.git already trims the trailing newline
}

// SwitchBranch checks out an existing local branch (`git switch <name>`). The
// caller must ensure the working tree is clean first; git itself also refuses
// the switch if uncommitted changes would be overwritten.
func (r *Repo) SwitchBranch(name string) error {
	if _, err := r.git("switch", name); err != nil {
		return fmt.Errorf("switch to %s: %w", name, err)
	}
	return nil
}
