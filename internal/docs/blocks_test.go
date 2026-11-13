package docs

import (
	"strings"
	"testing"
)

// Renderer tests assert stable anchors against the live registry/keymap rather
// than full golden strings — the README itself is the golden copy (drift test,
// Task 4); these only pin the shape and the facts M3 exists to fix.
func TestBlocks_containExpectedAnchors(t *testing.T) {
	b := Blocks()
	anchors := map[string][]string{
		"commands":       {"| Command | What it does |", "`regit reauthor`", "`regit redate --strategy=…`", "`regit retitle`", "`regit undo`"},
		"strategies":     {"Even Spread", "Backwards-from-Deadline"},
		"time-models":    {"Evening hours", "Non-working hours", "Preserve original"},
		"env-vars":       {"`REGIT_TIMEZONE`", "`REGIT_REWORD_VARIANTS`"},
		"verbs-overview": {"`squash`", "`reauthor`", "`drop-bodies`"},
		"verbs-detail":   {"`squash`", "`reauthor`"},
		"runfiles-map":   {"`run_dropbodies.go`", "`run_squash.go`", "`run_reauthor.go`"},
	}
	for name, wants := range anchors {
		content, ok := b[name]
		if !ok {
			t.Errorf("Blocks() missing %q", name)
			continue
		}
		for _, w := range wants {
			if !strings.Contains(content, w) {
				t.Errorf("block %q missing anchor %q\ncontent:\n%s", name, w, content)
			}
		}
	}
}

// The redate row must show only unconditionally required flags — --pin, --anchor
// etc. are Required but gated by AppliesIf and must NOT appear.
func TestCommandsBlock_omitsConditionallyRequiredFlags(t *testing.T) {
	c := Blocks()["commands"]
	for _, banned := range []string{"--pin", "--pick", "--anchor", "--deadline", "--compress-day", "--time-fixed"} {
		if strings.Contains(c, banned) {
			t.Errorf("commands block leaks conditionally-required flag %s:\n%s", banned, c)
		}
	}
}

// Files() and Blocks() must agree exactly: every declared name renderable,
// every rendered name claimed by exactly one file.
func TestFiles_matchBlocksBijectively(t *testing.T) {
	rendered := Blocks()
	claimed := map[string]string{}
	for _, f := range Files() {
		for _, n := range f.Blocks {
			if prev, dup := claimed[n]; dup {
				t.Errorf("block %q claimed by both %s and %s", n, prev, f.Path)
			}
			claimed[n] = f.Path
			if _, ok := rendered[n]; !ok {
				t.Errorf("%s declares unrenderable block %q", f.Path, n)
			}
		}
	}
	for n := range rendered {
		if _, ok := claimed[n]; !ok {
			t.Errorf("rendered block %q is not claimed by any file", n)
		}
	}
}
