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

// keyCtx identifies which part of the UI currently owns the keybar, so the
// same keymap table can render a different filtered subset per focus state
// instead of the app hand-maintaining a separate hint string per screen.
// ctxAlways is the zero value: a row left untagged is a global row and shows
// up in every context's keybar (see keybarRowVisible), so most keymap rows
// need no explicit Ctx at all.
type keyCtx int

const (
	ctxAlways keyCtx = iota // zero value: global rows, shown in every context
	ctxList                 // the commit list owns focus (the default screen)
	ctxRedate               // the redate flow (calendar/strategy) is open
	ctxPanel                // the action panel owns the keybar
)

// exclusiveKeyCtx marks contexts whose keybar shows ONLY their own rows,
// instead of layering on top of the ctxAlways globals like every other
// context does. The action panel swallows every keystroke while open
// (updateActionPanel runs before any list/global handling — see app.go), so
// advertising the global row set ('m' reopen, 'A' apply, ...) underneath it
// would promise keys the panel doesn't actually honor. Kept as a data table
// rather than a special case in keybarRowVisible so a future exclusive
// context is a table entry, not a new branch.
var exclusiveKeyCtx = map[keyCtx]bool{
	ctxPanel: true,
}

// keybarRowVisible reports whether row belongs on the keybar for ctx: its own
// context always qualifies, and the global ctxAlways rows also qualify unless
// ctx is exclusive (see exclusiveKeyCtx).
func keybarRowVisible(row keyBinding, ctx keyCtx) bool {
	return row.Ctx == ctx || (row.Ctx == ctxAlways && !exclusiveKeyCtx[ctx])
}

