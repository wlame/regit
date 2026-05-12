package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
	"github.com/wlame/regit/internal/termsafe"
)

// detailDateLayout is the full author/committer-date format used in the detail
// panel: ISO date, 24h time, and the numeric timezone offset.
const detailDateLayout = "2006-01-02 15:04 -0700"

// renderDetail builds the commit-detail panel body for a commit, bounded to
// maxHeight rows so it can never grow taller than its pane (lipgloss .Height is
// only a minimum, so an unbounded body would overflow the frame). The header and
// the commit message are kept; the changed-files list absorbs the rows left
// between them and shows a "… +N more files" summary when it does not fit. A
// final clampLines guarantees the height invariant even for a very long body.
//
// It is pure (no git, no Model) so it unit-tests without a terminal. width is the
// inner text width; statErr (when non-empty) shows as a non-fatal note.
func renderDetail(c model.Commit, st gitio.Stat, haveStat bool, statErr string, plan *model.Plan, loc *time.Location, width, maxHeight int, sty styles) string {
	// Header block: identity, dates, diffstat (always shown).
	var head []string
	head = appendLines(head, sty.modalTitle.Render("DETAIL "+short(c.Hash)))
	head = appendLines(head, sty.dim.Render(c.Hash))
	head = appendLines(head, "")
	head = appendLines(head, fmt.Sprintf("%s %s", termsafe.Line(c.AuthorName), sty.dim.Render("<"+termsafe.Line(c.AuthorEmail)+">")))
	if c.CommitterName != c.AuthorName || c.CommitterEmail != c.AuthorEmail {
		head = appendLines(head, sty.dim.Render(fmt.Sprintf("committer %s <%s>", termsafe.Line(c.CommitterName), termsafe.Line(c.CommitterEmail))))
	}
	head = appendLines(head, sty.dim.Render("authored ")+c.OrigAuthorDate.In(loc).Format(detailDateLayout))
	if eff := plan.EffectiveAuthorDate(c); !eff.Equal(c.OrigAuthorDate) {
		head = appendLines(head, sty.dim.Render("       → ")+sty.newDate.Render(eff.In(loc).Format(detailDateLayout)))
	}
	head = appendLines(head, sty.dim.Render("committed ")+c.OrigCommitDate.In(loc).Format(detailDateLayout))
	head = appendLines(head, "")
	head = appendLines(head, renderDiffstat(st, haveStat, statErr, sty))

	// Message block: subject + wrapped body, built in full so it stays visible.
	var msg []string
	msg = appendLines(msg, "")
	msg = appendLines(msg, lipgloss.NewStyle().Bold(true).Width(width).Render(termsafe.Line(plan.EffectiveSubject(c))))
	if c.Body != "" {
		msg = appendLines(msg, "")
		msg = appendLines(msg, sty.dim.Width(width).Render(termsafe.Block(c.Body)))
	}

	// The file list gets the rows left between the header (plus one spacing row)
	// and the message.
	var files []string
	if haveStat {
		budget := maxHeight - len(head) - 1 - len(msg)
		files = renderFileLines(st.Files, budget, sty)
	}

	all := head
	all = append(all, "")
	all = append(all, files...)
	all = append(all, msg...)
	return clampLines(all, maxHeight)
}

// appendLines splits s on newlines and appends each visual line, so later height
// budgeting and clamping count real screen rows (a width-wrapped body is one
// string holding several lines).
func appendLines(dst []string, s string) []string {
	return append(dst, strings.Split(s, "\n")...)
}

// renderFileLines renders up to budget file rows. When the files do not fit it
// shows the first (budget-1) and a dim "… +N more files" summary so the message
// below stays visible. A budget < 1 (a very long body) yields no file rows — the
// diffstat header still reports the total count.
func renderFileLines(filesStat []gitio.FileStat, budget int, sty styles) []string {
	if budget < 1 || len(filesStat) == 0 {
		return nil
	}
	if len(filesStat) <= budget {
		out := make([]string, 0, len(filesStat))
		for _, f := range filesStat {
			out = append(out, renderFileLine(f, sty))
		}
		return out
	}
	// When budget==1, show==0: the single available row is spent on the overflow summary.
	show := budget - 1
	out := make([]string, 0, budget)
	for _, f := range filesStat[:show] {
		out = append(out, renderFileLine(f, sty))
	}
	return append(out, sty.dim.Render(fmt.Sprintf("… +%d more files", len(filesStat)-show)))
}

// clampLines joins lines into at most max rows. When content is cut, the last
// kept row becomes a "…" so the panel signals more content without ever growing
// past max. A max < 1 yields the empty string.
func clampLines(lines []string, max int) string {
	if max < 1 {
		return ""
	}
	if len(lines) <= max {
		return strings.Join(lines, "\n")
	}
	kept := make([]string, max)
	copy(kept, lines[:max])
	kept[max-1] = "…"
	return strings.Join(kept, "\n")
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
	// Separator is the U+00B7 middle dot ("N files · +X −Y").
	return fmt.Sprintf("%d files · %s %s", len(st.Files), plus, minus)
}

// renderFileLine renders one "<status> <path> +x −y" file row, with the counts
// colored and binary files marked "Bin".
func renderFileLine(f gitio.FileStat, sty styles) string {
	counts := sty.dim.Render("Bin")
	if !f.Binary {
		counts = sty.newDate.Render(fmt.Sprintf("+%d", f.Add)) + " " + sty.statusErr.Render(fmt.Sprintf("−%d", f.Del))
	}
	return fmt.Sprintf(" %s %s  %s", sty.hash.Render(f.Status), termsafe.Line(f.Path), counts)
}
