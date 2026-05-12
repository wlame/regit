package termsafe

import (
	"strings"
	"testing"
)

func TestLine_stripsEscapesAndFoldsNewlines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text unchanged", "Fix the login bug", "Fix the login bug"},
		{"OSC window-title spoof removed", "\x1b]0;pwned\x07Subject", "Subject"},
		{"OSC-52 clipboard write removed", "hi\x1b]52;c;ZXZpbA==\x07there", "hithere"},
		{"CSI color codes removed", "\x1b[31mred\x1b[0m text", "red text"},
		{"cursor-move CSI removed", "a\x1b[2Jb", "ab"},
		{"lone ESC removed", "a\x1bb", "ab"},
		{"newline folded to space", "line one\nline two", "line one line two"},
		{"tab folded to space", "a\tb", "a b"},
		{"raw 8-bit CSI byte removed", "a\x9bb", "ab"},
		{"valid UTF-8 C1 control removed", "a\u009bb", "ab"},
		{"unicode preserved", "café → résumé ✓", "café → résumé ✓"},
		{"trailing ESC dropped", "text\x1b", "text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Line(tc.in); got != tc.want {
				t.Errorf("Line(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.ContainsRune(Line(tc.in), 0x1b) {
				t.Errorf("Line(%q) still contains ESC", tc.in)
			}
		})
	}
}

func TestBlock_preservesNewlinesButStripsEscapes(t *testing.T) {
	in := "First line\n\nBody with \x1b[31mcolor\x1b[0m and a\ttab\nlast"
	want := "First line\n\nBody with color and a\ttab\nlast"
	if got := Block(in); got != want {
		t.Errorf("Block() = %q, want %q", got, want)
	}
}

func TestLine_fastPathReturnsInputUnchanged(t *testing.T) {
	in := "no control bytes here"
	if got := Line(in); got != in {
		t.Errorf("Line(%q) = %q, want identity on clean input", in, got)
	}
}
