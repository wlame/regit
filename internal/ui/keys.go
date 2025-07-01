package ui

import (
	"fmt"
	"strings"
)

// keys.go is the single source of truth for the keymap. The help overlay (the
// '?' screen) and the compact keybar hint are GENERATED from the keymap table, so
// the documentation can never drift from the keys the code actually handles
// (spec §6.8) — there is one table, not a hand-maintained help slice plus a
// separately hard-coded keybar string.

// keyScope groups a binding by the context it applies in, so the generated help
// can section bindings and the keybar can show a curated subset.
type keyScope int

const (
	scopeGlobal keyScope = iota
	scopeWorkspace
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
	{Key: "tab", Scope: scopeGlobal, Desc: "cycle focus across the list and the active workspace widget", Bar: "[tab]focus"},

	// Workspaces / tabs
	{Key: "< / >", Scope: scopeWorkspace, Desc: "switch workspace tabs (Dates · Restructure · …)", Bar: "[<>]tabs"},
	{Key: "tab", Scope: scopeWorkspace, Desc: "focus the active workspace's widget (calendar or feature menu)"},
	{Key: "↑/↓ · enter", Scope: scopeWorkspace, Desc: "in a feature menu: move the cursor / run the selected feature"},

	// Actions (work from any pane)
	{Key: "s", Scope: scopeAction, Desc: "open the strategy palette (Dates)", Bar: "[s]trategy"},
	{Key: "S", Scope: scopeAction, Desc: "split the selected commit with the LLM (review → enter applies, esc cancels)", Bar: "[S]plit"},
	{Key: "w", Scope: scopeAction, Desc: "reword the selected commit with the LLM (pick a variant, enter applies)", Bar: "[w]reword"},
	{Key: "e / enter", Scope: scopeAction, Desc: "edit the selected commit (title, body, date)", Bar: "[e]dit"},
	{Key: "D", Scope: scopeAction, Desc: "drop the bodies of ALL editable commits (titles kept)", Bar: "[D]rop-bodies"},
	{Key: "R", Scope: scopeAction, Desc: "regex-replace across ALL editable commit titles", Bar: "[R]etitle"},
	{Key: ".", Scope: scopeAction, Desc: "show full detail for the selected commit (overlay when narrow)", Bar: "[.]detail"},
	{Key: "p", Scope: scopeAction, Desc: "preview the before/after dates", Bar: "[p]review"},
	{Key: "A", Scope: scopeAction, Desc: "apply the rewrite (creates a backup first)", Bar: "[A]pply"},
	{Key: "u", Scope: scopeAction, Desc: "undo the last apply", Bar: "[u]ndo"},
	{Key: "r", Scope: scopeAction, Desc: "reset all pending edits", Bar: "[r]eset"},

	// Commit list (focused)
	{Key: "↑/↓ or k/j", Scope: scopeList, Desc: "move the selection"},
	{Key: "ctrl+d/u", Scope: scopeList, Desc: "half page down / up (pgdn/pgup move a full page)"},
	{Key: "g / G", Scope: scopeList, Desc: "newest / oldest commit (home/end do the same)"},
	{Key: "} / {", Scope: scopeList, Desc: "next / previous day group"},

	// Selection (commit list)
	{Key: "space", Scope: scopeSelection, Desc: "toggle the cursor commit in the selection set", Bar: "[space]select"},
	{Key: "a", Scope: scopeSelection, Desc: "select all active commits"},
	{Key: "n", Scope: scopeSelection, Desc: "clear the selection"},
	{Key: "v", Scope: scopeSelection, Desc: "visual range — anchor, move to extend, v/enter to fix, esc to cancel"},
	{Key: "esc", Scope: scopeSelection, Desc: "cancel the selection (back to the workspace default)"},
	{Key: "(reword)", Scope: scopeSelection, Desc: "with a set selected, Reword runs multi-reword over every chosen commit"},

	// Branch & base
	{Key: "B", Scope: scopeBranch, Desc: "open the branch picker — enter switches branch, tab sets it as the base", Bar: "[B]ranch"},
	{Key: "b", Scope: scopeBranch, Desc: "set/clear the base at the cursor commit (freeze older history)", Bar: "[b]ase"},

	// Calendar (focused)
	{Key: "↑/↓", Scope: scopeCalendar, Desc: "move the cursor by one day"},
	{Key: "←/→", Scope: scopeCalendar, Desc: "move the cursor by one week"},
	{Key: "[ / ]", Scope: scopeCalendar, Desc: "previous / next year"},
	{Key: "0-9", Scope: scopeCalendar, Desc: "pin a per-day quota, applied live (digits on one day combine: 2 then 4 = 24)"},
	{Key: "space", Scope: scopeCalendar, Desc: "pick/unpick the day for a random split (reshuffles live)"},
	{Key: "g", Scope: scopeCalendar, Desc: "re-apply all pinned quotas"},

	// Strategy pane (focused)
	{Key: "↑/↓", Scope: scopeStrategy, Desc: "move between fields"},
	{Key: "←/→", Scope: scopeStrategy, Desc: "change the time-of-day model"},
	{Key: "enter", Scope: scopeStrategy, Desc: "apply the strategy with the current parameters"},
	{Key: "tab / esc", Scope: scopeStrategy, Desc: "back to the commit list"},
}

// scopeOrder and scopeTitles drive the help overlay's sectioning.
var scopeOrder = []keyScope{
	scopeGlobal, scopeWorkspace, scopeAction, scopeList, scopeSelection, scopeBranch, scopeCalendar, scopeStrategy,
}

var scopeTitles = map[keyScope]string{
	scopeGlobal:    "GLOBAL",
	scopeWorkspace: "WORKSPACES",
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
