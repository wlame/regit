package ui

import "testing"

func TestComputeLayout_ListWidthBounds(t *testing.T) {
	cases := []struct {
		name         string
		termW, termH int
		wantList     int
		wantDetail   int
	}{
		{"zero width renders at the 120 target floor", 0, 40, 56, 0},             // 120 < 142 -> 2-col, 120-64
		{"narrow clamps up to the list floor", 90, 40, listFloorWidth, 0},        // 90-64=26 -> 44, 2-col
		{"target floor width", 120, 40, 56, 0},                                   // 120-64, 2-col
		{"wide terminal caps the list at 80, detail stretches", 220, 40, 80, 74}, // avail=220-66=154; list capped 80; detail=154-80
		{"ultrawide caps list at 80, detail stretches", 400, 40, 80, 94},         // avail=240-66=174; list capped 80; detail=174-80
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeLayout(tc.termW, tc.termH)
			if got.listWidth != tc.wantList {
				t.Fatalf("listWidth = %d, want %d", got.listWidth, tc.wantList)
			}
			if got.detailWidth != tc.wantDetail {
				t.Fatalf("detailWidth = %d, want %d", got.detailWidth, tc.wantDetail)
			}
			if got.contentWidth > maxContentWidth {
				t.Fatalf("contentWidth = %d exceeds the %d cap", got.contentWidth, maxContentWidth)
			}
		})
	}
}

func TestComputeLayout_DetailStretchesPastMinimum(t *testing.T) {
	got := computeLayout(220, 40)
	if got.listWidth != 80 {
		t.Fatalf("list should cap at 80, got %d", got.listWidth)
	}
	if got.detailWidth <= detailMinWidth {
		t.Fatalf("detail should stretch past the %d minimum, got %d", detailMinWidth, got.detailWidth)
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
