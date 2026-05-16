package capability

import (
	"encoding/json"
	"strings"
	"testing"
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
