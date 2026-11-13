package capability

import (
	"testing"

	"github.com/wlame/regit/internal/termsafe"
)

// FuzzRenderParseRoundTrip fuzzes the footer-fidelity contract: for arbitrary
// user-controlled flag values, the line Render emits must re-Parse to exactly
// the values it rendered (post-sanitization) — quoting, control-byte
// stripping, and tokenization may never disagree.
func FuzzRenderParseRoundTrip(f *testing.F) {
	f.Add("^feat: ", "")
	f.Add("a b", "c'd")
	f.Add(`$(rm -rf) "quoted"`, "back\\slash")
	f.Add("tab\there", "new\nline")
	f.Add("\x1b]0;title\x07", "unicode café ☕")
	f.Add("HEAD~3..HEAD", "*glob?[x]!")

	cmd, ok := Lookup("retitle")
	if !ok {
		f.Fatal("retitle not in registry")
	}

	f.Fuzz(func(t *testing.T, match, replace string) {
		wantMatch := termsafe.Line(match)
		wantReplace := termsafe.Line(replace)
		if wantMatch == "" {
			// An all-control match sanitizes away; RetitleArgMap would omit the
			// required flag and Parse rightly rejects the line. Not a round-trip
			// case.
			t.Skip()
		}
		line := Render("retitle", RetitleArgMap(RetitleOptions{Match: match, Replace: replace}))
		toks := tokenize(line)
		if len(toks) < 2 || toks[0] != "regit" || toks[1] != "retitle" {
			t.Fatalf("unexpected rendered shape %q -> %v", line, toks)
		}
		parsed, err := Parse(cmd, toks[2:])
		if err != nil {
			t.Fatalf("rendered line does not re-parse: %v\nline: %q", err, line)
		}
		if got := parsed.String("match"); got != wantMatch {
			t.Errorf("match round-trip: got %q, want %q (line %q)", got, wantMatch, line)
		}
		if got := parsed.String("replace"); got != wantReplace {
			t.Errorf("replace round-trip: got %q, want %q (line %q)", got, wantReplace, line)
		}
	})
}
