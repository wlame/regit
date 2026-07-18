package model

import (
	"fmt"
	"regexp"
	"strings"
)

// SideCommit is the light view of a commit reachable only through a merge's
// non-first parents — everything the fold message needs, nothing more. gitio
// fills it from `git log <merge> ^<merge>^1`; tests fill it literally.
type SideCommit struct {
	Hash        string
	Subject     string
	AuthorName  string
	AuthorEmail string
}

// Identity names a distinct side author credited via a Co-authored-by trailer.
type Identity struct {
	Name  string
	Email string
}

// LinearizeFold is one merge commit scheduled to become an ordinary commit:
// it keeps the merge's tree, identities, and dates, and carries the synthesized
// message. Side is oldest-first.
type LinearizeFold struct {
	Merge     Commit
	Side      []SideCommit
	NewTitle  string
	NewBody   string
	CoAuthors []Identity
}

// LinearizePlan is the full set of folds applied in one rewrite, in history
// (root-first) order — the same order as the commits slice they came from.
type LinearizePlan struct {
	Folds []LinearizeFold
}

// mergeSubjectPatterns extract the branch name from the standard merge-subject
// shapes, held as data (design-principles: rules as a table, code as the
// mechanism). First match wins; the capture group is the branch name.
var mergeSubjectPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^Merge branch '([^']+)'`),
	regexp.MustCompile(`^Merge pull request #\d+ from [^/\s]+/(\S+)`),
	regexp.MustCompile(`^Merge remote-tracking branch 'origin/([^']+)'`),
}

// humanizePrefixes are branch-name prefixes stripped for display, as data.
var humanizePrefixes = []string{"feature/", "fix/", "bugfix/", "chore/"}

// branchNameFromMergeSubject extracts and humanizes the merged branch's name,
// or "" when the subject matches no known pattern.
func branchNameFromMergeSubject(subject string) string {
	for _, p := range mergeSubjectPatterns {
		if m := p.FindStringSubmatch(subject); m != nil {
			name := m[1]
			for _, pre := range humanizePrefixes {
				if strings.HasPrefix(name, pre) {
					name = strings.TrimPrefix(name, pre)
					break
				}
			}
			return name
		}
	}
	return ""
}

// synthesizeFoldTitle applies the title precedence from the spec:
// one folded commit → its subject; branch name found → "<branch>: <oldest
// subject>"; otherwise the merge's own subject unchanged.
func synthesizeFoldTitle(mergeSubject string, side []SideCommit) string {
	if len(side) == 1 {
		return side[0].Subject
	}
	if name := branchNameFromMergeSubject(mergeSubject); name != "" && len(side) > 0 {
		return fmt.Sprintf("%s: %s", name, side[0].Subject)
	}
	return mergeSubject
}

// foldCoAuthors returns the distinct side authors (deduped by email, oldest
// first), excluding the merge commit's own author.
func foldCoAuthors(merge Commit, side []SideCommit) []Identity {
	seen := map[string]bool{merge.AuthorEmail: true}
	var out []Identity
	for _, s := range side {
		if s.AuthorEmail == "" || seen[s.AuthorEmail] {
			continue
		}
		seen[s.AuthorEmail] = true
		out = append(out, Identity{Name: s.AuthorName, Email: s.AuthorEmail})
	}
	return out
}

// synthesizeFoldBody renders the folded commits' subjects as bullets (oldest
// first) followed by Co-authored-by trailers, separated by a blank line. An
// empty side yields an empty body.
func synthesizeFoldBody(side []SideCommit, coAuthors []Identity) string {
	var b strings.Builder
	for _, s := range side {
		b.WriteString("- " + s.Subject + "\n")
	}
	if len(coAuthors) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		for _, a := range coAuthors {
			b.WriteString(fmt.Sprintf("Co-authored-by: %s <%s>\n", a.Name, a.Email))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// BuildLinearizePlan walks the first-parent commits slice (root-first) and
// produces one fold per merge commit, with the synthesized message filled in.
// sideOf maps a merge hash to its side commits (oldest first); a missing entry
// means an empty side (all side work TREESAME/cherry-picked away).
func BuildLinearizePlan(commits []Commit, sideOf map[string][]SideCommit) LinearizePlan {
	var plan LinearizePlan
	for _, c := range commits {
		if !c.IsMerge {
			continue
		}
		side := sideOf[c.Hash]
		coAuthors := foldCoAuthors(c, side)
		plan.Folds = append(plan.Folds, LinearizeFold{
			Merge:     c,
			Side:      side,
			NewTitle:  synthesizeFoldTitle(c.Subject, side),
			NewBody:   synthesizeFoldBody(side, coAuthors),
			CoAuthors: coAuthors,
		})
	}
	return plan
}

// RewrittenCommits narrows the full history to the suffix the apply actually
// re-emits: everything from the earliest fold's merge onward. Commits before it
// keep their original hashes. Mirrors SquashPlan.RewrittenCommits so the pushed
// guard cannot over-flag untouched early history.
func (p LinearizePlan) RewrittenCommits(commits []Commit) []Commit {
	if len(p.Folds) == 0 {
		return nil
	}
	pos := make(map[string]int, len(commits))
	for i, c := range commits {
		pos[c.Hash] = i
	}
	first := len(commits)
	for _, f := range p.Folds {
		if i, ok := pos[f.Merge.Hash]; ok && i < first {
			first = i
		}
	}
	if first >= len(commits) {
		return nil
	}
	return commits[first:]
}
