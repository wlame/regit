// contributors.go builds the per-author color index that drives the colored
// usernames and the AUTHORS panel. It is deliberately small and (for the builder)
// pure: given the commit list it produces a stable, ranked, color-assigned set of
// contributors plus a lookup index. Rendering helpers live here too so every
// author-name site colors identically.
package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/termsafe"
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
	lipgloss.Color("221"), // light gold
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
			// Sanitize at CONSTRUCTION, not at render: Label reaches the AUTHORS
			// pane through lipgloss.Render, which wraps its input in style codes
			// without inspecting it. The commit list's author column already
			// sanitizes the same authorLabel(c) value (list.go); doing it here
			// closes the second path rather than leaving two policies for one
			// value.
			Key: key, Label: termsafe.Line(authorLabel(c)), Count: 1,
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

// hashStyleFor returns the style for a commit's short hash in the list: the
// contributor's color when the commit shares the cursor commit's author (the
// same-author highlight), otherwise the default hash style.
func (m Model) hashStyleFor(c model.Commit) lipgloss.Style {
	if len(m.plan.Commits) > 0 && contributorKey(c) == contributorKey(m.commitAtCursor()) {
		if ct, ok := m.contributorFor(c); ok {
			return lipgloss.NewStyle().Foreground(ct.Color)
		}
	}
	return m.st.hash
}

// minAuthorsPaneHeight is the smallest row budget (including the pane border) at
// which the AUTHORS panel actually renders something: border(2) + the title row
// + one author (or "+N more") row. A budget of border(2)+title(1) alone still
// has nowhere to put a contributor, so the panel would render only a bare
// title — not useful — hence the extra row. Below this the panel yields its
// rows to the flow zone's widget.
const minAuthorsPaneHeight = 4

// visibleContributors picks which contributors the panel shows for a row budget
// of maxLines author lines, always including the lit (cursor) contributor. When
// everything fits it returns them all with hidden=0. When it overflows it returns
// the first maxLines-1 (reserving the last line for a "+N more" summary) with
// hidden = the count not shown; if the lit contributor would be hidden it is
// swapped into the last returned slot so it is always on screen.
func visibleContributors(contribs []contributor, litIdx, maxLines int) ([]contributor, int) {
	if maxLines <= 0 {
		return nil, len(contribs)
	}
	if len(contribs) <= maxLines {
		return contribs, 0
	}
	show := maxLines - 1 // reserve one line for the "+N more" summary
	vis := make([]contributor, show)
	copy(vis, contribs[:show])
	hidden := len(contribs) - show
	// Guarantee the lit author is visible — but only when there is a slot to
	// swap it into. With maxLines==1, show==0 leaves no contributor line at all
	// (the single line is the "+N more" summary), so the swap must be skipped to
	// avoid indexing vis[-1].
	if show > 0 && litIdx >= show && litIdx < len(contribs) {
		vis[show-1] = contribs[litIdx]
	}
	return vis, hidden
}

// viewAuthorsPane renders the AUTHORS panel for the given row budget (including
// the pane border). Returns "" when the budget is too small or there are no
// contributors. The cursor commit's author is highlighted (bold, ● glyph); every
// other contributor gets a dim · glyph. Contributor labels use their assigned
// color; counts are dimmed.
func (m Model) viewAuthorsPane(budget int) string {
	if budget < minAuthorsPaneHeight || len(m.contribs) == 0 {
		return ""
	}
	maxLines := budget - 2 /* border */ - 1 /* title */
	if maxLines < 1 {
		return ""
	}

	litIdx := -1
	if len(m.plan.Commits) > 0 {
		if i, ok := m.contribIdx[contributorKey(m.commitAtCursor())]; ok {
			litIdx = i
		}
	}
	vis, hidden := visibleContributors(m.contribs, litIdx, maxLines)

	lines := []string{m.st.modalTitle.Render("AUTHORS")}
	for _, ct := range vis {
		glyph := "·"
		labelStyle := lipgloss.NewStyle().Foreground(ct.Color)
		if litIdx >= 0 && ct.Key == m.contribs[litIdx].Key {
			glyph = "●"
			labelStyle = labelStyle.Bold(true)
		}
		lines = append(lines, fmt.Sprintf("%s %s %s",
			glyph, labelStyle.Render(ct.Label), m.st.dim.Render(fmt.Sprintf("%d", ct.Count))))
	}
	if hidden > 0 {
		lines = append(lines, m.st.dim.Render(fmt.Sprintf("… +%d more", hidden)))
	}
	// Match the calendar/strategy panes' width policy (ws_dates.go): natural
	// width (rightPaneWidth) when the flow zone is wide enough, else clamped
	// down to whatever computeLayout budgeted it — otherwise a narrow flow
	// column (e.g. ~100-140 columns, where the calendar has already clamped)
	// would still render this pane at the full rightPaneWidth and truncate its
	// right border.
	width := min(rightPaneWidth, computeLayout(m.width, m.height).flowWidth)
	return m.st.pane.Width(width).Render(strings.Join(lines, "\n"))
}
