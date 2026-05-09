package gitio

import "strings"

// RefImpact names one ref (tag or branch) that points at a commit inside a
// rewrite set — i.e. a ref the rewrite would leave pointing at a now-orphaned
// commit. The pre-apply impact report lists these so the user sees the blast
// radius before confirming.
type RefImpact struct {
	Name   string // full ref name, e.g. "refs/tags/v1.0"
	Kind   string // "tag", "branch", or "remote"
	Commit string // the commit the ref resolves to (in the rewrite set)
}

// RefsPointingAt returns every tag/branch/remote ref whose target commit is in
// set. Annotated tags are resolved to their commit via %(*objectname); a
// lightweight tag or branch resolves via %(objectname). The caller passes the
// set of commit hashes a rewrite will replace (e.g. plan.Active() hashes).
func (r *Repo) RefsPointingAt(set map[string]bool) ([]RefImpact, error) {
	if len(set) == 0 {
		return nil, nil
	}
	// %(*objectname) is non-empty only for annotated tags (the commit the tag
	// object points at); for everything else use %(objectname).
	format := "%(refname)\x1f%(objecttype)\x1f%(objectname)\x1f%(*objectname)"
	out, err := r.gitRaw("for-each-ref", "--format="+format, "refs/tags", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	var impacts []RefImpact
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\x1f")
		if len(fields) < 4 {
			continue
		}
		name, objType, obj, peeled := fields[0], fields[1], fields[2], fields[3]
		commit := obj
		if objType == "tag" && peeled != "" {
			commit = peeled // annotated tag → the commit it wraps
		}
		if !set[commit] {
			continue
		}
		impacts = append(impacts, RefImpact{Name: name, Kind: refKind(name), Commit: commit})
	}
	return impacts, nil
}

// refKind classifies a full ref name for display.
func refKind(name string) string {
	switch {
	case strings.HasPrefix(name, "refs/tags/"):
		return "tag"
	case strings.HasPrefix(name, "refs/remotes/"):
		return "remote"
	default:
		return "branch"
	}
}
