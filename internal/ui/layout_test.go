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
		{"narrow drops the right column, list fills width", 90, 40, 88, 0},       // 90 < 108 -> list-only, 90-2
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

// TestComputeLayout_narrowTerminalsFitTheFrame proves the fix for the frame
// overflow at widths between minTermWidth and ~108: below the threshold the right
// column is dropped (showRight=false) and the total rendered width (list pane +
// its border + right column + join frames) never exceeds the terminal.
func TestComputeLayout_narrowTerminalsFitTheFrame(t *testing.T) {
	for w := minTermWidth; w <= 140; w++ {
		d := computeLayout(w, 40)
		// Total columns the body join consumes: pane widths plus their borders.
		// list-only is one pane (2 border cols); with the right column it is two
		// panes (paneFrames) or three when detail is present (paneFrames3).
		total := d.listWidth + 2
		if d.showRight {
			frames := paneFrames
			if d.detailWidth > 0 {
				frames = paneFrames3
			}
			total = d.listWidth + d.detailWidth + d.rightWidth + frames
		}
		if total > w {
			t.Errorf("width %d: body total %d exceeds terminal (list=%d detail=%d right=%d showRight=%v)",
				w, total, d.listWidth, d.detailWidth, d.rightWidth, d.showRight)
		}
		// Below the list+right threshold the right column must be dropped.
		if w < listFloorWidth+rightPaneWidth+paneFrames && d.showRight {
			t.Errorf("width %d: right column should be hidden", w)
		}
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

func TestComputeLayout_InputWidth(t *testing.T) {
	if got := computeLayout(220, 40).inputWidth; got != inputMaxWidth {
		t.Fatalf("wide input width = %d, want %d", got, inputMaxWidth)
	}
	if got := computeLayout(60, 40).inputWidth; got != inputMinWidth {
		t.Fatalf("narrow input width = %d, want %d", got, inputMinWidth)
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

// TestComputeLayoutInputWidthClamp: modal input width tracks the terminal
// inside the readable band; hardcoded 42/48/30 widths are gone.
func TestComputeLayoutInputWidthClamp(t *testing.T) {
	cases := []struct {
		name  string
		termW int
		want  int
	}{
		{"narrow floor", 60, 50},  // 60-17=43 → floor 50
		{"mid tracks", 80, 63},    // 80-17=63, inside [50,75]
		{"wide ceiling", 200, 75}, // clamped to 75
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeLayout(tc.termW, 40).inputWidth; got != tc.want {
				t.Fatalf("inputWidth(%d) = %d, want %d", tc.termW, got, tc.want)
			}
		})
	}
}

// TestComputeLayoutDetailOverlayWidth: the '.' detail overlay tracks the
// terminal instead of the old hardcoded 50 columns.
func TestComputeLayoutDetailOverlayWidth(t *testing.T) {
	cases := []struct {
		name  string
		termW int
		want  int
	}{
		{"narrow floor", 45, 40},   // 45-12=33 → floor 40
		{"mid tracks", 80, 68},     // 80-12=68
		{"wide ceiling", 200, 100}, // clamped to 100
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeLayout(tc.termW, 40).detailOverlayWidth; got != tc.want {
				t.Fatalf("detailOverlayWidth(%d) = %d, want %d", tc.termW, got, tc.want)
			}
		})
	}
}

// TestComputeLayoutPreviewWidth: the pipeline preview takes ~80% of the
// usable width inside a readable band, instead of hugging its content.
func TestComputeLayoutPreviewWidth(t *testing.T) {
	cases := []struct {
		name  string
		termW int
		want  int
	}{
		{"narrow floor", 60, 56},   // 60*8/10=48 → floor 56
		{"mid tracks", 120, 96},    // 120*8/10
		{"wide ceiling", 240, 160}, // 240*8/10=192 → ceil 160
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeLayout(tc.termW, 40).previewWidth; got != tc.want {
				t.Fatalf("previewWidth(%d) = %d, want %d", tc.termW, got, tc.want)
			}
		})
	}
}

// TestComputeLayoutPanelHeight: the open panel's row budget is bounded so it
// can never crowd out the commit list. The overhead is 4 (title + footer +
// the RoundedBorder frame's top AND bottom rows — verified against the
// actual rendered height in TestPanelOpenAtMinTerminalKeepsListFloor).
func TestComputeLayoutPanelHeight(t *testing.T) {
	if got := panelHeightFor(5); got != 9 {
		t.Fatalf("panelHeightFor(5) = %d, want 9 (rows+title+footer+2 border rows)", got)
	}
	if got := panelHeightFor(50); got != panelMaxRows+panelHeightOverhead {
		t.Fatalf("panelHeightFor(50) = %d, want cap %d", got, panelMaxRows+panelHeightOverhead)
	}
}
