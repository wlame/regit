package gitio

import (
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/model"
)

// diffFlags are the flags passed to `git diff` whenever we generate a diff.
// They are a single source of truth shared by Hunks() and the round-trip tests.
//
// --src-prefix=a/ / --dst-prefix=b/ pin the standard prefixes so a user's
// diff.noprefix=true config cannot strip them: the resulting patch is fed to
// `git apply --index` in BuildTreeFromHunks, which rejects a prefix-less header
// with "git diff header lacks filename information". Rename detection is left ON
// (the hunk parser models renames via OldPath, and a rename diff rebuilds the
// same tree) — unlike DiffChangeSet/CommitStat, whose parsers do not model R
// records and therefore pass --no-renames.
var diffFlags = []string{"--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/", "-U3", "--binary"}

// DiffText returns the raw unified diff between two trees with contextLines of
// surrounding context (-U<n>). Unlike Hunks (which uses -U3 for precise
// splitting), reword wants WIDE context so each hunk carries enclosing
// function/class/docstring names. contextLines <= 0 falls back to 3.
func (r *Repo) DiffText(parentTree, commitTree string, contextLines int) (string, error) {
	if contextLines <= 0 {
		contextLines = 3
	}
	return r.gitRaw("diff", "--no-color", "--no-ext-diff",
		fmt.Sprintf("-U%d", contextLines), parentTree, commitTree)
}

// Hunks parses diff(parentTree, commitTree) into stable-ID hunks. It uses
// `git diff --binary` so binary changes carry an applyable binary patch; those
// become a single whole-file Hunk with Binary=true (never sub-split).
func (r *Repo) Hunks(parentTree, commitTree string) ([]model.Hunk, error) {
	args := append([]string{"diff"}, diffFlags...)
	args = append(args, parentTree, commitTree)
	out, err := r.gitRaw(args...)
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
		if len(bodies) == 0 {
			// Mode-only sections (e.g. chmod +x) emit "old mode"/"new mode" header lines
			// but no @@ hunks. We still need a Hunk so the downstream "rebuilt tree ==
			// original tree" invariant holds: git apply reads the mode change from
			// FileHeader when Body is empty. Mark Binary=true so it is never sub-split.
			n++
			hunks = append(hunks, model.Hunk{
				ID: fmt.Sprintf("H%d", n), File: file, OldPath: oldPath,
				FileHeader: header, Body: "", Binary: true,
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
// from a diff section. Strategy (in priority order):
//
//  1. "rename from"/"rename to" lines — authoritative for renames.
//  2. "--- a/<old>" and "+++ b/<new>" lines — correct even for paths with spaces.
//  3. The "diff --git a/<old> b/<new>" line as a last resort (breaks on spaces).
//
// /dev/null in the --- line means an add (no old path); /dev/null in the +++
// line means a delete (file is gone in the new tree).
func parsePaths(section string) (file, oldPath string) {
	var diffGitA, diffGitB string // fallback from the diff --git line
	var renameFrom, renameTo string
	var minusPath, plusPath string

	for _, ln := range strings.Split(section, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			// Fallback only — does not handle spaces in filenames correctly.
			rest := strings.TrimPrefix(ln, "diff --git ")
			// The line is "a/<old> b/<new>" but paths may contain spaces, so we
			// cannot simply split on whitespace. We rely on the higher-priority
			// sources above whenever they are present.
			fields := strings.Fields(rest)
			if len(fields) == 2 {
				diffGitA = strings.TrimPrefix(fields[0], "a/")
				diffGitB = strings.TrimPrefix(fields[1], "b/")
			}

		case strings.HasPrefix(ln, "rename from "):
			renameFrom = strings.TrimPrefix(ln, "rename from ")

		case strings.HasPrefix(ln, "rename to "):
			renameTo = strings.TrimPrefix(ln, "rename to ")

		case strings.HasPrefix(ln, "--- "):
			// Git appends a trailing tab when the filename contains a space;
			// strip it so path comparisons work correctly.
			p := strings.TrimRight(strings.TrimPrefix(ln, "--- "), "\t")
			if p != "/dev/null" {
				minusPath = strings.TrimPrefix(p, "a/")
			}

		case strings.HasPrefix(ln, "+++ "):
			// Same trailing-tab convention as the --- line above.
			p := strings.TrimRight(strings.TrimPrefix(ln, "+++ "), "\t")
			if p != "/dev/null" {
				plusPath = strings.TrimPrefix(p, "b/")
			}
		}
	}

	// Renames: git emits explicit "rename from/to" lines.
	if renameFrom != "" || renameTo != "" {
		return renameTo, renameFrom
	}

	// Normal text diffs: prefer the --- / +++ lines (handle spaces correctly).
	if plusPath != "" {
		// minusPath is empty only for pure additions (--- /dev/null).
		if minusPath != "" && minusPath != plusPath {
			oldPath = minusPath
		}
		return plusPath, oldPath
	}
	if minusPath != "" {
		// Pure deletion (no +++ b/... line).
		return minusPath, ""
	}

	// Last resort: the diff --git line (breaks on spaces but beats returning "").
	if diffGitB != "" {
		if diffGitA != "" && diffGitA != diffGitB {
			oldPath = diffGitA
		}
		return diffGitB, oldPath
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
