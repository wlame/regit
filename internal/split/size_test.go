// internal/split/size_test.go
package split

import (
	"testing"

	"github.com/wlame/regit/internal/model"
)

func nHunks(lines int) []model.Hunk {
	// One hunk whose additions account for the requested changed-line count.
	return []model.Hunk{{ID: "H1", Additions: lines}}
}

func TestEffectiveMaxCommits_scalesWithSize(t *testing.T) {
	cases := []struct{ lines, want int }{
		{40, 2}, {120, 3}, {200, 4}, {10000, 4},
	}
	for _, c := range cases {
		got := EffectiveMaxCommits(nHunks(c.lines), 4, 40)
		if got != c.want {
			t.Errorf("lines=%d: got %d, want %d", c.lines, got, c.want)
		}
	}
}

func TestShouldSkipSplit_tinyChange(t *testing.T) {
	skip, _ := ShouldSkipSplit(nHunks(5), 20)
	if !skip {
		t.Fatal("expected skip for a 5-line change")
	}
}

func TestShouldSkipSplit_singleHunkSingleFile(t *testing.T) {
	skip, _ := ShouldSkipSplit([]model.Hunk{{ID: "H1", File: "a", Additions: 100}}, 20)
	if !skip {
		t.Fatal("expected skip for a single hunk in a single file")
	}
}

func TestShouldSkipSplit_binaryOnly(t *testing.T) {
	skip, _ := ShouldSkipSplit([]model.Hunk{
		{ID: "H1", File: "a.png", Binary: true},
		{ID: "H2", File: "b.png", Binary: true},
	}, 20)
	if !skip {
		t.Fatal("expected skip for binary-only change")
	}
}

// TestShouldSkipSplit_twoHunksSingleFile asserts that a change with two hunks
// confined to a single file is NOT skipped — splitting one file into multiple
// commits is legitimate.
func TestShouldSkipSplit_twoHunksSingleFile(t *testing.T) {
	skip, _ := ShouldSkipSplit([]model.Hunk{
		{ID: "H1", File: "a.go", Additions: 30},
		{ID: "H2", File: "a.go", Additions: 30},
	}, 20)
	if skip {
		t.Fatal("expected NOT to skip a two-hunk single-file change")
	}
}
