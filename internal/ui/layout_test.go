package ui

import "testing"

func TestComputeLayout_ListWidthBounds(t *testing.T) {
	// list width == usable - rightPaneWidth(60) - paneFrames(4) == usable - 64,
	// clamped to [listFloorWidth, listCeilWidth].
	cases := []struct {
		name         string
		termW, termH int
		wantList     int
	}{
		{"zero width renders at the 120 target floor", 0, 40, 56},               // 120-64
		{"narrow clamps up to the list floor", 90, 40, listFloorWidth},          // 90-64=26 -> 44
		{"target floor width", 120, 40, 56},                                     // 120-64
		{"wide terminal uses the extra width", 220, 40, 156},                    // 220-64
		{"ultrawide caps at 240 then the list ceiling", 400, 40, listCeilWidth}, // 240-64=176 -> 160
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeLayout(tc.termW, tc.termH)
			if got.listWidth != tc.wantList {
				t.Fatalf("listWidth = %d, want %d", got.listWidth, tc.wantList)
			}
			if got.contentWidth > maxContentWidth {
				t.Fatalf("contentWidth = %d exceeds the %d cap", got.contentWidth, maxContentWidth)
			}
		})
	}
}

func TestComputeLayout_EditorWidth(t *testing.T) {
	if got := computeLayout(220, 40).editorWidth; got != editorMaxWidth {
		t.Fatalf("wide editor width = %d, want %d", got, editorMaxWidth)
	}
	if got := computeLayout(60, 40).editorWidth; got != editorMinWidth {
		t.Fatalf("narrow editor width = %d, want %d", got, editorMinWidth)
	}
}

func TestComputeLayout_BodyHeightFloor(t *testing.T) {
	if got := computeLayout(120, 10).bodyHeight; got != minBodyHeight {
		t.Fatalf("tiny terminal bodyHeight = %d, want floor %d", got, minBodyHeight)
	}
	if got := computeLayout(120, 40).bodyHeight; got != 32 { // 40 - chromeRows(8)
		t.Fatalf("bodyHeight = %d, want 32", got)
	}
}
