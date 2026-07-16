package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// keys.go is the single source of truth for the keymap. The help overlay (the
// '?' screen), the compact keybar hint, and the README's Keys section (via
// DocSections and internal/docs) are all GENERATED from the keymap table, so
// the documentation can never drift from the keys the code actually handles —
// there is one table, not a hand-maintained help slice plus a
// separately hard-coded keybar string.

// keyScope groups a binding by the context it applies in, so the generated help
// can section bindings and the keybar can show a curated subset.
type keyScope int

const (
	scopeGlobal keyScope = iota
	scopeFlow
	scopeAction
	scopeList
	scopeSelection
	scopeBranch
	scopeCalendar
	scopeStrategy
)

// keyBinding is one row of the keymap data table. Bar is the short token shown in
// the always-visible keybar (e.g. "[s]trategy"); an empty Bar omits the row from
// the keybar but still documents it in the help overlay.
type keyBinding struct {
	Key   string
	Scope keyScope
	Desc  string
	Bar   string
}

// keymap is the full keymap. Keybar order follows this slice's order.
var keymap = []keyBinding{
	// Global
	{Key: "?", Scope: scopeGlobal, Desc: "toggle this help", Bar: "[?]help"},
	{Key: "q / ctrl+c", Scope: scopeGlobal, Desc: "quit", Bar: "[q]uit"},
	{Key: "tab", Scope: scopeGlobal, Desc: "cycle focus across the list and the active flow's widget", Bar: "[tab]focus"},

	// Flow zone
	{Key: "tab", Scope: scopeFlow, Desc: "focus the active flow's widget (the calendar), when one is open"},
	{Key: "esc", Scope: scopeFlow, Desc: "exit the active flow, returning the zone to the commit detail"},

	// Actions (work from any pane)
	{Key: "e / enter", Scope: scopeAction, Desc: "edit the selected commit (title, body, date)", Bar: "[e]dit"},
	{Key: ".", Scope: scopeAction, Desc: "show full detail for the selected commit (overlay when narrow)", Bar: "[.]detail"},
	{Key: "p", Scope: scopeAction, Desc: "preview the pending changes (read-only)", Bar: "[p]review"},
	{Key: "A", Scope: scopeAction, Desc: "apply: preview the before/after, then enter to apply (confirm if pushed)", Bar: "[A]pply"},
	{Key: "U", Scope: scopeAction, Desc: "undo the last apply", Bar: "[U]ndo"},
	{Key: "R", Scope: scopeAction, Desc: "reset all pending edits", Bar: "[R]eset"},
	{Key: "L", Scope: scopeAction, Desc: "toggle the command-log footer (read-only)", Bar: "[L]og"},
	{Key: "m", Scope: scopeAction, Desc: "open the action panel for the current selection", Bar: "[m]actions"},

	// Commit list (focused)
	{Key: "d", Scope: scopeList, Desc: "toggle date grouping in the commit list", Bar: "[d]ates"},
	{Key: "a", Scope: scopeList, Desc: "toggle the author column in the commit list", Bar: "[a]uthor"},
	{Key: "↑/↓ or k/j", Scope: scopeList, Desc: "move the selection"},
	{Key: "ctrl+d/u", Scope: scopeList, Desc: "half page down / up (pgdn/pgup move a full page)"},
	{Key: "g / G", Scope: scopeList, Desc: "newest / oldest commit (home/end do the same)"},
	{Key: "} / {", Scope: scopeList, Desc: "next / previous day group"},

	// Selection (commit list)
	{Key: "space", Scope: scopeSelection, Desc: "toggle the cursor commit in the selection set", Bar: "[space]select"},
	{Key: "s", Scope: scopeSelection, Desc: "select all active commits"},
	{Key: "n", Scope: scopeSelection, Desc: "clear the selection"},
	{Key: "v", Scope: scopeSelection, Desc: "visual range — anchor, move to extend, v/enter to fix, esc to cancel"},
	{Key: "esc", Scope: scopeSelection, Desc: "cancel the selection (back to Active if a flow is open, else Single)"},
	{Key: "(reword)", Scope: scopeSelection, Desc: "with a set selected, Reword runs multi-reword over every chosen commit"},

	// Branch & base
	{Key: "B", Scope: scopeBranch, Desc: "open the branch picker — enter switches branch, tab sets it as the base", Bar: "[B]ranch"},
	{Key: "b", Scope: scopeBranch, Desc: "set/clear the base at the cursor commit (freeze older history)", Bar: "[b]ase"},

	// Calendar (focused)
	{Key: "↑/↓", Scope: scopeCalendar, Desc: "move the cursor by one day"},
	{Key: "←/→", Scope: scopeCalendar, Desc: "move the cursor by one week"},
	{Key: "0-9", Scope: scopeCalendar, Desc: "pin a per-day quota (Manual Pin strategy active); live-stages the distribution"},
	{Key: "space", Scope: scopeCalendar, Desc: "pick/unpick the day (Picked Days strategy active); reshuffles live"},

	// Strategy pane (focused)
	{Key: "↑/↓", Scope: scopeStrategy, Desc: "move between fields"},
	{Key: "←/→", Scope: scopeStrategy, Desc: "change strategy (on the top row) / time model"},
	{Key: "enter", Scope: scopeStrategy, Desc: "apply the strategy with the current parameters"},
	{Key: "tab / esc", Scope: scopeStrategy, Desc: "back to the commit list"},
}

