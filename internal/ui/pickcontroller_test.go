package ui

import (
	"testing"
	"time"

	"github.com/wlame/regit/internal/strategy"
)

func testWins(sizes ...int) []strategy.Window {
	// Adjacent windows sharing boundary days: win i covers days [3i+1 .. 3i+4]
	// of March 2026 and shares day 3i+4 with win i+1's start... keep it simpler:
	// win0 = Mar 1..3 size s0, win1 = Mar 3..6 size s1 (shared day Mar 3).
	day := func(d int) time.Time {
		return time.Date(2026, 3, d, 0, 0, 0, 0, time.UTC)
	}
	switch len(sizes) {
	case 1:
		return []strategy.Window{{StartDay: day(1), EndDay: day(10), Size: sizes[0]}}
	default:
		return []strategy.Window{
			{StartDay: day(1), EndDay: day(3), Size: sizes[0]},
			{StartDay: day(3), EndDay: day(6), Size: sizes[1]},
		}
	}
}

func freshState() *strategyState {
	return &strategyState{
		quotas:   map[string]int{},
		picked:   map[string]bool{},
		pickSeq:  map[string]int{},
		quotaSeq: map[string]int{},
	}
}

func d(day int) string {
	return time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

func TestPickWithEviction_SingleCommitSwaps(t *testing.T) {
	a, wins := freshState(), testWins(1)
	if _, ok := pickWithEviction(a, d(2), wins, time.UTC); !ok {
		t.Fatal("first pick refused")
	}
	evicted, ok := pickWithEviction(a, d(5), wins, time.UTC)
	if !ok || len(evicted) != 1 || evicted[0] != d(2) {
		t.Fatalf("second pick must evict the first: evicted=%v ok=%v", evicted, ok)
	}
	if a.picked[d(2)] || !a.picked[d(5)] {
		t.Errorf("state after swap: %v", a.picked)
	}
}

func TestPickWithEviction_EvictsMostRecent(t *testing.T) {
	a, wins := freshState(), testWins(2)[:1] // simpler: one window size 2
	pickWithEviction(a, d(2), wins, time.UTC)
	pickWithEviction(a, d(7), wins, time.UTC)
	evicted, _ := pickWithEviction(a, d(4), wins, time.UTC)
	if len(evicted) != 1 || evicted[0] != d(7) {
		t.Fatalf("must evict the most recent pick (day 7), got %v", evicted)
	}
	if !a.picked[d(2)] || !a.picked[d(4)] {
		t.Errorf("surviving picks wrong: %v", a.picked)
	}
}

func TestPickWithEviction_RecencyBeatsCalendarOrder(t *testing.T) {
	a, wins := freshState(), testWins(2) // one window size 2
	// Pick calendar-latest day FIRST: eviction must follow pick recency, not
	// calendar position, so the third pick evicts d(2) (picked second), never
	// d(7) (calendar-latest but picked first).
	pickWithEviction(a, d(7), wins, time.UTC)
	pickWithEviction(a, d(2), wins, time.UTC)
	evicted, ok := pickWithEviction(a, d(4), wins, time.UTC)
	if !ok || len(evicted) != 1 || evicted[0] != d(2) {
		t.Fatalf("must evict the most recently picked day (day 2), got %v ok=%v", evicted, ok)
	}
	if !a.picked[d(7)] || !a.picked[d(4)] || a.picked[d(2)] {
		t.Errorf("survivors must be day 7 and day 4: %v", a.picked)
	}
}

func TestPickWithEviction_OutOfWindowRefused(t *testing.T) {
	a := freshState()
	if _, ok := pickWithEviction(a, d(25), testWins(1), time.UTC); ok {
		t.Fatal("day outside every window must be refused")
	}
}

func TestPickWithEviction_SharedDayPrefersFreeCapacity(t *testing.T) {
	a, wins := freshState(), testWins(1, 2)
	pickWithEviction(a, d(2), wins, time.UTC) // fills segment 0
	// Shared day 3: segment 0 full, segment 1 free → goes to segment 1,
	// no eviction.
	evicted, ok := pickWithEviction(a, d(3), wins, time.UTC)
	if !ok || len(evicted) != 0 {
		t.Fatalf("shared day should use segment 1's capacity: evicted=%v", evicted)
	}
	if !a.picked[d(2)] || !a.picked[d(3)] {
		t.Errorf("picks: %v", a.picked)
	}
}

func TestPinWithEviction_ShrinksMostRecentAndClamps(t *testing.T) {
	a, wins := freshState(), testWins(3)
	if _, _, ok := pinWithEviction(a, d(2), 2, wins, time.UTC); !ok {
		t.Fatal("first pin refused")
	}
	// Capacity 3, used 2. A new pin of 2 overflows by 1 → day 2's quota
	// shrinks to 1.
	evicted, clamped, ok := pinWithEviction(a, d(4), 2, wins, time.UTC)
	if !ok || clamped != 2 {
		t.Fatalf("pin refused or clamped unexpectedly: %d %v", clamped, ok)
	}
	if a.quotas[d(2)] != 1 || a.quotas[d(4)] != 2 {
		t.Errorf("quotas after shrink: %v (evicted=%v)", a.quotas, evicted)
	}
	// A single oversized pin clamps to capacity.
	a = freshState()
	_, clamped, _ = pinWithEviction(a, d(2), 9, wins, time.UTC)
	if clamped != 3 || a.quotas[d(2)] != 3 {
		t.Errorf("oversized pin: clamped=%d quotas=%v", clamped, a.quotas)
	}
}

func TestPinWithEviction_SharedDayPrefersFittingSegment(t *testing.T) {
	// win0 = Mar 1..3 size 2, win1 = Mar 3..6 size 5; day 3 sits on the shared
	// boundary. With 1 of win0's 2 already used, a quota of 3 only fits win1 —
	// attribution must weigh the REAL quota, not a 1-unit probe that would
	// squeeze into win0's leftover slot and then shrink the older pin.
	a, wins := freshState(), testWins(2, 5)
	if _, _, ok := pinWithEviction(a, d(2), 1, wins, time.UTC); !ok {
		t.Fatal("first pin refused")
	}
	evicted, clamped, ok := pinWithEviction(a, d(3), 3, wins, time.UTC)
	if !ok || clamped != 3 || len(evicted) != 0 {
		t.Fatalf("shared-day pin must land in win1 untouched: evicted=%v clamped=%d ok=%v",
			evicted, clamped, ok)
	}
	if a.quotas[d(2)] != 1 || a.quotas[d(3)] != 3 {
		t.Errorf("quotas: %v, want day2=1 day3=3", a.quotas)
	}
}

func TestReattributePins_ClampsQuotaToRemainingRoom(t *testing.T) {
	a, wins := freshState(), testWins(3) // one window Mar 1..10 size 3
	pinWithEviction(a, d(2), 2, wins, time.UTC)
	pinWithEviction(a, d(4), 1, wins, time.UTC) // sum 3 = full
	// Shrink the window to Size 2 over the same days. Quotas replay
	// oldest-first: d(2) (weight 2) fits room 2; d(4) overflows with no room
	// left → dropped.
	shrunk := []strategy.Window{{
		StartDay: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		EndDay:   time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
		Size:     2,
	}}
	dropped := reattributePins(a, shrunk, time.UTC)
	if len(a.quotas) != 1 || a.quotas[d(2)] != 2 {
		t.Errorf("quotas after shrink: %v, want only day2=2", a.quotas)
	}
	if len(dropped) != 1 || dropped[0] != d(4) {
		t.Errorf("dropped = %v, want [%s]", dropped, d(4))
	}
}

func TestReattributePins_DropsOutOfWindowAndOverflow(t *testing.T) {
	a := freshState()
	wide := testWins(3)
	pickWithEviction(a, d(2), wide, time.UTC)
	pickWithEviction(a, d(5), wide, time.UTC)
	pickWithEviction(a, d(8), wide, time.UTC)
	// Selection change: now one window Mar 1..6 with capacity 1.
	narrow := []strategy.Window{{
		StartDay: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		EndDay:   time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC),
		Size:     1,
	}}
	dropped := reattributePins(a, narrow, time.UTC)
	// day 8 out of window; capacity 1 keeps the OLDEST pick (day 2),
	// drops day 5.
	if len(a.picked) != 1 || !a.picked[d(2)] {
		t.Errorf("survivors: %v (dropped %v)", a.picked, dropped)
	}
	if len(dropped) != 2 {
		t.Errorf("dropped = %v, want two days", dropped)
	}
}

