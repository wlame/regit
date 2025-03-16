package ui

// layout.go centralizes the TUI's region-sizing math. Every screen region's
// width/height is derived purely from the terminal size by computeLayout, so the
// interface uses the whole window (clamped to a readable band) and reflows on
// resize instead of relying on fixed constants. Keeping this pure — no Bubble
// Tea, no rendering, no git — makes the breakpoint logic trivially testable and
// gives later redesign phases one seam to extend.

const (
	// maxContentWidth caps how many columns of content we ever lay out, so the
	// UI stays scannable on ultrawide terminals. The readable target floor is
	// 120 columns; below that the layout degrades but still renders.
	maxContentWidth = 240

	// listFloorWidth/listCeilWidth bound the commit-list pane. The floor matches
	// the pre-redesign minimum so narrow terminals do not regress; the ceiling
	// stops subjects from stretching uncomfortably wide.
	listFloorWidth = 44
	listCeilWidth  = 160

	// paneFrames is the columns consumed by the list and right panes' left/right
	// borders (2 each). rightPaneWidth (the fixed calendar column) is defined in
	// view.go.
	paneFrames = 4

	// chromeRows is the vertical budget taken by the title bar, status line,
	// keybar, and the spacing around the body. minBodyHeight keeps the body
	// usable on very short terminals.
	chromeRows    = 8
	minBodyHeight = 8

	// editorChrome is the columns the editor modal spends on the cursor, label
	// column, padding, and border; the field width is clamped to a readable band.
	editorChrome   = 17
	editorMaxWidth = 75
	editorMinWidth = 50
)

// layoutDims is the computed column/row budget for the main screen, derived
// entirely from the terminal size. Only listWidth and editorWidth are consumed
// by production code today; contentWidth, rightWidth, and bodyHeight are
// reserved for later redesign phases (3-zone responsive breakpoints) and not yet
// wired — height still flows through the listHeight() method, width through
// rightPaneWidth const.
type layoutDims struct {
	contentWidth int // terminal width, capped at maxContentWidth (reserved for later phases; not yet wired)
	listWidth    int // commit-list pane width (border excluded)
	rightWidth   int // right column (calendar/strategy) width — currently fixed (reserved for later phases; not yet wired)
	bodyHeight   int // rows available to the list/right body (reserved for later phases; not yet wired)
	editorWidth  int // editor-modal field width
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

	// The list takes every column not claimed by the fixed right column and the
	// two panes' borders, within readable bounds.
	listW := usable - rightPaneWidth - paneFrames
	if listW < listFloorWidth {
		listW = listFloorWidth
	}
	if listW > listCeilWidth {
		listW = listCeilWidth
	}

	bodyH := termH - chromeRows
	if bodyH < minBodyHeight {
		bodyH = minBodyHeight
	}

	editorW := usable - editorChrome
	if editorW > editorMaxWidth {
		editorW = editorMaxWidth
	}
	if editorW < editorMinWidth {
		editorW = editorMinWidth
	}

	return layoutDims{
		contentWidth: usable,
		listWidth:    listW,
		rightWidth:   rightPaneWidth,
		bodyHeight:   bodyH,
		editorWidth:  editorW,
	}
}
