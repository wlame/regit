package ui

// layout.go centralizes the TUI's region-sizing math. Every screen region's
// width/height is derived purely from the terminal size by computeLayout, so the
// interface uses the whole window (clamped to a readable band) and reflows on
// resize instead of relying on fixed constants. Keeping this pure — no Bubble
// Tea, no rendering, no git — makes the breakpoint logic trivially testable and
// gives future layout work one seam to extend.

const (
	// maxContentWidth caps how many columns of content we ever lay out, so the
	// UI stays scannable on ultrawide terminals. The readable target floor is
	// 120 columns; below that the layout degrades but still renders.
	maxContentWidth = 240

	// listFloorWidth/listCeilWidth bound the commit-list pane. The floor is a
	// conservative minimum so narrow terminals do not regress; the ceiling
	// caps the list at a scannable ~80 columns so the detail pane absorbs the rest
	// of a wide terminal and stretches.
	listFloorWidth = 44
	listCeilWidth  = 80

	// paneFrames is the columns consumed by the list and right panes' left/right
	// borders (2 each). rightPaneWidth (the fixed calendar column) is defined in
	// view.go.
	paneFrames = 4

	// detailMinWidth is the narrowest the commit-detail column may be before we
	// drop it entirely and fall back to the two-column layout (detail-on-overlay).
	detailMinWidth = 32

	// paneFrames3 is the columns consumed by three panes' left/right borders
	// (2 each) when the detail column is present, vs paneFrames for two panes.
	paneFrames3 = 6

	// chromeRows is the vertical budget taken by the title bar, status line,
	// keybar, and the spacing around the body. minBodyHeight keeps the body
	// usable on very short terminals.
	chromeRows    = 8
	minBodyHeight = 8

	// inputChrome is the columns a modal spends on the cursor, label column,
	// padding, and border around a single-line text input; the field width is
	// clamped to a readable band shared by the editor and every modal input.
	inputChrome   = 17
	inputMaxWidth = 75
	inputMinWidth = 50

	// detailOverlayMargin/Floor/Ceil bound the on-demand ('.') detail overlay:
	// wide enough to read a body, capped so lines stay scannable on huge
	// terminals. The margin keeps the centered overlay visually distinct from
	// the full-screen view behind it.
	detailOverlayMargin = 12
	detailOverlayFloor  = 40
	detailOverlayCeil   = 100

	// previewFloor/Ceil bound the pipeline preview overlay at ~80% of the
	// usable width: wide enough for before→after date pairs, capped for
	// scannability on ultrawide terminals.
	previewFloor = 56
	previewCeil  = 160
)

// layoutDims is the computed column/row budget for the main screen, derived
// entirely from the terminal size. listWidth, detailWidth, and inputWidth are
// consumed by production rendering today; contentWidth, rightWidth, and
// bodyHeight are reserved for future work (3-zone responsive
// breakpoints) and not yet wired — height still flows through the listHeight()
// method, width through rightPaneWidth const.
type layoutDims struct {
	contentWidth       int  // terminal width, capped at maxContentWidth (reserved for future work; not yet wired)
	listWidth          int  // commit-list pane width (border excluded)
	detailWidth        int  // commit-detail pane width (0 when the detail column is hidden)
	rightWidth         int  // right column (calendar/strategy) width — currently fixed (reserved for future work; not yet wired)
	showRight          bool // whether the fixed right column fits; false → list-only layout on narrow terminals
	bodyHeight         int  // rows available to the list/right body (reserved for future work; not yet wired)
	inputWidth         int  // single-line text-input width for the editor and every modal field
	detailOverlayWidth int  // width of the on-demand commit-detail overlay ('.')
	previewWidth       int  // pipeline (stage→preview→apply) overlay width
}

// computeLayout turns a terminal size into the region budget for the main
// screen. termW/termH are 0 before the first tea.WindowSizeMsg arrives; in that
// case we render at the 120-column target floor until the real size is known.
// Before the first size message, all computed field values assume this 120-column
// floor, so no hard-coded pre-size width should be treated as load-bearing.
func computeLayout(termW, termH int) layoutDims {
	if termW <= 0 {
		termW = 120
	}

	usable := termW
	if usable > maxContentWidth {
		usable = maxContentWidth
	}

	// Decide whether the fixed right column (calendar/strategy) fits at all. On a
	// terminal too narrow to seat even the list floor plus the right column, we drop
	// the right column entirely and give the whole width to the list, rather than
	// joining a floor-width list to a fixed right column and overflowing the frame
	// (which is what happened for every width between minTermWidth and ~108).
	showRight := usable >= listFloorWidth+rightPaneWidth+paneFrames

	var listW, detailW int
	switch {
	case !showRight:
		// List-only: the list pane spends the whole usable width minus its own
		// left/right border. The detail and right columns are hidden; detail is
		// still reachable as an on-demand overlay.
		listW = usable - 2
		if listW < listFloorWidth {
			listW = listFloorWidth
		}
	case usable >= listFloorWidth+detailMinWidth+rightPaneWidth+paneFrames3:
		// Wide enough to seat a detail column between the list and the right column.
		// The list keeps priority: it grows to listCeilWidth first, detail takes the
		// rest.
		avail := usable - rightPaneWidth - paneFrames3
		listW = avail - detailMinWidth
		if listW > listCeilWidth {
			listW = listCeilWidth
		}
		if listW < listFloorWidth {
			listW = listFloorWidth
		}
		detailW = avail - listW
	default:
		// Two-column: list + right, no detail pane (overlay on demand).
		listW = usable - rightPaneWidth - paneFrames
		if listW < listFloorWidth {
			listW = listFloorWidth
		}
		if listW > listCeilWidth {
			listW = listCeilWidth
		}
	}

	bodyH := termH - chromeRows
	if bodyH < minBodyHeight {
		bodyH = minBodyHeight
	}

	inputW := usable - inputChrome
	if inputW > inputMaxWidth {
		inputW = inputMaxWidth
	}
	if inputW < inputMinWidth {
		inputW = inputMinWidth
	}

	detailOverlayW := usable - detailOverlayMargin
	if detailOverlayW > detailOverlayCeil {
		detailOverlayW = detailOverlayCeil
	}
	if detailOverlayW < detailOverlayFloor {
		detailOverlayW = detailOverlayFloor
	}

	rightW := rightPaneWidth
	if !showRight {
		rightW = 0
	}

	previewW := usable * 8 / 10
	if previewW > previewCeil {
		previewW = previewCeil
	}
	if previewW < previewFloor {
		previewW = previewFloor
	}

	return layoutDims{
		contentWidth:       usable,
		listWidth:          listW,
		detailWidth:        detailW,
		rightWidth:         rightW,
		showRight:          showRight,
		bodyHeight:         bodyH,
		inputWidth:         inputW,
		detailOverlayWidth: detailOverlayW,
		previewWidth:       previewW,
	}
}
