package gitio

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wlame/regit/internal/model"
)

// FuzzParseDiff fuzzes the unified-diff hunk extractor with arbitrary bytes:
// it must never panic, and every hunk it does produce must carry the unique
// sequential IDs the split coverage validator depends on.
func FuzzParseDiff(f *testing.F) {
	f.Add("diff --git a/x b/x\nindex 000..111 100644\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n")
	f.Add("diff --git a/bin b/bin\nBinary files a/bin and b/bin differ\n")
	f.Add("diff --git a/m b/m\nold mode 100644\nnew mode 100755\n")
	f.Add("diff --git a/two b/two\n--- a/two\n+++ b/two\n@@ -1 +1 @@\n-x\n+y\n@@ -5 +5 @@\n-p\n+q\n")
	f.Add("not a diff at all\n@@ stray hunk header @@\n")
	f.Add("")

	f.Fuzz(func(t *testing.T, diff string) {
		hunks := parseDiff(diff)
		seen := map[string]bool{}
		for i, h := range hunks {
			if want := fmt.Sprintf("H%d", i+1); h.ID != want {
				t.Fatalf("hunk %d has ID %q, want %q", i, h.ID, want)
			}
			if seen[h.ID] {
				t.Fatalf("duplicate hunk ID %q", h.ID)
			}
			seen[h.ID] = true
		}
	})
}

// FuzzBuildMessage fuzzes message assembly across edit shapes: the result
// always ends in exactly one newline block boundary, an untouched edit replays
// the raw message verbatim, and a subject-only edit never alters the bytes
// after the first line.
func FuzzBuildMessage(f *testing.F) {
	f.Add("Subject only", "", uint8(0))
	f.Add("Title\n\nBody line.", "New title", uint8(1))
	f.Add("Wrapped first\nparagraph line two", "New", uint8(1))
	f.Add("Title\n\n\ntriple separated body", "x", uint8(2))
	f.Add("Ünïcode ☕\n\ncafé", "", uint8(3))
	f.Add("", "", uint8(0))

	f.Fuzz(func(t *testing.T, message, newSubject string, mode uint8) {
		c := model.Commit{Message: message}
		c.Subject, c.Body = model.SplitMessage(message)

		var e model.Edit
		switch mode % 4 {
		case 1:
			e.NewSubject = &newSubject
		case 2:
			e.DropBody = true
		case 3:
			e.NewBody = &newSubject
		}

		got := buildMessage(c, e)
		if !strings.HasSuffix(got, "\n") {
			t.Fatalf("message must end with a newline: %q", got)
		}
		raw := strings.TrimRight(message, "\n")
		switch mode % 4 {
		case 0:
			if got != raw+"\n" {
				t.Fatalf("untouched edit must replay verbatim: %q -> %q", message, got)
			}
		case 1:
			// Subject-only edit: everything after the first line is verbatim.
			_, rest, hadRest := strings.Cut(raw, "\n")
			want := newSubject + "\n"
			if hadRest {
				want = newSubject + "\n" + rest + "\n"
			}
			if got != want {
				t.Fatalf("subject edit reshaped the message: %q -> %q (want %q)", message, got, want)
			}
		}
	})
}