// TestPinWithEviction_RepinMustDeleteStaleEntryFirst is the regression test for
// the HARD CONTRACT app.go's digit handler relies on: re-pinning a day already
// present in quotaSeq (the two-digit build-up, e.g. "2" then "4" combining to
// 24) must delete that day's OLD quotas/quotaSeq entry before calling
// pinWithEviction again. Skipping the delete replays the day TWICE in
// AttributePins' order slice (once from pickOrder(a.quotaSeq), once appended),
// which corrupts segment attribution on a SHARED boundary day and can evict an
// unrelated day's pin entirely — not a cosmetic difference.
//
// Fixture: win0 = Mar 1..3 size 3, win1 = Mar 3..6 size 24 (day 3 is the shared
// boundary; day 2 belongs to win0 only). day 2 is pinned first (quota 2, wins
// win0). day 3 is then pinned to 2 (wins win1, the only segment with room) and
// finally re-pinned to 24 (still fits win1's capacity of 24).
func TestPinWithEviction_RepinMustDeleteStaleEntryFirst(t *testing.T) {
	wins := testWins(3, 24)

	a := freshState()
	if _, _, ok := pinWithEviction(a, d(2), 2, wins, time.UTC); !ok {
		t.Fatal("setup: day 2 pin refused")
	}
	if _, _, ok := pinWithEviction(a, d(3), 2, wins, time.UTC); !ok {
		t.Fatal("setup: day 3 first-digit pin refused")
	}

	// The digit handler's contract: delete the day's stale entry, computed
	// from its OLD quota, before replaying it at the new combined value.
	delete(a.quotas, d(3))
	delete(a.quotaSeq, d(3))
	shrunk, clamped, ok := pinWithEviction(a, d(3), 24, wins, time.UTC)

	if !ok || clamped != 24 {
		t.Fatalf("re-pin to 24 should land untouched in win1 (capacity 24): "+
			"clamped=%d ok=%v", clamped, ok)
	}
	if len(shrunk) != 0 {
		t.Errorf("re-pin must not shrink anything when the delete precedes it: shrunk=%v", shrunk)
	}
	if a.quotas[d(2)] != 2 {
		t.Errorf("day 2's independent pin must survive untouched, got quotas=%v", a.quotas)
	}
	if a.quotas[d(3)] != 24 {
		t.Errorf("day 3 should end at the combined quota 24, got quotas=%v", a.quotas)
	}
	if len(a.quotaSeq) != 2 {
		t.Errorf("exactly 2 days should remain tracked, got quotaSeq=%v", a.quotaSeq)
	}
}

