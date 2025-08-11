package capability

import "testing"

func TestRenderRoundTrip(t *testing.T) {
	// Use the reword command (real registry entry) with a representative argMap.
	args := map[string]string{
		"llm":     "ollama/qwen2.5-coder",
		"execute": "true",
	}
	line := Render("reword", args)
	if line == "" {
		t.Fatal("Render returned empty")
	}
	toks := tokenize(line)
	// First two tokens are "regit reword"; the rest are flags+positionals.
	if len(toks) < 2 || toks[0] != "regit" || toks[1] != "reword" {
		t.Fatalf("tokens = %v", toks)
	}
	parsed, err := Parse(mustLookup(t, "reword"), toks[2:])
	if err != nil {
		t.Fatalf("re-parse err: %v from %q", err, line)
	}
	if parsed.String("llm") != "ollama/qwen2.5-coder" || !parsed.Bool("execute") {
		t.Errorf("round-trip lost values: %q", line)
	}
}

func TestRenderOmitsDefaults(t *testing.T) {
	// repo defaults to "."; an argMap repeating the default must not appear.
	line := Render("reword", map[string]string{"repo": ".", "execute": "true"})
	if contains(tokenize(line), "--repo=.") {
		t.Errorf("Render should omit default-valued flags: %q", line)
	}
}

func mustLookup(t *testing.T, name string) Command {
	t.Helper()
	c, ok := Lookup(name)
	if !ok {
		t.Fatalf("missing %q", name)
	}
	return c
}
