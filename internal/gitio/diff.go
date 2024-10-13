package gitio

import (
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/model"
)

// Hunks parses diff(parentTree, commitTree) into stable-ID hunks. It uses
// `git diff --binary` so binary changes carry an applyable binary patch; those
// become a single whole-file Hunk with Binary=true (never sub-split).
func (r *Repo) Hunks(parentTree, commitTree string) ([]model.Hunk, error) {
	out, err := r.gitRaw("diff", "--binary", "--no-color", "--no-ext-diff", "-U3", parentTree, commitTree)
	if err != nil {
		return nil, err
	}
	return parseDiff(out), nil
}

// parseDiff turns a unified diff into hunks. Each file section starts with a
// "diff --git " line; within a section the header is everything before the first
// "@@ " line, and each "@@ " starts a new hunk body.
func parseDiff(diff string) []model.Hunk {
	var hunks []model.Hunk
	n := 0
	for _, sec := range splitSections(diff) {
		file, oldPath := parsePaths(sec)
		header, bodies, binary := splitHunkBodies(sec)
		if binary {
			n++
			hunks = append(hunks, model.Hunk{
				ID: fmt.Sprintf("H%d", n), File: file, OldPath: oldPath,
				FileHeader: header, Body: strings.Join(bodies, ""), Binary: true,
			})
			continue
		}
		for _, b := range bodies {
			n++
			add, del := countChanges(b)
			hunks = append(hunks, model.Hunk{
				ID: fmt.Sprintf("H%d", n), File: file, OldPath: oldPath,
				FileHeader: header, Body: b, Additions: add, Deletions: del,
			})
		}
	}
	return hunks
}

// splitSections breaks a diff into per-file sections, each beginning with the
// "diff --git " marker line (kept at the start of each returned section).
func splitSections(diff string) []string {
	const marker = "diff --git "
	lines := strings.SplitAfter(diff, "\n")
	var sections []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			sections = append(sections, cur.String())
			cur.Reset()
		}
	}
	for _, ln := range lines {
		if strings.HasPrefix(ln, marker) {
			flush()
		}
		cur.WriteString(ln)
	}
	flush()
	return sections
}

// splitHunkBodies returns the file-header block, the list of hunk bodies (each
// starting with "@@ "), and whether the section is a binary patch.
func splitHunkBodies(section string) (header string, bodies []string, binary bool) {
	lines := strings.SplitAfter(section, "\n")
	var head strings.Builder
	var cur strings.Builder
	inHunks := false
	for _, ln := range lines {
		if strings.HasPrefix(ln, "GIT binary patch") || strings.HasPrefix(ln, "Binary files ") {
			binary = true
		}
		if strings.HasPrefix(ln, "@@ ") {
			inHunks = true
			if cur.Len() > 0 {
				bodies = append(bodies, cur.String())
				cur.Reset()
			}
		}
		if inHunks {
			cur.WriteString(ln)
		} else {
			head.WriteString(ln)
		}
	}
	if cur.Len() > 0 {
		bodies = append(bodies, cur.String())
	}
	if binary {
		// For binary patches the whole post-header section is the "body".
		return head.String(), []string{strings.TrimPrefix(section, head.String())}, true
	}
	return head.String(), bodies, false
}

// parsePaths extracts the post-image path and (for renames) the pre-image path
// from a section's "diff --git a/<old> b/<new>" line.
func parsePaths(section string) (file, oldPath string) {
	for _, ln := range strings.Split(section, "\n") {
		if strings.HasPrefix(ln, "diff --git ") {
			fields := strings.Fields(strings.TrimPrefix(ln, "diff --git "))
			if len(fields) == 2 {
				a := strings.TrimPrefix(fields[0], "a/")
				b := strings.TrimPrefix(fields[1], "b/")
				if a != b {
					oldPath = a
				}
				return b, oldPath
			}
		}
	}
	return "", ""
}

// countChanges tallies added/removed lines in a hunk body (ignoring the @@ line).
func countChanges(body string) (add, del int) {
	for _, ln := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(ln, "@@"):
			continue
		case strings.HasPrefix(ln, "+"):
			add++
		case strings.HasPrefix(ln, "-"):
			del++
		}
	}
	return add, del
}
