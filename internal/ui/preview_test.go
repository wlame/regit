package ui

import (
	"testing"

	"github.com/mattn/go-runewidth"
)

// TestTruncate_measuresDisplayCellsNotRunes proves truncate bounds output by
// terminal cells, so wide (CJK/emoji) glyphs cannot overflow a fixed-width column
// and wrap the row. A rune-count truncation would let "十" (2 cells each) through.
func TestTruncate_measuresDisplayCellsNotRunes(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		width int
	}{
		{"ascii fits", "hello", 10},
		{"ascii cut", "hello world", 5},
		{"cjk cut", "修复登录问题的一个非常长的提交", 8},
		{"emoji cut", "fix 🎉🎉🎉🎉🎉🎉 bug", 6},
		{"mixed", "réparé 修复 done", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := truncate(tc.in, tc.width)
			if w := runewidth.StringWidth(out); w > tc.width {
				t.Errorf("truncate(%q, %d) = %q has display width %d > %d", tc.in, tc.width, out, w, tc.width)
			}
		})
	}
	if truncate("anything", 0) != "" {
		t.Error("truncate to width 0 should be empty")
	}
}
