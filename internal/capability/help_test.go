package capability

import (
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
