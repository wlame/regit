package capability

import (
	"strings"
	"testing"

	"github.com/wlame/regit/internal/strategy"
)

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

// TestRetitleArgMap_spacedMatch_roundTrip guards Render's footer fidelity for
// a match value that contains whitespace. Previously the unquoted emission
// `--match=^feat: ` split on the space, so tokenize → Parse lost the trailing
// space. After the fix, shellQuote wraps the value and tokenize reconstructs it.
func TestRetitleArgMap_spacedMatch_roundTrip(t *testing.T) {
	argMap := RetitleArgMap("^feat: ", "", "")
	line := Render("retitle", argMap)
	toks := tokenize(line)
	// First two tokens are "regit retitle"; the rest are flags.
	if len(toks) < 2 || toks[0] != "regit" || toks[1] != "retitle" {
		t.Fatalf("unexpected tokens: %v", toks)
	}
	parsed, err := Parse(mustLookup(t, "retitle"), toks[2:])
	if err != nil {
		t.Fatalf("re-parse err: %v from %q", err, line)
	}
	if got := parsed.String("match"); got != "^feat: " {
		t.Errorf("match round-trip: got %q, want %q (rendered: %q)", got, "^feat: ", line)
	}
}

// TestShellQuote verifies that space-free values are returned unchanged and that
// values with spaces or metacharacters are single-quoted.
func TestShellQuote(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "''"},
		{"simple", "simple"},
		{"ollama/qwen2.5-coder", "ollama/qwen2.5-coder"},
		{"^feat: ", "'^feat: '"},
		{"don't", `'don'\''t'`},
		{"a b", "'a b'"},
		{"$HOME", "'$HOME'"},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRenderWrapsLongInvocation: a long command wraps with a backslash continuation
// and still round-trips through tokenize -> Parse.
func TestRenderWrapsLongInvocation(t *testing.T) {
	args := RedateArgMap("Manual-Pin", "Working-hours", strategy.Params{
		Quotas: map[string]int{
			"2026-01-01": 1, "2026-01-02": 2, "2026-01-03": 3,
			"2026-01-04": 4, "2026-01-05": 5,
		},
	}, true)
	line := Render("redate", args)
	if !strings.Contains(line, "\\\n") {
		t.Fatalf("expected a wrapped continuation in:\n%s", line)
	}
	toks := tokenize(line)
	parsed, err := Parse(mustLookup(t, "redate"), toks[2:])
	if err != nil {
		t.Fatalf("wrapped line failed to re-parse: %v\n%s", err, line)
	}
	if parsed.String("strategy") != "Manual-Pin" {
		t.Errorf("strategy = %q after wrap", parsed.String("strategy"))
	}
	if got := parsed.KeyVals("pin"); len(got) != 5 {
		t.Errorf("pin lost across wrap: %v", got)
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
