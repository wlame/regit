// Package docs renders regit's generated documentation blocks and splices them
// into the marked regions of README.md and AGENTS.md. Every block is a pure
// projection of in-code data (the capability registry, the strategy/time-model
// names, the config env-override table), so the docs
// cannot drift from the code: cmd/docgen rewrites the files (`just docs-sync`)
// and drift_test.go fails CI whenever they differ.
package docs

import (
	"fmt"
	"strings"

	"github.com/wlame/regit/internal/capability"
	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/strategy"
)

// Blocks renders every generated block, keyed by marker name. Files() assigns
// each name to the file that carries it.
func Blocks() map[string]string {
	return map[string]string{
		"commands":       commandsBlock(),
		"strategies":     nameLine(strategyNames()),
		"time-models":    nameLine(timeModelNames()),
		"env-vars":       envVarsBlock(),
		"verbs-overview": verbsLine(),
		"verbs-detail":   verbsLine(),
		"runfiles-map":   runFilesLine(),
	}
}

// commandsBlock renders one row per registry verb. The usage column shows only
// unconditionally required flags (Required with an empty AppliesIf) — flags
// like redate's --pin are required only for one strategy and would mislead.
func commandsBlock() string {
	var b strings.Builder
	b.WriteString("| Command | What it does |\n|---------|--------------|\n")
	for _, c := range capability.Registry() {
		usage := "regit " + c.Name
		for _, p := range c.Params {
			if p.Required && p.AppliesIf == "" {
				usage += " --" + p.Name + "=…"
			}
		}
		fmt.Fprintf(&b, "| `%s` | %s |\n", usage, c.Summary)
	}
	return strings.TrimRight(b.String(), "\n")
}

// nameLine joins display names with the README's " · " separator.
func nameLine(names []string) string { return strings.Join(names, " · ") + "." }

func strategyNames() []string {
	var out []string
	for _, s := range strategy.Strategies() {
		out = append(out, s.Name())
	}
	return out
}

func timeModelNames() []string {
	var out []string
	for _, tm := range strategy.TimeModels() {
		out = append(out, tm.Name())
	}
	return out
}

// envVarsBlock lists exactly the REGIT_* overrides the config package honors.
func envVarsBlock() string {
	var toks []string
	for _, n := range config.EnvOverrideNames() {
		toks = append(toks, "`"+n+"`")
	}
	return strings.Join(toks, " · ")
}

// verbsLine renders the flat verb list in registry order; used by both
// AGENTS.md occurrences so they cannot disagree.
func verbsLine() string {
	var toks []string
	for _, c := range capability.Registry() {
		toks = append(toks, "`"+c.Name+"`")
	}
	return strings.Join(toks, " · ")
}

// runFilesLine names the per-verb Run implementation files; dashes flatten
// ("drop-bodies" → run_dropbodies.go), matching the real filenames (asserted
// by the run-file existence drift test).
func runFilesLine() string {
	var toks []string
	for _, c := range capability.Registry() {
		toks = append(toks, "`run_"+strings.ReplaceAll(c.Name, "-", "")+".go`")
	}
	return "`run_*.go` files: " + strings.Join(toks, " · ")
}
