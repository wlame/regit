package ui

// selection.go models the commit list's current selection. SelectionMode (declared
// in feature.go) sets the list's visual language; selectionState carries the live
// state for the multi-commit modes. Display indices are newest-first (index 0 =
// the newest commit), matching m.listCursor. These helpers are pure so the
// transitions are unit-testable without a terminal.

// selectionState is the live selection on the commit list. Marked commits live
// in set. SelRange means a range is being marked right now: anchor is where it
// started, the live cursor is its other end, and the commits between them are
// a pending band that joins set when the range is finished (see
// Model.finishRange).
type selectionState struct {
	mode   SelectionMode
	anchor int             // band anchor (display index) while a range is being marked
	set    map[string]bool // marked commit hashes
}

// newSelection returns a fresh Single-mode selection (cursor only).
func newSelection() selectionState {
	return selectionState{mode: SelSingle, set: map[string]bool{}}
}

// reset returns the selection to a clean Single mode, dropping every mark and
// any pending band. Used when leaving a flow or reloading so a stale
// multi-selection never leaks across contexts.
func (s *selectionState) reset() {
	s.mode = SelSingle
	s.anchor = 0
	s.set = map[string]bool{}
}

// beginVisual starts marking a range anchored at the cursor's display index.
// Earlier marks are kept. Movement extends the band live; rangeBounds derives
// it from the anchor and the current cursor.
func (s *selectionState) beginVisual(cursor int) {
	s.mode = SelRange
	s.anchor = cursor
}

// endBand stops marking a range: the selection becomes the marked set, or the
// plain cursor when nothing is marked. Model.finishRange adds the band's
// commits to the set first; cancelling a range calls this alone.
func (s *selectionState) endBand() {
	if len(s.set) > 0 {
		s.mode = SelSet
	} else {
		s.mode = SelSingle
	}
}

// rangeBounds returns the inclusive [lo, hi] display-index span of the band
// being marked: from the anchor to the live cursor. Indices are newest-first,
// so lo <= hi numerically.
func (s selectionState) rangeBounds(cursor int) (int, int) {
	lo, hi := s.anchor, cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

// inRange reports whether a display index falls inside the band being marked.
func (s selectionState) inRange(displayIdx, cursor int) bool {
	if s.mode != SelRange {
		return false
	}
	lo, hi := s.rangeBounds(cursor)
	return displayIdx >= lo && displayIdx <= hi
}

// toggle flips a commit's mark, switching the list into Set mode (a band being
// marked stays open). Frozen commits are never selectable, so a frozen hash is
// ignored.
func (s *selectionState) toggle(hash string, frozen bool) {
	if frozen {
		return
	}
	if s.mode != SelRange {
		s.mode = SelSet
	}
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

// clear removes every mark and any pending band, staying in Set mode so the
// user can re-pick.
func (s *selectionState) clear() {
	s.mode = SelSet
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
