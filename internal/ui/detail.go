package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// detailDateLayout is the full author/committer-date format used in the detail
// panel: ISO date, 24h time, and the numeric timezone offset.
const detailDateLayout = "2006-01-02 15:04 -0700"

// renderDetail builds the commit-detail panel body for the cursor commit. It is
// a pure function (no git, no Model) so it unit-tests without a terminal; the
// lazy stat fetch and pane framing live in app.go / viewDetailPane.
//
// haveStat reports whether st is populated; statErr (when non-empty) is shown as
// a non-fatal note while the dates and message still render. width is the inner
// text width (the pane border/padding is added by the caller).
func renderDetail(c model.Commit, st gitio.Stat, haveStat bool, statErr string, plan *model.Plan, loc *time.Location, width int, sty styles) string {
	var b strings.Builder

	b.WriteString(sty.modalTitle.Render("DETAIL "+short(c.Hash)) + "\n")
	b.WriteString(sty.dim.Render(c.Hash) + "\n\n")

	b.WriteString(fmt.Sprintf("%s %s\n", c.AuthorName, sty.dim.Render("<"+c.AuthorEmail+">")))
	if c.CommitterName != c.AuthorName || c.CommitterEmail != c.AuthorEmail {
		b.WriteString(sty.dim.Render(fmt.Sprintf("committer %s <%s>\n", c.CommitterName, c.CommitterEmail)))
	}

	authored := c.OrigAuthorDate.In(loc).Format(detailDateLayout)
	b.WriteString(sty.dim.Render("authored ") + authored + "\n")
	// When a pending edit moved the date, show the new value in green.
	if eff := plan.EffectiveAuthorDate(c); !eff.Equal(c.OrigAuthorDate) {
		b.WriteString(sty.dim.Render("       → ") + sty.newDate.Render(eff.In(loc).Format(detailDateLayout)) + "\n")
	}
	b.WriteString(sty.dim.Render("committed ") + c.OrigCommitDate.In(loc).Format(detailDateLayout) + "\n\n")

	b.WriteString(renderDiffstat(st, haveStat, statErr, sty) + "\n")
	if haveStat {
		for _, f := range st.Files {
			b.WriteString(renderFileLine(f, sty) + "\n")
		}
	}
	b.WriteString("\n")

	// Effective subject (may be retitled/reworded), then the wrapped body.
	b.WriteString(lipgloss.NewStyle().Bold(true).Width(width).Render(plan.EffectiveSubject(c)) + "\n")
	if c.Body != "" {
		b.WriteString("\n" + sty.dim.Render(lipgloss.NewStyle().Width(width).Render(c.Body)))
	}

	return b.String()
}

// renderDiffstat renders the "N files · +X −Y" header, or a loading/unavailable
// note when the stat is not ready.
func renderDiffstat(st gitio.Stat, haveStat bool, statErr string, sty styles) string {
	if statErr != "" {
		return sty.statusWarn.Render("stat unavailable: " + statErr)
	}
	if !haveStat {
		return sty.dim.Render("stat loading…")
	}
	plus := sty.newDate.Render(fmt.Sprintf("+%d", st.Additions))
	minus := sty.statusErr.Render(fmt.Sprintf("−%d", st.Deletions))
	// Separator is the U+00B7 middle dot per spec §6.3 ("N files · +X −Y").
	return fmt.Sprintf("%d files · %s %s", len(st.Files), plus, minus)
}

// renderFileLine renders one "<status> <path> +x −y" file row, with the counts
// colored and binary files marked "Bin".
func renderFileLine(f gitio.FileStat, sty styles) string {
	counts := sty.dim.Render("Bin")
	if !f.Binary {
		counts = sty.newDate.Render(fmt.Sprintf("+%d", f.Add)) + " " + sty.statusErr.Render(fmt.Sprintf("−%d", f.Del))
	}
	return fmt.Sprintf(" %s %s  %s", sty.hash.Render(f.Status), f.Path, counts)
}
