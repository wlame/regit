package gitio

import (
	"fmt"
	"sort"
	"strings"
)

// BackupRefPrefix namespaces every backup ref ghist creates so they are easy to
// list, restore from, and prune without colliding with user refs.
const BackupRefPrefix = "refs/ghist/backup/"

// BackupRef is one saved snapshot of a branch tip created before a rewrite.
type BackupRef struct {
	Name string // full ref name, e.g. refs/ghist/backup/1717689600
	Hash string // commit the ref points at
}

// ResolveRef returns the commit hash a ref or revision points to.
func (r *Repo) ResolveRef(rev string) (string, error) {
	return r.git("rev-parse", "--verify", rev)
}

// UpdateRef points a ref at newValue. When oldValue is non-empty git performs a
// compare-and-swap, refusing the update if the ref no longer matches oldValue —
// a guard against clobbering concurrent changes.
func (r *Repo) UpdateRef(ref, newValue, oldValue string) error {
	args := []string{"update-ref", ref, newValue}
	if oldValue != "" {
		args = append(args, oldValue)
	}
	_, err := r.git(args...)
	return err
}

// DeleteRef removes a ref (used when pruning backups).
func (r *Repo) DeleteRef(ref string) error {
	_, err := r.git("update-ref", "-d", ref)
	return err
}

// ListBackupRefs returns every ghist backup ref, newest first (refs are named
// with a unix timestamp suffix, so lexical descending order is chronological).
func (r *Repo) ListBackupRefs() ([]BackupRef, error) {
	out, err := r.gitRaw("for-each-ref", "--format=%(refname) %(objectname)", BackupRefPrefix)
	if err != nil {
		return nil, err
	}
	var refs []BackupRef
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		refs = append(refs, BackupRef{Name: parts[0], Hash: parts[1]})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name > refs[j].Name })
	return refs, nil
}

// CurrentBranch returns the checked-out branch name, or an error if HEAD is
// detached (rewrites require a branch to move).
func (r *Repo) CurrentBranch() (string, error) {
	name, err := r.git("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("HEAD is detached; check out a branch before rewriting")
	}
	return name, nil
}

// BranchRef converts a branch name to its full ref path.
func BranchRef(branch string) string {
	return "refs/heads/" + branch
}
