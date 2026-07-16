package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/wlame/regit/internal/capability"
)

// actionpanel.go is the selection-first action surface: a full-width bottom
// panel listing every registry command valid for the CURRENT selection. It
// replaces the per-tab feature menus. Rows are a pure projection of
// capability.Registry() joined to featureBindings — the same
// single-source-of-truth guarantee the menus had, now keyed by what is
// selected instead of which tab is active (spec §2).

// selKind classifies the live selection for the compatibility matrix. It is
// deliberately coarser than SelectionMode: the panel only cares whether the
// user picked something explicitly, not how the list renders it.
type selKind int

const (
	selKindCursor selKind = iota // no explicit selection; actions target the cursor
	selKindSet                   // an explicit non-empty Set
	selKindRange                 // an explicit visual Range
)

// selectionKind maps the selection state to its kind. An empty Set counts as
// cursor: the user toggled everything back off, so cursor semantics apply.
func selectionKind(s selectionState) selKind {
	switch {
	case s.mode == SelRange:
		return selKindRange
	case s.mode == SelSet && len(s.set) > 0:
		return selKindSet
	default:
		return selKindCursor
	}
}

// actionCompat is the (declared Selection × live kind) → allowed table from
// spec §2. SelSingle actions accept any kind (they act on the cursor or walk
// the set, e.g. multi-reword); Set/Range actions need an explicit selection;
// Active and Global actions ignore the selection entirely.
var actionCompat = map[capability.SelectionMode]map[selKind]bool{
	capability.SelSingle: {selKindCursor: true, selKindSet: true, selKindRange: true},
	capability.SelSet:    {selKindSet: true, selKindRange: true},
	capability.SelRange:  {selKindSet: true, selKindRange: true},
	capability.SelActive: {selKindCursor: true, selKindSet: true, selKindRange: true},
	capability.SelGlobal: {selKindCursor: true, selKindSet: true, selKindRange: true},
}

// panelRow is one rendered action: display title, the CLI verb it corresponds
// to (transparency — the same name capability.Render logs), a context detail
// or disabled reason, and the Run closure from featureBindings.
type panelRow struct {
	Title   string
	Verb    string
	Detail  string
	Enabled bool
	Run     func(Model) (tea.Model, tea.Cmd)
}

// panelState is the bottom panel's UI state. cursor indexes panelRows().
type panelState struct {
	open   bool
	cursor int
}

// panelMaxRows caps BOTH the row budget listHeight reserves (panelHeightFor,
// layout.go) AND the number of rows viewActionPanel actually renders
// (panelShownRows, layout.go) — the two must agree, or the panel can render
// more than the space reserved for it and overlap the commit list beneath it.
// Past the cap, the panel shows a cursor-following window instead of every
// row. With 8 registry verbs today this is headroom, not a limit.
const panelMaxRows = 10

// panelDetail computes a row's context detail and enablement beyond the
// compatibility matrix. Data-driven: verbs with special rules get an entry;
// everything else falls through to the matrix verdict alone.
var panelDetail = map[string]func(m Model) (detail string, enabled bool, reason string){
	"squash": func(m Model) (string, bool, string) {
		chains, ignored := m.affectedChains()
		members := 0
		for _, ch := range chains {
			members += len(ch.Members)
		}
		if len(chains) == 0 {
			return "", false, "select 2+ consecutive commits"
		}
		d := fmt.Sprintf("%d → %d commit(s)", members, len(chains))
		if len(ignored) > 0 {
			d += fmt.Sprintf(" · %d ignored", len(ignored))
		}
		return d, true, ""
	},
	"drop-bodies": func(m Model) (string, bool, string) {
		n := m.dropBodiesCount()
		if n == 0 {
			return "", false, "no bodies in scope"
		}
		return fmt.Sprintf("%d bodies", n), true, ""
	},
	"retitle": func(m Model) (string, bool, string) {
		return fmt.Sprintf("%d title(s) in scope", len(m.affectedCommits())), true, ""
	},
	"reauthor": func(m Model) (string, bool, string) {
		return fmt.Sprintf("%d commit(s)", len(m.affectedCommits())), true, ""
	},
	"redate": func(m Model) (string, bool, string) {
		return "opens calendar", true, ""
	},
}

