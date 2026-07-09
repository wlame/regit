package ui

// pickcontroller.go enforces the per-segment pick/pin capacity rules on the
// Dates calendar. It is a small pure state machine over strategyState's maps —
// no Bubble Tea, no terminal — so the eviction/attribution rules are fully
// table-testable.
//
// Rules (spec "Pick/pin capacity & eviction"):
//   - a segment with N commits holds at most N picked days / quotas summing N;
//   - picking into a full segment evicts that segment's MOST RECENT pick;
//   - an overflowing pin shrinks the most recently pinned day's quota first;
//   - a day on a shared window boundary is attributed by AttributePins
//     (free capacity first, then the earlier segment).

import (
	"sort"
	"time"

	"github.com/wlame/regit/internal/strategy"
)

// pickOrder returns seq's day keys in ascending pick order (oldest first).
func pickOrder(seq map[string]int) []string {
	out := make([]string, 0, len(seq))
	for k := range seq {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return seq[out[i]] < seq[out[j]] })
	return out
}

// pickWithEviction toggles ON the given day for Picked Days. ok=false means
// the day lies outside every window (refused, nothing changes). When the
// owning segment is already at capacity the segment's most recent pick is
// evicted; the evicted day keys are returned for a status message.
func pickWithEviction(
	a *strategyState,
	day string,
	wins []strategy.Window,
	loc *time.Location,
) ([]string, bool) {
	unit := func() map[string]int {
		w := make(map[string]int, len(a.pickSeq)+1)
		for k := range a.pickSeq {
			w[k] = 1
		}
		w[day] = 1
		return w
	}
	order := append(pickOrder(a.pickSeq), day) // the new pick is the most recent
	owner, overflow := strategy.AttributePins(order, unit(), wins, loc)
	if _, ok := owner[day]; !ok {
		return nil, false // outside every window
	}
	var evicted []string
	for _, over := range overflow {
		if over != day {
			continue
		}
		// The new pick overflowed its segment: evict that segment's most
		// recent EXISTING pick, then re-check (a single eviction always frees
		// one slot for a weight-1 pick, so one pass suffices).
		seg := owner[day]
		victim, best := "", -1
		for k, s := range owner {
			if k != day && s == seg && a.pickSeq[k] > best {
				victim, best = k, a.pickSeq[k]
			}
		}
		if victim != "" {
			delete(a.picked, victim)
			delete(a.pickSeq, victim)
			evicted = append(evicted, victim)
		}
	}
	a.picked[day] = true
	a.nextSeq++
	a.pickSeq[day] = a.nextSeq
	return evicted, true
}

// pinWithEviction sets a Manual Pin quota on day. The quota is first clamped
// to the owning segment's total capacity; then older pins in that segment are
// shrunk (most recent first) until the segment's sum fits. Returns the days
// whose quota was shrunk or removed, the clamped quota, and ok=false when the
// day lies outside every window.
func pinWithEviction(
	a *strategyState,
	day string,
	quota int,
	wins []strategy.Window,
	loc *time.Location,
) ([]string, int, bool) {
	weights := func() map[string]int {
		w := make(map[string]int, len(a.quotaSeq)+1)
		for k := range a.quotaSeq {
			w[k] = a.quotas[k]
		}
		w[day] = quota
		return w
	}
	order := append(pickOrder(a.quotaSeq), day)
	// Attribute with the REAL quota so a shared-boundary day lands in the
	// segment whose free capacity actually fits it (AttributePins picks the
	// first candidate with room, else the earliest segment + overflow).
	owner, _ := strategy.AttributePins(order, weights(), wins, loc)
	seg, ok := owner[day]
	if !ok {
		return nil, quota, false
	}
	if capSize := wins[seg].Size; quota > capSize {
		quota = capSize
	}
	// Shrink most recent other pins in this segment until the new pin fits.
	var touched []string
	sum := func() int {
		total := quota
		for k, s := range owner {
			if k != day && s == seg {
				total += a.quotas[k]
			}
		}
		return total
	}
	victims := pickOrder(a.quotaSeq)
	for i := len(victims) - 1; i >= 0 && sum() > wins[seg].Size; i-- {
		k := victims[i]
		// Comma-ok guards a stale out-of-window day from zero-value-matching
		// segment 0 (a missing map key would otherwise read as s == 0).
		if s, owned := owner[k]; k == day || !owned || s != seg {
			continue
		}
		over := sum() - wins[seg].Size
		if a.quotas[k] > over {
			a.quotas[k] -= over
		} else {
			delete(a.quotas, k)
			delete(a.quotaSeq, k)
		}
		touched = append(touched, k)
	}
	a.quotas[day] = quota
	a.nextSeq++
	a.quotaSeq[day] = a.nextSeq
	return touched, quota, true
}

// reattributePins re-checks every pick and pin after the selection (and hence
// the windows) changed: entries outside every window are dropped, and segments
// over capacity evict their most recent entries until they fit. Returns the
// dropped/shrunk day keys so the caller can mention them in the status line.
func reattributePins(
	a *strategyState,
	wins []strategy.Window,
	loc *time.Location,
) []string {
	var dropped []string

	// Picks: replay oldest-first; a pick that no longer fits (no window, or
	// its only candidates are full) is dropped — keeping the OLDEST choices is
	// the least surprising outcome of a selection change.
	unit := make(map[string]int, len(a.pickSeq))
	for k := range a.pickSeq {
		unit[k] = 1
	}
	owner, overflow := strategy.AttributePins(pickOrder(a.pickSeq), unit, wins, loc)
	for k := range a.picked {
		_, owned := owner[k]
		over := false
		for _, o := range overflow {
			if o == k {
				over = true
			}
		}
		if !owned || over {
			delete(a.picked, k)
			delete(a.pickSeq, k)
			dropped = append(dropped, k)
		}
	}

	// Quotas: same replay with quota weights; an overflowing pin is clamped to
	// what still fits rather than dropped outright when possible.
	weights := make(map[string]int, len(a.quotaSeq))
	for k := range a.quotaSeq {
		weights[k] = a.quotas[k]
	}
	qOwner, qOverflow := strategy.AttributePins(
		pickOrder(a.quotaSeq),
		weights,
		wins,
		loc,
	)
	used := make(map[int]int)
	for k, s := range qOwner {
		over := false
		for _, o := range qOverflow {
			if o == k {
				over = true
			}
		}
		if !over {
			used[s] += a.quotas[k]
		}
	}
	for _, k := range pickOrder(a.quotaSeq) {
		s, owned := qOwner[k]
		if !owned {
			delete(a.quotas, k)
			delete(a.quotaSeq, k)
			dropped = append(dropped, k)
			continue
		}
		over := false
		for _, o := range qOverflow {
			if o == k {
				over = true
			}
		}
		if !over {
			continue
		}
		room := wins[s].Size - used[s]
		if room <= 0 {
			delete(a.quotas, k)
			delete(a.quotaSeq, k)
			dropped = append(dropped, k)
			continue
		}
		a.quotas[k] = room
		used[s] += room
		dropped = append(dropped, k)
	}
	return dropped
}
