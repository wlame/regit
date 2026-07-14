package gitio

import (
	"path"
	"sort"
	"strings"
)

// PathIndex is the one-pass history index behind the purge finder: every path
// ever added on the branch (with the commit that introduced it), rename edges
// for chain suggestions, and the derived set of ancestor directories. Built
// once per session by ScanHistoryPaths; read-only afterwards.
type PathIndex struct {
	AddedIn     map[string]string // path -> hash of the commit that first added it
	renamedTo   map[string]string // old path -> new path (R edges, newest wins)
	renamedFrom map[string]string // new path -> old path
	Dirs        []string          // sorted ancestor directories of all added paths
}

// ScanHistoryPaths walks the current branch once and indexes added paths and
// renames. -M enables rename detection; --reverse makes the FIRST add win when
// a path was added, deleted, and re-added.
func (r *Repo) ScanHistoryPaths() (*PathIndex, error) {
	out, err := r.gitRaw("log", "--format=%x00%H", "--name-status",
		"--diff-filter=AR", "-M", "--reverse", "HEAD")
	if err != nil {
		return nil, err
	}
	return ParsePathScan(out), nil
}

// ParsePathScan parses the record-separated log output produced by
// ScanHistoryPaths. It is pure and exported so tests (including cross-package
// fixture-driven construction of a PathIndex) and a fuzz target can drive it
// without a repository.
func ParsePathScan(out string) *PathIndex {
	idx := &PathIndex{
		AddedIn:     map[string]string{},
		renamedTo:   map[string]string{},
		renamedFrom: map[string]string{},
	}
	dirs := map[string]bool{}
	var hash string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "\x00") {
			hash = strings.TrimPrefix(line, "\x00")
			continue
		}
		fields := strings.Split(line, "\t")
		switch {
		case len(fields) == 2 && fields[0] == "A":
			p := fields[1]
			if _, seen := idx.AddedIn[p]; !seen {
				idx.AddedIn[p] = hash
			}
			addAncestorDirs(dirs, p)
		case len(fields) == 3 && strings.HasPrefix(fields[0], "R"):
			old, neu := fields[1], fields[2]
			idx.renamedTo[old] = neu
			idx.renamedFrom[neu] = old
			addAncestorDirs(dirs, neu)
		}
	}
	for d := range dirs {
		idx.Dirs = append(idx.Dirs, d)
	}
	sort.Strings(idx.Dirs)
	return idx
}

// addAncestorDirs records every ancestor directory of p ("a/b/c.go" -> "a", "a/b").
func addAncestorDirs(dirs map[string]bool, p string) {
	for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
		dirs[d] = true
	}
}

// RenameChain returns p plus every transitive rename ancestor and descendant,
// oldest name first. A path with no rename edges returns just itself.
func (idx *PathIndex) RenameChain(p string) []string {
	oldest := p
	for {
		prev, ok := idx.renamedFrom[oldest]
		if !ok {
			break
		}
		oldest = prev
	}
	chain := []string{oldest}
	for cur := oldest; ; {
		next, ok := idx.renamedTo[cur]
		if !ok {
			break
		}
		chain = append(chain, next)
		cur = next
	}
	return chain
}

// Match returns paths whose basename or full path contains query
// (case-insensitive), expanded to full rename chains, files first, then
// matching directory rows. Sorted for stable UI/CLI output.
func (idx *PathIndex) Match(query string) []string {
	q := strings.ToLower(query)
	hit := func(p string) bool { return strings.Contains(strings.ToLower(p), q) }
	files := map[string]bool{}
	for p := range idx.AddedIn {
		if hit(p) {
			for _, m := range idx.RenameChain(p) {
				files[m] = true
			}
		}
	}
	// Rename targets that were never plain "A" adds still deserve matching.
	for _, neu := range idx.renamedTo {
		if hit(neu) {
			for _, m := range idx.RenameChain(neu) {
				files[m] = true
			}
		}
	}
	var out []string
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	for _, d := range idx.Dirs {
		if hit(d) {
			out = append(out, d)
		}
	}
	return out
}
