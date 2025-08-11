package capability

import (
	"fmt"
	"strings"
)

// HelpTerse renders the top-level usage: the verb list with one-line summaries
// and a note on the dry-run/--execute convention. Projected from the registry.
func HelpTerse() string {
	var b strings.Builder
	b.WriteString("regit — safely rewrite commit dates, split, reword, and restructure history.\n\n")
	b.WriteString("Usage:\n  regit [--repo=PATH]                 launch the interactive TUI\n")
	b.WriteString("  regit <command> [flags]             run a command headlessly\n")
	b.WriteString("  regit <command> --help              flags for one command\n")
	b.WriteString("  regit --llm-help                    full agent-facing documentation\n\n")
	b.WriteString("Commands:\n")
	for _, c := range Registry() {
		b.WriteString(fmt.Sprintf("  %-12s %s\n", c.Name, c.Summary))
	}
	b.WriteString("\nMutating commands preview by default; pass --execute to apply,\n")
	b.WriteString("and --execute --force to rewrite already-pushed commits.\n")
	return b.String()
}

// HelpCommand renders one command's flags (name, type hint, default, help).
func HelpCommand(c Command) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("regit %s — %s\n\nFlags:\n", c.Name, c.Summary))
	for _, p := range c.Params {
		def := ""
		if p.Default != "" {
			def = " (default: " + p.Default + ")"
		}
		applies := ""
		if p.AppliesIf != "" {
			applies = " [only with " + humanPredicate(p.AppliesIf) + "]"
		}
		b.WriteString(fmt.Sprintf("  --%-16s %s%s%s\n", p.Name+"="+typeHint(p), p.Help, def, applies))
	}
	return b.String()
}

// typeHint is a short uppercase placeholder for a flag's value in help text.
func typeHint(p Param) string {
	switch p.Type {
	case ParamBool:
		return "BOOL"
	case ParamInt:
		return "N"
	case ParamFloat:
		return "F"
	case ParamEnum:
		return "NAME"
	case ParamStringList:
		return "A,B"
	case ParamKeyVals:
		return "K=N,…"
	case ParamDate:
		return "YYYY-MM-DD"
	case ParamClock:
		return "HH:MM"
	default:
		return "VALUE"
	}
}