// TestPinWithEviction_MissingDeleteCorruptsSharedBoundaryAttribution documents
// (as a literal before/after comparison) WHY the delete-before-repin step in
// the previous test is not optional: replaying day 3 twice — once via
// pickOrder(a.quotaSeq) (the stale entry) and once via the explicit re-pin
// append — pushes AttributePins' segment selection for day 3 into win0
// (already near capacity) instead of win1, which then clamps day 3's quota
// down to win0's tiny capacity AND evicts day 2's unrelated pin to make room.
func TestPinWithEviction_MissingDeleteCorruptsSharedBoundaryAttribution(t *testing.T) {
	wins := testWins(3, 24)

	a := freshState()
	pinWithEviction(a, d(2), 2, wins, time.UTC)
	pinWithEviction(a, d(3), 2, wins, time.UTC)
	// Re-pin WITHOUT deleting the stale day-3 entry first (the bug).
	shrunk, clamped, ok := pinWithEviction(a, d(3), 24, wins, time.UTC)

	if !ok {
		t.Fatal("the corrupted attribution still resolves day 3 to some window")
	}
	if clamped != 3 {
		t.Errorf("corrupted attribution should misroute day 3 into win0 (capacity 3) "+
			"and clamp it there, got clamped=%d", clamped)
	}
	if len(shrunk) != 1 || shrunk[0] != d(2) {
		t.Errorf("corrupted attribution should evict day 2 to make room, got shrunk=%v", shrunk)
	}
	if _, stillPinned := a.quotas[d(2)]; stillPinned {
		t.Errorf("day 2's pin should have been wiped out by the corruption, got quotas=%v", a.quotas)
	}
}
