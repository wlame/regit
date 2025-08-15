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

// TestLinkDatesFalse_roundTrip guards the fidelity of the --link-dates=false
// flag through a full Render → tokenize → Parse cycle. link-dates has
// Default="true", so a value of "false" passes the default-omission check but
// was previously silently dropped in the bool branch — producing a command that
// re-ran with link-dates=true (a different behaviour).
func TestLinkDatesFalse_roundTrip(t *testing.T) {
	c := mustLookup(t, "redate")
	// Minimal redate argMap with execute=true and linkDates=false.
	// strategy and time are required for Validate; linkDates=false is the focus.
	args := map[string]string{
		"strategy":   "Even-Spread",
		"time":       "Working-hours",
		"execute":    "true",
		"link-dates": "false",
	}
	line := Render("redate", args)
	toks := tokenize(line)
	// Trim "regit redate" prefix before parsing.
	if len(toks) < 2 {
		t.Fatalf("too few tokens: %v", toks)
	}
	parsed, err := Parse(c, toks[2:])
	if err != nil {
		t.Fatalf("re-parse err: %v from %q", err, line)
	}
	if !parsed.Has("link-dates") {
		t.Errorf("--link-dates=false was silently dropped; rendered line: %q", line)
	}
	if parsed.Bool("link-dates") {
		t.Errorf("link-dates should be false after round-trip, got true; line: %q", line)
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
