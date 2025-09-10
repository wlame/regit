// internal/capability/squash_render_test.go
package capability

import "testing"

func TestSquashArgMap_footerRoundTrips(t *testing.T) {
	spec := "6f3a^..a2df,88ac^..91de"
	line := Render("squash", SquashArgMap(spec))
	// Strip "regit " and re-parse; the positional must come back identical.
	toks := tokenize(line)
	if len(toks) < 2 || toks[0] != "regit" || toks[1] != "squash" {
		t.Fatalf("unexpected render: %q", line)
	}
	cmd, _ := Lookup("squash")
	a, err := Parse(cmd, toks[2:])
	if err != nil {
		t.Fatalf("Parse(%q): %v", line, err)
	}
	if len(a.Positional) != 1 || a.Positional[0] != spec {
		t.Errorf("positional round-trip failed: got %v, want [%s]", a.Positional, spec)
	}
	if !a.Bool("execute") {
		t.Error("--execute should round-trip")
	}
}
