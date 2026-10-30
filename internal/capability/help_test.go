package capability

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/config"
	"github.com/wlame/regit/internal/llm"
)

func TestHelpTerseListsEveryVerb(t *testing.T) {
	out := HelpTerse()
	for _, c := range Registry() {
		if !strings.Contains(out, c.Name) {
			t.Errorf("HelpTerse missing verb %q", c.Name)
		}
	}
	if !strings.Contains(out, "--execute") {
		t.Error("HelpTerse should mention the --execute convention")
	}
}

func TestHelpCommandListsFlags(t *testing.T) {
	c, _ := Lookup("reword")
	out := HelpCommand(c)
	if !strings.Contains(out, "--llm") || !strings.Contains(out, "--execute") {
		t.Errorf("HelpCommand missing flags:\n%s", out)
	}
}

func TestLLMHelpTextCoversEveryCommand(t *testing.T) {
	out, err := LLMHelp("text")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range Registry() {
		if !strings.Contains(out, c.Name) {
			t.Errorf("LLMHelp text missing %q", c.Name)
		}
		for _, p := range c.Params {
			if !strings.Contains(out, "--"+p.Name) {
				t.Errorf("LLMHelp missing --%s for %s", p.Name, c.Name)
			}
		}
	}
	// Must include at least one bad example annotation and the env-var section.
	if !strings.Contains(out, "REGIT_LLM_API_KEY") {
		t.Error("LLMHelp should document API-key env vars")
	}
}

func TestLLMHelpJSONParses(t *testing.T) {
	out, err := LLMHelp("json")
	if err != nil {
		t.Fatal(err)
	}
	var cmds []map[string]any
	if err := json.Unmarshal([]byte(out), &cmds); err != nil {
		t.Fatalf("json invalid: %v", err)
	}
	if len(cmds) != len(Registry()) {
		t.Errorf("json has %d commands, registry has %d", len(cmds), len(Registry()))
	}
}

// TestHelp_positionalProjection verifies the PositionalSpec drives every help
// surface: the per-command usage line, the --llm-help document, and the JSON
// projection all describe the same slot.
func TestHelp_positionalProjection(t *testing.T) {
	squash, _ := Lookup("squash")
	hc := HelpCommand(squash)
	if !strings.Contains(hc, "regit squash <range>… [flags]") {
		t.Errorf("HelpCommand usage line missing required variadic <range>:\n%s", hc)
	}
	if !strings.Contains(hc, "<range>") || !strings.Contains(hc, "chain spec") {
		t.Errorf("HelpCommand missing the positional description:\n%s", hc)
	}

	txt, err := LLMHelp("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(txt, "regit split [<commit-ish>] [flags]") {
		t.Errorf("LLMHelp missing split usage line")
	}

	js, err := LLMHelp("json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, `"Positional"`) || !strings.Contains(js, `"commit-ish"`) {
		t.Errorf("JSON projection missing the positional spec")
	}
}

// TestLLMHelp_envSectionIsProjected verifies the env-var section of --llm-help
// lists every override the config loader actually honors (projected from
// config.EnvOverrideNames) and every provider API-key variable — adding a row
// to either table updates the doc automatically.
func TestLLMHelp_envSectionIsProjected(t *testing.T) {
	txt, err := LLMHelp("")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range config.EnvOverrideNames() {
		if !strings.Contains(txt, name) {
			t.Errorf("--llm-help missing env override %s", name)
		}
	}
	for provider, envVar := range llm.ProviderEnvKeys() {
		if !strings.Contains(txt, envVar) {
			t.Errorf("--llm-help missing %s API key var %s", provider, envVar)
		}
	}
	if !strings.Contains(txt, "REGIT_LLM_API_KEY") {
		t.Errorf("--llm-help missing the tool-wide REGIT_LLM_API_KEY")
	}
}

