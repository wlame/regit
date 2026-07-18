package capability

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/llm"
)

// HelpTerse renders the top-level usage: the verb list with one-line summaries
// and a note on the dry-run/--execute convention. Projected from the registry.
func HelpTerse() string {
	inv := Invocation()
	var b strings.Builder
	b.WriteString("regit — safely rewrite commit dates, split, reword, and restructure history.\n\n")
	b.WriteString("Usage:\n")
	b.WriteString(fmt.Sprintf("  %-35s launch the interactive TUI\n", inv+" [--repo=PATH]"))
	b.WriteString(fmt.Sprintf("  %-35s run a command headlessly\n", inv+" <command> [flags]"))
	b.WriteString(fmt.Sprintf("  %-35s flags for one command\n", inv+" <command> --help"))
	b.WriteString(fmt.Sprintf("  %-35s full agent-facing documentation\n", inv+" --llm-help"))
	b.WriteString("\nCommands:\n")
	for _, c := range Registry() {
		b.WriteString(fmt.Sprintf("  %-12s %s\n", c.Name, c.Summary))
	}
	b.WriteString("\nMutating commands preview by default; pass --execute to apply,\n")
	b.WriteString("and --execute --force to rewrite already-pushed commits.\n")
	return b.String()
}

// usageLine renders "<invocation> <verb> [<positional>…] [flags]" from the
// command's PositionalSpec, so every help surface shows the same arity
// Validate enforces and respects the active invocation prefix.
func usageLine(c Command) string {
	s := Invocation() + " " + c.Name
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

// exampleCmd rewrites a canonical registry example ("regit …") to the active
// invocation prefix at render time. Registry data stays canonical — one source
// of truth, N projections — so this is the only place examples are rewritten.
func exampleCmd(cmd string) string {
	if rest, ok := strings.CutPrefix(cmd, "regit "); ok {
		return Invocation() + " " + rest
	}
	return cmd
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
	inv := Invocation()
	b.WriteString(fmt.Sprintf("%s %s — %s\n\nUsage:\n  %s\n", inv, c.Name,
		c.Summary, usageLine(c)))
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
	b.WriteString("\nInvocation: `regit <command>` and `git re <command>` are equivalent after\n")
	b.WriteString("`regit install`. Hints and help follow whichever form was used.\n")
	b.WriteString("Exception: git answers a bare `git re --help` itself; use `git re -h` instead.\n")

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
				b.WriteString(fmt.Sprintf("  [%s] %s\n        # %s\n", tag, exampleCmd(e.Cmd), e.Why))
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
	b.WriteString("Config overrides (env beats the YAML file; the list is projected from the\n")
	b.WriteString("same table the config loader reads):\n")
	for _, name := range config.EnvOverrideNames() {
		b.WriteString("  " + name + "\n")
	}
	b.WriteString("\nAPI keys (env only, never flags): REGIT_LLM_API_KEY overrides every provider;\n")
	b.WriteString("otherwise the provider-standard variable applies:\n")
	envKeys := llm.ProviderEnvKeys()
	for _, provider := range sortedKeys(envKeys) {
		b.WriteString(fmt.Sprintf("  %-12s %s\n", provider, envKeys[provider]))
	}
	b.WriteString("Local providers (ollama, lmstudio) need no key.\n\n")
	b.WriteString("Source & docs: https://github.com/wlame/regit\n")
	return b.String(), nil
}

// sortedKeys returns the map's keys in sorted order, for deterministic output.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