// keyBinding is one row of the keymap data table. Bar is the short token shown in
// the always-visible keybar (e.g. "[s]trategy"); an empty Bar omits the row from
// the keybar but still documents it in the help overlay. Ctx narrows which
// focus context's keybar the row appears in (see keyCtx); the zero value
// ctxAlways means "every context".
type keyBinding struct {
	Key   string
	Scope keyScope
	Ctx   keyCtx
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
	{Key: "↑/↓ · enter · esc", Scope: scopeAction, Ctx: ctxPanel, Desc: "action panel: move · run · close", Bar: "↑/↓ move · enter run · esc close"},

	// Commit list (focused)
	{Key: "d", Scope: scopeList, Ctx: ctxList, Desc: "toggle date grouping in the commit list", Bar: "[d]ates"},
	{Key: "a", Scope: scopeList, Ctx: ctxList, Desc: "toggle the author column in the commit list", Bar: "[a]uthor"},
	{Key: "↑/↓ or k/j", Scope: scopeList, Ctx: ctxList, Desc: "move the selection"},
	{Key: "ctrl+d/u", Scope: scopeList, Ctx: ctxList, Desc: "half page down / up (pgdn/pgup move a full page)"},
	{Key: "g / G", Scope: scopeList, Ctx: ctxList, Desc: "newest / oldest commit (home/end do the same)"},
	{Key: "} / {", Scope: scopeList, Ctx: ctxList, Desc: "next / previous day group"},

	// Selection (commit list)
	{Key: "space", Scope: scopeSelection, Ctx: ctxList, Desc: "toggle the cursor commit in the selection set", Bar: "[space]select"},
	{Key: "s", Scope: scopeSelection, Ctx: ctxList, Desc: "select all active commits"},
	{Key: "n", Scope: scopeSelection, Ctx: ctxList, Desc: "clear the selection"},
	{Key: "v", Scope: scopeSelection, Ctx: ctxList, Desc: "visual range — anchor, move to extend, v/enter to fix, esc to cancel"},
	{Key: "esc", Scope: scopeSelection, Ctx: ctxList, Desc: "cancel the selection (back to Active if a flow is open, else Single)"},
	{Key: "(reword)", Scope: scopeSelection, Ctx: ctxList, Desc: "with a set selected, Reword runs multi-reword over every chosen commit"},

	// Branch & base
	{Key: "B", Scope: scopeBranch, Desc: "open the branch picker — enter switches branch, tab sets it as the base", Bar: "[B]ranch"},
	{Key: "b", Scope: scopeBranch, Desc: "set/clear the base at the cursor commit (freeze older history)", Bar: "[b]ase"},

	// Calendar (redate flow)
	{Key: "↑/↓", Scope: scopeCalendar, Ctx: ctxRedate, Desc: "move the cursor by one day"},
	{Key: "←/→", Scope: scopeCalendar, Ctx: ctxRedate, Desc: "move the cursor by one week"},
	{Key: "0-9", Scope: scopeCalendar, Ctx: ctxRedate, Desc: "pin a per-day quota (Manual Pin strategy active); live-stages the distribution"},
	{Key: "space", Scope: scopeCalendar, Ctx: ctxRedate, Desc: "pick/unpick the day (Picked Days strategy active); reshuffles live"},

	// Strategy pane (redate flow)
	{Key: "↑/↓", Scope: scopeStrategy, Ctx: ctxRedate, Desc: "move between fields"},
	{Key: "←/→", Scope: scopeStrategy, Ctx: ctxRedate, Desc: "change strategy (on the top row) / time model"},
	{Key: "enter", Scope: scopeStrategy, Ctx: ctxRedate, Desc: "apply the strategy with the current parameters"},
	{Key: "tab / esc", Scope: scopeStrategy, Ctx: ctxRedate, Desc: "back to the commit list"},
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
	scopeCalendar:  "CALENDAR (redate flow)",
	scopeStrategy:  "STRATEGY PANE (redate flow)",
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

// barTokens collects the Bar token of every keymap row that has one and
// satisfies include, in table order. Shared by keybarHint, keybarHintWidth,
// and keybarHintFor so the three keybar renderers can never disagree about
// how tokens are gathered — only about which rows qualify.
func barTokens(include func(keyBinding) bool) []string {
	var toks []string
	for _, k := range keymap {
		if k.Bar != "" && include(k) {
			toks = append(toks, k.Bar)
		}
	}
	return toks
}

// truncateKeybar joins toks with spaces, keeping tokens in order until the
// next would overflow width, then appends an ellipsis. It never wraps, so
// callers can rely on a single-line keybar for their height budget. width<=0
// means "no limit" — the full joined string is returned untruncated.
func truncateKeybar(toks []string, width int) string {
	full := strings.Join(toks, " ")
	if width <= 0 || lipgloss.Width(full) <= width {
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

// allRows is the barTokens include predicate for the unfiltered keybar: every
// row qualifies regardless of Ctx.
func allRows(keyBinding) bool { return true }

// keybarHint renders the always-visible one-line keybar from every keymap row
// that declares a Bar token, in table order, ignoring Ctx.
func keybarHint() string {
	return strings.Join(barTokens(allRows), " ")
}

// keybarHintWidth renders the unfiltered keybar (see keybarHint) truncated to
// fit width.
func keybarHintWidth(width int) string {
	return truncateKeybar(barTokens(allRows), width)
}

// keybarHintFor renders the keybar for a single focus context: the rows
// keybarRowVisible says belong to ctx, truncated to fit width exactly like
// keybarHintWidth. This is what the running app renders (see view.go); the
// unfiltered keybarHint/keybarHintWidth remain for tests and callers that
// want the full table regardless of focus.
func keybarHintFor(ctx keyCtx, width int) string {
	visible := func(k keyBinding) bool { return keybarRowVisible(k, ctx) }
	return truncateKeybar(barTokens(visible), width)
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

// currentKeyCtx maps the model's live focus state to the keybar context, so
// the keybar always reflects the keys the current screen actually honors
// instead of the full global set regardless of what owns input. The open
// panel takes priority (it swallows every keystroke — see updateActionPanel),
// then the redate flow; the commit list is the default otherwise.
func (m Model) currentKeyCtx() keyCtx {
	switch {
	case m.panel.open:
		return ctxPanel
	case m.flow == flowRedate:
		return ctxRedate
	default:
		return ctxList
	}
}
