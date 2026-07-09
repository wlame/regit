package docs

import (
	"strings"
	"testing"
)

// FuzzSpliceBlocks fuzzes the marked-block splicer with arbitrary documents
// and block content: it must never panic, and when it succeeds the operation
// must be idempotent (splicing the same blocks into its own output is a
// no-op) with the begin/end markers preserved.
func FuzzSpliceBlocks(f *testing.F) {
	f.Add("before\n<!-- regit:begin x -->\nold\n<!-- regit:end x -->\nafter\n", "x", "new content")
	f.Add("<!-- regit:begin a -->\n<!-- regit:end a -->\n", "a", "")
	f.Add("no markers at all\n", "x", "y")
	f.Add("<!-- regit:end orphan -->\n", "orphan", "z")
	f.Add("<!-- regit:begin unclosed -->\n", "unclosed", "z")
	f.Add("<!-- regit:begin x -->\nnested <!-- regit:begin x -->\n<!-- regit:end x -->\n", "x", "n")

	f.Fuzz(func(t *testing.T, src, name, content string) {
		blocks := map[string]string{name: content}
		out, err := SpliceBlocks(src, blocks)
		if err != nil {
			return // malformed marker structure is allowed to error
		}
		// Idempotence: a second splice of the same blocks changes nothing.
		again, err := SpliceBlocks(out, blocks)
		if err != nil {
			t.Fatalf("second splice errored on splicer output: %v\noutput: %q", err, out)
		}
		if again != out {
			t.Fatalf("splice not idempotent:\nfirst : %q\nsecond: %q", out, again)
		}
		// A document that had the block keeps a begin/end pair around the new
		// content. (Exact marker COUNTS may legitimately shrink: content between
		// markers — including a stray nested begin — is replaced wholesale.)
		begin := "<!-- regit:begin " + name + " -->"
		end := "<!-- regit:end " + name + " -->"
		if strings.Contains(src, begin) && strings.Contains(src, end) {
			if !strings.Contains(out, begin) || !strings.Contains(out, end) {
				t.Fatalf("spliced block lost its markers:\nin : %q\nout: %q", src, out)
			}
		}
	})
}
