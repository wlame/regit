// contributors.go builds the per-author color index that drives the colored
// usernames and the AUTHORS panel. It is deliberately small and (for the builder)
// pure: given the commit list it produces a stable, ranked, color-assigned set of
// contributors plus a lookup index. Rendering helpers live here too so every
// author-name site colors identically.
package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/model"
)

// contributor is one distinct author in the current history. Key is the identity
// used for dedupe/lookup; Label is what the UI prints; Count drives ranking; Color
// is assigned from contributorPalette by that rank.
type contributor struct {
	Key   string
	Label string
	Count int
	Color lipgloss.Color
}

// contributorPalette is the ordered set of foreground colors handed to
// contributors by rank (most-active first). They are 256-color codes chosen to be
// legible on the dark background and to avoid the hues already used by styles
// (cursor 213, marker/warn 214, picked/ok 84, hash 178, accent 63, greys, etc.),
// so a username can never be confused with a UI signal. When a repo has more
// contributors than colors the assignment wraps — distinct color per contributor
// is guaranteed only up to len(contributorPalette), which comfortably covers real
// teams.
var contributorPalette = []lipgloss.Color{
	lipgloss.Color("39"),  // azure
	lipgloss.Color("208"), // orange
	lipgloss.Color("141"), // violet
	lipgloss.Color("43"),  // teal
	lipgloss.Color("168"), // rose
	lipgloss.Color("179"), // gold
	lipgloss.Color("111"), // cornflower
	lipgloss.Color("149"), // spring green
	lipgloss.Color("173"), // salmon
	lipgloss.Color("117"), // sky
	lipgloss.Color("175"), // mauve
	lipgloss.Color("107"), // olive
	lipgloss.Color("216"), // peach
	lipgloss.Color("152"), // pale cyan
}

// contributorKey returns the identity key for a commit: the lowercased, trimmed
// author email (git's canonical identity, so "A. Lame" and "wlame" under one
// address are one person), falling back to the display label when the email is
// blank.
func contributorKey(c model.Commit) string {
	if e := strings.ToLower(strings.TrimSpace(c.AuthorEmail)); e != "" {
		return e
	}
	return authorLabel(c)
}

// buildContributors scans commits (in any order), dedupes authors by
// contributorKey, ranks them by commit count descending (ties broken by label
// ascending for determinism), assigns each a palette color by rank, and returns
// the ranked slice plus a key -> slice-index lookup map.
func buildContributors(commits []model.Commit) ([]contributor, map[string]int) {
	pos := make(map[string]int) // key -> index while accumulating counts
	var list []contributor
	for _, c := range commits {
		key := contributorKey(c)
		if i, ok := pos[key]; ok {
			list[i].Count++
			continue
		}
		pos[key] = len(list)
		list = append(list, contributor{
			Key: key, Label: authorLabel(c), Count: 1,
		})
	}

	// SliceStable keeps first-seen order among equal (count,label) pairs.
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return list[i].Label < list[j].Label
	})

	// Positions changed under the sort, so assign colors by final rank and
	// rebuild the lookup index from scratch.
	idx := make(map[string]int, len(list))
	for i := range list {
		list[i].Color = contributorPalette[i%len(contributorPalette)]
		idx[list[i].Key] = i
	}
	return list, idx
}

// rebuildContributors recomputes the cached contributor index from the current
// plan. Called on load and after every reload so a rewrite that changes
// authorship (reauthor) refreshes colors and counts.
func (m *Model) rebuildContributors() {
	m.contribs, m.contribIdx = buildContributors(m.plan.Commits)
}

// contributorFor returns the contributor for a commit, ok=false when the author
// is not in the index (empty history, or a commit built outside the cached plan).
// Callers fall back to the default style when ok is false.
func (m Model) contributorFor(c model.Commit) (contributor, bool) {
	if i, ok := m.contribIdx[contributorKey(c)]; ok && i < len(m.contribs) {
		return m.contribs[i], true
	}
	return contributor{}, false
}

// authorColorStyle returns the foreground style for a commit's author — the
// contributor's assigned color, or the default author style when the author is
// not in the index.
func (m Model) authorColorStyle(c model.Commit) lipgloss.Style {
	if ct, ok := m.contributorFor(c); ok {
		return lipgloss.NewStyle().Foreground(ct.Color)
	}
	return m.st.author
}
