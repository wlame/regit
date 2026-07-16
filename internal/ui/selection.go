package ui

// selection.go models the commit list's current selection. SelectionMode (declared
// in feature.go) sets the list's visual language; selectionState carries the live
// state for the multi-commit modes. Display indices are newest-first (index 0 =
// the newest commit), matching m.listCursor. These helpers are pure so the
// transitions are unit-testable without a terminal.

// selectionState is the live selection on the commit list. Only the fields
// relevant to the current mode are used: Range uses anchor/visual, Set uses set.
type selectionState struct {
	mode   SelectionMode
	anchor int             // range anchor (display index), set when visual mode starts
	end    int             // fixed second endpoint, set by confirmVisual; used only when fixed
	visual bool            // true while a range is being extended (v pressed, not yet fixed)
	fixed  bool            // true once a visual range is confirmed: the span stops following the cursor
	set    map[string]bool // selected commit hashes (SelSet)
}

// newSelection returns a fresh Single-mode selection (cursor only).
func newSelection() selectionState {
	return selectionState{mode: SelSingle, set: map[string]bool{}}
}

// reset returns the selection to a clean Single mode, dropping any range/set
// state. Used when leaving a flow or reloading so a stale multi-selection never
// leaks across contexts.
func (s *selectionState) reset() {
	s.mode = SelSingle
	s.visual = false
	s.fixed = false
	s.anchor = 0
	s.end = 0
	s.set = map[string]bool{}
}

// beginVisual starts range visual mode anchored at the cursor's display index.
// Movement then extends the range live; rangeBounds derives the span from the
// anchor and the current cursor, so no per-keystroke extend bookkeeping is needed.
func (s *selectionState) beginVisual(cursor int) {
	s.mode = SelRange
	s.visual = true
	s.fixed = false
	s.anchor = cursor
}

// confirmVisual fixes the current range at [anchor, cursor] and stops it extending.
// The span is stored (s.end) so it no longer follows the cursor — the user can move
// the cursor to navigate without silently growing the range a consuming feature
// (retitle, drop-bodies, squash, reauthor) will act on.
func (s *selectionState) confirmVisual(cursor int) {
	s.visual = false
	s.fixed = true
	s.end = cursor
}

// rangeBounds returns the inclusive [lo, hi] display-index span of the range.
// While the range is being extended (visual) it follows the live cursor; once
// confirmed (fixed) it uses the stored endpoint and ignores cursor drift. A Range
// that was never confirmed through confirmVisual (e.g. set directly) falls back to
// the live cursor. Indices are newest-first, so lo <= hi numerically.
func (s selectionState) rangeBounds(cursor int) (int, int) {
	other := cursor
	if s.fixed && !s.visual {
		other = s.end
	}
	lo, hi := s.anchor, other
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

// inRange reports whether a display index falls inside the current range span.
func (s selectionState) inRange(displayIdx, cursor int) bool {
	if s.mode != SelRange {
		return false
	}
	lo, hi := s.rangeBounds(cursor)
	return displayIdx >= lo && displayIdx <= hi
}

// toggle flips a commit's membership in the Set, switching the list into Set mode.
// Frozen commits are never selectable, so a frozen hash is ignored.
func (s *selectionState) toggle(hash string, frozen bool) {
	if frozen {
		return
	}
	s.mode = SelSet
	if s.set == nil {
		s.set = map[string]bool{}
	}
	if s.set[hash] {
		delete(s.set, hash)
	} else {
		s.set[hash] = true
	}
}

// selectAll puts every given (active, non-frozen) hash into the Set.
func (s *selectionState) selectAll(activeHashes []string) {
	s.mode = SelSet
	s.set = make(map[string]bool, len(activeHashes))
	for _, h := range activeHashes {
		s.set[h] = true
	}
}

// clear empties the Set, staying in Set mode so the user can re-pick.
func (s *selectionState) clear() {
	s.set = map[string]bool{}
}

// isSet reports whether a commit hash is in the Set selection.
func (s selectionState) isSet(hash string) bool {
	return s.set[hash]
}

// selectedHashes returns the chosen hashes (Set mode); order is unspecified, so
// callers that need display order must sort by index themselves.
func (s selectionState) selectedHashes() []string {
	out := make([]string, 0, len(s.set))
	for h := range s.set {
		out = append(out, h)
	}
	return out
}