// TestHelp_GitReInvocation checks every help surface swaps its prefix when the
// process was launched as git-re: terse usage lines, per-command usage, and
// example lines (which stay canonical "regit …" in the registry data).
func TestHelp_GitReInvocation(t *testing.T) {
	SetInvocation("git re")
	defer SetInvocation("regit")

	terse := HelpTerse()
	if !strings.Contains(terse, "git re <command> [flags]") {
		t.Fatalf("HelpTerse missing git re usage line:\n%s", terse)
	}
	if strings.Contains(terse, "regit <command>") {
		t.Fatalf("HelpTerse still shows regit usage line:\n%s", terse)
	}

	c, _ := Lookup("undo")
	hc := HelpCommand(c)
	if !strings.Contains(hc, "git re undo") {
		t.Fatalf("HelpCommand missing git re prefix:\n%s", hc)
	}

	doc, err := LLMHelp("text")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "[ok ] git re undo") {
		t.Fatalf("LLMHelp examples not rewritten to git re:\n%s", doc)
	}
}

// TestLLMHelp_MentionsBothForms pins the dual-invocation note under the
// default prefix, so agents learn about `git re` from `regit --llm-help` too.
func TestLLMHelp_MentionsBothForms(t *testing.T) {
	doc, err := LLMHelp("text")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "git re <command>") {
		t.Fatalf("LLMHelp missing the git re equivalence note:\n%s", doc)
	}
}

// TestExampleCmd_RewritesOnlyCanonicalPrefix guards the substitution helper.
func TestExampleCmd_RewritesOnlyCanonicalPrefix(t *testing.T) {
	SetInvocation("git re")
	defer SetInvocation("regit")
	if got := exampleCmd("regit undo --list"); got != "git re undo --list" {
		t.Fatalf("exampleCmd = %q", got)
	}
	if got := exampleCmd("some other text"); got != "some other text" {
		t.Fatalf("exampleCmd should leave non-canonical text alone, got %q", got)
	}
}

// flagLinesOf returns the lines of the "Flags:" section of a help render.
func flagLinesOf(out string) []string {
	var lines []string
	in := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.TrimSpace(line) == "Flags:":
			in = true
		case in && strings.HasPrefix(line, "  --"):
			lines = append(lines, line)
		case in:
			return lines
		}
	}
	return lines
}

func TestHelpCommand_marksRequiredFlags(t *testing.T) {
	c, _ := Lookup("redate")
	for _, line := range flagLinesOf(HelpCommand(c)) {
		if strings.HasPrefix(line, "  --strategy=") && !strings.Contains(line, "(required)") {
			t.Errorf("required --strategy not marked: %q", line)
		}
	}
}

func TestLLMHelp_showsFlagDefaults(t *testing.T) {
	out, err := LLMHelp("text")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(default: .)") {
		t.Error("LLMHelp should show the --repo default")
	}
}

func TestHelpCommand_omitsFalseBoolDefault(t *testing.T) {
	c, _ := Lookup("split")
	if out := HelpCommand(c); strings.Contains(out, "(default: false)") {
		t.Errorf("a false bool default is noise and doubles --execute's own note:\n%s", out)
	}
}

func TestHelpCommand_separatesEveryFlagFromItsHelp(t *testing.T) {
	for _, c := range Registry() {
		for _, line := range flagLinesOf(HelpCommand(c)) {
			flag := strings.Fields(line)[0]
			rest := strings.TrimPrefix(line, "  "+flag)
			if !strings.HasPrefix(rest, "  ") {
				t.Errorf("%s: flag runs into its help text: %q", c.Name, line)
			}
		}
	}
}

func TestHelpCommand_skipsEmptyFlagsHeading(t *testing.T) {
	for _, c := range Registry() {
		if len(c.Params) == 0 && strings.Contains(HelpCommand(c), "Flags:") {
			t.Errorf("%s has no flags but prints a Flags: heading", c.Name)
		}
	}
}

func TestLLMHelp_hasNoTrailingWhitespace(t *testing.T) {
	out, err := LLMHelp("text")
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(out, "\n") {
		if line != strings.TrimRight(line, " \t") {
			t.Errorf("line %d has trailing whitespace: %q", i+1, line)
		}
	}
}
