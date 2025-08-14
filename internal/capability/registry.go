package capability

import (
	"strings"

	"github.com/wlame/regit/internal/strategy"
)

// registry is the ordered single source of truth. Phase 1 seeds split and reword
// (metadata only; Run wired in later phases). New verbs append rows here.
var registry = []Command{
	{
		Name:      "split",
		Summary:   "Split one commit into 2–4 semantic commits using an LLM.",
		Mutating:  true,
		Workspace: WSRestructure,
		Selection: SelSingle,
		Params: []Param{
			{Name: "llm", Type: ParamString, Help: "provider/model, e.g. ollama/qwen2.5-coder"},
			{Name: "agent", Type: ParamString, Help: "agent CLI to drive instead of HTTP"},
			{Name: "agent-args", Type: ParamString, Help: "replace default agent args; {schema} is injected"},
			{Name: "llm-base-url", Type: ParamString, Help: "override the OpenAI-compatible endpoint"},
			{Name: "max-commits", Type: ParamInt, Default: "0", Help: "clamp on resulting commits (0 = config/auto)"},
			{Name: "prompt", Type: ParamString, Help: "short guidance passed to the LLM"},
			{Name: "test-cmd", Type: ParamString, Help: "validate each intermediate commit before applying"},
			{Name: "no-cache", Type: ParamBool, Default: "false", Help: "ignore any cached proposal"},
			{Name: "clear-cache", Type: ParamBool, Default: "false", Help: "delete the split cache and exit"},
			{Name: "repo", Type: ParamString, Default: ".", Help: "path to the git repository"},
			{Name: "execute", Type: ParamBool, Default: "false", Help: "apply the split (default: dry-run preview)"},
			{Name: "force", Type: ParamBool, Default: "false", Help: "allow rewriting already-pushed commits"},
		},
		Examples: []Example{
			{Cmd: "regit split HEAD --llm=ollama/qwen2.5-coder", Why: "preview a split of HEAD (dry-run)"},
			{Cmd: "regit split HEAD --llm=ollama/qwen2.5-coder --execute", Why: "apply the split"},
			{Cmd: "regit split HEAD --yes", Why: "deprecated: --yes is an alias for --execute", Bad: true},
		},
		Limitations: []string{
			"Requires an LLM (--llm or --agent).",
			"Skips commits that are too small, single-hunk, pure-rename, or binary-only.",
		},
		ExitCodes: map[int]string{0: "ok / dry-run", 1: "usage error", 2: "guard blocked", 3: "apply/LLM error"},
		Run:       runSplit,
	},
	{
		Name:      "reword",
		Summary:   "Regenerate commit message(s) with an LLM (one proposal headless).",
		Mutating:  true,
		Workspace: WSRestructure,
		Selection: SelSingle,
		Params: []Param{
			{Name: "llm", Type: ParamString, Help: "provider/model, e.g. ollama/qwen2.5-coder"},
			{Name: "agent", Type: ParamString, Help: "agent CLI to drive instead of HTTP"},
			{Name: "agent-args", Type: ParamString, Help: "replace default agent args"},
			{Name: "agent-scan", Type: ParamBool, Default: "false", Help: "agent inspects the commit read-only (requires --agent)"},
			{Name: "llm-base-url", Type: ParamString, Help: "override the OpenAI-compatible endpoint"},
			{Name: "prompt", Type: ParamString, Help: "short guidance passed to the LLM"},
			{Name: "context", Type: ParamStringList, Help: "context keys to include, e.g. readme,filestates"},
			{Name: "context-lines", Type: ParamInt, Default: "0", Help: "git diff -U depth (0 = config default)"},
			{Name: "no-cache", Type: ParamBool, Default: "false", Help: "ignore cached proposals"},
			{Name: "clear-cache", Type: ParamBool, Default: "false", Help: "delete the reword cache and exit"},
			{Name: "repo", Type: ParamString, Default: ".", Help: "path to the git repository"},
			{Name: "execute", Type: ParamBool, Default: "false", Help: "apply the reword (default: dry-run preview)"},
			{Name: "force", Type: ParamBool, Default: "false", Help: "allow rewriting already-pushed commits"},
		},
		Examples: []Example{
			{Cmd: "regit reword HEAD --llm=ollama/qwen2.5-coder", Why: "preview a new message for HEAD"},
			{Cmd: "regit reword HEAD~2 HEAD --llm=openai/gpt-4o --execute", Why: "reword two commits and apply"},
			{Cmd: "regit reword --agent-scan HEAD", Why: "--agent-scan without --agent is an error", Bad: true},
		},
		Limitations: []string{
			"Headless always requests exactly one variant (the TUI browses several).",
			"--agent-scan requires --agent.",
		},
		ExitCodes: map[int]string{0: "ok / dry-run", 1: "usage error", 2: "guard blocked", 3: "apply/LLM error"},
		Run:       runReword,
	},
}

// Registry returns the ordered command table.
func Registry() []Command { return registry }

// Lookup returns the command with the given name and whether it exists.
func Lookup(name string) (Command, bool) {
	for _, c := range registry {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// strategyNames returns every day-strategy name in kebab form, registry order.
func strategyNames() []string {
	var out []string
	for _, s := range strategy.Strategies() {
		out = append(out, canonName(s.Name()))
	}
	return out
}

// timeModelNames returns every time-model name in kebab form, registry order.
func timeModelNames() []string {
	var out []string
	for _, tm := range strategy.TimeModels() {
		out = append(out, canonName(tm.Name()))
	}
	return out
}

// strategyByName resolves a CLI value (kebab or spaced, any case) to a Strategy,
// or nil if unknown.
func strategyByName(name string) strategy.Strategy {
	want := canonName(name)
	for _, s := range strategy.Strategies() {
		if canonName(s.Name()) == want {
			return s
		}
	}
	return nil
}

// timeModelByName resolves a CLI value to a TimeModel, or nil if unknown.
func timeModelByName(name string) strategy.TimeModel {
	want := canonName(name)
	for _, tm := range strategy.TimeModels() {
		if canonName(tm.Name()) == want {
			return tm
		}
	}
	return nil
}

// canonName canonicalizes a strategy/time-model name to the registry's kebab
// form: spaces → dashes, matched case-insensitively against the engine names so
// the returned value is always the exact casing the engine reports. Names that
// already contain dashes (e.g. "Preserve-Relative") are preserved.
func canonName(name string) string {
	kebab := strings.ReplaceAll(name, " ", "-")
	// Match case-insensitively against the real names so "even-spread" → "Even-Spread".
	for _, s := range strategy.Strategies() {
		real := strings.ReplaceAll(s.Name(), " ", "-")
		if strings.EqualFold(real, kebab) {
			return real
		}
	}
	for _, tm := range strategy.TimeModels() {
		real := strings.ReplaceAll(tm.Name(), " ", "-")
		if strings.EqualFold(real, kebab) {
			return real
		}
	}
	return kebab
}
