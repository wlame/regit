package ui

// helpLines is the content of the help overlay (the '?' screen). Kept here so the
// keybinding documentation lives next to the code that handles the keys.
var helpLines = []string{
	"ghist — rewrite commit dates safely",
	"",
	"GLOBAL",
	"  ?            toggle this help",
	"  q / ctrl+c  quit",
	"  tab          switch focus between commit list and calendar",
	"",
	"COMMIT LIST (focused)",
	"  ↑/↓ or k/j   move selection",
	"  e / enter    edit selected commit (title, body, date)",
	"",
	"CALENDAR (focused)",
	"  ←/→          move cursor by one day",
	"  ↑/↓          move cursor by one week",
	"  [ / ]        previous / next year",
	"  0-9          set a per-day quota on the cursor day",
	"  g            apply accumulated quotas (Manual Pin)",
	"",
	"ACTIONS",
	"  s            open the strategy palette",
	"  p            preview the before/after dates",
	"  A            apply the rewrite (creates a backup first)",
	"  u            undo the last apply",
	"  r            reset all pending edits",
	"",
	"In forms: tab moves between fields, enter confirms, esc cancels.",
}
