package capability

import (
	"encoding/json"
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

// usageLine renders "regit <verb> [<positional>…] [flags]" from the command's
// PositionalSpec, so every help surface shows the same arity Validate enforces.
func usageLine(c Command) string {
	s := "regit " + c.Name
	if p := c.Positional; p != nil {
		arg := "<" + p.Name + ">"
		if p.Variadic {
			arg += "…"
		}
		if !p.Required {
			arg = "[" + arg + "]"
		}
		s += " " + arg
	}
	return s + " [flags]"
}

// positionalHelp renders the one-line positional description, or "" when the
// command takes none.
func positionalHelp(c Command) string {
	if c.Positional == nil {
		return ""
	}
	return fmt.Sprintf("  <%s>  %s\n", c.Positional.Name, c.Positional.Help)
}

// HelpCommand renders one command's usage, positional slot, and flags (name,
// type hint, default, help).
func HelpCommand(c Command) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("regit %s — %s\n\nUsage:\n  %s\n", c.Name, c.Summary, usageLine(c)))
	if ph := positionalHelp(c); ph != "" {
		b.WriteString("\nArguments:\n" + ph)
	}
	b.WriteString("\nFlags:\n")
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

// LLMHelp renders the self-contained, agent-facing document from the registry.
// format: "" or "text" → plain text; "md" → Markdown; "json" → the registry as
// JSON for tooling. It carries everything an agent needs to compose correct
// commands: overview, every command's params/examples/limits/exit-codes, and the
// environment variables regit reads.
func LLMHelp(format string) (string, error) {
	if format == "json" {
		b, err := json.MarshalIndent(Registry(), "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	md := format == "md"
	h1 := func(s string) string {
		if md {
			return "# " + s + "\n\n"
		}
		return s + "\n" + strings.Repeat("=", len(s)) + "\n\n"
	}
	h2 := func(s string) string {
		if md {
			return "## " + s + "\n\n"
		}
		return "\n" + s + "\n" + strings.Repeat("-", len(s)) + "\n"
	}

	var b strings.Builder
	b.WriteString(h1("regit — headless command reference"))
	b.WriteString("regit safely rewrites git history: spread commit/author dates across a\n")
	b.WriteString("calendar (redate), split or reword commits with an LLM, and bulk-edit\n")
	b.WriteString("titles/bodies. Core concepts: day-strategies × time-models (redate),\n")
	b.WriteString("a backup ref before every apply, and the dry-run/--execute/--force gradient.\n\n")
	b.WriteString("Convention: mutating commands PREVIEW by default; pass --execute to apply,\n")
	b.WriteString("and --execute --force to rewrite already-pushed commits. `undo` is exempt and\n")
	b.WriteString("restores immediately.\n")

	for _, c := range Registry() {
		b.WriteString(h2(c.Name))
		b.WriteString(c.Summary + "\n\n")
		b.WriteString("Usage: " + usageLine(c) + "\n")
		if ph := positionalHelp(c); ph != "" {
			b.WriteString("Arguments:\n" + ph)
		}
		b.WriteString("\nFlags:\n")
		for _, p := range c.Params {
			applies := ""
			if p.AppliesIf != "" {
				applies = " [only with " + humanPredicate(p.AppliesIf) + "]"
			}
			req := ""
			if p.Required {
				req = " (required)"
			}
			b.WriteString(fmt.Sprintf("  --%-16s %s%s%s\n", p.Name+"="+typeHint(p), p.Help, req, applies))
		}
		if len(c.Examples) > 0 {
			b.WriteString("\nExamples:\n")
			for _, e := range c.Examples {
				tag := "ok "
				if e.Bad {
					tag = "BAD"
				}
				b.WriteString(fmt.Sprintf("  [%s] %s\n        # %s\n", tag, e.Cmd, e.Why))
			}
		}
		if len(c.Limitations) > 0 {
			b.WriteString("\nLimitations:\n")
			for _, l := range c.Limitations {
				b.WriteString("  - " + l + "\n")
			}
		}
		if len(c.ExitCodes) > 0 {
			b.WriteString("\nExit codes: ")
			for _, code := range []int{0, 1, 2, 3} {
				if msg, ok := c.ExitCodes[code]; ok {
					b.WriteString(fmt.Sprintf("%d=%s  ", code, msg))
				}
			}
			b.WriteString("\n")
		}
	}

	b.WriteString(h2("Environment variables"))
	b.WriteString("  REGIT_* override config (REGIT_TIMEZONE, REGIT_DEFAULT_STRATEGY, REGIT_SEED, …).\n")
	b.WriteString("  API keys (env only): REGIT_LLM_API_KEY → OPENAI_API_KEY / OPENROUTER_API_KEY → none (local).\n\n")
	b.WriteString("Source & docs: https://github.com/wlame/regit\n")
	return b.String(), nil
}