// scopeOrder and scopeTitles drive the help overlay's sectioning.
var scopeOrder = []keyScope{
	scopeGlobal, scopeFlow, scopeAction, scopeList, scopeSelection, scopeBranch, scopeCalendar, scopeStrategy,
}

var scopeTitles = map[keyScope]string{
	scopeGlobal:    "GLOBAL",
	scopeFlow:      "FLOW",
	scopeAction:    "ACTIONS (any pane)",
	scopeList:      "COMMIT LIST (focused)",
	scopeSelection: "SELECTION (commit list)",
	scopeBranch:    "BRANCH & BASE",
	scopeCalendar:  "CALENDAR (focused)",
	scopeStrategy:  "STRATEGY PANE (focused)",
}

// helpText renders the '?' overlay body from the keymap, grouped by scope. The
// caller wraps it in the modal style.
func helpText() string {
	var b strings.Builder
	b.WriteString("regit — rewrite commit history safely\n")
	for _, sc := range scopeOrder {
		b.WriteString("\n" + scopeTitles[sc] + "\n")
		for _, k := range keymap {
			if k.Scope == sc {
				b.WriteString(fmt.Sprintf("  %-14s %s\n", k.Key, k.Desc))
			}
		}
	}
	return b.String()
}

// keybarHint renders the always-visible one-line keybar from the keymap rows that
// declare a Bar token, in table order.
func keybarHint() string {
	var toks []string
	for _, k := range keymap {
		if k.Bar != "" {
			toks = append(toks, k.Bar)
		}
	}
	return strings.Join(toks, " ")
}

// keybarHintWidth renders the keybar truncated to fit width: it keeps tokens in
// table order until the next would overflow, then appends an ellipsis. It never
// wraps, so callers can rely on a single-line keybar for their height budget.
func keybarHintWidth(width int) string {
	if width <= 0 {
		return keybarHint()
	}
	var toks []string
	for _, k := range keymap {
		if k.Bar != "" {
			toks = append(toks, k.Bar)
		}
	}
	full := strings.Join(toks, " ")
	if lipgloss.Width(full) <= width {
		return full
	}
	const ell = " …"
	var kept []string
	for _, tok := range toks {
		cand := strings.Join(append(kept, tok), " ")
		if lipgloss.Width(cand+ell) > width {
			break
		}
		kept = append(kept, tok)
	}
	if len(kept) == 0 {
		return "…"
	}
	return strings.Join(kept, " ") + ell
}

// DocBinding is one keymap row exposed for documentation rendering. It is a
// read-only copy: consumers (internal/docs, which generates the README keys
// table) can render the keymap but cannot reach or mutate the table itself.
type DocBinding struct {
	Key  string // the key chord as shown to the user, e.g. "e / enter"
	Desc string // one-line action description
	Bar  string // keybar token ("[e]dit"), "" when the row is help-only
}

// DocSection groups one scope's bindings under its help-overlay title.
type DocSection struct {
	Title    string
	Bindings []DocBinding
}

// DocSections projects the keymap in scope order — the same grouping the '?'
// overlay renders — so generated documentation mirrors the in-app help.
func DocSections() []DocSection {
	var out []DocSection
	for _, sc := range scopeOrder {
		sec := DocSection{Title: scopeTitles[sc]}
		for _, k := range keymap {
			if k.Scope == sc {
				sec.Bindings = append(sec.Bindings, DocBinding{Key: k.Key, Desc: k.Desc, Bar: k.Bar})
			}
		}
		out = append(out, sec)
	}
	return out
}
