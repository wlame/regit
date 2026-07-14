package model

import (
	"fmt"
	"path"
	"strings"
)

// PurgeSpec is the validated selection for a purge run: repo-relative file
// and/or directory paths to remove from history. Exact paths only — no globs
// in v1 (git pathspec magic characters are rejected so a literal path cannot
// silently become a pattern).
type PurgeSpec struct {
	Paths []string
}

// Validate rejects empty, absolute, escaping, glob-carrying, and duplicate
// paths. Paths are matched byte-exactly against history, so no cleaning is
// applied beyond validation — callers pass paths discovered by the scan.
func (s PurgeSpec) Validate() error {
	if len(s.Paths) == 0 {
		return fmt.Errorf("purge: no paths given")
	}
	seen := map[string]bool{}
	for _, p := range s.Paths {
		switch {
		case p == "":
			return fmt.Errorf("purge: empty path")
		case strings.HasPrefix(p, "/"):
			return fmt.Errorf("purge: %q is absolute; paths are repo-relative", p)
		case p != path.Clean(p) || strings.HasPrefix(p, ".."):
			return fmt.Errorf("purge: %q must be a clean repo-relative path", p)
		case strings.ContainsAny(p, "*?["):
			return fmt.Errorf("purge: %q contains glob characters; exact paths only", p)
		case seen[p]:
			return fmt.Errorf("purge: duplicate path %q", p)
		}
		seen[p] = true
	}
	return nil
}

// UnderAnyPath reports whether p equals one of the prefixes or lies inside a
// prefix directory. The "/" check keeps "dir/subzero" from matching "dir/sub".
func UnderAnyPath(p string, prefixes []string) bool {
	for _, pre := range prefixes {
		if p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}