// panelRows projects the registry into panel rows in registry order. A command
// appears iff it has a featureBinding (headless-only verbs like undo stay
// keyboard/CLI-only). Disabled rows keep their reason in Detail so the panel
// teaches the selection model instead of hiding actions (spec §2).
func (m Model) panelRows() []panelRow {
	kind := selectionKind(m.sel)
	var out []panelRow
	for _, c := range capability.Registry() {
		b, ok := featureBindings[c.Name]
		if !ok {
			continue
		}
		enabled := actionCompat[c.Selection][kind]
		detail, reason := "", ""
		if f, ok := panelDetail[c.Name]; ok {
			var extraOK bool
			detail, extraOK, reason = f(m)
			enabled = enabled && extraOK
		}
		if !enabled {
			if reason == "" {
				reason = "needs an explicit selection"
			}
			detail = reason
		}
		out = append(out, panelRow{
			Title:   b.Title,
			Verb:    c.Name,
			Detail:  detail,
			Enabled: enabled,
			Run:     b.Run,
		})
	}
	return out
}

// panelWindowStart returns the first row index to render so the cursor stays
// visible within a `shown`-row window into `total` rows. It centers the
// cursor in the window and clamps so the window never runs past either end —
// a stateless computation (no persisted offset, unlike the commit list's
// scrollOffset) since the panel only needs this once total exceeds
// panelMaxRows, which the registry has headroom before reaching.
func panelWindowStart(cursor, total, shown int) int {
	if total <= shown {
		return 0
	}
	start := cursor - shown/2
	if start < 0 {
		start = 0
	}
	if start > total-shown {
		start = total - shown
	}
	return start
}

// viewActionPanel renders the bottom panel at the given full terminal width.
// The body above stays visible — the panel is a region, not an overlay. Rows
// are windowed to panelShownRows so the rendered height never exceeds the
// budget listHeight reserved via panelHeightFor (panelMaxRows bounds both).
func (m Model) viewActionPanel(width int) string {
	rows := m.panelRows()
	shown := panelShownRows(len(rows))
	start := panelWindowStart(m.panel.cursor, len(rows), shown)
	kind := selectionKind(m.sel)
	scope := "cursor commit"
	if kind != selKindCursor {
		scope = fmt.Sprintf("%d selected commit(s)", len(m.affectedCommits()))
	}
	var b strings.Builder
	b.WriteString(m.st.modalTitle.Render("ACTIONS · "+scope) + "\n")
	for i := start; i < start+shown && i < len(rows); i++ {
		r := rows[i]
		marker, title := "  ", r.Title
		if i == m.panel.cursor {
			marker = m.st.selected.Render("▸ ")
			title = m.st.selected.Render(r.Title)
		} else if !r.Enabled {
			title = m.st.dim.Render(r.Title)
		}
		line := fmt.Sprintf("%s%-22s %-34s %s",
			marker, title, m.st.dim.Render(r.Detail), m.st.help.Render(r.Verb))
		b.WriteString(line + "\n")
	}
	b.WriteString(m.st.help.Render("↑/↓ move · enter run · esc close"))
	frame := m.st.paneActive.Width(max(20, width-2))
	return frame.Render(lipglossMaxWidth(b.String(), max(16, width-4)))
}

// updateActionPanel owns every key while the panel is open. Arrow-first per
// the design: ↑/↓ move, enter runs (closing the panel first so the launched
// flow/overlay owns the next frame), esc/m close. ←/→ and [/] are reserved
// for the 2b option rows and deliberately swallowed so they never leak to the
// list underneath.
func (m Model) updateActionPanel(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.panelRows()
	if m.panel.cursor >= len(rows) {
		m.panel.cursor = max(0, len(rows)-1)
	}
	switch msg.String() {
	case "esc", "m":
		m.panel.open = false
		return m, nil
	case "up", "k":
		if m.panel.cursor > 0 {
			m.panel.cursor--
		}
		return m, nil
	case "down", "j":
		if m.panel.cursor < len(rows)-1 {
			m.panel.cursor++
		}
		return m, nil
	case "enter":
		r := rows[m.panel.cursor]
		if !r.Enabled {
			m.setStatus(r.Detail, statusWarn)
			return m, nil
		}
		m.panel.open = false
		return r.Run(m)
	case "left", "right", "[", "]":
		return m, nil // reserved: option-row value cycling lands in Phase 2b
	}
	return m, nil
}

// lipglossMaxWidth clamps every line of s to width, ANSI-aware.
func lipglossMaxWidth(s string, width int) string {
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}
