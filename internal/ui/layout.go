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
	// caps the list at a scannable ~80 columns so the flow zone absorbs the rest
	// of a wide terminal and stretches.
	listFloorWidth = 44
	listCeilWidth  = 80

	// paneFrames is the columns consumed by the list and flow-zone panes'
	// left/right borders (2 each).
	paneFrames = 4

	// flowMinWidth is the narrowest the flow zone (calendar or commit detail)
	// may be before we drop it entirely and fall back to the list-only layout
	// (detail reachable via the '.' overlay). Below their natural width, the
	// calendar clamps down to it (ws_dates.go) and the detail pane just renders
	// narrower — neither forces the column wider the way a fixed column did.
	flowMinWidth = 40

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
// entirely from the terminal size. listWidth, flowWidth, and inputWidth are
// consumed by production rendering today; contentWidth and bodyHeight are
// reserved for future work (3-zone responsive breakpoints) and not yet wired —
// height still flows through the listHeight() method.
type layoutDims struct {
	contentWidth       int  // terminal width, capped at maxContentWidth (reserved for future work; not yet wired)
	listWidth          int  // commit-list pane width (border excluded)
	flowWidth          int  // flow-zone pane width; on a one-column terminal, the list's width
	showRight          bool // whether list and flow zone fit side by side; false → one column
	bodyHeight         int  // rows available to the body (reserved for future work; not yet wired)
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
	return computeLayoutWith(termW, termH, flowMinWidth)
}

// flowZoneMinWidths is the flow column's minimum width per open flow, for flows
// whose widget needs more than flowMinWidth beside the list. The redate flow's
// calendar wraps every week row below its natural width.
var flowZoneMinWidths = map[flowID]int{
	flowRedate: rightPaneWidth,
}

// layout is the screen layout for the model's size, open flow, and open
// wizard.
func (m Model) layout() layoutDims {
	return m.layoutWithWizard(m.zoneWizardOpen())
}

// layoutWithWizard is the layout with or without a wizard in the flow zone.
// Launching a wizard sizes its fields with true before its mode is set.
func (m Model) layoutWithWizard(wizardOpen bool) layoutDims {
	flowMin, ok := flowZoneMinWidths[m.flow]
	if !ok {
		flowMin = flowMinWidth
	}
	if wizardOpen {
		flowMin = max(flowMin, zoneWizardMinWidth)
	}
	return computeLayoutWith(m.width, m.height, flowMin)
}

// computeLayoutWith is computeLayout with the flow column's minimum width as a
// parameter. A terminal too narrow for the list floor plus flowMin beside it
// gets the one-column layout.
func computeLayoutWith(termW, termH, flowMin int) layoutDims {
	if termW <= 0 {
		termW = 120
	}

	usable := termW
	if usable > maxContentWidth {
		usable = maxContentWidth
	}

	// Decide whether the flow zone fits at all. On a terminal too narrow to
	// seat even the list floor plus a usable flow column, drop the flow zone
	// entirely and give the whole width to the list, rather than joining a
	// floor-width list to a too-narrow flow column and overflowing the frame.
	showRight := usable >= listFloorWidth+flowMin+paneFrames

	var listW, flowW int
	switch {
	case !showRight:
		// One column: the list pane spends the whole usable width minus its own
		// left/right border. There is no side-by-side flow zone; an open flow
		// takes the list's place (viewMain) and so gets the list's width.
		// Detail is still reachable as an on-demand overlay.
		listW = usable - 2
		if listW < listFloorWidth {
			listW = listFloorWidth
		}
		flowW = listW
	default:
		// Two-column: list + flow zone. The list keeps priority: it grows to
		// listCeilWidth first, the flow zone absorbs whatever remains — unlike
		// the old fixed calendar column, nothing here caps how wide it gets.
		listW = usable - flowMin - paneFrames
		if listW > listCeilWidth {
			listW = listCeilWidth
		}
		if listW < listFloorWidth {
			listW = listFloorWidth
		}
		flowW = usable - listW - paneFrames
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
		flowWidth:          flowW,
		showRight:          showRight,
		bodyHeight:         bodyH,
		inputWidth:         inputW,
		detailOverlayWidth: detailOverlayW,
		previewWidth:       previewW,
	}
}

// panelHeightOverhead is the panel's chrome outside its row lines: a title
// line, a footer hint line, and the RoundedBorder frame's top AND bottom
// border rows (paneActive, styles.go — the border draws two extra rows, not
// one; an earlier version of this budget undercounted that and let the
// rendered panel run one row past what it had reserved).
const panelHeightOverhead = 4

// panelHeightFor is the terminal rows an open action panel consumes: its rows
// plus panelHeightOverhead, capped so the panel can never starve the commit
// list of space. Callers should pass the exact row count they intend to
// render (see panelWindowRows) so this budget and viewActionPanel's actual
// rendered height always agree — panelMaxRows bounds both, not just this
// budget math.
func panelHeightFor(rows int) int {
	h := rows + panelHeightOverhead
	if h > panelMaxRows+panelHeightOverhead {
		h = panelMaxRows + panelHeightOverhead
	}
	return h
}

// panelShownRows caps a row count at panelMaxRows alone, with no awareness of
// the terminal height. It is panelWindowRows' building block and its
// pre-WindowSizeMsg fallback (m.height <= 0, so no real budget exists yet).
func panelShownRows(total int) int {
	return min(total, panelMaxRows)
}

// panelWindowRows returns how many action rows the panel actually renders,
// given it has `total` rows available: capped by panelMaxRows as always, and
// further reduced on a short terminal so opening the panel costs fewer rows
// the less room there is. avail mirrors listHeight's own budget exactly (see
// mainChromeRows, list.go, and the matching deduction in viewFlowZone,
// workspace.go) minus the list's floor, the log footer if it's also visible,
// and the panel's own fixed overhead — so all three call sites agree on what
// the terminal can hold, and the frame fits the terminal even with both the
// log footer and the action panel open at once (verified by
// TestPanelOpenAtMinTerminalKeepsListFloor).
func (m Model) panelWindowRows(total int) int {
	if m.height <= 0 {
		return panelShownRows(total)
	}
	avail := m.height - minListRows - mainChromeRows - panelHeightOverhead
	if m.logVisible {
		avail -= logPanelHeight
	}
	if avail < 1 {
		avail = 1
	}
	capacity := min(avail, panelMaxRows)
	return min(total, capacity)
}
